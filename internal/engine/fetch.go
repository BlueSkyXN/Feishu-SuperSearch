package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	budgetctl "github.com/BlueSkyXN/Feishu-SuperSearch/internal/budget"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/session"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type fetchWork struct {
	index    int
	req      kernel.ProviderFetchRequest
	provider kernel.Provider
	key      string
	leader   bool
	complete func(kernel.Artifact, error)
	wait     func(context.Context) (kernel.Artifact, error)
}

func (e *Engine) Fetch(ctx context.Context, req kernel.FetchBatchRequest) (kernel.ArtifactBatch, error) {
	reusingSession := strings.TrimSpace(req.SessionID) != ""
	req.Identity = req.Identity.Normalized()
	req.Budget = req.Budget.WithDefaults()
	if len(req.Items) == 0 {
		return kernel.ArtifactBatch{}, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "fetch items are required"}
	}
	rec, err := e.getOrCreateSession(ctx, req.SessionID, req.Identity)
	if err != nil {
		return kernel.ArtifactBatch{}, err
	}
	budget := budgetctl.ForRequest(ctx, req.Budget)
	callCtx, cancel := context.WithDeadline(ctx, budget.Deadline())
	defer cancel()
	items := dedupFetchItems(req.Items)
	results := make([]kernel.ArtifactResult, len(items))
	works := make([]fetchWork, 0, len(items))
	ss := rec.Snapshot(false)
	artifactMap := map[string]kernel.Artifact{}
	var artifactMu sync.Mutex
	for _, a := range ss.Artifacts {
		artifactMap[a.Ref.CanonicalID] = a
	}
	for i, item := range items {
		ref := item.Ref
		if item.Projection.Empty() {
			results[i] = kernel.ArtifactResult{Ref: ref, Error: &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "fetch projection is required"}}
			continue
		}
		if known, ok := knownSessionRef(ss, ref); ok {
			ref = known
		} else if reusingSession {
			results[i] = kernel.ArtifactResult{Ref: ref, Error: &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "fetch ref is not present in the selected session"}}
			continue
		}
		if err := validateRefIdentity(ref, req.Identity); err != nil {
			results[i] = kernel.ArtifactResult{Ref: ref, Error: kernel.DetailFromError(err)}
			continue
		}
		if ref.ScopeKey == "" {
			ref.ScopeKey = req.Identity.ScopeKey
		}
		if ref.CanonicalID == "" && ref.NativeID != "" {
			ref.CanonicalID = kernel.BuildCanonicalID(ref.ScopeKey, ref.Kind, ref.NativeID)
		}
		item.Ref = ref
		if existing, ok := artifactMap[ref.CanonicalID]; ok && existing.Projection.Has(item.Projection) {
			a := existing
			results[i] = kernel.ArtifactResult{Ref: ref, Artifact: &a, Cached: true}
			continue
		}
		p, perr := e.providerForRef(ref, kernel.OpFetch)
		if perr != nil {
			results[i] = kernel.ArtifactResult{Ref: ref, Error: kernel.DetailFromError(perr)}
			continue
		}
		if verr := validateProviderOperation(p, kernel.OpFetch, req.Identity); verr != nil {
			results[i] = kernel.ArtifactResult{Ref: ref, Error: kernel.DetailFromError(verr)}
			continue
		}
		if ref.Source != "" && p.Descriptor().Source != ref.Source {
			results[i] = kernel.ArtifactResult{Ref: ref, Error: &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, ProviderID: p.Descriptor().ID, Source: ref.Source, Message: "object source does not match selected provider"}}
			continue
		}
		if ref.Kind != "" && !providerSupportsKind(p.Descriptor(), ref.Kind) {
			results[i] = kernel.ArtifactResult{Ref: ref, Error: &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, ProviderID: p.Descriptor().ID, Source: p.Descriptor().Source, Message: "object kind is not supported by selected provider"}}
			continue
		}
		missing := item.Projection
		if existing, ok := artifactMap[ref.CanonicalID]; ok {
			missing = item.Projection.Missing(existing.Projection)
		}
		if missing == 0 {
			missing = item.Projection
		}
		if unsupported := missing &^ p.Descriptor().FetchableProjection; unsupported != 0 {
			results[i] = kernel.ArtifactResult{Ref: ref, Error: &kernel.ErrorDetail{Type: kernel.ErrUnsupported, ProviderID: p.Descriptor().ID, Source: p.Descriptor().Source, Message: "requested projection is not supported by provider", Details: map[string]any{"projection": unsupported.Strings()}}}
			continue
		}
		providerReq := kernel.ProviderFetchRequest{Ref: ref, Projection: missing, Identity: req.Identity, SessionID: rec.ID}
		key := fmt.Sprintf("%s|%s|%d|%s|%s", ref.ScopeKey, ref.CanonicalID, missing, p.Descriptor().ID, p.Descriptor().Version)
		leader, complete, wait := e.flights.Acquire(key)
		works = append(works, fetchWork{index: i, req: providerReq, provider: p, key: key, leader: leader, complete: complete, wait: wait})
	}

	apply := func(work fetchWork, artifact kernel.Artifact, fetchErr error, shared bool) {
		if fetchErr != nil {
			results[work.index] = kernel.ArtifactResult{Ref: work.req.Ref, Error: kernel.DetailFromError(fetchErr), Shared: shared}
			return
		}
		artifact.Ref = mergeRef(work.req.Ref, artifact.Ref)
		artifactMu.Lock()
		if old, ok := artifactMap[artifact.Ref.CanonicalID]; ok {
			artifact = mergeArtifact(old, artifact)
		}
		artifactMap[artifact.Ref.CanonicalID] = artifact
		artifactMu.Unlock()
		copy := artifact
		results[work.index] = kernel.ArtifactResult{Ref: artifact.Ref, Artifact: &copy, Shared: shared}
		e.addEvent(rec, kernel.EventArtifactUpsert, artifact.Ref.Source, map[string]any{"artifact": artifact, "shared": shared})
	}

	sem := make(chan struct{}, e.cfg.GlobalConcurrency)
	var wg sync.WaitGroup
	groups := map[kernel.ProviderID][]fetchWork{}
	for _, work := range works {
		work := work
		if !work.leader {
			wg.Add(1)
			go func() {
				defer wg.Done()
				artifact, waitErr := work.wait(callCtx)
				apply(work, artifact, waitErr, true)
			}()
			continue
		}
		groups[work.provider.Descriptor().ID] = append(groups[work.provider.Descriptor().ID], work)
	}

	for _, group := range groups {
		maxBatch := group[0].provider.Descriptor().BatchLimits.MaxFetchItems
		if maxBatch <= 0 {
			maxBatch = 1
		}
		for start := 0; start < len(group); start += maxBatch {
			end := start + maxBatch
			if end > len(group) {
				end = len(group)
			}
			chunk := append([]fetchWork(nil), group[start:end]...)
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				if !budget.Reserve(kernel.CostEstimate{Calls: 1, Fetches: len(chunk)}) {
					err := &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, Message: "fetch budget exhausted"}
					for _, work := range chunk {
						work.complete(kernel.Artifact{}, err)
						apply(work, kernel.Artifact{}, err, false)
					}
					return
				}
				fetcher, ok := chunk[0].provider.(kernel.Fetcher)
				if !ok {
					err := &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: "provider does not implement fetch"}
					for _, work := range chunk {
						work.complete(kernel.Artifact{}, err)
						apply(work, kernel.Artifact{}, err, false)
					}
					return
				}
				reqs := make([]kernel.ProviderFetchRequest, len(chunk))
				for i := range chunk {
					reqs[i] = chunk[i].req
				}
				oneCtx, oneCancel := context.WithTimeout(callCtx, e.cfg.ProviderTimeout*2)
				artifacts, callErr := fetcher.Fetch(oneCtx, reqs)
				oneCancel()
				matched := matchBatchArtifacts(reqs, artifacts)
				for i, work := range chunk {
					artifact, ok := matched[i]
					itemErr := error(nil)
					if !ok {
						if callErr != nil {
							itemErr = callErr
						} else {
							itemErr = &kernel.ErrorDetail{Type: kernel.ErrNotFound, ProviderID: work.provider.Descriptor().ID, Source: work.provider.Descriptor().Source, Message: "provider returned no artifact for requested object"}
						}
					} else if validationErr := validateFetchedArtifact(work.provider.Descriptor(), work.req, artifact); validationErr != nil {
						itemErr = validationErr
					} else if !budget.AddBytes(encodedSize(artifact)) {
						itemErr = &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, ProviderID: work.provider.Descriptor().ID, Source: work.provider.Descriptor().Source, Message: "artifact exceeded byte budget"}
					}
					work.complete(artifact, itemErr)
					apply(work, artifact, itemErr, false)
				}
			}()
		}
	}
	wg.Wait()
	partial := false
	for _, r := range results {
		if r.Error != nil {
			partial = true
		}
	}
	state := budget.Snapshot()
	rec.WithLock(func(r *session.Record) {
		artifactMu.Lock()
		defer artifactMu.Unlock()
		for id, a := range artifactMap {
			r.Artifacts[id] = a
		}
		r.Budget = state
	})
	batch := kernel.ArtifactBatch{SessionID: rec.ID, Items: results, Partial: partial, Budget: state}
	if err := e.sessions.Save(ctx, rec); err != nil {
		return batch, sessionPersistenceError(err, "save_fetch")
	}
	if partial && len(works) == 0 {
		return batch, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: "no fetch item could be executed"}
	}
	return batch, nil
}

func knownSessionRef(snapshot kernel.SessionSnapshot, requested kernel.ObjectRef) (kernel.ObjectRef, bool) {
	refs := make([]kernel.ObjectRef, 0, len(snapshot.Candidates)+len(snapshot.Artifacts)+2*len(snapshot.Relations))
	for _, candidate := range snapshot.Candidates {
		refs = append(refs, candidate.Ref)
	}
	for _, artifact := range snapshot.Artifacts {
		refs = append(refs, artifact.Ref)
	}
	for _, relation := range snapshot.Relations {
		refs = append(refs, relation.From, relation.To)
	}
	for _, known := range refs {
		if requested.CanonicalID != "" && known.CanonicalID == requested.CanonicalID {
			return known, true
		}
		if requested.NativeID != "" && requested.NativeID == known.NativeID &&
			(requested.Source == "" || requested.Source == known.Source) &&
			(requested.Kind == "" || requested.Kind == known.Kind) {
			return known, true
		}
	}
	return kernel.ObjectRef{}, false
}

func validateRefIdentity(ref kernel.ObjectRef, identity kernel.Identity) error {
	identity = identity.Normalized()
	if ref.ScopeKey != "" && ref.ScopeKey != identity.ScopeKey {
		return &kernel.ErrorDetail{Type: kernel.ErrIdentityRequired, Message: "object belongs to a different identity scope"}
	}
	if strings.HasPrefix(ref.CanonicalID, "feishu:") {
		parts := strings.SplitN(ref.CanonicalID, ":", 4)
		if len(parts) != 4 || parts[1] == "" || parts[2] == "" || parts[3] == "" {
			return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "malformed Feishu canonical_id"}
		}
		if parts[1] != identity.ScopeKey {
			return &kernel.ErrorDetail{Type: kernel.ErrIdentityRequired, Message: "canonical object belongs to a different identity scope"}
		}
		if ref.Kind != "" && string(ref.Kind) != parts[2] {
			return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "canonical_id kind does not match object ref"}
		}
		if ref.NativeID != "" && ref.NativeID != parts[3] {
			return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "canonical_id native id does not match object ref"}
		}
	}
	return nil
}

func providerSupportsKind(descriptor kernel.ProviderDescriptor, kind kernel.ObjectKind) bool {
	if len(descriptor.ObjectKinds) == 0 {
		return true
	}
	for _, supported := range descriptor.ObjectKinds {
		if supported == kind {
			return true
		}
	}
	return false
}

func validateFetchedArtifact(descriptor kernel.ProviderDescriptor, request kernel.ProviderFetchRequest, artifact kernel.Artifact) error {
	allowed := descriptor.ReturnedProjection | descriptor.FetchableProjection
	if artifact.Projection.Empty() || artifact.Projection&^allowed != 0 {
		return &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, ProviderID: descriptor.ID, Source: descriptor.Source, Message: "provider returned an invalid artifact projection"}
	}
	if !artifact.Projection.Has(request.Projection) {
		return &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, ProviderID: descriptor.ID, Source: descriptor.Source, Message: "provider did not materialize the requested projection", Details: map[string]any{"missing_projection": request.Projection.Missing(artifact.Projection).Strings()}}
	}
	return nil
}

// matchBatchArtifacts maps provider output to requests without assuming that a
// batch-capable provider preserves order. Index order is used only as a final
// fallback for adapters that do preserve it but omit identifiers.
func matchBatchArtifacts(reqs []kernel.ProviderFetchRequest, artifacts []kernel.Artifact) map[int]kernel.Artifact {
	out := map[int]kernel.Artifact{}
	used := make([]bool, len(artifacts))
	for i, req := range reqs {
		for j, artifact := range artifacts {
			if used[j] {
				continue
			}
			if req.Ref.CanonicalID != "" && artifact.Ref.CanonicalID == req.Ref.CanonicalID ||
				req.Ref.NativeID != "" && artifact.Ref.NativeID == req.Ref.NativeID {
				out[i] = artifact
				used[j] = true
				break
			}
		}
	}
	for i := range reqs {
		if _, ok := out[i]; ok {
			continue
		}
		if i < len(artifacts) && !used[i] {
			out[i] = artifacts[i]
			used[i] = true
		}
	}
	return out
}

func dedupFetchItems(items []kernel.FetchRequest) []kernel.FetchRequest {
	m := map[string]kernel.FetchRequest{}
	order := []string{}
	for _, i := range items {
		key := i.Ref.CanonicalID
		if key == "" {
			key = string(i.Ref.Kind) + "|" + i.Ref.NativeID + "|" + string(i.Ref.Source)
		}
		if old, ok := m[key]; ok {
			old.Projection |= i.Projection
			m[key] = old
		} else {
			m[key] = i
			order = append(order, key)
		}
	}
	out := make([]kernel.FetchRequest, 0, len(order))
	for _, k := range order {
		out = append(out, m[k])
	}
	return out
}
func (e *Engine) providerForRef(ref kernel.ObjectRef, op kernel.Operation) (kernel.Provider, error) {
	// A deliberately configured operation route has highest precedence.
	if ref.Source != "" {
		if p, ok := e.registry.ExplicitOperationRoute(ref.Source, op); ok {
			return p, nil
		}
	}
	// Otherwise preserve the provider that produced the reference when it can
	// perform the requested operation.
	if ref.ProviderID != "" {
		if p, ok := e.registry.AvailableByID(ref.ProviderID); ok && p.Descriptor().Operations[op] {
			return p, nil
		}
	}
	if ref.Source != "" {
		if p, ok := e.registry.ForOperation(ref.Source, op); ok {
			return p, nil
		}
	}
	return nil, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: "cannot determine provider for object operation", Details: map[string]any{"canonical_id": ref.CanonicalID, "source": ref.Source, "provider_id": ref.ProviderID, "operation": op}}
}
func mergeRef(a, b kernel.ObjectRef) kernel.ObjectRef {
	out := a
	if out.Platform == "" {
		out.Platform = b.Platform
	}
	if out.ScopeKey == "" {
		out.ScopeKey = b.ScopeKey
	}
	if out.Kind == "" {
		out.Kind = b.Kind
	}
	if out.NativeID == "" {
		out.NativeID = b.NativeID
	}
	if out.CanonicalID == "" {
		out.CanonicalID = b.CanonicalID
	}
	if out.ProviderID == "" {
		out.ProviderID = b.ProviderID
	}
	if out.Source == "" {
		out.Source = b.Source
	}
	if out.URL == "" {
		out.URL = b.URL
	}
	return out
}
func mergeArtifact(a, b kernel.Artifact) kernel.Artifact {
	out := a
	out.Ref = mergeRef(out.Ref, b.Ref)
	out.Projection |= b.Projection
	if out.Metadata == nil {
		out.Metadata = map[string]any{}
	}
	for k, v := range b.Metadata {
		out.Metadata[k] = v
	}
	out.Chunks = mergeChunks(out.Chunks, b.Chunks)
	if b.Summary != nil {
		out.Summary = b.Summary
	}
	out.Relations = mergeRelations(out.Relations, b.Relations)
	out.Attachments = mergeAttachments(out.Attachments, b.Attachments)
	if b.Version != "" {
		out.Version = b.Version
	}
	out.Provenance = b.Provenance
	return out
}
func mergeChunks(a, b []kernel.ContentChunk) []kernel.ContentChunk {
	seen := map[string]bool{}
	out := []kernel.ContentChunk{}
	for _, x := range append(append([]kernel.ContentChunk{}, a...), b...) {
		k := x.ID
		if k == "" {
			k = x.Kind + "|" + x.Text
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, x)
		}
	}
	return out
}
func mergeRelations(a, b []kernel.Relation) []kernel.Relation {
	seen := map[string]bool{}
	out := []kernel.Relation{}
	for _, x := range append(append([]kernel.Relation{}, a...), b...) {
		k := x.Type + "|" + x.From.CanonicalID + "|" + x.To.CanonicalID
		if !seen[k] {
			seen[k] = true
			out = append(out, x)
		}
	}
	return out
}
func mergeAttachments(a, b []kernel.Attachment) []kernel.Attachment {
	seen := map[string]bool{}
	out := []kernel.Attachment{}
	for _, x := range append(append([]kernel.Attachment{}, a...), b...) {
		k := x.ID
		if k == "" {
			k = x.Name + "|" + x.URL
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, x)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
