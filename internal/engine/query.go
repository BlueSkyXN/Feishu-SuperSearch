package engine

import (
	"context"
	"time"

	budgetctl "github.com/BlueSkyXN/Feishu-SuperSearch/internal/budget"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/fusion"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/session"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func (e *Engine) Query(ctx context.Context, req kernel.QueryRequest) (kernel.QuerySnapshot, error) {
	started := time.Now()
	req.Identity = req.Identity.Normalized()
	if req.Source == "" {
		return kernel.QuerySnapshot{}, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "source is required"}
	}
	if req.Limit <= 0 {
		req.Limit = 50
	}
	req.Budget = req.Budget.WithDefaults()
	rec, err := e.getOrCreateSession(ctx, req.SessionID, req.Identity)
	if err != nil {
		return kernel.QuerySnapshot{}, err
	}
	budget := budgetctl.ForRequest(ctx, req.Budget)
	callCtx, cancel := context.WithDeadline(ctx, budget.Deadline())
	defer cancel()
	p, ok := e.registry.ForOperation(req.Source, kernel.OpQuery)
	if !ok {
		return kernel.QuerySnapshot{}, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Source: req.Source, Message: "provider not registered"}
	}
	if err := validateProviderOperation(p, kernel.OpQuery, req.Identity); err != nil {
		return kernel.QuerySnapshot{}, err
	}
	q, ok := p.(kernel.Querier)
	if !ok {
		return kernel.QuerySnapshot{}, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, ProviderID: p.Descriptor().ID, Source: req.Source, Message: "provider does not implement query"}
	}
	if !budget.Reserve(kernel.CostEstimate{Calls: 1}) {
		return kernel.QuerySnapshot{}, &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, Message: "query call budget exhausted"}
	}
	oneCtx, oneCancel := context.WithTimeout(callCtx, e.cfg.ProviderTimeout)
	page, err := q.Query(oneCtx, kernel.ProviderQueryRequest{Container: req.Container, Filter: req.Filter, Sort: req.Sort, Identity: req.Identity, Limit: req.Limit, Cursor: req.Cursor, SessionID: rec.ID})
	oneCancel()
	if err == nil && !budget.AddBytes(encodedSize(page)) {
		err = &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, ProviderID: p.Descriptor().ID, Source: req.Source, Message: "query response exceeded byte budget"}
		page.HasMore = false
		page.NextCursor = ""
	}
	run := kernel.SourceRun{ProviderID: p.Descriptor().ID, Source: req.Source, Pages: 1, ElapsedMS: time.Since(started).Milliseconds()}
	if err != nil {
		run.Status = statusForError(err)
		run.Error = kernel.DetailFromError(err)
	} else if len(page.Candidates) == 0 {
		run.Status = kernel.StatusEmpty
	} else {
		run.Status = kernel.StatusOK
	}
	run.HitCount = len(page.Candidates)
	for i := range page.Candidates {
		c := fusion.Canonicalize(page.Candidates[i], req.Identity.ScopeKey)
		if c.Ref.ProviderID == "" {
			c.Ref.ProviderID = p.Descriptor().ID
		}
		page.Candidates[i] = c
	}
	cont := (*kernel.Continuation)(nil)
	if page.HasMore {
		c := kernel.Continuation{ProviderID: p.Descriptor().ID, Source: req.Source, Cursor: page.NextCursor, HasMore: true, PagesFetched: 1}
		cont = &c
	}
	state := budget.Snapshot()
	rec.WithLock(func(r *session.Record) {
		for _, c := range page.Candidates {
			r.Candidates[c.Ref.CanonicalID] = c
		}
		if cont != nil {
			r.Frontiers[req.Source] = *cont
		}
		r.SourceRuns = append(r.SourceRuns, run)
		r.Budget = state
	})
	snap := kernel.QuerySnapshot{SessionID: rec.ID, Source: req.Source, Candidates: page.Candidates, Continuation: cont, SourceRun: run, Budget: state}
	if saveErr := e.sessions.Save(ctx, rec); saveErr != nil {
		return snap, sessionPersistenceError(saveErr, "save_query")
	}
	if err != nil {
		return snap, err
	}
	return snap, nil
}
