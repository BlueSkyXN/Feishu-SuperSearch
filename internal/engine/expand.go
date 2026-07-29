package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	budgetctl "github.com/BlueSkyXN/Feishu-SuperSearch/internal/budget"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/session"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type expandFrontier struct {
	ref   kernel.ObjectRef
	depth int
	root  bool
}

type expandResult struct {
	item      expandFrontier
	relations []kernel.Relation
	err       error
	leaf      bool
}

// Expand performs a bounded breadth-first relation traversal. Providers remain
// one-hop adapters; the kernel owns cross-provider traversal, visited tracking,
// concurrency, and the shared expansion/call/byte budget.
func (e *Engine) Expand(ctx context.Context, req kernel.ExpandRequest) (kernel.RelationBatch, error) {
	reusingSession := strings.TrimSpace(req.SessionID) != ""
	req.Identity = req.Identity.Normalized()
	req.Budget = req.Budget.WithDefaults()
	if err := req.Ref.Validate(); err != nil {
		return kernel.RelationBatch{}, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: err.Error()}
	}
	if req.MaxDepth <= 0 {
		req.MaxDepth = 1
	}
	if req.MaxDepth > 2 {
		req.MaxDepth = 2
	}
	rec, err := e.getOrCreateSession(ctx, req.SessionID, req.Identity)
	if err != nil {
		return kernel.RelationBatch{}, err
	}
	snap := rec.Snapshot(false)
	if known, ok := knownSessionRef(snap, req.Ref); ok {
		req.Ref = known
	} else if reusingSession {
		return kernel.RelationBatch{}, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "expand ref is not present in the selected session"}
	}
	if err := validateRefIdentity(req.Ref, req.Identity); err != nil {
		return kernel.RelationBatch{}, err
	}
	if req.Ref.ScopeKey == "" {
		req.Ref.ScopeKey = req.Identity.ScopeKey
	}
	if req.Ref.CanonicalID == "" && req.Ref.NativeID != "" {
		req.Ref.CanonicalID = kernel.BuildCanonicalID(req.Ref.ScopeKey, req.Ref.Kind, req.Ref.NativeID)
	}

	budget := budgetctl.ForRequest(ctx, req.Budget)
	callCtx, cancel := context.WithDeadline(ctx, budget.Deadline())
	defer cancel()
	frontier := []expandFrontier{{ref: req.Ref, depth: 0, root: true}}
	visited := map[string]bool{objectKey(req.Ref): true}
	all := append([]kernel.Relation(nil), snap.Relations...)
	newRelations := []kernel.Relation{}
	partialErrors := map[string]*kernel.ErrorDetail{}
	var rootErr error
	allowedRelations := map[string]bool{}
	for _, relationType := range req.Relations {
		if relationType != "" {
			allowedRelations[relationType] = true
		}
	}

	for len(frontier) > 0 {
		sem := make(chan struct{}, e.cfg.GlobalConcurrency)
		results := make(chan expandResult, len(frontier))
		var wg sync.WaitGroup
		for _, item := range frontier {
			item := item
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				p, pErr := e.providerForRef(item.ref, kernel.OpExpand)
				if pErr != nil {
					results <- expandResult{item: item, err: pErr, leaf: !item.root}
					return
				}
				if opErr := validateProviderOperation(p, kernel.OpExpand, req.Identity); opErr != nil {
					results <- expandResult{item: item, err: opErr, leaf: !item.root && kernel.DetailFromError(opErr).Type == kernel.ErrUnsupported}
					return
				}
				if item.ref.Source != "" && p.Descriptor().Source != item.ref.Source {
					results <- expandResult{item: item, err: &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, ProviderID: p.Descriptor().ID, Source: item.ref.Source, Message: "object source does not match selected provider"}}
					return
				}
				if item.ref.Kind != "" && !providerSupportsKind(p.Descriptor(), item.ref.Kind) {
					results <- expandResult{item: item, err: &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, ProviderID: p.Descriptor().ID, Source: p.Descriptor().Source, Message: "object kind is not supported by selected provider"}}
					return
				}
				expander, ok := p.(kernel.Expander)
				if !ok {
					err := &kernel.ErrorDetail{Type: kernel.ErrUnsupported, ProviderID: p.Descriptor().ID, Source: p.Descriptor().Source, Message: "provider does not implement expand"}
					results <- expandResult{item: item, err: err, leaf: !item.root}
					return
				}
				if !budget.Reserve(kernel.CostEstimate{Calls: 1, Expanded: 1}) {
					results <- expandResult{item: item, err: &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, ProviderID: p.Descriptor().ID, Source: p.Descriptor().Source, Message: "expand budget exhausted"}}
					return
				}
				oneCtx, oneCancel := context.WithTimeout(callCtx, e.cfg.ProviderTimeout*2)
				rels, callErr := expander.Expand(oneCtx, kernel.ProviderExpandRequest{Ref: item.ref, Relations: req.Relations, Identity: req.Identity, MaxDepth: 1, SessionID: rec.ID})
				oneCancel()
				if callErr == nil && !budget.AddBytes(encodedSize(rels)) {
					callErr = &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, ProviderID: p.Descriptor().ID, Source: p.Descriptor().Source, Message: "relation response exceeded byte budget"}
				}
				results <- expandResult{item: item, relations: rels, err: callErr}
			}()
		}
		wg.Wait()
		close(results)

		next := []expandFrontier{}
		stopForBudget := false
		for result := range results {
			if result.err != nil {
				detail := kernel.DetailFromError(result.err)
				if result.item.root {
					rootErr = result.err
				}
				if !result.leaf {
					partialErrors[objectKey(result.item.ref)] = detail
				}
				if detail.Type == kernel.ErrBudgetExhausted || detail.Type == kernel.ErrDeadlineExceeded || detail.Type == kernel.ErrCancelled {
					stopForBudget = true
				}
				continue
			}
			for i := range result.relations {
				relation := result.relations[i]
				if len(allowedRelations) > 0 && !allowedRelations[relation.Type] {
					continue
				}
				relation.From = normalizeRelationRef(relation.From, result.item.ref, req.Identity)
				relation.To = normalizeRelationRef(relation.To, kernel.ObjectRef{}, req.Identity)
				if relation.Provenance.RetrievedAt.IsZero() {
					relation.Provenance.RetrievedAt = time.Now().UTC()
				}
				newRelations = mergeRelations(newRelations, []kernel.Relation{relation})
				if result.item.depth+1 < req.MaxDepth {
					key := objectKey(relation.To)
					if key != "" && !visited[key] {
						visited[key] = true
						next = append(next, expandFrontier{ref: relation.To, depth: result.item.depth + 1})
					}
				}
			}
		}
		if stopForBudget {
			break
		}
		frontier = next
	}

	state := budget.Snapshot()
	all = mergeRelations(all, newRelations)
	var partial *kernel.ErrorDetail
	if len(partialErrors) > 0 {
		partial = &kernel.ErrorDetail{Type: kernel.ErrUpstreamPermanent, Message: "one or more relation expansions failed", Details: map[string]any{"objects": partialErrors}}
	}
	if len(newRelations) == 0 && rootErr != nil {
		return kernel.RelationBatch{SessionID: rec.ID, Partial: true, Error: kernel.DetailFromError(rootErr), Budget: state}, rootErr
	}
	rec.WithLock(func(r *session.Record) {
		r.Relations = all
		r.Budget = state
	})
	batch := kernel.RelationBatch{SessionID: rec.ID, Relations: newRelations, Partial: partial != nil, Error: partial, Budget: state}
	if err := e.sessions.Save(ctx, rec); err != nil {
		return batch, sessionPersistenceError(err, "save_expand")
	}
	return batch, nil
}

func objectKey(ref kernel.ObjectRef) string {
	if ref.CanonicalID != "" {
		return ref.CanonicalID
	}
	if ref.NativeID == "" {
		return ""
	}
	return fmt.Sprintf("%s|%s|%s", ref.Source, ref.Kind, ref.NativeID)
}

func normalizeRelationRef(ref, fallback kernel.ObjectRef, identity kernel.Identity) kernel.ObjectRef {
	ref = mergeRef(ref, fallback)
	if ref.Platform == "" {
		ref.Platform = "feishu"
	}
	if ref.NativeID != "" {
		ref.ScopeKey = identity.Normalized().ScopeKey
		ref.CanonicalID = kernel.BuildCanonicalID(ref.ScopeKey, ref.Kind, ref.NativeID)
	}
	return ref
}
