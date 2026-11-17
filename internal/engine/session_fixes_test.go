package engine_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/engine"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/session"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/provider"
)

// --- shared helpers -------------------------------------------------------

// fetchProjectionProvider serves content and summary as separate projections
// and blocks in Fetch until the test releases it, so two concurrent requests
// can be forced into a deterministic interleaving.
type fetchProjectionProvider struct {
	descriptor kernel.ProviderDescriptor

	mu         sync.Mutex
	inFlight   int
	entered    chan struct{}
	release    chan struct{}
	fetchCalls int
}

func newFetchProjectionProvider() *fetchProjectionProvider {
	return &fetchProjectionProvider{
		descriptor: kernel.ProviderDescriptor{
			ID:                  "test.proj.docs",
			Source:              kernel.SourceDocs,
			ObjectKinds:         []kernel.ObjectKind{kernel.KindDocument},
			Operations:          kernel.OperationSet{kernel.OpSearch: true, kernel.OpFetch: true},
			RequiredIdentity:    []kernel.IdentityMode{kernel.IdentityAuto},
			BatchLimits:         kernel.BatchLimits{MaxFetchItems: 1},
			FetchableProjection: kernel.ProjectionContent | kernel.ProjectionSummary,
			ReturnedProjection:  kernel.ProjectionHead,
			Backend:             "test",
			Version:             "1",
		},
		entered: make(chan struct{}, 4),
		release: make(chan struct{}),
	}
}

func (p *fetchProjectionProvider) Descriptor() kernel.ProviderDescriptor { return p.descriptor }
func (p *fetchProjectionProvider) Health(context.Context, kernel.Identity) (string, error) {
	return "test/1", nil
}
func (p *fetchProjectionProvider) Search(_ context.Context, r kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	scope := r.Identity.Normalized().ScopeKey
	ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: scope, Kind: kernel.KindDocument, NativeID: "doc_proj", ProviderID: p.descriptor.ID, Source: kernel.SourceDocs}
	return kernel.CandidatePage{Candidates: []kernel.Candidate{{Ref: ref, Source: kernel.SourceDocs, Kind: kernel.KindDocument, Title: "并发读取目标", NativeRank: 1, Projection: kernel.ProjectionHead}}, RawCount: 1}, nil
}
func (p *fetchProjectionProvider) Fetch(ctx context.Context, reqs []kernel.ProviderFetchRequest) ([]kernel.Artifact, error) {
	p.mu.Lock()
	p.inFlight++
	p.fetchCalls++
	p.mu.Unlock()
	p.entered <- struct{}{}
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	p.mu.Lock()
	p.inFlight--
	p.mu.Unlock()
	out := make([]kernel.Artifact, 0, len(reqs))
	for _, r := range reqs {
		a := kernel.Artifact{Ref: r.Ref, Projection: r.Projection, Provenance: kernel.Provenance{ProviderID: p.descriptor.ID, Backend: "test", Operation: "fetch", RetrievedAt: time.Now().UTC()}}
		if r.Projection&kernel.ProjectionContent != 0 {
			a.Chunks = []kernel.ContentChunk{{ID: "c1", Kind: "text", Text: "正文内容"}}
		}
		if r.Projection&kernel.ProjectionSummary != 0 {
			a.Summary = &kernel.NativeSummary{Text: "摘要内容"}
		}
		out = append(out, a)
	}
	return out, nil
}
func (p *fetchProjectionProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fetchCalls
}

func fetchProjRef(scope string) kernel.ObjectRef {
	return kernel.ObjectRef{Platform: "feishu", ScopeKey: scope, Kind: kernel.KindDocument, NativeID: "doc_proj", CanonicalID: kernel.BuildCanonicalID(scope, kernel.KindDocument, "doc_proj"), ProviderID: "test.proj.docs", Source: kernel.SourceDocs}
}

// --- 问题 A：并发 Fetch 写回合并 -------------------------------------------

// TestConcurrentFetchOfDifferentProjectionsKeepsBoth verifies that two
// concurrent Fetches of the same object — one reading content, one reading
// summary — both survive in the session. The barriers force both requests to
// read the same (old) session snapshot before either provider call returns,
// then release content first and summary second, which is the interleaving
// that lost content before the merge-on-writeback fix.
func TestConcurrentFetchOfDifferentProjectionsKeepsBoth(t *testing.T) {
	for _, storeKind := range []string{"memory", "sqlite"} {
		t.Run(storeKind, func(t *testing.T) {
			p := newFetchProjectionProvider()
			registry := provider.NewRegistry()
			if err := registry.Register(p, true); err != nil {
				t.Fatal(err)
			}
			var store session.Store
			if storeKind == "sqlite" {
				s, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sfs.db"), time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				store = s
			} else {
				store = session.NewMemoryStore(time.Hour)
			}
			e := engine.New(registry, store, engine.Config{GlobalConcurrency: 4, ProviderTimeout: 5 * time.Second, SessionTTL: time.Hour})
			defer e.Close()
			ctx := context.Background()
			id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()

			// Discover the object in a session first.
			search, err := e.Search(ctx, kernel.SearchRequest{Query: "并发", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
			if err != nil {
				t.Fatal(err)
			}
			ref := search.Candidates[0].Ref

			bothEntered := make(chan struct{}, 1)
			go func() {
				<-p.entered
				<-p.entered
				close(bothEntered)
			}()

			type outcome struct {
				batch kernel.ArtifactBatch
				err   error
			}
			outcomes := make(chan outcome, 2)
			start := make(chan struct{})
			for _, projection := range []kernel.ProjectionSet{kernel.ProjectionContent, kernel.ProjectionSummary} {
				projection := projection
				go func() {
					<-start
					batch, err := e.Fetch(ctx, kernel.FetchBatchRequest{SessionID: search.SessionID, Identity: id, Items: []kernel.FetchRequest{{Ref: ref, Projection: projection}}})
					outcomes <- outcome{batch: batch, err: err}
				}()
			}
			close(start)
			select {
			case <-bothEntered:
			case <-time.After(5 * time.Second):
				t.Fatal("both fetch requests never entered the provider")
			}

			// Let content finish and save first, then summary.
			p.release <- struct{}{}
			done := 0
			for done < 1 {
				select {
				case o := <-outcomes:
					if o.err != nil {
						t.Fatal(o.err)
					}
					done++
				case <-time.After(5 * time.Second):
					t.Fatal("content fetch did not return")
				}
			}
			// Ensure the content request fully saved its session before summary returns.
			time.Sleep(50 * time.Millisecond)
			p.release <- struct{}{}
			for done < 2 {
				select {
				case o := <-outcomes:
					if o.err != nil {
						t.Fatal(o.err)
					}
					done++
				case <-time.After(5 * time.Second):
					t.Fatal("summary fetch did not return")
				}
			}
			if p.calls() != 2 {
				t.Fatalf("provider calls=%d want 2", p.calls())
			}

			snap, err := e.Session(ctx, search.SessionID, id)
			if err != nil {
				t.Fatal(err)
			}
			var stored *kernel.Artifact
			for i := range snap.Artifacts {
				if snap.Artifacts[i].Ref.CanonicalID == ref.CanonicalID {
					stored = &snap.Artifacts[i]
				}
			}
			if stored == nil {
				t.Fatalf("no artifact stored; artifacts=%+v", snap.Artifacts)
			}
			if !stored.Projection.Has(kernel.ProjectionContent) || !stored.Projection.Has(kernel.ProjectionSummary) {
				t.Fatalf("stored projection=%v want content|summary", stored.Projection.Strings())
			}
			if len(stored.Chunks) != 1 || stored.Chunks[0].Text != "正文内容" {
				t.Fatalf("stored chunks=%+v want content chunk", stored.Chunks)
			}
			if stored.Summary == nil || stored.Summary.Text != "摘要内容" {
				t.Fatalf("stored summary=%+v", stored.Summary)
			}

			// The union must now be a cache hit without another provider call.
			union, err := e.Fetch(ctx, kernel.FetchBatchRequest{SessionID: search.SessionID, Identity: id, Items: []kernel.FetchRequest{{Ref: ref, Projection: kernel.ProjectionContent | kernel.ProjectionSummary}}})
			if err != nil {
				t.Fatal(err)
			}
			if len(union.Items) != 1 || !union.Items[0].Cached || union.Items[0].Artifact == nil {
				t.Fatalf("union fetch items=%+v", union.Items)
			}
			a := union.Items[0].Artifact
			if !a.Projection.Has(kernel.ProjectionContent) || !a.Projection.Has(kernel.ProjectionSummary) || len(a.Chunks) != 1 || a.Summary == nil {
				t.Fatalf("cached artifact=%+v", a)
			}
			if p.calls() != 2 {
				t.Fatalf("union fetch should hit cache, calls=%d", p.calls())
			}
		})
	}
}

// TestConcurrentFetchUnionDoesNotLockOutPartialProjection verifies that a
// concurrent request for content+summary does not mark the session artifact
// as fully read while a separate content request is still in flight; after
// both finish, fetching summary alone must still be a cache hit (union kept
// both) and fetching content alone must also hit.
func TestConcurrentFetchUnionDoesNotLockOutPartialProjection(t *testing.T) {
	p := newFetchProjectionProvider()
	registry := provider.NewRegistry()
	if err := registry.Register(p, true); err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{GlobalConcurrency: 4, ProviderTimeout: 5 * time.Second, SessionTTL: time.Hour})
	defer e.Close()
	ctx := context.Background()
	id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()
	search, err := e.Search(ctx, kernel.SearchRequest{Query: "并发", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil {
		t.Fatal(err)
	}
	ref := search.Candidates[0].Ref

	bothEntered := make(chan struct{}, 1)
	go func() {
		<-p.entered
		<-p.entered
		close(bothEntered)
	}()
	outcomes := make(chan kernel.ArtifactBatch, 2)
	errs := make(chan error, 2)
	start := make(chan struct{})
	requests := []kernel.ProjectionSet{kernel.ProjectionContent | kernel.ProjectionSummary, kernel.ProjectionContent}
	for _, projection := range requests {
		projection := projection
		go func() {
			<-start
			batch, err := e.Fetch(ctx, kernel.FetchBatchRequest{SessionID: search.SessionID, Identity: id, Items: []kernel.FetchRequest{{Ref: ref, Projection: projection}}})
			outcomes <- batch
			errs <- err
		}()
	}
	close(start)
	<-bothEntered
	p.release <- struct{}{}
	p.release <- struct{}{}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		<-outcomes
	}
	for _, projection := range requests {
		cached, err := e.Fetch(ctx, kernel.FetchBatchRequest{SessionID: search.SessionID, Identity: id, Items: []kernel.FetchRequest{{Ref: ref, Projection: projection}}})
		if err != nil {
			t.Fatal(err)
		}
		if len(cached.Items) != 1 || !cached.Items[0].Cached {
			t.Fatalf("projection %v should be cached after union, items=%+v", projection.Strings(), cached.Items)
		}
	}
}

// --- 问题 B：Continue 只合并最近一次搜索 -----------------------------------

// multiSourcePagedProvider serves two sources with independently paged
// candidates whose match set depends on the query.
type multiSourcePagedProvider struct {
	source kernel.SourceID
	titles []string
}

func (p *multiSourcePagedProvider) Descriptor() kernel.ProviderDescriptor {
	return kernel.ProviderDescriptor{ID: kernel.ProviderID("test.multi." + p.source), Source: p.source, ObjectKinds: []kernel.ObjectKind{kernel.KindDocument}, Operations: kernel.OperationSet{kernel.OpSearch: true}, ReturnedProjection: kernel.ProjectionHead, SupportsPagination: true, Backend: "test"}
}
func (p *multiSourcePagedProvider) Search(_ context.Context, r kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	pageSize := r.PageSize
	if pageSize <= 0 {
		pageSize = 10
	}
	offset := 0
	if r.Cursor != "" {
		for _, c := range r.Cursor {
			offset = offset*10 + int(c-'0')
		}
	}
	end := offset + pageSize
	if end > len(p.titles) {
		end = len(p.titles)
	}
	page := kernel.CandidatePage{}
	scope := r.Identity.Normalized().ScopeKey
	for i := offset; i < end; i++ {
		id := string(p.source) + "_" + string(rune('a'+i))
		ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: scope, Kind: kernel.KindDocument, NativeID: id, ProviderID: p.Descriptor().ID, Source: p.source}
		page.Candidates = append(page.Candidates, kernel.Candidate{Ref: ref, Source: p.source, Kind: kernel.KindDocument, Title: p.titles[i], NativeRank: i - offset + 1, Projection: kernel.ProjectionHead})
	}
	if end < len(p.titles) {
		page.HasMore = true
		page.NextCursor = itoa(end)
	}
	page.RawCount = len(page.Candidates)
	return page, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func newMultiSourceEngine(t *testing.T, store session.Store) *engine.Engine {
	t.Helper()
	registry := provider.NewRegistry()
	for _, p := range []*multiSourcePagedProvider{
		{source: kernel.SourceDocs, titles: []string{"docs 第1条", "docs 第2条", "docs 第3条"}},
		{source: kernel.SourceTasks, titles: []string{"tasks 第1条", "tasks 第2条", "tasks 第3条"}},
	} {
		if err := registry.Register(p, true); err != nil {
			t.Fatal(err)
		}
	}
	return engine.New(registry, store, engine.Config{GlobalConcurrency: 4, ProviderTimeout: time.Second, SessionTTL: time.Hour})
}

// TestContinueDoesNotMixEarlierSearchCandidates reproduces: search docs first,
// then search tasks in the same session, continue tasks, and the result must
// contain only task candidates.
func TestContinueDoesNotMixEarlierSearchCandidates(t *testing.T) {
	e := newMultiSourceEngine(t, session.NewMemoryStore(time.Hour))
	defer e.Close()
	ctx := context.Background()
	id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()

	first, err := e.Search(ctx, kernel.SearchRequest{Query: "目标", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, LimitPerSource: 2, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Candidates) == 0 {
		t.Fatal("first search returned nothing")
	}
	second, err := e.Search(ctx, kernel.SearchRequest{SessionID: first.SessionID, Query: "目标", Sources: []kernel.SourceID{kernel.SourceTasks}, Identity: id, LimitPerSource: 1, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Candidates) != 1 {
		t.Fatalf("second search candidates=%d", len(second.Candidates))
	}

	more, err := e.Continue(ctx, kernel.ContinueRequest{SessionID: first.SessionID, Sources: []kernel.SourceID{kernel.SourceTasks}, Identity: id, MaxAdditionalPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range more.Candidates {
		if c.Source != kernel.SourceTasks {
			t.Fatalf("continue mixed candidate from %s: %+v", c.Source, c)
		}
	}
	if len(more.Candidates) != 2 {
		t.Fatalf("continue candidates=%d want 2 tasks", len(more.Candidates))
	}
	titles := map[string]bool{}
	for _, c := range more.Candidates {
		titles[c.Title] = true
	}
	for _, want := range []string{"tasks 第1条", "tasks 第2条"} {
		if !titles[want] {
			t.Fatalf("continue missing %q, got %v", want, titles)
		}
	}

	// History stays fetchable in the same session.
	snap, err := e.Session(ctx, first.SessionID, id)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[kernel.SourceID]bool{}
	for _, c := range snap.Candidates {
		sources[c.Source] = true
	}
	if !sources[kernel.SourceDocs] || !sources[kernel.SourceTasks] {
		t.Fatalf("session lost historical candidates: %v", sources)
	}
}

// TestContinueSameSourceNewQueryStaysOnNewQuery verifies that continuing
// after re-searching the same source with a different query (candidates are
// the same titles here, but ownership must track the latest search) does not
// resurrect candidates of the older query from another source.
func TestContinueSameSourceNewQueryStaysOnNewQuery(t *testing.T) {
	e := newMultiSourceEngine(t, session.NewMemoryStore(time.Hour))
	defer e.Close()
	ctx := context.Background()
	id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()

	first, err := e.Search(ctx, kernel.SearchRequest{Query: "旧查询", Sources: []kernel.SourceID{kernel.SourceDocs, kernel.SourceTasks}, Identity: id, LimitPerSource: 2, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Search(ctx, kernel.SearchRequest{SessionID: first.SessionID, Query: "新查询", Sources: []kernel.SourceID{kernel.SourceTasks}, Identity: id, LimitPerSource: 1, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil {
		t.Fatal(err)
	}
	more, err := e.Continue(ctx, kernel.ContinueRequest{SessionID: first.SessionID, Sources: []kernel.SourceID{kernel.SourceTasks}, Identity: id, MaxAdditionalPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range more.Candidates {
		if c.Source != kernel.SourceTasks {
			t.Fatalf("continue mixed candidate from %s: %+v", c.Source, c)
		}
	}
}

// TestContinueScopeSurvivesSQLiteRestart verifies the ownership scope is
// persisted and a fresh process reading the same database keeps continue
// scoped to the latest search.
func TestContinueScopeSurvivesSQLiteRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sfs.db")
	id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()
	ctx := context.Background()

	func() {
		e := newMultiSourceEngine(t, mustSQLite(t, path))
		defer e.Close()
		first, err := e.Search(ctx, kernel.SearchRequest{Query: "目标", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, LimitPerSource: 2, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
		if err != nil {
			t.Fatal(err)
		}
		second, err := e.Search(ctx, kernel.SearchRequest{SessionID: first.SessionID, Query: "目标", Sources: []kernel.SourceID{kernel.SourceTasks}, Identity: id, LimitPerSource: 1, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if len(second.Candidates) != 1 {
			t.Fatalf("second search candidates=%d", len(second.Candidates))
		}
	}()

	e := newMultiSourceEngine(t, mustSQLite(t, path))
	defer e.Close()
	sessions, err := e.Sessions(ctx, id)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
	sid := sessions[0].ID
	more, err := e.Continue(ctx, kernel.ContinueRequest{SessionID: sid, Sources: []kernel.SourceID{kernel.SourceTasks}, Identity: id, MaxAdditionalPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range more.Candidates {
		if c.Source != kernel.SourceTasks {
			t.Fatalf("continue after restart mixed candidate from %s: %+v", c.Source, c)
		}
	}
	if len(more.Candidates) != 2 {
		t.Fatalf("continue after restart candidates=%d want 2", len(more.Candidates))
	}
}

func mustSQLite(t *testing.T, path string) session.Store {
	t.Helper()
	s, err := session.NewSQLiteStore(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestContinueWithoutContinuableSourcesReturnsLatestSearchScope verifies the
// early return branch: with pagination exhausted for the latest search, the
// response reports the latest search's results, not the whole session
// history, and still respects partial state.
func TestContinueWithoutContinuableSourcesReturnsLatestSearchScope(t *testing.T) {
	e := newMultiSourceEngine(t, session.NewMemoryStore(time.Hour))
	defer e.Close()
	ctx := context.Background()
	id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()

	first, err := e.Search(ctx, kernel.SearchRequest{Query: "目标", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, LimitPerSource: 10, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.Search(ctx, kernel.SearchRequest{SessionID: first.SessionID, Query: "目标", Sources: []kernel.SourceID{kernel.SourceTasks}, Identity: id, LimitPerSource: 10, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Candidates) != 3 {
		t.Fatalf("second search candidates=%d", len(second.Candidates))
	}

	more, err := e.Continue(ctx, kernel.ContinueRequest{SessionID: first.SessionID, Sources: []kernel.SourceID{kernel.SourceTasks}, Identity: id, MaxAdditionalPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range more.Candidates {
		if c.Source != kernel.SourceTasks {
			t.Fatalf("no-continuable continue mixed candidate from %s: %+v", c.Source, c)
		}
	}
	if len(more.Candidates) != 3 {
		t.Fatalf("no-continuable continue candidates=%d want latest search's 3 tasks", len(more.Candidates))
	}
}

// --- 问题 C：分页预算耗尽状态与游标 -----------------------------------------

// TestSearchBudgetExhaustedOnLaterPageKeepsCursor reproduces the reported
// request shape: one candidate per page, max_calls=1, fixed pagination with
// two pages. The first page succeeds, the second page is never executed
// because the call budget is gone. The response must be partial with a
// budget error, keep the first-page cursor, and Continue must fetch the
// remaining tasks without duplicates or skips.
func TestSearchBudgetExhaustedOnLaterPageKeepsCursor(t *testing.T) {
	registry := provider.NewRegistry()
	if err := registry.Register(&pagedSearchProvider{pageSize: 1, total: 2}, true); err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e.Close()
	ctx := context.Background()
	id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()

	snap, err := e.Search(ctx, kernel.SearchRequest{
		Query: "A 项目", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id,
		LimitPerSource: 1, Budget: kernel.SearchBudget{MaxCalls: 1, MaxPagesPerSource: 2, DeadlineMS: 8000},
		Strategy: kernel.SearchStrategy{Pagination: "fixed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Candidates) != 1 {
		t.Fatalf("candidates=%d want 1", len(snap.Candidates))
	}
	if !snap.Partial {
		t.Fatal("budget-exhausted later page must mark snapshot partial")
	}
	if len(snap.Sources) != 1 {
		t.Fatalf("sources=%+v", snap.Sources)
	}
	run := snap.Sources[0]
	// The source already returned candidates; a budget-truncated later page
	// must keep the concrete budget error and mark the source partial.
	if run.Status != kernel.StatusPartial {
		t.Fatalf("source status=%s want %s", run.Status, kernel.StatusPartial)
	}
	if run.Error == nil || run.Error.Type != kernel.ErrBudgetExhausted {
		t.Fatalf("source error=%+v want budget_exhausted", run.Error)
	}
	if run.Pages != 1 {
		t.Fatalf("pages=%d want 1 (uncalled page must not count)", run.Pages)
	}
	if len(snap.Continuations) != 1 {
		t.Fatalf("continuations=%+v want the first-page cursor", snap.Continuations)
	}
	cont := snap.Continuations[0]
	if cont.Cursor == "" || !cont.HasMore || cont.PagesFetched != 1 {
		t.Fatalf("continuation=%+v", cont)
	}

	more, err := e.Continue(ctx, kernel.ContinueRequest{SessionID: snap.SessionID, Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, MaxAdditionalPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(more.Candidates) != 2 {
		t.Fatalf("continue candidates=%d want 2 (first + second page)", len(more.Candidates))
	}
	seen := map[string]bool{}
	for _, c := range more.Candidates {
		if seen[c.Title] {
			t.Fatalf("duplicate candidate %s", c.Title)
		}
		seen[c.Title] = true
	}
	if more.Candidates[0].Title != "doc_1" || more.Candidates[1].Title != "doc_2" {
		t.Fatalf("continue order=%v,%v want doc_1,doc_2 (no skip)", more.Candidates[0].Title, more.Candidates[1].Title)
	}
	if more.Partial {
		t.Fatal("successful retry must clear the budget-truncation state")
	}
	run = more.Sources[0]
	if run.Status != kernel.StatusOK || run.Error != nil {
		t.Fatalf("after retry run=%+v", run)
	}
}

// TestSearchUpstreamErrorOnLaterPageKeepsCursor covers a later-page upstream
// failure (not only budget): partial status, kept cursor, and a Continue that
// recovers the remaining rows.
func TestSearchUpstreamErrorOnLaterPageKeepsCursor(t *testing.T) {
	registry := provider.NewRegistry()
	if err := registry.Register(&cursorFailThenSucceedProvider{pagedSearchProvider: pagedSearchProvider{pageSize: 1, total: 2}, failCursors: map[string]bool{"1": true}}, true); err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e.Close()
	ctx := context.Background()
	id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()

	snap, err := e.Search(ctx, kernel.SearchRequest{
		Query: "A 项目", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id,
		LimitPerSource: 1, Budget: kernel.SearchBudget{MaxCalls: 4, MaxPagesPerSource: 2, DeadlineMS: 8000},
		Strategy: kernel.SearchStrategy{Pagination: "fixed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Candidates) != 1 || !snap.Partial {
		t.Fatalf("candidates=%d partial=%v", len(snap.Candidates), snap.Partial)
	}
	run := snap.Sources[0]
	if run.Status != kernel.StatusPartial || run.Error == nil || run.Error.Type != kernel.ErrUpstreamPermanent {
		t.Fatalf("run=%+v want partial with upstream error", run)
	}
	if run.Pages != 1 {
		t.Fatalf("run pages=%d want 1 (the failed page must not count)", run.Pages)
	}
	if len(snap.Continuations) != 1 || snap.Continuations[0].Cursor == "" || snap.Continuations[0].PagesFetched != 1 {
		t.Fatalf("continuations=%+v", snap.Continuations)
	}

	more, err := e.Continue(ctx, kernel.ContinueRequest{SessionID: snap.SessionID, Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, MaxAdditionalPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(more.Candidates) != 2 || more.Candidates[0].Title != "doc_1" || more.Candidates[1].Title != "doc_2" {
		t.Fatalf("continue candidates=%+v", more.Candidates)
	}
	if more.Partial || more.Sources[0].Status != kernel.StatusOK || more.Sources[0].Error != nil {
		t.Fatalf("after recovery snapshot=%+v sources=%+v", more, more.Sources)
	}
}

// cursorFailThenSucceedProvider fails specific cursors once so a later page
// fails on the first attempt and succeeds on Continue.
type cursorFailThenSucceedProvider struct {
	pagedSearchProvider
	failCursors map[string]bool
}

func (p *cursorFailThenSucceedProvider) Search(ctx context.Context, req kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	if p.failCursors[req.Cursor] {
		p.failCursors[req.Cursor] = false
		// permanent errors bypass searchOne's transient-retry, so the wave
		// ends with exactly one failed page and the next attempt succeeds.
		return kernel.CandidatePage{}, &kernel.ErrorDetail{Type: kernel.ErrUpstreamPermanent, Message: "page 2 upstream error"}
	}
	return p.pagedSearchProvider.Search(ctx, req)
}

// TestSearchSourceWithoutResultsKeepsFailureStatus verifies the no-results
// case with deterministic failures: a source whose provider rejects every
// call keeps its concrete failure status (not ok), and a source with no
// searchable provider reports unavailable with zero pages (the page was
// never executed, so it must not count as read).
func TestSearchSourceWithoutResultsKeepsFailureStatus(t *testing.T) {
	registry := provider.NewRegistry()
	if err := registry.Register(&pagedSearchProvider{pageSize: 1, total: 2}, true); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&missingScopeSearchProvider{}, true); err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{GlobalConcurrency: 4, ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e.Close()
	snap, err := e.Search(context.Background(), kernel.SearchRequest{
		Query: "A 项目", Sources: []kernel.SourceID{kernel.SourceDocs, kernel.SourceTasks, kernel.SourceChats}, Identity: kernel.Identity{Mode: kernel.IdentityAuto},
		LimitPerSource: 1, Budget: kernel.SearchBudget{MaxCalls: 4, MaxPagesPerSource: 1, DeadlineMS: 8000},
		Strategy: kernel.SearchStrategy{Pagination: "fixed"},
	})
	if err != nil {
		t.Fatalf("search with failing sources err=%v (want snapshot with candidates)", err)
	}
	if !snap.Partial {
		t.Fatal("a failing source must mark the snapshot partial")
	}
	bySource := map[kernel.SourceID]kernel.SourceRun{}
	for _, run := range snap.Sources {
		bySource[run.Source] = run
	}
	if run := bySource[kernel.SourceDocs]; run.Status != kernel.StatusOK || run.Pages != 1 {
		t.Fatalf("docs run=%+v", run)
	}
	called := bySource[kernel.SourceTasks]
	if called.Status != kernel.StatusMissingScope || called.Error == nil || called.Pages != 0 {
		t.Fatalf("tasks run=%+v want missing_scope whose failed page does not count as read", called)
	}
	neverRan := bySource[kernel.SourceChats]
	if neverRan.Status != kernel.StatusUnavailable || neverRan.Error == nil || neverRan.Pages != 0 {
		t.Fatalf("chats run=%+v want unavailable with no pages", neverRan)
	}
	if len(snap.Continuations) != 1 || snap.Continuations[0].Source != kernel.SourceDocs {
		t.Fatalf("continuations=%+v want only the docs cursor", snap.Continuations)
	}
}

// missingScopeSearchProvider fails every search call with missing_scope.
type missingScopeSearchProvider struct{}

func (p *missingScopeSearchProvider) Descriptor() kernel.ProviderDescriptor {
	return kernel.ProviderDescriptor{ID: "test.scope.tasks", Source: kernel.SourceTasks, ObjectKinds: []kernel.ObjectKind{kernel.KindTask}, Operations: kernel.OperationSet{kernel.OpSearch: true}, RequiredIdentity: []kernel.IdentityMode{kernel.IdentityAuto}, ReturnedProjection: kernel.ProjectionHead, Backend: "test"}
}

func (p *missingScopeSearchProvider) Search(context.Context, kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	return kernel.CandidatePage{}, &kernel.ErrorDetail{Type: kernel.ErrMissingScope, Message: "tasks scope required"}
}

// --- 复核返工回归（2026-11-17 rework） ---------------------------------------

// queryMatchProvider returns different match sets per query, so switching the
// query genuinely changes which candidates the latest search owns.
type queryMatchProvider struct {
	source kernel.SourceID
	titles map[string][]string // query -> titles
}

func (p *queryMatchProvider) Descriptor() kernel.ProviderDescriptor {
	return kernel.ProviderDescriptor{ID: kernel.ProviderID("test.qmatch." + p.source), Source: p.source, ObjectKinds: []kernel.ObjectKind{kernel.KindDocument}, Operations: kernel.OperationSet{kernel.OpSearch: true, kernel.OpFetch: true}, FetchableProjection: kernel.ProjectionContent, ReturnedProjection: kernel.ProjectionHead, SupportsPagination: true, Backend: "test"}
}

func (p *queryMatchProvider) Fetch(_ context.Context, reqs []kernel.ProviderFetchRequest) ([]kernel.Artifact, error) {
	out := make([]kernel.Artifact, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, kernel.Artifact{Ref: r.Ref, Projection: r.Projection, Chunks: []kernel.ContentChunk{{ID: "c", Text: "历史对象正文"}}})
	}
	return out, nil
}

func (p *queryMatchProvider) Search(_ context.Context, r kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	titles := p.titles[strings.TrimSpace(r.Query)]
	page := kernel.CandidatePage{}
	scope := r.Identity.Normalized().ScopeKey
	for i, title := range titles {
		id := string(p.source) + "_" + strings.Map(func(r rune) rune {
			if r < 'a' || r > 'z' {
				return '_'
			}
			return r
		}, strings.ToLower(title))
		ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: scope, Kind: kernel.KindDocument, NativeID: id, ProviderID: p.Descriptor().ID, Source: p.source}
		page.Candidates = append(page.Candidates, kernel.Candidate{Ref: ref, Source: p.source, Kind: kernel.KindDocument, Title: title, NativeRank: i + 1, Projection: kernel.ProjectionHead})
	}
	page.RawCount = len(page.Candidates)
	return page, nil
}

// R1: a partially cached fetch must return the requested union, not just the
// missing delta, while the provider is still asked only for the delta.
func TestFetchUnionIncludesAlreadyCachedProjection(t *testing.T) {
	p := newFetchProjectionProvider()
	close(p.release)
	registry := provider.NewRegistry()
	if err := registry.Register(p, true); err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{GlobalConcurrency: 4, ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e.Close()
	ctx := context.Background()
	id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()
	found, err := e.Search(ctx, kernel.SearchRequest{Query: "target", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil {
		t.Fatal(err)
	}
	ref := found.Candidates[0].Ref
	fetch := func(proj kernel.ProjectionSet) kernel.ArtifactBatch {
		t.Helper()
		b, err := e.Fetch(ctx, kernel.FetchBatchRequest{SessionID: found.SessionID, Identity: id, Items: []kernel.FetchRequest{{Ref: ref, Projection: proj}}})
		if err != nil || b.Partial || len(b.Items) != 1 || b.Items[0].Artifact == nil {
			t.Fatalf("fetch: %+v err=%v", b, err)
		}
		return b
	}

	// content first, then content|summary.
	first := fetch(kernel.ProjectionContent)
	if !first.Items[0].Artifact.Projection.Has(kernel.ProjectionContent) || len(first.Items[0].Artifact.Chunks) != 1 {
		t.Fatalf("content fetch=%+v", first.Items[0].Artifact)
	}
	beforeUnion := p.calls()
	union := kernel.ProjectionContent | kernel.ProjectionSummary
	got := fetch(union).Items[0].Artifact
	if !got.Projection.Has(union) || len(got.Chunks) != 1 || got.Summary == nil {
		t.Fatalf("union response=%+v want cached content plus fresh summary", got)
	}
	if p.calls()-beforeUnion != 1 {
		t.Fatalf("union fetch provider calls=%d want 1 (delta only)", p.calls()-beforeUnion)
	}
	stored, err := e.Session(ctx, found.SessionID, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range stored.Artifacts {
		if a.Ref.CanonicalID == ref.CanonicalID && (!a.Projection.Has(union) || len(a.Chunks) != 1 || a.Summary == nil) {
			t.Fatalf("stored artifact=%+v", a)
		}
	}
	// Fully cached union must not call the provider at all.
	beforeCached := p.calls()
	if cached := fetch(union); !cached.Items[0].Cached {
		t.Fatalf("fully cached union should hit the session cache: %+v", cached.Items[0])
	}
	if p.calls() != beforeCached {
		t.Fatalf("cached union made %d provider calls", p.calls()-beforeCached)
	}
}

// R1: summary first, then union — the summary-only cache must not drop
// content from the union response either.
func TestFetchUnionAfterSummaryIncludesCachedSummary(t *testing.T) {
	p := newFetchProjectionProvider()
	close(p.release)
	registry := provider.NewRegistry()
	if err := registry.Register(p, true); err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{GlobalConcurrency: 4, ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e.Close()
	ctx := context.Background()
	id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()
	found, err := e.Search(ctx, kernel.SearchRequest{Query: "target", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil {
		t.Fatal(err)
	}
	ref := found.Candidates[0].Ref
	fetch := func(proj kernel.ProjectionSet) *kernel.Artifact {
		t.Helper()
		b, err := e.Fetch(ctx, kernel.FetchBatchRequest{SessionID: found.SessionID, Identity: id, Items: []kernel.FetchRequest{{Ref: ref, Projection: proj}}})
		if err != nil || b.Partial || len(b.Items) != 1 || b.Items[0].Artifact == nil {
			t.Fatalf("fetch: %+v err=%v", b, err)
		}
		return b.Items[0].Artifact
	}
	if got := fetch(kernel.ProjectionSummary); got.Summary == nil || len(got.Chunks) != 0 {
		t.Fatalf("summary fetch=%+v", got)
	}
	union := kernel.ProjectionContent | kernel.ProjectionSummary
	got := fetch(union)
	if !got.Projection.Has(union) || len(got.Chunks) != 1 || got.Summary == nil {
		t.Fatalf("union after summary=%+v", got)
	}
}

// R2: an empty latest search must not resurrect historical candidates, across
// memory, sqlite, and sqlite after a process restart.
func TestEmptyLatestSearchDoesNotRestoreHistory(t *testing.T) {
	for _, mode := range []string{"memory", "sqlite", "sqlite-restart"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rework.db")
			var store session.Store = session.NewMemoryStore(time.Hour)
			if mode != "memory" {
				store = mustSQLite(t, path)
			}
			p := &queryMatchProvider{source: kernel.SourceDocs, titles: map[string][]string{
				"old query": {"历史结果"},
				"new query": nil,
			}}
			registry := provider.NewRegistry()
			if err := registry.Register(p, true); err != nil {
				t.Fatal(err)
			}
			e := engine.New(registry, store, engine.Config{GlobalConcurrency: 4, ProviderTimeout: time.Second, SessionTTL: time.Hour})
			defer func() { _ = e.Close() }()
			ctx := context.Background()
			id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()
			first, err := e.Search(ctx, kernel.SearchRequest{Query: "old query", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
			if err != nil || len(first.Candidates) != 1 {
				t.Fatalf("first=%+v err=%v", first, err)
			}
			second, err := e.Search(ctx, kernel.SearchRequest{SessionID: first.SessionID, Query: "new query", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
			if err != nil || len(second.Candidates) != 0 {
				t.Fatalf("second=%+v err=%v", second, err)
			}
			if mode == "sqlite-restart" {
				if err := e.Close(); err != nil {
					t.Fatal(err)
				}
				e = engine.New(registry, mustSQLite(t, path), engine.Config{GlobalConcurrency: 4, ProviderTimeout: time.Second, SessionTTL: time.Hour})
				defer func() { _ = e.Close() }()
			}
			ss, err := e.Session(ctx, first.SessionID, id)
			if err != nil {
				t.Fatal(err)
			}
			if ss.LatestSearchCandidateIDs == nil {
				t.Fatal("explicitly empty latest search must persist a non-nil empty ID list")
			}
			if len(ss.LatestSearchCandidateIDs) != 0 {
				t.Fatalf("latest IDs=%v want empty", ss.LatestSearchCandidateIDs)
			}
			more, err := e.Continue(ctx, kernel.ContinueRequest{SessionID: first.SessionID, Identity: id})
			if err != nil {
				t.Fatal(err)
			}
			if len(more.Candidates) != 0 {
				t.Fatalf("continue resurrected %d historical candidates", len(more.Candidates))
			}
			// Historical objects stay usable for Fetch in the same session.
			historical := first.Candidates[0].Ref
			batch, err := e.Fetch(ctx, kernel.FetchBatchRequest{SessionID: first.SessionID, Identity: id, Items: []kernel.FetchRequest{{Ref: historical, Projection: kernel.ProjectionContent}}})
			if err != nil || len(batch.Items) != 1 || batch.Items[0].Error != nil {
				t.Fatalf("historical fetch=%+v err=%v", batch, err)
			}
		})
	}
}

// R2: first page empty but more pages advertised still counts as an
// explicitly-known (empty) scope, and a later query switching the match set
// updates ownership.
func TestEmptyFirstPageSearchScopesOwnership(t *testing.T) {
	p := &queryMatchProvider{source: kernel.SourceDocs, titles: map[string][]string{"alpha": {"结果一"}}}
	registry := provider.NewRegistry()
	if err := registry.Register(p, true); err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{GlobalConcurrency: 4, ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e.Close()
	ctx := context.Background()
	id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()

	first, err := e.Search(ctx, kernel.SearchRequest{Query: "alpha", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, LimitPerSource: 1, Strategy: kernel.SearchStrategy{Pagination: "fixed"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil || len(first.Candidates) != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	// Switch to a query that matches nothing; the ownership becomes an
	// explicitly empty list and Continue must return nothing.
	_, err = e.Search(ctx, kernel.SearchRequest{SessionID: first.SessionID, Query: "beta", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, LimitPerSource: 1, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil {
		t.Fatal(err)
	}
	more, err := e.Continue(ctx, kernel.ContinueRequest{SessionID: first.SessionID, Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	if len(more.Candidates) != 0 {
		t.Fatalf("continue=%+v want none", more.Candidates)
	}
	// Switching back restores ownership of the matching candidate.
	back, err := e.Search(ctx, kernel.SearchRequest{SessionID: first.SessionID, Query: "alpha", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, LimitPerSource: 1, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil || len(back.Candidates) != 1 || back.Candidates[0].Title != "结果一" {
		t.Fatalf("back=%+v err=%v", back, err)
	}
}

// R3: Continue with no remaining pages must still honor the search's limit.
func TestContinueWithoutSourcesHonorsLimit(t *testing.T) {
	e := newMultiSourceEngine(t, session.NewMemoryStore(time.Hour))
	defer e.Close()
	ctx := context.Background()
	id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()
	first, err := e.Search(ctx, kernel.SearchRequest{Query: "target", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, Limit: 1, LimitPerSource: 10, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil || len(first.Candidates) != 1 || len(first.Continuations) != 0 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	more, err := e.Continue(ctx, kernel.ContinueRequest{SessionID: first.SessionID, Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	if len(more.Candidates) != 1 {
		t.Fatalf("continue returned %d candidates, want limit=1 honored", len(more.Candidates))
	}
	if more.Stats.Returned != 1 {
		t.Fatalf("stats.returned=%d want 1", more.Stats.Returned)
	}
}

// R3: an explicitly exhausted source takes the same early-return path.
func TestContinueWithExhaustedSourceHonorsLimit(t *testing.T) {
	e := newMultiSourceEngine(t, session.NewMemoryStore(time.Hour))
	defer e.Close()
	ctx := context.Background()
	id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()
	first, err := e.Search(ctx, kernel.SearchRequest{Query: "target", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, Limit: 1, LimitPerSource: 10, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil {
		t.Fatal(err)
	}
	more, err := e.Continue(ctx, kernel.ContinueRequest{SessionID: first.SessionID, Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	if len(more.Candidates) != 1 || more.Stats.Returned != 1 {
		t.Fatalf("continue candidates=%d stats.returned=%d want 1", len(more.Candidates), more.Stats.Returned)
	}
}

// R3: partial state and source errors survive the no-remaining-pages path.
func TestContinueWithoutSourcesPreservesPartial(t *testing.T) {
	registry := provider.NewRegistry()
	if err := registry.Register(&queryMatchProvider{source: kernel.SourceDocs, titles: map[string][]string{"target": {"唯一结果"}}}, true); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&missingScopeSearchProvider{}, true); err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{GlobalConcurrency: 4, ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e.Close()
	ctx := context.Background()
	id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()
	first, err := e.Search(ctx, kernel.SearchRequest{Query: "target", Sources: []kernel.SourceID{kernel.SourceDocs, kernel.SourceTasks}, Identity: id, LimitPerSource: 10, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil || !first.Partial || len(first.Continuations) != 0 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	more, err := e.Continue(ctx, kernel.ContinueRequest{SessionID: first.SessionID, Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	if !more.Partial {
		t.Fatal("continue with no remaining pages cleared partial")
	}
	bySource := sourceStatuses(more.Sources)
	if bySource[kernel.SourceTasks].Error == nil {
		t.Fatal("tasks source error was dropped")
	}
	if more.Stats.SourcesFailed != 1 || more.Stats.SourcesCompleted != 1 {
		t.Fatalf("stats=%+v", more.Stats)
	}
}

func sourceStatuses(runs []kernel.SourceRun) map[kernel.SourceID]kernel.SourceRun {
	m := map[kernel.SourceID]kernel.SourceRun{}
	for _, r := range runs {
		m[r.Source] = r
	}
	return m
}

// R4: an empty final page must not flip a source with accumulated hits to
// empty, in both automatic pagination and Continue.
func TestEmptyFinalPageKeepsNonemptySourceStatus(t *testing.T) {
	for _, mode := range []string{"automatic", "continue", "retry-then-empty", "all-empty"} {
		t.Run(mode, func(t *testing.T) {
			registry := provider.NewRegistry()
			p := &emptyFinalPageProvider{}
			if mode == "retry-then-empty" {
				p.failFirstCursorPage = true
			}
			if mode == "all-empty" {
				p.emptyFromStart = true
			}
			if err := registry.Register(p, true); err != nil {
				t.Fatal(err)
			}
			e := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{GlobalConcurrency: 4, ProviderTimeout: time.Second, SessionTTL: time.Hour})
			defer e.Close()
			ctx := context.Background()
			id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()
			strategy := "fixed"
			if mode == "continue" || mode == "retry-then-empty" {
				strategy = "none"
			}
			snap, err := e.Search(ctx, kernel.SearchRequest{Query: "target", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, LimitPerSource: 1, Strategy: kernel.SearchStrategy{Pagination: strategy}, Budget: kernel.SearchBudget{MaxPagesPerSource: 2}})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "continue" || mode == "retry-then-empty" {
				snap, err = e.Continue(ctx, kernel.ContinueRequest{SessionID: snap.SessionID, Identity: id, Sources: []kernel.SourceID{kernel.SourceDocs}})
				if err != nil {
					t.Fatal(err)
				}
			}
			run := snap.Sources[0]
			switch mode {
			case "all-empty":
				if len(snap.Candidates) != 0 || run.Status != kernel.StatusEmpty || run.HitCount != 0 {
					t.Fatalf("all-empty: candidates=%d run=%+v", len(snap.Candidates), run)
				}
			case "retry-then-empty":
				// page 1 ok, retried page 2 failed once, then a further
				// Continue recovers into an empty final page: hits stay, and
				// the recovered state must be ok, not empty.
				more, err := e.Continue(ctx, kernel.ContinueRequest{SessionID: snap.SessionID, Identity: id, Sources: []kernel.SourceID{kernel.SourceDocs}})
				if err != nil {
					t.Fatal(err)
				}
				run = more.Sources[0]
				if run.HitCount != 1 || run.Status != kernel.StatusOK || run.Error != nil {
					t.Fatalf("retry-then-empty run=%+v want ok with 1 hit", run)
				}
			default:
				if len(snap.Candidates) != 1 || run.Status != kernel.StatusOK || run.HitCount != 1 {
					t.Fatalf("%s: candidates=%d run=%+v", mode, len(snap.Candidates), run)
				}
			}
		})
	}
}

// emptyFinalPageProvider serves one hit on the first page and a truly empty
// second page; it can also fail the second page once or be empty throughout.
type emptyFinalPageProvider struct {
	failFirstCursorPage bool
	emptyFromStart      bool
	mu                  sync.Mutex
	failedOnce          bool
}

func (p *emptyFinalPageProvider) Descriptor() kernel.ProviderDescriptor {
	return kernel.ProviderDescriptor{ID: "test.emptyfinal.docs", Source: kernel.SourceDocs, ObjectKinds: []kernel.ObjectKind{kernel.KindDocument}, Operations: kernel.OperationSet{kernel.OpSearch: true}, ReturnedProjection: kernel.ProjectionHead, SupportsPagination: true, Backend: "test"}
}

func (p *emptyFinalPageProvider) Search(_ context.Context, r kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.emptyFromStart {
		return kernel.CandidatePage{}, nil
	}
	if r.Cursor == "" {
		scope := r.Identity.Normalized().ScopeKey
		ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: scope, Kind: kernel.KindDocument, NativeID: "only_result", ProviderID: p.Descriptor().ID, Source: kernel.SourceDocs}
		return kernel.CandidatePage{Candidates: []kernel.Candidate{{Ref: ref, Source: kernel.SourceDocs, Kind: kernel.KindDocument, Title: "唯一结果", NativeRank: 1, Projection: kernel.ProjectionHead}}, HasMore: true, NextCursor: "1", RawCount: 1}, nil
	}
	if p.failFirstCursorPage && !p.failedOnce {
		p.failedOnce = true
		return kernel.CandidatePage{}, &kernel.ErrorDetail{Type: kernel.ErrUpstreamPermanent, Message: "later page failed once"}
	}
	return kernel.CandidatePage{}, nil
}

// R2 boundary: an empty first page that still advertises a real cursor must
// keep the latest search's ownership and let Continue fetch the new query's
// next page (not the previous query's results).
func TestEmptyFirstPageWithRealCursorScopesContinue(t *testing.T) {
	p := &emptyFirstCursorProvider{multiSourcePagedProvider{source: kernel.SourceDocs, titles: []string{"旧查询结果"}}}
	registry := provider.NewRegistry()
	if err := registry.Register(p, true); err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{GlobalConcurrency: 4, ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e.Close()
	ctx := context.Background()
	id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()

	old, err := e.Search(ctx, kernel.SearchRequest{Query: "old", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil || len(old.Candidates) != 1 {
		t.Fatalf("old=%+v err=%v", old, err)
	}
	empty, err := e.Search(ctx, kernel.SearchRequest{SessionID: old.SessionID, Query: "new", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil || len(empty.Candidates) != 0 || len(empty.Continuations) != 1 {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
	if empty.Continuations[0].Cursor == "" || !empty.Continuations[0].HasMore {
		t.Fatalf("continuation=%+v want a real cursor", empty.Continuations[0])
	}
	more, err := e.Continue(ctx, kernel.ContinueRequest{SessionID: old.SessionID, Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	if len(more.Candidates) != 1 || more.Candidates[0].Title != "new result" {
		t.Fatalf("continue=%+v want only the new query's next page", more.Candidates)
	}
}

// emptyFirstCursorProvider: query "old" returns one hit; query "new" returns
// an empty first page that still has a real next cursor, then one hit.
type emptyFirstCursorProvider struct {
	multiSourcePagedProvider
}

func (p *emptyFirstCursorProvider) Search(ctx context.Context, req kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	if req.Query == "old" {
		return p.multiSourcePagedProvider.Search(ctx, req)
	}
	if req.Cursor == "" {
		return kernel.CandidatePage{HasMore: true, NextCursor: "next"}, nil
	}
	ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: req.Identity.Normalized().ScopeKey, Kind: kernel.KindDocument, NativeID: "new_query_result", ProviderID: p.Descriptor().ID, Source: kernel.SourceDocs}
	return kernel.CandidatePage{Candidates: []kernel.Candidate{{Ref: ref, Source: kernel.SourceDocs, Kind: kernel.KindDocument, Title: "new result", NativeRank: 1, Projection: kernel.ProjectionHead}}, RawCount: 1}, nil
}

// R2 compatibility: the latest-search ownership field must survive
// JSON round-trips in all four wire shapes — field missing (legacy
// snapshots), null (unknown scope), [] (explicitly empty search), and a
// non-empty list — with nil and empty staying distinct.
func TestOwnershipIDsSurviveJSONRoundTrip(t *testing.T) {
	for _, mode := range []string{"missing", "null", "empty", "nonempty"} {
		t.Run(mode, func(t *testing.T) {
			id := kernel.Identity{Mode: kernel.IdentityAuto}.Normalized()
			rec := session.NewRecord(id.ScopeKey, time.Hour)
			switch mode {
			case "empty":
				rec.LatestSearchCandidateIDs = []string{}
			case "nonempty":
				rec.LatestSearchCandidateIDs = []string{"current"}
			}
			wire, err := json.Marshal(rec.Snapshot(false))
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(wire, &fields); err != nil {
				t.Fatal(err)
			}
			if mode == "missing" {
				delete(fields, "latest_search_candidate_ids")
				if wire, err = json.Marshal(fields); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "empty" && strings.TrimSpace(string(fields["latest_search_candidate_ids"])) != "[]" {
				t.Fatalf("empty list serialized as %s, want []", fields["latest_search_candidate_ids"])
			}
			if mode == "nonempty" && strings.Contains(string(fields["latest_search_candidate_ids"]), "null") {
				t.Fatalf("non-empty list serialized as %s", fields["latest_search_candidate_ids"])
			}
			var loaded kernel.SessionSnapshot
			if err := json.Unmarshal(wire, &loaded); err != nil {
				t.Fatal(err)
			}
			restored := session.Restore(loaded).Snapshot(false)
			wantNil := mode == "missing" || mode == "null"
			if (restored.LatestSearchCandidateIDs == nil) != wantNil {
				t.Fatalf("mode=%s ids=%#v wantNil=%v", mode, restored.LatestSearchCandidateIDs, wantNil)
			}
			if mode == "nonempty" && (len(restored.LatestSearchCandidateIDs) != 1 || restored.LatestSearchCandidateIDs[0] != "current") {
				t.Fatalf("ids=%v", restored.LatestSearchCandidateIDs)
			}
			if mode == "empty" && len(restored.LatestSearchCandidateIDs) != 0 {
				t.Fatalf("ids=%v want empty non-nil", restored.LatestSearchCandidateIDs)
			}
		})
	}
}
