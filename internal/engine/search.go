package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	budgetctl "github.com/BlueSkyXN/Feishu-SuperSearch/internal/budget"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/fusion"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/session"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type sourcePageResult struct {
	source   kernel.SourceID
	provider kernel.ProviderID
	page     kernel.CandidatePage
	run      kernel.SourceRun
	err      error
}

func (e *Engine) Search(ctx context.Context, req kernel.SearchRequest) (kernel.SearchSnapshot, error) {
	started := time.Now()
	req = req.WithDefaults()
	if err := req.Validate(); err != nil {
		return kernel.SearchSnapshot{}, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: err.Error()}
	}
	rec, err := e.getOrCreateSession(ctx, req.SessionID, req.Identity)
	if err != nil {
		return kernel.SearchSnapshot{}, err
	}
	req.SessionID = rec.ID
	rec.WithLock(func(r *session.Record) { r.Request = &req })
	if history, ok := e.sessions.(session.QueryHistoryStore); ok {
		if err := history.RecordQuery(ctx, rec.ID, req); err != nil {
			return kernel.SearchSnapshot{}, sessionPersistenceError(err, "record_query")
		}
	}
	e.addEvent(rec, kernel.EventSessionStarted, "", map[string]any{"query": req.Query})
	sources := req.Sources
	if len(sources) == 0 {
		for _, s := range kernel.GlobalSources {
			if _, ok := e.registry.ForOperation(s, kernel.OpSearch); ok {
				sources = append(sources, s)
			}
		}
	}
	sources = orderedSources(sources)
	if len(sources) == 0 {
		return kernel.SearchSnapshot{}, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: "no searchable providers are registered"}
	}
	budget := budgetctl.ForRequest(ctx, req.Budget)
	deadline := budget.Deadline()
	searchCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	first := e.searchWave(searchCtx, rec, req, sources, nil, budget)
	all := collectCandidates(first)
	runs := runsFromResults(first)
	frontiers := frontiersFromResults(first)
	selected, allScored, unique := rankCandidates(req, all)

	maxPages := req.Budget.MaxPagesPerSource
	if maxPages > 1 && req.Strategy.Pagination != "none" {
		for round := 2; round <= maxPages; round++ {
			moreSources := chooseNextSources(req, selected, first, frontiers)
			if len(moreSources) == 0 {
				break
			}
			cursors := map[kernel.SourceID]string{}
			for _, s := range moreSources {
				cursors[s] = frontiers[s].Cursor
			}
			next := e.searchWave(searchCtx, rec, req, moreSources, cursors, budget)
			if len(next) == 0 {
				break
			}
			all = append(all, collectCandidates(next)...)
			mergeRuns(runs, next)
			mergeFrontiers(frontiers, next)
			selected, allScored, unique = rankCandidates(req, all)
			first = next
		}
	}
	budgetState := budget.Snapshot()
	rec.WithLock(func(r *session.Record) {
		r.Candidates = map[string]kernel.Candidate{}
		for _, c := range allScored {
			r.Candidates[c.Ref.CanonicalID] = c
		}
		r.Frontiers = frontiers
		r.SourceRuns = sortedRuns(runs)
		r.Budget = budgetState
	})
	partial, failed := partialFromRuns(runs)
	stats := kernel.SearchStats{RawCandidates: len(all), UniqueCandidates: unique, Returned: len(selected), ElapsedMS: time.Since(started).Milliseconds(), SourcesCompleted: len(runs) - failed, SourcesFailed: failed}
	snapshot := kernel.SearchSnapshot{SessionID: rec.ID, Query: req.Query, Candidates: selected, Sources: sortedRuns(runs), Continuations: sortedContinuations(frontiers), Partial: partial, Budget: budgetState, Stats: stats}
	if partial {
		e.addEvent(rec, kernel.EventSessionPartial, "", map[string]any{"failed_sources": failed})
	} else {
		e.addEvent(rec, kernel.EventSessionCompleted, "", map[string]any{"returned": len(selected)})
	}
	if err := e.sessions.Save(ctx, rec); err != nil {
		return snapshot, sessionPersistenceError(err, "save_search")
	}
	if len(selected) == 0 && failed == len(runs) {
		return snapshot, aggregateSearchFailure(runs)
	}
	return snapshot, nil
}

func (e *Engine) Continue(ctx context.Context, req kernel.ContinueRequest) (kernel.SearchSnapshot, error) {
	if req.SessionID == "" {
		return kernel.SearchSnapshot{}, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "session_id is required"}
	}
	req.Identity = req.Identity.Normalized()
	rec, err := e.sessionForIdentity(ctx, req.SessionID, req.Identity)
	if err != nil {
		return kernel.SearchSnapshot{}, err
	}
	snap := rec.Snapshot(false)
	if snap.Request == nil {
		return kernel.SearchSnapshot{}, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "session has no search request"}
	}
	base := snap.Request.WithDefaults()
	if req.MaxAdditionalPages <= 0 {
		req.MaxAdditionalPages = 1
	}
	if req.MaxAdditionalPages > 5 {
		return kernel.SearchSnapshot{}, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "max_additional_pages must not exceed 5"}
	}
	sources := req.Sources
	if len(sources) == 0 {
		for s, f := range snap.Frontiers {
			if f.HasMore {
				sources = append(sources, s)
			}
		}
	}
	sources = orderedSources(sources)
	if len(sources) == 0 {
		return kernel.SearchSnapshot{SessionID: rec.ID, Query: base.Query, Candidates: snap.Candidates, Sources: snap.SourceRuns, Continuations: sortedContinuations(snap.Frontiers), Budget: snap.Budget}, nil
	}
	state := snap.Budget
	state.MaxCalls += len(sources) * req.MaxAdditionalPages
	state.Deadline = time.Now().Add(time.Duration(base.Budget.DeadlineMS) * time.Millisecond)
	budget := budgetctl.FromState(state)
	continueCtx, cancel := context.WithDeadline(ctx, state.Deadline)
	defer cancel()
	runs := map[kernel.SourceID]kernel.SourceRun{}
	for _, r := range snap.SourceRuns {
		runs[r.Source] = r
	}
	frontiers := snap.Frontiers
	all := append([]kernel.Candidate(nil), snap.Candidates...)
	for round := 0; round < req.MaxAdditionalPages; round++ {
		active := []kernel.SourceID{}
		cursors := map[kernel.SourceID]string{}
		for _, s := range sources {
			f := frontiers[s]
			if f.HasMore && f.Cursor != "" {
				active = append(active, s)
				cursors[s] = f.Cursor
			}
		}
		if len(active) == 0 {
			break
		}
		res := e.searchWave(continueCtx, rec, base, active, cursors, budget)
		all = append(all, collectCandidates(res)...)
		mergeRuns(runs, res)
		mergeFrontiers(frontiers, res)
	}
	selected, allScored, unique := rankCandidates(base, all)
	budgetState := budget.Snapshot()
	rec.WithLock(func(r *session.Record) {
		r.Candidates = map[string]kernel.Candidate{}
		for _, c := range allScored {
			r.Candidates[c.Ref.CanonicalID] = c
		}
		r.Frontiers = frontiers
		r.SourceRuns = sortedRuns(runs)
		r.Budget = budgetState
	})
	partial, failed := partialFromRuns(runs)
	snapshot := kernel.SearchSnapshot{SessionID: rec.ID, Query: base.Query, Candidates: selected, Sources: sortedRuns(runs), Continuations: sortedContinuations(frontiers), Partial: partial, Budget: budgetState, Stats: kernel.SearchStats{RawCandidates: len(all), UniqueCandidates: unique, Returned: len(selected), SourcesCompleted: len(runs) - failed, SourcesFailed: failed}}
	if err := e.sessions.Save(ctx, rec); err != nil {
		return snapshot, sessionPersistenceError(err, "save_continue")
	}
	return snapshot, nil
}

func (e *Engine) searchWave(ctx context.Context, rec *session.Record, req kernel.SearchRequest, sources []kernel.SourceID, cursors map[kernel.SourceID]string, budget budgetctl.Account) []sourcePageResult {
	sem := make(chan struct{}, e.cfg.GlobalConcurrency)
	ch := make(chan sourcePageResult, len(sources))
	var wg sync.WaitGroup
	for _, source := range sources {
		source := source
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ch <- e.searchOne(ctx, rec, req, source, cursors[source], budget)
		}()
	}
	wg.Wait()
	close(ch)
	out := make([]sourcePageResult, 0, len(sources))
	for r := range ch {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].source < out[j].source })
	return out
}
func (e *Engine) searchOne(ctx context.Context, rec *session.Record, req kernel.SearchRequest, source kernel.SourceID, cursor string, budget budgetctl.Account) sourcePageResult {
	started := time.Now()
	result := sourcePageResult{source: source, run: kernel.SourceRun{Source: source, Status: kernel.StatusFailed}}
	p, ok := e.registry.ForOperation(source, kernel.OpSearch)
	if !ok {
		result.err = &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Source: source, Message: "provider not registered"}
		result.run.Error = kernel.DetailFromError(result.err)
		result.run.Status = kernel.StatusUnavailable
		return result
	}
	result.provider = p.Descriptor().ID
	result.run.ProviderID = result.provider
	if err := validateProviderOperation(p, kernel.OpSearch, req.Identity); err != nil {
		result.err = err
		result.run.Error = kernel.DetailFromError(err)
		result.run.Status = statusForError(err)
		return result
	}
	searcher, ok := p.(kernel.Searcher)
	if !ok {
		result.err = &kernel.ErrorDetail{Type: kernel.ErrUnsupported, ProviderID: result.provider, Source: source, Message: "provider does not implement search"}
		result.run.Error = kernel.DetailFromError(result.err)
		result.run.Status = kernel.StatusUnavailable
		return result
	}
	providerQuery := strings.TrimSpace(req.SourceQueries[source])
	if providerQuery == "" {
		providerQuery = req.Query
	}
	if max := p.Descriptor().SearchLimits.MaxQueryRunes; max > 0 && len([]rune(providerQuery)) > max {
		result.err = &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, ProviderID: result.provider, Source: source, Message: fmt.Sprintf("query exceeds provider limit of %d runes", max), Details: map[string]any{"query": providerQuery}}
		result.run.Error = kernel.DetailFromError(result.err)
		return result
	}
	if !budget.Reserve(kernel.CostEstimate{Calls: 1}) {
		result.err = &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, ProviderID: result.provider, Source: source, Message: "search call budget exhausted"}
		result.run.Error = kernel.DetailFromError(result.err)
		result.run.Status = kernel.StatusBudget
		return result
	}
	e.addEvent(rec, kernel.EventNodeStarted, source, map[string]any{"provider_id": result.provider, "cursor": cursor, "query": providerQuery})
	providerReq := kernel.ProviderSearchRequest{Query: providerQuery, Filters: req.Filters, Identity: req.Identity, PageSize: req.LimitPerSource, Cursor: cursor, SessionID: rec.ID}
	call := func() (kernel.CandidatePage, error) {
		callCtx, cancel := context.WithTimeout(ctx, e.cfg.ProviderTimeout)
		defer cancel()
		return searcher.Search(callCtx, providerReq)
	}
	page, err := call()
	if err != nil {
		d := kernel.DetailFromError(err)
		if d.Type == kernel.ErrRateLimited || d.Type == kernel.ErrUpstreamTransient {
			timer := time.NewTimer(150 * time.Millisecond)
			select {
			case <-timer.C:
				if budget.Reserve(kernel.CostEstimate{Calls: 1}) {
					page, err = call()
				}
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
			}
		}
	}
	if err == nil && !budget.AddBytes(encodedSize(page)) {
		err = &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, ProviderID: result.provider, Source: source, Message: "search response exceeded byte budget"}
		page.HasMore = false
		page.NextCursor = ""
	}
	result.page = page
	result.err = err
	result.run.Pages = 1
	result.run.HitCount = len(page.Candidates)
	result.run.ElapsedMS = time.Since(started).Milliseconds()
	if err != nil {
		result.run.Status = statusForError(err)
		result.run.Error = kernel.DetailFromError(err)
	} else if len(page.Candidates) == 0 {
		result.run.Status = kernel.StatusEmpty
	} else {
		result.run.Status = kernel.StatusOK
	}
	for i := range result.page.Candidates {
		c := &result.page.Candidates[i]
		if c.Ref.ProviderID == "" {
			c.Ref.ProviderID = result.provider
		}
		if c.Source == "" {
			c.Source = source
		}
		if c.Ref.Source == "" {
			c.Ref.Source = source
		}
		if c.NativeRank <= 0 {
			c.NativeRank = i + 1
		}
		if len(c.DiscoveredBy) == 0 {
			c.DiscoveredBy = []kernel.Discovery{{ProviderID: result.provider, Source: source, Rank: c.NativeRank, Score: c.NativeScore, Cursor: cursor}}
		}
		e.addEvent(rec, kernel.EventCandidateUpsert, source, map[string]any{"candidate": *c})
	}
	e.addEvent(rec, kernel.EventSourceCompleted, source, map[string]any{"run": result.run})
	return result
}

func collectCandidates(results []sourcePageResult) []kernel.Candidate {
	out := []kernel.Candidate{}
	for _, r := range results {
		out = append(out, r.page.Candidates...)
	}
	return out
}
func rankCandidates(req kernel.SearchRequest, all []kernel.Candidate) ([]kernel.Candidate, []kernel.Candidate, int) {
	weights := fusion.DefaultWeights()
	for k, v := range req.Strategy.Weights {
		weights[k] = v
	}
	quotas := fusion.DefaultQuotas()
	for k, v := range req.Strategy.Quotas {
		quotas[k] = v
	}
	allScored, unique := fusion.Fuse(req.Query, all, len(all), fusion.Config{K0: req.Strategy.K0, Weights: weights, Quotas: quotas, UseQuota: false}, req.Identity.ScopeKey)
	selected, _ := fusion.Fuse(req.Query, all, req.Limit, fusion.Config{K0: req.Strategy.K0, Weights: weights, Quotas: quotas, UseQuota: req.Strategy.SourceQuota}, req.Identity.ScopeKey)
	return selected, allScored, unique
}
func runsFromResults(results []sourcePageResult) map[kernel.SourceID]kernel.SourceRun {
	m := map[kernel.SourceID]kernel.SourceRun{}
	for _, r := range results {
		m[r.source] = r.run
	}
	return m
}
func frontiersFromResults(results []sourcePageResult) map[kernel.SourceID]kernel.Continuation {
	m := map[kernel.SourceID]kernel.Continuation{}
	for _, r := range results {
		m[r.source] = kernel.Continuation{ProviderID: r.provider, Source: r.source, Cursor: r.page.NextCursor, HasMore: r.page.HasMore, PagesFetched: r.run.Pages}
	}
	return m
}
func mergeRuns(m map[kernel.SourceID]kernel.SourceRun, results []sourcePageResult) {
	for _, r := range results {
		old := m[r.source]
		old.ProviderID = r.run.ProviderID
		old.Source = r.source
		old.Pages += r.run.Pages
		old.HitCount += r.run.HitCount
		old.ElapsedMS += r.run.ElapsedMS
		if r.run.Status == kernel.StatusFailed || r.run.Status == kernel.StatusMissingScope || r.run.Status == kernel.StatusDeadline {
			old.Status = r.run.Status
			old.Error = r.run.Error
		} else if old.Status == "" || old.Status == kernel.StatusEmpty {
			old.Status = r.run.Status
		}
		m[r.source] = old
	}
}
func mergeFrontiers(m map[kernel.SourceID]kernel.Continuation, results []sourcePageResult) {
	for _, r := range results {
		old := m[r.source]
		old.ProviderID = r.provider
		old.Source = r.source
		old.Cursor = r.page.NextCursor
		old.HasMore = r.page.HasMore
		old.PagesFetched++
		m[r.source] = old
	}
}
func sortedRuns(m map[kernel.SourceID]kernel.SourceRun) []kernel.SourceRun {
	out := make([]kernel.SourceRun, 0, len(m))
	for _, r := range m {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}
func sortedContinuations(m map[kernel.SourceID]kernel.Continuation) []kernel.Continuation {
	out := make([]kernel.Continuation, 0, len(m))
	for _, c := range m {
		if c.HasMore {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}

func aggregateSearchFailure(runs map[kernel.SourceID]kernel.SourceRun) *kernel.ErrorDetail {
	details := map[string]any{}
	counts := map[kernel.ErrorType]int{}
	retryable := false
	for source, run := range runs {
		if run.Error == nil {
			continue
		}
		counts[run.Error.Type]++
		if run.Error.Retryable || run.Error.Type == kernel.ErrRateLimited || run.Error.Type == kernel.ErrUpstreamTransient {
			retryable = true
		}
		details[string(source)] = run.Error
	}
	total := len(runs)
	typeOf := kernel.ErrUpstreamPermanent
	switch {
	case total > 0 && counts[kernel.ErrMissingScope] == total:
		typeOf = kernel.ErrMissingScope
	case total > 0 && counts[kernel.ErrBudgetExhausted] == total:
		typeOf = kernel.ErrBudgetExhausted
	case total > 0 && counts[kernel.ErrDeadlineExceeded] == total:
		typeOf = kernel.ErrDeadlineExceeded
	case retryable:
		typeOf = kernel.ErrUpstreamTransient
	}
	return &kernel.ErrorDetail{Type: typeOf, Message: "all search sources failed", Retryable: retryable, Details: details}
}

func partialFromRuns(m map[kernel.SourceID]kernel.SourceRun) (bool, int) {
	failed := 0
	for _, r := range m {
		if r.Status != kernel.StatusOK && r.Status != kernel.StatusEmpty {
			failed++
		}
	}
	return failed > 0, failed
}
func chooseNextSources(req kernel.SearchRequest, selected []kernel.Candidate, last []sourcePageResult, frontiers map[kernel.SourceID]kernel.Continuation) []kernel.SourceID {
	if req.Strategy.Pagination == "fixed" || req.Strategy.Pagination == "all" {
		out := []kernel.SourceID{}
		for s, f := range frontiers {
			if f.HasMore {
				out = append(out, s)
			}
		}
		return orderedSources(out)
	}
	top := map[kernel.SourceID]bool{}
	counts := map[kernel.SourceID]int{}
	for _, c := range selected {
		top[c.Source] = true
		counts[c.Source]++
	}
	out := []kernel.SourceID{}
	for _, r := range last {
		f := frontiers[r.source]
		quota := req.Strategy.Quotas[r.source]
		if quota <= 0 {
			quota = fusion.DefaultQuotas()[r.source]
		}
		if f.HasMore && (top[r.source] || counts[r.source] < quota) {
			out = append(out, r.source)
		}
	}
	return orderedSources(out)
}
