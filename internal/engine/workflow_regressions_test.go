package engine_test

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/engine"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/research"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/session"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
	"github.com/BlueSkyXN/Feishu-SuperSearch/provider"
)

// workflowProvider gives every search its own cursor and records actual
// provider work, so workflow assertions do not rely on reported statistics.
type workflowProvider struct {
	mu           sync.Mutex
	searches     []kernel.ProviderSearchRequest
	fetched      []string
	queryOverlap *kernel.Candidate
	beforeSearch func(context.Context, kernel.ProviderSearchRequest) error
}

func (p *workflowProvider) Descriptor() kernel.ProviderDescriptor {
	return kernel.ProviderDescriptor{
		ID: "test.workflow.docs", Source: kernel.SourceDocs, Backend: "test",
		ObjectKinds:  []kernel.ObjectKind{kernel.KindDocument},
		Operations:   kernel.OperationSet{kernel.OpSearch: true, kernel.OpQuery: true, kernel.OpFetch: true},
		SearchLimits: kernel.SearchLimits{MaxQueryRunes: 30}, BatchLimits: kernel.BatchLimits{MaxFetchItems: 1},
		ReturnedProjection: kernel.ProjectionHead, FetchableProjection: kernel.ProjectionContent | kernel.ProjectionStructure,
		SupportsPagination: true,
	}
}

func (p *workflowProvider) Search(ctx context.Context, req kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	p.mu.Lock()
	p.searches = append(p.searches, req)
	p.mu.Unlock()
	if p.beforeSearch != nil {
		if err := p.beforeSearch(ctx, req); err != nil {
			return kernel.CandidatePage{}, err
		}
	}
	if req.Cursor != "" && req.Cursor != req.Query+"-next" {
		return kernel.CandidatePage{}, fmt.Errorf("cursor %q does not belong to query %q", req.Cursor, req.Query)
	}
	page := kernel.CandidatePage{}
	for i := 1; i <= 2; i++ {
		id := fmt.Sprintf("%s_%s_%d", req.Query, req.Cursor, i)
		ref := kernel.ObjectRef{Platform: "feishu", Source: kernel.SourceDocs, Kind: kernel.KindDocument, NativeID: id, ProviderID: p.Descriptor().ID}
		page.Candidates = append(page.Candidates, kernel.Candidate{Ref: ref, Source: kernel.SourceDocs, Kind: kernel.KindDocument, Title: id, NativeRank: i, Projection: kernel.ProjectionHead, AvailableProjection: p.Descriptor().FetchableProjection})
	}
	if req.Cursor == "" {
		page.HasMore, page.NextCursor = true, req.Query+"-next"
	}
	return page, nil
}

func (p *workflowProvider) Query(_ context.Context, _ kernel.ProviderQueryRequest) (kernel.CandidatePage, error) {
	ref := kernel.ObjectRef{Platform: "feishu", Source: kernel.SourceDocs, Kind: kernel.KindDocument, NativeID: "structured-result", ProviderID: p.Descriptor().ID}
	page := kernel.CandidatePage{Candidates: []kernel.Candidate{{Ref: ref, Source: kernel.SourceDocs, Kind: kernel.KindDocument, NativeRank: 1}}, HasMore: true, NextCursor: "structured-next"}
	if p.queryOverlap != nil {
		page.Candidates = append(page.Candidates, *p.queryOverlap)
	}
	return page, nil
}

func (p *workflowProvider) Fetch(_ context.Context, requests []kernel.ProviderFetchRequest) ([]kernel.Artifact, error) {
	var out []kernel.Artifact
	for _, req := range requests {
		p.mu.Lock()
		p.fetched = append(p.fetched, req.Ref.NativeID)
		p.mu.Unlock()
		out = append(out, kernel.Artifact{Ref: req.Ref, Projection: req.Projection, Chunks: []kernel.ContentChunk{{ID: "body", Kind: "text", Text: "evidence " + req.Ref.NativeID}}})
	}
	return out, nil
}

func workflowSearch(query, sessionID string) kernel.SearchRequest {
	return kernel.SearchRequest{Query: query, SessionID: sessionID, Sources: []kernel.SourceID{kernel.SourceDocs}, Limit: 10, LimitPerSource: 2, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}}
}

func newWorkflowEngine(t *testing.T, p *workflowProvider, storeKind string) (*engine.Engine, string) {
	t.Helper()
	path := ""
	if storeKind == "sqlite" {
		path = filepath.Join(t.TempDir(), "sessions.db")
	}
	return newWorkflowEngineWithStore(t, p, storeKind, path)
}

func newWorkflowEngineWithStore(t *testing.T, p *workflowProvider, storeKind, path string) (*engine.Engine, string) {
	t.Helper()
	var store session.Store = session.NewMemoryStore(time.Hour)
	if storeKind == "sqlite" {
		var err error
		store, err = session.NewSQLiteStore(path, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
	}
	registry := provider.NewRegistry()
	if err := registry.Register(p, true); err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, store, engine.Config{ProviderTimeout: 5 * time.Second})
	t.Cleanup(func() { _ = e.Close() })
	return e, path
}

func TestConcurrentSearchPublishesCoherentPagingState(t *testing.T) {
	for _, storeKind := range []string{"memory", "sqlite"} {
		t.Run(storeKind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			p := &workflowProvider{beforeSearch: func(ctx context.Context, req kernel.ProviderSearchRequest) error {
				if req.Query != "alpha" || req.Cursor != "" {
					return nil
				}
				close(entered)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}}
			e, path := newWorkflowEngine(t, p, storeKind)
			seed, err := e.Search(ctx, workflowSearch("seed", ""))
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := e.Search(ctx, workflowSearch("alpha", seed.SessionID)); done <- err }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			before, err := e.Session(ctx, seed.SessionID, kernel.Identity{})
			if err != nil || before.Request.Query != "seed" || before.Frontiers[kernel.SourceDocs].Cursor != "seed-next" {
				t.Fatalf("in-flight search changed completed state: snapshot=%+v err=%v", before, err)
			}
			if _, err := e.Search(ctx, workflowSearch("beta", seed.SessionID)); err != nil {
				t.Fatal(err)
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if path != "" {
				if err := e.Close(); err != nil {
					t.Fatal(err)
				}
				e, _ = newWorkflowEngineWithStore(t, p, storeKind, path)
			}
			snap, err := e.Session(ctx, seed.SessionID, kernel.Identity{})
			if err != nil || snap.Request.Query != "alpha" || snap.Frontiers[kernel.SourceDocs].Cursor != "alpha-next" || len(snap.Candidates) != 6 || len(snap.LatestSearchCandidateIDs) != 2 {
				t.Fatalf("latest completed search state=%+v err=%v", snap, err)
			}
			continued, err := e.Continue(ctx, kernel.ContinueRequest{SessionID: seed.SessionID})
			if err != nil || continued.Partial || continued.Query != "alpha" || len(continued.Candidates) != 4 {
				t.Fatalf("continue=%+v err=%v", continued, err)
			}
		})
	}
}

func TestQueryPreservesLatestSearchPagingState(t *testing.T) {
	for _, storeKind := range []string{"memory", "sqlite"} {
		t.Run(storeKind, func(t *testing.T) {
			p := &workflowProvider{}
			e, _ := newWorkflowEngine(t, p, storeKind)
			ctx := context.Background()
			search, err := e.Search(ctx, workflowSearch("keyword", ""))
			if err != nil {
				t.Fatal(err)
			}
			before, _ := e.Session(ctx, search.SessionID, kernel.Identity{})
			overlap := search.Candidates[1]
			overlap.NativeRank, overlap.Title = 1, "structured query version"
			p.queryOverlap = &overlap
			query, err := e.Query(ctx, kernel.QueryRequest{SessionID: search.SessionID, Source: kernel.SourceDocs, Limit: 1})
			if err != nil || query.Continuation == nil || query.Continuation.Cursor != "structured-next" || len(query.Candidates) != 2 {
				t.Fatalf("query=%+v err=%v", query, err)
			}
			after, _ := e.Session(ctx, search.SessionID, kernel.Identity{})
			if !reflect.DeepEqual(after.Frontiers, before.Frontiers) || !reflect.DeepEqual(after.SourceRuns, before.SourceRuns) || after.Budget != before.Budget || len(after.Candidates) != len(before.Candidates)+1 {
				t.Fatalf("Query overwrote Search state: before=%+v after=%+v", before, after)
			}
			assertExistingCandidatesUnchanged(t, before, after)
			continued, err := e.Continue(ctx, kernel.ContinueRequest{SessionID: search.SessionID})
			if err != nil || continued.Partial || len(continued.Candidates) != 4 {
				t.Fatalf("continue=%+v err=%v", continued, err)
			}
		})
	}
}

func TestStaleContinueCannotOverwriteNewSearchOrContinue(t *testing.T) {
	for _, storeKind := range []string{"memory", "sqlite"} {
		for _, newerOperation := range []string{"search", "continue"} {
			t.Run(storeKind+"/"+newerOperation, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				entered, release := make(chan struct{}), make(chan struct{})
				var first sync.Once
				p := &workflowProvider{beforeSearch: func(ctx context.Context, req kernel.ProviderSearchRequest) error {
					block := false
					if req.Query == "seed" && req.Cursor != "" {
						first.Do(func() { block = true })
					}
					if !block {
						return nil
					}
					close(entered)
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}}
				e, _ := newWorkflowEngine(t, p, storeKind)
				seed, err := e.Search(ctx, workflowSearch("seed", ""))
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { _, err := e.Continue(ctx, kernel.ContinueRequest{SessionID: seed.SessionID}); done <- err }()
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if newerOperation == "search" {
					_, err = e.Search(ctx, workflowSearch("newer", seed.SessionID))
				} else {
					_, err = e.Continue(ctx, kernel.ContinueRequest{SessionID: seed.SessionID})
				}
				if err != nil {
					t.Fatal(err)
				}
				before, _ := e.Session(ctx, seed.SessionID, kernel.Identity{})
				close(release)
				if err := <-done; err == nil || kernel.DetailFromError(err).Subtype != "search_state_changed" {
					t.Fatalf("stale continue error=%v", err)
				}
				after, _ := e.Session(ctx, seed.SessionID, kernel.Identity{})
				if !reflect.DeepEqual(before.Request, after.Request) || !reflect.DeepEqual(before.Frontiers, after.Frontiers) || !reflect.DeepEqual(before.LatestSearchCandidateIDs, after.LatestSearchCandidateIDs) || !reflect.DeepEqual(before.SourceRuns, after.SourceRuns) || before.Budget != after.Budget {
					t.Fatalf("stale Continue changed paging state: before=%+v after=%+v", before, after)
				}
				assertExistingCandidatesUnchanged(t, before, after)
			})
		}
	}
}

func assertExistingCandidatesUnchanged(t *testing.T, before, after kernel.SessionSnapshot) {
	t.Helper()
	byID := map[string]kernel.Candidate{}
	for _, candidate := range after.Candidates {
		byID[candidate.Ref.CanonicalID] = candidate
	}
	for _, candidate := range before.Candidates {
		if !reflect.DeepEqual(byID[candidate.Ref.CanonicalID], candidate) {
			t.Fatalf("existing search candidate changed: before=%+v after=%+v", candidate, byID[candidate.Ref.CanonicalID])
		}
	}
}

type workflowReranker func(context.Context, []kernel.Candidate) ([]kernel.Candidate, error)

func (workflowReranker) Name() string { return "test-reranker" }
func (f workflowReranker) Rerank(ctx context.Context, _ string, candidates []kernel.Candidate) ([]kernel.Candidate, error) {
	return f(ctx, candidates)
}

func TestResearchRerankContinuesConsumedBudget(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		calls, fetches, wantFetches int
		partial                     bool
	}{
		{"fetch_budget_exhausted", 3, 1, 1, true},
		{"call_budget_exhausted", 2, 2, 1, true},
		{"remaining_budget", 3, 2, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &workflowProvider{}
			e, _ := newWorkflowEngine(t, p, "memory")
			var deadline time.Time
			reranker := workflowReranker(func(ctx context.Context, candidates []kernel.Candidate) ([]kernel.Candidate, error) {
				deadline, _ = ctx.Deadline()
				out := append([]kernel.Candidate(nil), candidates...)
				out[0], out[1] = out[1], out[0]
				return out, nil
			})
			req := research.Request{UserRequest: planner.UserRequest{Query: "budget", Sources: []kernel.SourceID{kernel.SourceDocs}, Deep: true, FetchTopK: 1, Budget: kernel.SearchBudget{MaxCalls: tc.calls, MaxFetches: tc.fetches, MaxPagesPerSource: 1, DeadlineMS: 5000}}}
			result, err := (research.Service{Kernel: e, Reranker: reranker}).Run(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			p.mu.Lock()
			fetches, calls := len(p.fetched), len(p.searches)+len(p.fetched)
			p.mu.Unlock()
			if fetches != tc.wantFetches || calls != 1+tc.wantFetches || len(result.ArtifactPack.Artifacts) != tc.wantFetches {
				t.Fatalf("actual calls=%d fetches=%d artifacts=%d", calls, fetches, len(result.ArtifactPack.Artifacts))
			}
			state := result.PlanResult.Budget
			if state.CallsUsed != calls || state.FetchesUsed != fetches || result.PlanResult.Partial != tc.partial || (len(result.Warnings) > 0) != tc.partial {
				t.Fatalf("reported state=%+v partial=%v warnings=%v", state, result.PlanResult.Partial, result.Warnings)
			}
			if deadline.IsZero() || !deadline.Equal(state.Deadline) {
				t.Fatalf("rerank deadline=%v plan deadline=%v", deadline, state.Deadline)
			}
		})
	}
}

func TestResearchRulesPassCompactedQueryToProvider(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{"请帮我搜索项目延期", "项目延期"},
		{"请帮我搜索最近一个月关于项目延期原因以及相关会议讨论和未完成任务的资料", "最近一个月关于项目延期原因以及相关会议讨论和未完成任务的资料"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			p := &workflowProvider{}
			e, _ := newWorkflowEngine(t, p, "memory")
			result, err := (research.Service{Kernel: e}).Run(context.Background(), research.Request{UserRequest: planner.UserRequest{Query: tc.query, Sources: []kernel.SourceID{kernel.SourceDocs}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}}})
			if err != nil || result.PlanResult.Partial || result.CandidatePack.Query != tc.query {
				t.Fatalf("research=%+v err=%v", result, err)
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			if len(p.searches) != 1 || p.searches[0].Query != tc.want {
				t.Fatalf("provider searches=%+v want query=%q", p.searches, tc.want)
			}
		})
	}
}
