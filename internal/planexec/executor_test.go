package planexec

import (
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestExecuteNodeStatefulOperationsAndPartialResults(t *testing.T) {
	candidate := planCandidate("one", kernel.KindDocument)
	artifact := planArtifact(candidate.Ref)
	relation := kernel.Relation{From: candidate.Ref, To: candidate.Ref, Type: "related"}
	partial := &kernel.ErrorDetail{Type: kernel.ErrMissingScope, Message: "partial"}
	k := &executionKernel{
		searchFn: func(context.Context, kernel.SearchRequest) (kernel.SearchSnapshot, error) {
			return kernel.SearchSnapshot{SessionID: "rs_search", Candidates: []kernel.Candidate{candidate}, Partial: true}, nil
		},
		queryFn: func(context.Context, kernel.QueryRequest) (kernel.QuerySnapshot, error) {
			return kernel.QuerySnapshot{SessionID: "rs_query", Candidates: []kernel.Candidate{candidate}}, partial
		},
		fetchFn: func(context.Context, kernel.FetchBatchRequest) (kernel.ArtifactBatch, error) {
			return kernel.ArtifactBatch{SessionID: "rs_fetch", Items: []kernel.ArtifactResult{{Artifact: &artifact}, {Error: partial}}}, nil
		},
		expandFn: func(context.Context, kernel.ExpandRequest) (kernel.RelationBatch, error) {
			return kernel.RelationBatch{SessionID: "rs_expand", Relations: []kernel.Relation{relation}, Partial: true, Error: partial}, nil
		},
		resolveFn: func(context.Context, kernel.ResolveRequest) ([]kernel.ObjectRef, error) {
			return []kernel.ObjectRef{candidate.Ref}, nil
		},
	}
	executor := New(k, 0)
	if executor.concurrency != 4 {
		t.Fatalf("default concurrency=%d", executor.concurrency)
	}
	plan := kernel.RetrievalPlan{Identity: kernel.Identity{Mode: kernel.IdentityUser}, Budget: kernel.SearchBudget{MaxCalls: 8}}

	searchResult, sid := executor.executeNode(context.Background(), plan, kernel.PlanNode{ID: "search", Op: kernel.PlanSearch, Request: json.RawMessage(`{"source":"docs","query":"q"}`)}, nil, "")
	if searchResult.State != kernel.NodePartial || len(searchResult.Candidates) != 1 || sid != "rs_search" {
		t.Fatalf("search=%+v sid=%s", searchResult, sid)
	}
	queryResult, sid := executor.executeNode(context.Background(), plan, kernel.PlanNode{ID: "query", Op: kernel.PlanQuery, Request: json.RawMessage(`{"source":"tasks"}`)}, nil, "rs_search")
	if queryResult.State != kernel.NodePartial || queryResult.Error == nil || sid != "rs_query" {
		t.Fatalf("query=%+v sid=%s", queryResult, sid)
	}
	fetchResult, sid := executor.executeNode(context.Background(), plan, kernel.PlanNode{ID: "fetch", Op: kernel.PlanFetch, Request: json.RawMessage(`{"items":[]}`)}, nil, "rs_query")
	if fetchResult.State != kernel.NodePartial || len(fetchResult.Artifacts) != 1 || sid != "rs_fetch" {
		t.Fatalf("fetch=%+v sid=%s", fetchResult, sid)
	}
	expandResult, sid := executor.executeNode(context.Background(), plan, kernel.PlanNode{ID: "expand", Op: kernel.PlanExpand, Request: json.RawMessage(`{"ref":{"native_id":"one"}}`)}, nil, "rs_fetch")
	if expandResult.State != kernel.NodePartial || len(expandResult.Relations) != 1 || sid != "rs_expand" {
		t.Fatalf("expand=%+v sid=%s", expandResult, sid)
	}
	resolveResult, sid := executor.executeNode(context.Background(), plan, kernel.PlanNode{ID: "resolve", Op: kernel.PlanResolve, Request: json.RawMessage(`{"text":"张三"}`)}, nil, "rs_expand")
	refs, ok := resolveResult.Value.([]kernel.ObjectRef)
	if !ok || len(refs) != 1 || sid != "rs_expand" {
		t.Fatalf("resolve=%+v sid=%s", resolveResult, sid)
	}
}

func TestExecuteNodeStatefulErrorsAndMalformedRequests(t *testing.T) {
	operationErr := &kernel.ErrorDetail{Type: kernel.ErrUpstreamPermanent, Message: "failed"}
	k := &executionKernel{
		searchFn: func(context.Context, kernel.SearchRequest) (kernel.SearchSnapshot, error) {
			return kernel.SearchSnapshot{SessionID: "search-error"}, operationErr
		},
		queryFn: func(context.Context, kernel.QueryRequest) (kernel.QuerySnapshot, error) {
			return kernel.QuerySnapshot{SessionID: "query-error"}, operationErr
		},
		fetchFn: func(context.Context, kernel.FetchBatchRequest) (kernel.ArtifactBatch, error) {
			return kernel.ArtifactBatch{SessionID: "fetch-error"}, operationErr
		},
		expandFn: func(context.Context, kernel.ExpandRequest) (kernel.RelationBatch, error) {
			return kernel.RelationBatch{SessionID: "expand-error"}, operationErr
		},
		resolveFn: func(context.Context, kernel.ResolveRequest) ([]kernel.ObjectRef, error) { return nil, operationErr },
	}
	executor := New(k, 1)
	plan := kernel.RetrievalPlan{}
	tests := []struct {
		op      kernel.PlanOp
		request string
	}{
		{kernel.PlanSearch, `{"query":"q"}`},
		{kernel.PlanQuery, `{"source":"tasks"}`},
		{kernel.PlanFetch, `{"items":[]}`},
		{kernel.PlanExpand, `{"ref":{"native_id":"one"}}`},
		{kernel.PlanResolve, `{"text":"person"}`},
	}
	for _, test := range tests {
		result, _ := executor.executeNode(context.Background(), plan, kernel.PlanNode{ID: string(test.op), Op: test.op, Request: json.RawMessage(test.request)}, nil, "sid")
		if result.State != kernel.NodeFailed || result.Error == nil || result.Error.Type != kernel.ErrUpstreamPermanent {
			t.Errorf("op=%s result=%+v", test.op, result)
		}
		malformed, _ := executor.executeNode(context.Background(), plan, kernel.PlanNode{ID: "bad", Op: test.op, Request: json.RawMessage(`{`)}, nil, "sid")
		if malformed.State != kernel.NodeFailed || malformed.Error == nil || malformed.Error.Type != kernel.ErrInvalidRequest {
			t.Errorf("malformed op=%s result=%+v", test.op, malformed)
		}
	}
}

func TestExecuteNodeTransformsAndMapFetchPartial(t *testing.T) {
	c1 := planCandidate("one", kernel.KindDocument)
	c2 := planCandidate("two", kernel.KindMessage)
	a1, a2 := planArtifact(c1.Ref), planArtifact(c2.Ref)
	r1 := kernel.Relation{From: c1.Ref, To: c2.Ref, Type: "related"}
	deps := map[string]kernel.NodeResult{
		"b": {Candidates: []kernel.Candidate{c2}, Artifacts: []kernel.Artifact{a2}},
		"a": {Candidates: []kernel.Candidate{c1, c1}, Artifacts: []kernel.Artifact{a1}, Relations: []kernel.Relation{r1}},
	}
	partial := &kernel.ErrorDetail{Type: kernel.ErrMissingScope, Message: "partial fetch"}
	var captured kernel.FetchBatchRequest
	k := &executionKernel{fetchFn: func(_ context.Context, request kernel.FetchBatchRequest) (kernel.ArtifactBatch, error) {
		captured = request
		return kernel.ArtifactBatch{SessionID: "rs_map", Items: []kernel.ArtifactResult{{Artifact: &a1}, {Error: partial}}}, nil
	}}
	executor := New(k, 2)
	plan := kernel.RetrievalPlan{Identity: kernel.Identity{Mode: kernel.IdentityUser}, Budget: kernel.SearchBudget{MaxFetches: 2}}

	merge, _ := executor.executeNode(context.Background(), plan, kernel.PlanNode{ID: "merge", Op: kernel.PlanMerge}, deps, "sid")
	if len(merge.Candidates) != 3 || len(merge.Artifacts) != 2 || len(merge.Relations) != 1 {
		t.Fatalf("merge=%+v", merge)
	}
	dedup, _ := executor.executeNode(context.Background(), plan, kernel.PlanNode{ID: "dedup", Op: kernel.PlanDedup}, deps, "sid")
	if len(dedup.Candidates) != 2 {
		t.Fatalf("dedup candidates=%v", dedup.Candidates)
	}
	rank, _ := executor.executeNode(context.Background(), plan, kernel.PlanNode{ID: "rank", Op: kernel.PlanRank, Request: json.RawMessage(`{"query":"one","limit":1,"k0":30,"source_quota":true}`)}, deps, "sid")
	if len(rank.Candidates) != 1 {
		t.Fatalf("rank=%+v", rank)
	}
	limit, _ := executor.executeNode(context.Background(), plan, kernel.PlanNode{ID: "limit", Op: kernel.PlanLimit, Request: json.RawMessage(`{"top_k":1}`)}, deps, "sid")
	if len(limit.Candidates) != 1 || len(limit.Artifacts) != 1 {
		t.Fatalf("limit=%+v", limit)
	}
	project, _ := executor.executeNode(context.Background(), plan, kernel.PlanNode{ID: "project", Op: kernel.PlanProject}, deps, "sid")
	if len(project.Candidates) != 3 || len(project.Artifacts) != 2 || len(project.Relations) != 1 {
		t.Fatalf("project=%+v", project)
	}

	mapFetchRequest := json.RawMessage(`{"top_k":5,"max_items":2,"projection_by_kind":{"document":["summary"]}}`)
	mapDeps := map[string]kernel.NodeResult{"search": {Candidates: []kernel.Candidate{c1, c2}}}
	mapFetch, sid := executor.executeNode(context.Background(), plan, kernel.PlanNode{ID: "map", Op: kernel.PlanMapFetch, Request: mapFetchRequest}, mapDeps, "sid")
	if mapFetch.State != kernel.NodePartial || len(mapFetch.Artifacts) != 1 || sid != "rs_map" || len(captured.Items) != 2 {
		t.Fatalf("map_fetch=%+v sid=%s request=%+v", mapFetch, sid, captured)
	}
	if captured.Items[0].Projection != kernel.ProjectionSummary || captured.Items[1].Projection != kernel.ProjectionContext|kernel.ProjectionContent {
		t.Fatalf("map projections=%v", captured.Items)
	}
	malformed, _ := executor.executeNode(context.Background(), plan, kernel.PlanNode{ID: "bad-map", Op: kernel.PlanMapFetch, Request: json.RawMessage(`{`)}, deps, "sid")
	if malformed.State != kernel.NodeFailed {
		t.Fatalf("malformed map_fetch=%+v", malformed)
	}
	unsupported, _ := executor.executeNode(context.Background(), plan, kernel.PlanNode{ID: "unknown", Op: "unknown"}, deps, "sid")
	if unsupported.State != kernel.NodeFailed || unsupported.Error == nil {
		t.Fatalf("unsupported=%+v", unsupported)
	}
}

func TestMapFetchReturnsFailureWhenKernelFetchFails(t *testing.T) {
	candidate := planCandidate("one", kernel.KindDocument)
	opErr := &kernel.ErrorDetail{Type: kernel.ErrUpstreamPermanent, Message: "fetch failed"}
	executor := New(&executionKernel{fetchFn: func(context.Context, kernel.FetchBatchRequest) (kernel.ArtifactBatch, error) {
		return kernel.ArtifactBatch{SessionID: "failed-session"}, opErr
	}}, 1)
	result, sid := executor.executeNode(context.Background(), kernel.RetrievalPlan{}, kernel.PlanNode{ID: "map", Op: kernel.PlanMapFetch, Request: json.RawMessage(`{}`), MaxItems: 1}, map[string]kernel.NodeResult{"search": {Candidates: []kernel.Candidate{candidate}}}, "sid")
	if result.State != kernel.NodeFailed || result.Error == nil || result.Error.Type != kernel.ErrUpstreamPermanent || sid != "failed-session" {
		t.Fatalf("result=%+v sid=%s", result, sid)
	}
}

func TestExecutorRetriesTransientNodeAndSkipsFailedDependency(t *testing.T) {
	var calls atomic.Int32
	candidate := planCandidate("recovered", kernel.KindDocument)
	k := &executionKernel{searchFn: func(context.Context, kernel.SearchRequest) (kernel.SearchSnapshot, error) {
		if calls.Add(1) == 1 {
			return kernel.SearchSnapshot{}, &kernel.ErrorDetail{Type: kernel.ErrUpstreamTransient, Message: "retry", Retryable: true}
		}
		return kernel.SearchSnapshot{SessionID: "rs_retry", Candidates: []kernel.Candidate{candidate}}, nil
	}}
	plan := kernel.RetrievalPlan{
		Version: "retrieval-plan/v1",
		Budget:  kernel.SearchBudget{DeadlineMS: 2000, MaxCalls: 4},
		Nodes:   []kernel.PlanNode{{ID: "search", Op: kernel.PlanSearch, Request: json.RawMessage(`{"query":"q"}`), Retry: kernel.RetryPolicy{MaxAttempts: 2, BackoffMS: 1}}},
	}
	events, results, err := New(k, 1).Execute(context.Background(), plan)
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
	if !retried || result.Partial || result.Nodes["search"].Attempts != 2 || result.SessionID != "rs_retry" || len(result.Output) != 1 {
		t.Fatalf("result=%+v retried=%v", result, retried)
	}

	k = &executionKernel{searchFn: func(context.Context, kernel.SearchRequest) (kernel.SearchSnapshot, error) {
		return kernel.SearchSnapshot{}, &kernel.ErrorDetail{Type: kernel.ErrUpstreamPermanent, Message: "permanent"}
	}}
	plan.Nodes = []kernel.PlanNode{
		{ID: "search", Op: kernel.PlanSearch, Request: json.RawMessage(`{"query":"q"}`)},
		{ID: "query", Op: kernel.PlanQuery, DependsOn: []string{"search"}, Request: json.RawMessage(`{"source":"tasks"}`)},
	}
	plan.Output = []string{"query"}
	events, results, err = New(k, 1).Execute(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	result = <-results
	if !result.Partial || result.Nodes["search"].State != kernel.NodeFailed || result.Nodes["query"].State != kernel.NodeSkipped {
		t.Fatalf("failed dependency result=%+v", result)
	}
}

func TestExecutorCancellationMarksRunningNodeCancelled(t *testing.T) {
	k := &executionKernel{searchFn: func(ctx context.Context, _ kernel.SearchRequest) (kernel.SearchSnapshot, error) {
		<-ctx.Done()
		return kernel.SearchSnapshot{}, ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	plan := kernel.RetrievalPlan{Version: "retrieval-plan/v1", Nodes: []kernel.PlanNode{{ID: "search", Op: kernel.PlanSearch, Request: json.RawMessage(`{"query":"q"}`)}}}
	events, results, err := New(k, 1).Execute(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	result := <-results
	if !result.Partial || result.Nodes["search"].State != kernel.NodeCancelled {
		t.Fatalf("cancelled result=%+v", result)
	}
}

func TestValidatePlanRejectsInvalidGraphs(t *testing.T) {
	valid := kernel.RetrievalPlan{Version: "retrieval-plan/v1", Nodes: []kernel.PlanNode{{ID: "root", Op: kernel.PlanSearch}}}
	if err := validatePlan(valid); err != nil {
		t.Fatal(err)
	}
	withoutVersion := valid
	withoutVersion.Version = ""
	if err := validatePlan(withoutVersion); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		plan kernel.RetrievalPlan
		want string
	}{
		{"version", kernel.RetrievalPlan{Version: "v2", Nodes: valid.Nodes}, "unsupported plan version"},
		{"empty", kernel.RetrievalPlan{Version: "retrieval-plan/v1"}, "no nodes"},
		{"missing id", kernel.RetrievalPlan{Version: "retrieval-plan/v1", Nodes: []kernel.PlanNode{{Op: kernel.PlanSearch}}}, "id is required"},
		{"duplicate", kernel.RetrievalPlan{Version: "retrieval-plan/v1", Nodes: []kernel.PlanNode{{ID: "same"}, {ID: "same"}}}, "duplicate"},
		{"dangling", kernel.RetrievalPlan{Version: "retrieval-plan/v1", Nodes: []kernel.PlanNode{{ID: "root", DependsOn: []string{"missing"}}}}, "unknown node"},
		{"cycle", kernel.RetrievalPlan{Version: "retrieval-plan/v1", Nodes: []kernel.PlanNode{{ID: "a", DependsOn: []string{"b"}}, {ID: "b", DependsOn: []string{"a"}}}}, "cycle"},
	}
	tooMany := kernel.RetrievalPlan{Version: "retrieval-plan/v1", Nodes: make([]kernel.PlanNode, 201)}
	for i := range tooMany.Nodes {
		tooMany.Nodes[i].ID = string(rune('a'+i%26)) + time.Unix(int64(i), 0).Format("150405")
	}
	tests = append(tests, struct {
		name string
		plan kernel.RetrievalPlan
		want string
	}{"too many", tooMany, "exceeds 200"})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validatePlan(test.plan); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want %q", err, test.want)
			}
		})
	}
	if _, _, err := New(&executionKernel{}, 1).Execute(context.Background(), tests[0].plan); err == nil {
		t.Fatal("Execute accepted invalid plan")
	}
}

func TestSchedulerAndCollectionHelpers(t *testing.T) {
	ready := &readyHeap{}
	heap.Init(ready)
	heap.Push(ready, nodeItem{id: "later", priority: 1, seq: 2})
	heap.Push(ready, nodeItem{id: "first", priority: 2, seq: 1})
	heap.Push(ready, nodeItem{id: "tie", priority: 2, seq: 3})
	if got := heap.Pop(ready).(nodeItem).id; got != "first" {
		t.Fatalf("ready pop=%s", got)
	}
	retries := &retryHeap{}
	heap.Init(retries)
	now := time.Now()
	heap.Push(retries, retryItem{id: "later", due: now.Add(time.Second), seq: 1})
	heap.Push(retries, retryItem{id: "first", due: now, seq: 2})
	heap.Push(retries, retryItem{id: "tie", due: now, seq: 3})
	if got := heap.Pop(retries).(retryItem).id; got != "first" {
		t.Fatalf("retry pop=%s", got)
	}

	c1, c2 := planCandidate("one", kernel.KindDocument), planCandidate("two", kernel.KindMessage)
	a1, a2 := planArtifact(c1.Ref), planArtifact(c2.Ref)
	r1, r2 := kernel.Relation{Type: "a"}, kernel.Relation{Type: "b"}
	deps := map[string]kernel.NodeResult{"b": {Candidates: []kernel.Candidate{c2}, Artifacts: []kernel.Artifact{a2}, Relations: []kernel.Relation{r2}}, "a": {Candidates: []kernel.Candidate{c1}, Artifacts: []kernel.Artifact{a1}, Relations: []kernel.Relation{r1}}}
	if got := depCandidates(deps); len(got) != 2 || got[0].Ref.NativeID != "one" {
		t.Fatalf("candidates=%v", got)
	}
	if got := depArtifacts(deps); len(got) != 2 || got[0].Ref.NativeID != "one" {
		t.Fatalf("artifacts=%v", got)
	}
	if got := depRelations(deps); len(got) != 2 || got[0].Type != "a" {
		t.Fatalf("relations=%v", got)
	}
	runtime := map[string]*runtimeNode{"a": {state: kernel.NodeSucceeded, result: deps["a"]}, "b": {state: kernel.NodeRunning}}
	node := kernel.PlanNode{DependsOn: []string{"a", "b"}}
	if ready, skip := dependencyState(node, runtime); ready || skip {
		t.Fatalf("running dependency ready=%v skip=%v", ready, skip)
	}
	runtime["b"].state = kernel.NodeFailed
	if ready, skip := dependencyState(node, runtime); ready || !skip {
		t.Fatalf("failed dependency ready=%v skip=%v", ready, skip)
	}
	node.Optional = true
	if ready, skip := dependencyState(node, runtime); !ready || skip {
		t.Fatalf("optional dependency ready=%v skip=%v", ready, skip)
	}
	if got := collectDeps(kernel.PlanNode{DependsOn: []string{"a"}}, runtime); len(got) != 1 {
		t.Fatalf("collected=%v", got)
	}
}

func TestRetryStateAndProjectionHelpers(t *testing.T) {
	if maxAttempts(kernel.PlanNode{Retry: kernel.RetryPolicy{MaxAttempts: 4}}) != 4 || maxAttempts(kernel.PlanNode{Op: kernel.PlanSearch}) != 2 || maxAttempts(kernel.PlanNode{Op: kernel.PlanRank}) != 1 {
		t.Fatal("maxAttempts mismatch")
	}
	if retryDelay(kernel.PlanNode{}, 0) != 200*time.Millisecond || retryDelay(kernel.PlanNode{Retry: kernel.RetryPolicy{BackoffMS: 10, MaxBackoffMS: 25}}, 4) != 25*time.Millisecond {
		t.Fatal("retryDelay mismatch")
	}
	if shouldRetry(nil) || !shouldRetry(&kernel.ErrorDetail{Retryable: true}) || !shouldRetry(&kernel.ErrorDetail{Type: kernel.ErrRateLimited}) || !shouldRetry(&kernel.ErrorDetail{Type: kernel.ErrUpstreamTransient}) || shouldRetry(&kernel.ErrorDetail{Type: kernel.ErrMissingScope}) {
		t.Fatal("shouldRetry mismatch")
	}
	for _, op := range []kernel.PlanOp{kernel.PlanMerge, kernel.PlanDedup, kernel.PlanRank, kernel.PlanLimit, kernel.PlanProject, kernel.PlanMapFetch} {
		if !acceptsPartial(op) {
			t.Errorf("%s should accept partial dependencies", op)
		}
	}
	if acceptsPartial(kernel.PlanSearch) {
		t.Fatal("search should not accept a failed dependency")
	}
	for _, state := range []kernel.NodeState{kernel.NodeSucceeded, kernel.NodePartial, kernel.NodeEmpty, kernel.NodeFailed, kernel.NodeSkipped, kernel.NodeCancelled} {
		if !isTerminal(state) {
			t.Errorf("state %s should be terminal", state)
		}
	}
	if isTerminal(kernel.NodeRunning) {
		t.Fatal("running is terminal")
	}
	for kind, want := range map[kernel.ObjectKind]kernel.ProjectionSet{
		kernel.KindDocument: kernel.ProjectionStructure | kernel.ProjectionContent,
		kernel.KindMessage:  kernel.ProjectionContext | kernel.ProjectionContent,
		kernel.KindMinute:   kernel.ProjectionSummary | kernel.ProjectionRelations,
		kernel.KindMeeting:  kernel.ProjectionContent | kernel.ProjectionRelations,
		kernel.KindTask:     kernel.ProjectionContent,
		kernel.KindUnknown:  kernel.ProjectionContent,
	} {
		if got := defaultProjection(kind); got != want {
			t.Errorf("kind=%s projection=%s want=%s", kind, got, want)
		}
	}
	if got := failed("x", errors.New("bad")); got.State != kernel.NodeFailed || got.Error.Type != kernel.ErrInvalidRequest {
		t.Fatalf("failed=%+v", got)
	}
	if got := failedDetail("x", &kernel.ErrorDetail{Type: kernel.ErrMissingScope, Message: "bad"}); got.Error.Type != kernel.ErrMissingScope {
		t.Fatalf("failedDetail=%+v", got)
	}
}

type executionKernel struct {
	searchFn  func(context.Context, kernel.SearchRequest) (kernel.SearchSnapshot, error)
	queryFn   func(context.Context, kernel.QueryRequest) (kernel.QuerySnapshot, error)
	fetchFn   func(context.Context, kernel.FetchBatchRequest) (kernel.ArtifactBatch, error)
	expandFn  func(context.Context, kernel.ExpandRequest) (kernel.RelationBatch, error)
	resolveFn func(context.Context, kernel.ResolveRequest) ([]kernel.ObjectRef, error)
}

func (*executionKernel) Capabilities(context.Context, kernel.CapabilityRequest) (kernel.CapabilitySnapshot, error) {
	return kernel.CapabilitySnapshot{}, nil
}
func (k *executionKernel) Search(ctx context.Context, request kernel.SearchRequest) (kernel.SearchSnapshot, error) {
	if k.searchFn != nil {
		return k.searchFn(ctx, request)
	}
	return kernel.SearchSnapshot{}, nil
}
func (*executionKernel) Continue(context.Context, kernel.ContinueRequest) (kernel.SearchSnapshot, error) {
	return kernel.SearchSnapshot{}, nil
}
func (k *executionKernel) Query(ctx context.Context, request kernel.QueryRequest) (kernel.QuerySnapshot, error) {
	if k.queryFn != nil {
		return k.queryFn(ctx, request)
	}
	return kernel.QuerySnapshot{}, nil
}
func (k *executionKernel) Fetch(ctx context.Context, request kernel.FetchBatchRequest) (kernel.ArtifactBatch, error) {
	if k.fetchFn != nil {
		return k.fetchFn(ctx, request)
	}
	return kernel.ArtifactBatch{}, nil
}
func (k *executionKernel) Expand(ctx context.Context, request kernel.ExpandRequest) (kernel.RelationBatch, error) {
	if k.expandFn != nil {
		return k.expandFn(ctx, request)
	}
	return kernel.RelationBatch{}, nil
}
func (k *executionKernel) Resolve(ctx context.Context, request kernel.ResolveRequest) ([]kernel.ObjectRef, error) {
	if k.resolveFn != nil {
		return k.resolveFn(ctx, request)
	}
	return nil, nil
}
func (*executionKernel) Execute(context.Context, kernel.RetrievalPlan) (<-chan kernel.RetrievalEvent, <-chan kernel.PlanResult, error) {
	return nil, nil, errors.New("not implemented")
}
func (*executionKernel) Session(context.Context, string, kernel.Identity) (kernel.SessionSnapshot, error) {
	return kernel.SessionSnapshot{}, nil
}

func planCandidate(id string, kind kernel.ObjectKind) kernel.Candidate {
	return kernel.Candidate{Ref: kernel.ObjectRef{NativeID: id, CanonicalID: "canonical:" + id, Kind: kind}, Source: kernel.SourceDocs, Kind: kind, NativeRank: 1, Title: id}
}

func planArtifact(ref kernel.ObjectRef) kernel.Artifact {
	return kernel.Artifact{Ref: ref, Projection: kernel.ProjectionContent, Chunks: []kernel.ContentChunk{{ID: "content", Text: ref.NativeID}}}
}

var _ kernel.Kernel = (*executionKernel)(nil)
