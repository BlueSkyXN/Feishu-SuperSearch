package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/adapter/mock"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/engine"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/session"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/provider"
)

func newEngine(t *testing.T) *engine.Engine {
	t.Helper()
	r := provider.NewRegistry()
	for _, p := range mock.NewProviders(mock.DemoDataset()) {
		if err := r.Register(p, true); err != nil {
			t.Fatal(err)
		}
	}
	return engine.New(r, session.NewMemoryStore(time.Hour), engine.Config{GlobalConcurrency: 4, ProviderTimeout: time.Second, SessionTTL: time.Hour})
}
func TestSearchFetchContinueExpandAndPlan(t *testing.T) {
	e := newEngine(t)
	defer e.Close()
	ctx := context.Background()
	id := kernel.Identity{Mode: kernel.IdentityAuto}
	search, err := e.Search(ctx, kernel.SearchRequest{Query: "A 项目 延期", Sources: []kernel.SourceID{kernel.SourceDocs, kernel.SourceMessages, kernel.SourceMinutes, kernel.SourceMeetings, kernel.SourceTasks}, Identity: id, Limit: 20, LimitPerSource: 1, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Candidates) < 3 {
		t.Fatalf("too few candidates: %d", len(search.Candidates))
	}
	first := search.Candidates[0]
	fetch, err := e.Fetch(ctx, kernel.FetchBatchRequest{SessionID: search.SessionID, Identity: id, Items: []kernel.FetchRequest{{Ref: first.Ref, Projection: kernel.ProjectionContent | kernel.ProjectionSummary | kernel.ProjectionContext}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(fetch.Items) != 1 || fetch.Items[0].Artifact == nil {
		t.Fatalf("fetch=%+v", fetch)
	}
	// Repeating the same projection should use the session cache.
	cached, err := e.Fetch(ctx, kernel.FetchBatchRequest{SessionID: search.SessionID, Identity: id, Items: []kernel.FetchRequest{{Ref: first.Ref, Projection: kernel.ProjectionContent}}})
	if err != nil {
		t.Fatal(err)
	}
	if !cached.Items[0].Cached {
		t.Fatal("expected cached fetch")
	}

	var meeting *kernel.Candidate
	for i := range search.Candidates {
		if search.Candidates[i].Kind == kernel.KindMeeting {
			meeting = &search.Candidates[i]
			break
		}
	}
	if meeting != nil {
		rels, err := e.Expand(ctx, kernel.ExpandRequest{SessionID: search.SessionID, Identity: id, Ref: meeting.Ref, MaxDepth: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(rels.Relations) < 3 {
			t.Fatalf("expected meeting→minute→tasks traversal, got %d relations", len(rels.Relations))
		}
		types := map[string]bool{}
		for _, rel := range rels.Relations {
			types[rel.Type] = true
		}
		if !types["meeting.has_minute"] || !types["minute.has_task"] {
			t.Fatalf("unexpected relation types: %v", types)
		}
	}

	raw, _ := json.Marshal(kernel.SearchRequest{Query: "A 项目", Sources: []kernel.SourceID{kernel.SourceDocs, kernel.SourceMessages}, Limit: 10, LimitPerSource: 3})
	plan := kernel.RetrievalPlan{Version: "retrieval-plan/v1", Identity: id, Budget: kernel.SearchBudget{DeadlineMS: 5000, MaxCalls: 10}, Nodes: []kernel.PlanNode{{ID: "search", Op: kernel.PlanSearch, Request: raw}, {ID: "fetch", Op: kernel.PlanMapFetch, DependsOn: []string{"search"}, Request: json.RawMessage(`{"top_k":2,"max_items":2,"default_projection":["content"]}`)}}, Output: []string{"search", "fetch"}}
	events, results, err := e.Execute(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	result := <-results
	if result.SessionID == "" || len(result.Output["search"].Candidates) == 0 || len(result.Output["fetch"].Artifacts) == 0 {
		t.Fatalf("plan result=%+v", result)
	}
}

func TestPlanUsesOneSharedBudgetAcrossSearchAndFetch(t *testing.T) {
	e := newEngine(t)
	defer e.Close()
	ctx := context.Background()
	id := kernel.Identity{Mode: kernel.IdentityAuto}

	raw, err := json.Marshal(kernel.SearchRequest{
		Query:          "A 项目",
		Sources:        []kernel.SourceID{kernel.SourceDocs, kernel.SourceMessages},
		Limit:          10,
		LimitPerSource: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := kernel.RetrievalPlan{
		Version:  "retrieval-plan/v1",
		Identity: id,
		Budget: kernel.SearchBudget{
			DeadlineMS:        5000,
			MaxCalls:          2, // consumed by the two source searches
			MaxFetches:        2,
			MaxPagesPerSource: 1,
			MaxExpandedNodes:  2,
			MaxBytes:          1 << 20,
		},
		Nodes: []kernel.PlanNode{
			{ID: "search", Op: kernel.PlanSearch, Request: raw},
			{ID: "fetch", Op: kernel.PlanMapFetch, DependsOn: []string{"search"}, Request: json.RawMessage(`{"top_k":1,"max_items":1,"default_projection":["content"]}`)},
		},
		Output: []string{"search", "fetch"},
	}
	events, results, err := e.Execute(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	result := <-results
	if result.Budget.CallsUsed != 2 {
		t.Fatalf("calls_used=%d want 2", result.Budget.CallsUsed)
	}
	if result.Budget.FetchesUsed != 0 {
		t.Fatalf("fetches_used=%d want 0; call+fetch reservation must be atomic", result.Budget.FetchesUsed)
	}
	if result.Output["fetch"].State != kernel.NodePartial {
		t.Fatalf("fetch state=%s want partial", result.Output["fetch"].State)
	}
	if !result.Partial {
		t.Fatal("expected partial plan after budget exhaustion")
	}
}

type flakySearchProvider struct {
	mu    sync.Mutex
	calls int
}

type healthSearchProvider struct {
	descriptor   kernel.ProviderDescriptor
	healthErr    error
	operationErr map[kernel.Operation]error
	searchErr    error
	title        string
	mu           sync.Mutex
	calls        int
}

func (p *healthSearchProvider) Descriptor() kernel.ProviderDescriptor { return p.descriptor }
func (p *healthSearchProvider) Health(context.Context, kernel.Identity) (string, error) {
	return "test/1", p.healthErr
}
func (p *healthSearchProvider) HealthOperation(_ context.Context, operation kernel.Operation, _ kernel.Identity) (string, error) {
	return "test/1", p.operationErr[operation]
}
func (p *healthSearchProvider) Search(_ context.Context, request kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	if p.searchErr != nil {
		return kernel.CandidatePage{}, p.searchErr
	}
	ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: request.Identity.Normalized().ScopeKey, Kind: kernel.KindDocument, NativeID: string(p.descriptor.ID), ProviderID: p.descriptor.ID, Source: kernel.SourceDocs}
	return kernel.CandidatePage{Candidates: []kernel.Candidate{{Ref: ref, Source: kernel.SourceDocs, Kind: kernel.KindDocument, Title: p.title, NativeRank: 1, Projection: kernel.ProjectionHead}}, RawCount: 1}, nil
}

func healthDescriptor(id kernel.ProviderID) kernel.ProviderDescriptor {
	return kernel.ProviderDescriptor{ID: id, Source: kernel.SourceDocs, ObjectKinds: []kernel.ObjectKind{kernel.KindDocument}, Operations: kernel.OperationSet{kernel.OpSearch: true}, ReturnedProjection: kernel.ProjectionHead, Backend: "test"}
}

func TestCapabilityHandshakeFallsBackOnlyForCompatibilityFailure(t *testing.T) {
	registry := provider.NewRegistry()
	primary := &healthSearchProvider{descriptor: healthDescriptor("api.docs"), healthErr: &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, Message: "incompatible"}, title: "primary"}
	backup := &healthSearchProvider{descriptor: healthDescriptor("cli.docs"), title: "backup"}
	if err := registry.Register(primary, true); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(backup, false); err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e.Close()
	capabilities, err := e.Capabilities(context.Background(), kernel.CapabilityRequest{Probe: true})
	if err != nil || len(capabilities.Providers) != 2 {
		t.Fatalf("capabilities=%+v err=%v", capabilities, err)
	}
	result, err := e.Search(context.Background(), kernel.SearchRequest{Query: "test", Sources: []kernel.SourceID{kernel.SourceDocs}, Limit: 5, LimitPerSource: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].Title != "backup" || result.Sources[0].ProviderID != "cli.docs" {
		t.Fatalf("result=%+v", result)
	}
	if primary.calls != 0 || backup.calls != 1 {
		t.Fatalf("primary calls=%d backup calls=%d", primary.calls, backup.calls)
	}
}

func TestCapabilityHandshakeDoesNotHidePermissionFailure(t *testing.T) {
	registry := provider.NewRegistry()
	missingScope := &kernel.ErrorDetail{Type: kernel.ErrMissingScope, Message: "scope required"}
	primary := &healthSearchProvider{descriptor: healthDescriptor("api.docs"), healthErr: missingScope, searchErr: missingScope}
	backup := &healthSearchProvider{descriptor: healthDescriptor("cli.docs"), title: "backup"}
	if err := registry.Register(primary, true); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(backup, false); err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e.Close()
	_, _ = e.Capabilities(context.Background(), kernel.CapabilityRequest{Probe: true})
	result, err := e.Search(context.Background(), kernel.SearchRequest{Query: "test", Sources: []kernel.SourceID{kernel.SourceDocs}, Limit: 5, LimitPerSource: 5})
	if detail := kernel.DetailFromError(err); err == nil || detail.Type != kernel.ErrMissingScope {
		t.Fatalf("result=%+v err=%v detail=%+v", result, err, detail)
	}
	if primary.calls != 1 || backup.calls != 0 {
		t.Fatalf("primary calls=%d backup calls=%d", primary.calls, backup.calls)
	}
}

func TestCapabilityHandshakeFallsBackPerOperation(t *testing.T) {
	registry := provider.NewRegistry()
	descriptor := healthDescriptor("api.docs")
	descriptor.Operations[kernel.OpFetch] = true
	primary := &healthSearchProvider{descriptor: descriptor, operationErr: map[kernel.Operation]error{kernel.OpSearch: &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, Message: "search surface missing"}}, title: "primary"}
	backup := &healthSearchProvider{descriptor: healthDescriptor("cli.docs"), title: "backup"}
	if err := registry.Register(primary, true); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(backup, false); err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e.Close()
	capabilities, err := e.Capabilities(context.Background(), kernel.CapabilityRequest{Probe: true})
	if err != nil {
		t.Fatal(err)
	}
	var primaryCapability *kernel.ProviderCapability
	for index := range capabilities.Providers {
		if capabilities.Providers[index].Descriptor.ID == "api.docs" {
			primaryCapability = &capabilities.Providers[index]
		}
	}
	if primaryCapability == nil || primaryCapability.Status != kernel.StatusPartial || primaryCapability.Operations[kernel.OpSearch].Error == nil || primaryCapability.Operations[kernel.OpFetch].Status != kernel.StatusOK {
		t.Fatalf("primary capability=%+v", primaryCapability)
	}
	result, err := e.Search(context.Background(), kernel.SearchRequest{Query: "test", Sources: []kernel.SourceID{kernel.SourceDocs}, Limit: 5, LimitPerSource: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].Title != "backup" || primary.calls != 0 || backup.calls != 1 {
		t.Fatalf("result=%+v primary=%d backup=%d", result, primary.calls, backup.calls)
	}
	if selected, ok := registry.ForOperation(kernel.SourceDocs, kernel.OpFetch); !ok || selected.Descriptor().ID != "api.docs" {
		t.Fatalf("fetch selected=%v ok=%v", selected, ok)
	}
}

func TestSQLitePersistsCapabilityCacheAndQueryHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sfs.db")
	registry := provider.NewRegistry()
	searchProvider := &healthSearchProvider{descriptor: healthDescriptor("api.docs"), title: "persisted"}
	if err := registry.Register(searchProvider, true); err != nil {
		t.Fatal(err)
	}
	store, err := session.NewSQLiteStore(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, store, engine.Config{ProviderTimeout: time.Second, SessionTTL: time.Hour})
	if _, err := e.Capabilities(context.Background(), kernel.CapabilityRequest{Probe: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Search(context.Background(), kernel.SearchRequest{Query: "persist query", Sources: []kernel.SourceID{kernel.SourceDocs}}); err != nil {
		t.Fatal(err)
	}
	history, err := store.ListQueryHistory(context.Background(), 10)
	if err != nil || len(history) != 1 || history[0].Query != "persist query" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := session.NewSQLiteStore(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	registry2 := provider.NewRegistry()
	if err := registry2.Register(&healthSearchProvider{descriptor: healthDescriptor("api.docs"), title: "unused"}, true); err != nil {
		t.Fatal(err)
	}
	e2 := engine.New(registry2, reopened, engine.Config{ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e2.Close()
	capabilities, err := e2.Capabilities(context.Background(), kernel.CapabilityRequest{})
	if err != nil || len(capabilities.Providers) != 1 || capabilities.Providers[0].Version != "test/1" || capabilities.Providers[0].Status != kernel.StatusOK {
		t.Fatalf("capabilities=%+v err=%v", capabilities, err)
	}
}

func (p *flakySearchProvider) Descriptor() kernel.ProviderDescriptor {
	return kernel.ProviderDescriptor{
		ID:                 "test.flaky.docs",
		Source:             kernel.SourceDocs,
		ObjectKinds:        []kernel.ObjectKind{kernel.KindDocument},
		Operations:         kernel.OperationSet{kernel.OpSearch: true},
		RequiredIdentity:   []kernel.IdentityMode{kernel.IdentityAuto},
		ReturnedProjection: kernel.ProjectionHead | kernel.ProjectionSnippet,
		Backend:            "test",
		Version:            "1",
	}
}
func (p *flakySearchProvider) Search(_ context.Context, r kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls <= 2 { // consume Engine.searchOne's immediate retry, then let plan retry
		return kernel.CandidatePage{}, &kernel.ErrorDetail{Type: kernel.ErrUpstreamTransient, Message: "temporary failure", Retryable: true}
	}
	ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: r.Identity.Normalized().ScopeKey, Kind: kernel.KindDocument, NativeID: "doc_flaky", Source: kernel.SourceDocs}
	return kernel.CandidatePage{Candidates: []kernel.Candidate{{Ref: ref, Source: kernel.SourceDocs, Kind: kernel.KindDocument, Title: "recovered", NativeRank: 1, Projection: kernel.ProjectionHead}}}, nil
}

func TestPlanRetryHeapRetriesTransientNode(t *testing.T) {
	registry := provider.NewRegistry()
	flaky := &flakySearchProvider{}
	if err := registry.Register(flaky, true); err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{GlobalConcurrency: 2, ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e.Close()
	raw, _ := json.Marshal(kernel.SearchRequest{Query: "recover", Sources: []kernel.SourceID{kernel.SourceDocs}, Limit: 5, LimitPerSource: 5})
	plan := kernel.RetrievalPlan{
		Version:  "retrieval-plan/v1",
		Identity: kernel.Identity{Mode: kernel.IdentityAuto},
		Budget:   kernel.SearchBudget{DeadlineMS: 3000, MaxCalls: 5, MaxPagesPerSource: 1, MaxFetches: 1, MaxExpandedNodes: 1, MaxBytes: 1 << 20},
		Nodes:    []kernel.PlanNode{{ID: "search", Op: kernel.PlanSearch, Request: raw, Retry: kernel.RetryPolicy{MaxAttempts: 2, BackoffMS: 1}}},
		Output:   []string{"search"},
	}
	events, results, err := e.Execute(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	retried := false
	for event := range events {
		if event.Type == kernel.EventNodeRetrying {
			retried = true
		}
	}
	result := <-results
	if !retried {
		t.Fatal("expected retry event")
	}
	node := result.Output["search"]
	if node.State != kernel.NodeSucceeded || node.Attempts != 2 || len(node.Candidates) != 1 {
		t.Fatalf("node=%+v", node)
	}
	if result.Budget.CallsUsed != 3 {
		t.Fatalf("calls=%d want 3", result.Budget.CallsUsed)
	}
}

type countingBatchProvider struct {
	mu         sync.Mutex
	fetchCalls int
	delay      time.Duration
	omit       bool
}

func (p *countingBatchProvider) Descriptor() kernel.ProviderDescriptor {
	return kernel.ProviderDescriptor{
		ID:                  "test.batch.docs",
		Source:              kernel.SourceDocs,
		ObjectKinds:         []kernel.ObjectKind{kernel.KindDocument},
		Operations:          kernel.OperationSet{kernel.OpFetch: true},
		RequiredIdentity:    []kernel.IdentityMode{kernel.IdentityAuto},
		BatchLimits:         kernel.BatchLimits{MaxFetchItems: 10},
		FetchableProjection: kernel.ProjectionContent,
		ReturnedProjection:  kernel.ProjectionHead,
		Backend:             "test",
		Version:             "1",
	}
}
func (p *countingBatchProvider) Fetch(ctx context.Context, reqs []kernel.ProviderFetchRequest) ([]kernel.Artifact, error) {
	p.mu.Lock()
	p.fetchCalls++
	p.mu.Unlock()
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	out := make([]kernel.Artifact, len(reqs))
	for i, req := range reqs {
		projection := req.Projection
		if p.omit {
			projection = kernel.ProjectionHead
		}
		out[i] = kernel.Artifact{Ref: req.Ref, Projection: projection, Chunks: []kernel.ContentChunk{{ID: req.Ref.NativeID, Text: "content " + req.Ref.NativeID}}, Provenance: kernel.Provenance{ProviderID: p.Descriptor().ID, Backend: "test", Operation: "fetch", RetrievedAt: time.Now().UTC()}}
	}
	return out, nil
}
func (p *countingBatchProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fetchCalls
}

func newBatchEngine(t *testing.T, p *countingBatchProvider) *engine.Engine {
	t.Helper()
	registry := provider.NewRegistry()
	if err := registry.Register(p, true); err != nil {
		t.Fatal(err)
	}
	return engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{GlobalConcurrency: 4, ProviderTimeout: time.Second, SessionTTL: time.Hour})
}
func batchRef(id string) kernel.ObjectRef {
	scope := (kernel.Identity{Mode: kernel.IdentityAuto}).Normalized().ScopeKey
	return kernel.ObjectRef{Platform: "feishu", ScopeKey: scope, Kind: kernel.KindDocument, NativeID: id, CanonicalID: kernel.BuildCanonicalID(scope, kernel.KindDocument, id), ProviderID: "test.batch.docs", Source: kernel.SourceDocs}
}

func TestFetchUsesProviderBatchCapability(t *testing.T) {
	p := &countingBatchProvider{}
	e := newBatchEngine(t, p)
	defer e.Close()
	batch, err := e.Fetch(context.Background(), kernel.FetchBatchRequest{Identity: kernel.Identity{Mode: kernel.IdentityAuto}, Items: []kernel.FetchRequest{{Ref: batchRef("a"), Projection: kernel.ProjectionContent}, {Ref: batchRef("b"), Projection: kernel.ProjectionContent}}})
	if err != nil {
		t.Fatal(err)
	}
	if p.calls() != 1 || len(batch.Items) != 2 || batch.Budget.CallsUsed != 1 || batch.Budget.FetchesUsed != 2 {
		t.Fatalf("calls=%d batch=%+v", p.calls(), batch)
	}
}

func TestConcurrentFetchesShareSingleflight(t *testing.T) {
	p := &countingBatchProvider{delay: 75 * time.Millisecond}
	e := newBatchEngine(t, p)
	defer e.Close()
	start := make(chan struct{})
	results := make(chan kernel.ArtifactBatch, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			batch, err := e.Fetch(context.Background(), kernel.FetchBatchRequest{Identity: kernel.Identity{Mode: kernel.IdentityAuto}, Items: []kernel.FetchRequest{{Ref: batchRef("same"), Projection: kernel.ProjectionContent}}})
			results <- batch
			errs <- err
		}()
	}
	close(start)
	shared := false
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		batch := <-results
		if len(batch.Items) != 1 || batch.Items[0].Artifact == nil {
			t.Fatalf("batch=%+v", batch)
		}
		shared = shared || batch.Items[0].Shared
	}
	if p.calls() != 1 {
		t.Fatalf("fetch calls=%d want 1", p.calls())
	}
	if !shared {
		t.Fatal("one caller should report shared in-flight result")
	}
}

func TestFetchRejectsUnsupportedOrIncompleteProjection(t *testing.T) {
	p := &countingBatchProvider{}
	e := newBatchEngine(t, p)
	defer e.Close()
	batch, err := e.Fetch(context.Background(), kernel.FetchBatchRequest{Identity: kernel.Identity{Mode: kernel.IdentityAuto}, Items: []kernel.FetchRequest{{Ref: batchRef("unsupported"), Projection: kernel.ProjectionAttachments}}})
	if detail := kernel.DetailFromError(err); err == nil || detail.Type != kernel.ErrUnsupported || p.calls() != 0 || len(batch.Items) != 1 || batch.Items[0].Error == nil {
		t.Fatalf("batch=%+v err=%v calls=%d", batch, err, p.calls())
	}

	p.omit = true
	batch, err = e.Fetch(context.Background(), kernel.FetchBatchRequest{Identity: kernel.Identity{Mode: kernel.IdentityAuto}, Items: []kernel.FetchRequest{{Ref: batchRef("incomplete"), Projection: kernel.ProjectionContent}}})
	if err != nil || len(batch.Items) != 1 || batch.Items[0].Error == nil || batch.Items[0].Error.Type != kernel.ErrVersionIncompatible {
		t.Fatalf("batch=%+v err=%v", batch, err)
	}
}

type failingSaveStore struct {
	*session.MemoryStore
}

func (s *failingSaveStore) Save(context.Context, *session.Record) error {
	return errors.New("private database path must not be returned")
}

func TestSearchReturnsSanitizedSessionPersistenceFailure(t *testing.T) {
	p := &captureSearchProvider{}
	registry := provider.NewRegistry()
	if err := registry.Register(p, true); err != nil {
		t.Fatal(err)
	}
	store := &failingSaveStore{MemoryStore: session.NewMemoryStore(time.Hour)}
	e := engine.New(registry, store, engine.Config{ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e.Close()
	snapshot, err := e.Search(context.Background(), kernel.SearchRequest{Query: "A 项目", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: kernel.Identity{Mode: kernel.IdentityAuto}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	detail := kernel.DetailFromError(err)
	if snapshot.SessionID == "" || err == nil || detail.Type != kernel.ErrInternal || detail.Subtype != "session_store" || detail.Message != "session persistence failed" {
		t.Fatalf("snapshot=%+v err=%v detail=%+v", snapshot, err, detail)
	}
	if strings.Contains(detail.Error(), "private database") {
		t.Fatalf("persistence error leaked raw storage details: %v", detail)
	}
}

type captureSearchProvider struct {
	mu    sync.Mutex
	query string
}

func (p *captureSearchProvider) Descriptor() kernel.ProviderDescriptor {
	return kernel.ProviderDescriptor{ID: "test.capture.docs", Source: kernel.SourceDocs, ObjectKinds: []kernel.ObjectKind{kernel.KindDocument}, Operations: kernel.OperationSet{kernel.OpSearch: true}, RequiredIdentity: []kernel.IdentityMode{kernel.IdentityAuto}, SearchLimits: kernel.SearchLimits{MaxQueryRunes: 30}, ReturnedProjection: kernel.ProjectionHead, Backend: "test"}
}
func (p *captureSearchProvider) Search(_ context.Context, req kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	p.mu.Lock()
	p.query = req.Query
	p.mu.Unlock()
	ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: req.Identity.Normalized().ScopeKey, Kind: kernel.KindDocument, NativeID: "captured", Source: kernel.SourceDocs}
	return kernel.CandidatePage{Candidates: []kernel.Candidate{{Ref: ref, Source: kernel.SourceDocs, Kind: kernel.KindDocument, Title: req.Query, NativeRank: 1, Projection: kernel.ProjectionHead}}}, nil
}

func TestSearchUsesSourceSpecificQuery(t *testing.T) {
	p := &captureSearchProvider{}
	registry := provider.NewRegistry()
	if err := registry.Register(p, true); err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e.Close()
	snap, err := e.Search(context.Background(), kernel.SearchRequest{Query: "这是一个超过文档接口长度限制但用于全局排序的原始问题文本", SourceQueries: map[kernel.SourceID]string{kernel.SourceDocs: "A 项目 延期"}, Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: kernel.Identity{Mode: kernel.IdentityAuto}, Limit: 5, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Candidates) != 1 || snap.Candidates[0].Title != "A 项目 延期" {
		t.Fatalf("snapshot=%+v", snap)
	}
}

func TestSessionCannotBeReusedAcrossIdentityScopes(t *testing.T) {
	e := newEngine(t)
	defer e.Close()
	first, err := e.Search(context.Background(), kernel.SearchRequest{Query: "A 项目", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: kernel.Identity{Profile: "one", Mode: kernel.IdentityUser}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Search(context.Background(), kernel.SearchRequest{Query: "A 项目", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: kernel.Identity{Profile: "two", Mode: kernel.IdentityUser}, SessionID: first.SessionID, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if kernel.DetailFromError(err).Type != kernel.ErrIdentityRequired {
		t.Fatalf("err=%v", err)
	}
	other := kernel.Identity{Profile: "two", Mode: kernel.IdentityUser}
	if _, err := e.Continue(context.Background(), kernel.ContinueRequest{SessionID: first.SessionID, Identity: other}); kernel.DetailFromError(err).Type != kernel.ErrIdentityRequired {
		t.Fatalf("cross-scope continue err=%v", err)
	}
	if _, err := e.Session(context.Background(), first.SessionID, other); kernel.DetailFromError(err).Type != kernel.ErrIdentityRequired {
		t.Fatalf("cross-scope session err=%v", err)
	}
	if sessions, err := e.Sessions(context.Background(), other); err != nil || len(sessions) != 0 {
		t.Fatalf("cross-scope list=%+v err=%v", sessions, err)
	}
	if err := e.DeleteSession(context.Background(), first.SessionID, other); kernel.DetailFromError(err).Type != kernel.ErrIdentityRequired {
		t.Fatalf("cross-scope delete err=%v", err)
	}
	owner := kernel.Identity{Profile: "one", Mode: kernel.IdentityUser}
	if _, err := e.Session(context.Background(), first.SessionID, owner); err != nil {
		t.Fatalf("owner session err=%v", err)
	}
	if _, err := e.Continue(context.Background(), kernel.ContinueRequest{SessionID: first.SessionID, Identity: owner, MaxAdditionalPages: 6}); kernel.DetailFromError(err).Type != kernel.ErrInvalidRequest {
		t.Fatalf("unbounded continue err=%v", err)
	}
}

func TestFetchWithSessionRejectsUnknownObjectRef(t *testing.T) {
	e := newEngine(t)
	defer e.Close()
	identity := kernel.Identity{Mode: kernel.IdentityAuto}
	search, err := e.Search(context.Background(), kernel.SearchRequest{Query: "A 项目", Sources: []kernel.SourceID{kernel.SourceMessages}, Identity: identity, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := e.Fetch(context.Background(), kernel.FetchBatchRequest{
		SessionID: search.SessionID,
		Identity:  identity,
		Items: []kernel.FetchRequest{{
			Ref:        kernel.ObjectRef{Source: kernel.SourceMessages, Kind: kernel.KindMessage, NativeID: "../tasks/private"},
			Projection: kernel.ProjectionContent,
		}},
	})
	if len(batch.Items) != 1 || batch.Items[0].Error == nil || batch.Items[0].Error.Type != kernel.ErrInvalidRequest {
		t.Fatalf("batch=%+v err=%v", batch, err)
	}
}

func TestExpandWithSessionRejectsUnknownObjectRef(t *testing.T) {
	e := newEngine(t)
	defer e.Close()
	identity := kernel.Identity{Mode: kernel.IdentityAuto}
	search, err := e.Search(context.Background(), kernel.SearchRequest{Query: "A 项目", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: identity, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Expand(context.Background(), kernel.ExpandRequest{
		SessionID: search.SessionID,
		Identity:  identity,
		Ref:       kernel.ObjectRef{Source: kernel.SourceMessages, Kind: kernel.KindMessage, NativeID: "om_private"},
	})
	if detail := kernel.DetailFromError(err); err == nil || detail.Type != kernel.ErrInvalidRequest {
		t.Fatalf("unknown expand err=%v detail=%+v", err, detail)
	}
}

func TestExpandFiltersRequestedRelationTypes(t *testing.T) {
	e := newEngine(t)
	defer e.Close()
	search, err := e.Search(context.Background(), kernel.SearchRequest{Query: "A 项目", Sources: []kernel.SourceID{kernel.SourceMeetings}, Identity: kernel.Identity{Mode: kernel.IdentityAuto}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
	if err != nil || len(search.Candidates) == 0 {
		t.Fatalf("search=%+v err=%v", search, err)
	}
	batch, err := e.Expand(context.Background(), kernel.ExpandRequest{SessionID: search.SessionID, Identity: kernel.Identity{Mode: kernel.IdentityAuto}, Ref: search.Candidates[0].Ref, Relations: []string{"meeting.has_minute"}, MaxDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range batch.Relations {
		if rel.Type != "meeting.has_minute" {
			t.Fatalf("unexpected relation: %+v", rel)
		}
	}
}
