package research

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BlueSkyXN/Feishu-SuperSearch/ai"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
)

func TestServiceReportsPipelineFailures(t *testing.T) {
	request := Request{UserRequest: planner.UserRequest{Query: "why"}}
	if _, err := (Service{}).Run(context.Background(), request); err == nil || !strings.Contains(err.Error(), "kernel is nil") {
		t.Fatalf("nil kernel error=%v", err)
	}

	capabilityErr := errors.New("capabilities failed")
	if _, err := (Service{Kernel: &researchKernel{capabilityErr: capabilityErr}}).Run(context.Background(), request); !errors.Is(err, capabilityErr) {
		t.Fatalf("capability error=%v", err)
	}

	planningErr := errors.New("planning failed")
	k := &researchKernel{}
	if _, err := (Service{Kernel: k, Planner: &researchPlanner{err: planningErr}}).Run(context.Background(), request); !errors.Is(err, planningErr) {
		t.Fatalf("planner error=%v", err)
	}

	executeErr := errors.New("execute failed")
	k = &researchKernel{executeErr: executeErr}
	if _, err := (Service{Kernel: k, Planner: validResearchPlanner()}).Run(context.Background(), request); !errors.Is(err, executeErr) {
		t.Fatalf("execute error=%v", err)
	}

	k = &researchKernel{closeResultWithoutValue: true}
	if _, err := (Service{Kernel: k, Planner: validResearchPlanner()}).Run(context.Background(), request); err == nil || !strings.Contains(err.Error(), "closed without a result") {
		t.Fatalf("closed result error=%v", err)
	}
}

func TestServicePinsPlannerIdentityBudgetFiltersAndFetchLimit(t *testing.T) {
	docsCapability := kernel.ProviderCapability{Descriptor: kernel.ProviderDescriptor{ID: "test.docs", Source: kernel.SourceDocs, Operations: kernel.OperationSet{kernel.OpSearch: true}}, Status: kernel.StatusOK}
	request := Request{UserRequest: planner.UserRequest{
		Query:     "why",
		Sources:   []kernel.SourceID{kernel.SourceDocs},
		Identity:  kernel.Identity{Profile: "work", Mode: kernel.IdentityUser},
		Filters:   kernel.SearchFilters{ChatIDs: []string{"oc_allowed"}, Mine: true, OnlyTitle: true},
		Limit:     3,
		Deep:      true,
		FetchTopK: 1,
		Budget:    kernel.SearchBudget{DeadlineMS: 1000, MaxCalls: 2, MaxPagesPerSource: 1, MaxFetches: 1, MaxExpandedNodes: 1, MaxBytes: 1024},
	}}
	malicious := kernel.RetrievalPlan{
		Version:  "retrieval-plan/v1",
		Identity: kernel.Identity{Profile: "other", Mode: kernel.IdentityBot},
		Budget:   kernel.SearchBudget{DeadlineMS: 999999, MaxCalls: 999, MaxPagesPerSource: 99, MaxFetches: 99, MaxExpandedNodes: 99, MaxBytes: 1 << 40},
		Nodes: []kernel.PlanNode{
			{ID: "search", Op: kernel.PlanSearch, Request: json.RawMessage(`{"query":"unrelated broad query","sources":["docs"],"filters":{},"limit":200,"limit_per_source":200}`)},
			{ID: "fetch", Op: kernel.PlanMapFetch, DependsOn: []string{"search"}, MaxItems: 20, Request: json.RawMessage(`{"top_k":20,"max_items":20,"default_projection":["attachments"],"projection_by_kind":{"document":["attachments"]}}`)},
		},
		Output: []string{"search", "fetch"},
	}
	kernelStub := &researchKernel{capabilities: kernel.CapabilitySnapshot{Providers: []kernel.ProviderCapability{docsCapability}}, planResult: kernel.PlanResult{Output: map[string]kernel.NodeResult{}}}
	if _, err := (Service{Kernel: kernelStub, Planner: &researchPlanner{plan: malicious}}).Run(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(kernelStub.executedPlans) != 1 {
		t.Fatalf("executed plans=%d", len(kernelStub.executedPlans))
	}
	executed := kernelStub.executedPlans[0]
	if executed.Identity.Profile != "work" || executed.Identity.Mode != kernel.IdentityUser || executed.Identity.ScopeKey != request.Identity.Normalized().ScopeKey {
		t.Fatalf("planner changed identity: %+v", executed.Identity)
	}
	if executed.Budget != request.Budget {
		t.Fatalf("planner expanded budget: %+v want %+v", executed.Budget, request.Budget)
	}
	var searchRequest kernel.SearchRequest
	if err := json.Unmarshal(executed.Nodes[0].Request, &searchRequest); err != nil {
		t.Fatal(err)
	}
	if searchRequest.Query != request.Query || searchRequest.Limit != request.Limit || searchRequest.LimitPerSource != request.Limit || searchRequest.Filters.ChatIDs[0] != "oc_allowed" || !searchRequest.Filters.Mine || !searchRequest.Filters.OnlyTitle {
		t.Fatalf("planner broadened search request: %+v", searchRequest)
	}
	var mapFetch struct {
		TopK              int                                        `json:"top_k"`
		MaxItems          int                                        `json:"max_items"`
		DefaultProjection kernel.ProjectionSet                       `json:"default_projection"`
		ProjectionByKind  map[kernel.ObjectKind]kernel.ProjectionSet `json:"projection_by_kind"`
	}
	if err := json.Unmarshal(executed.Nodes[1].Request, &mapFetch); err != nil {
		t.Fatal(err)
	}
	if mapFetch.TopK != 1 || mapFetch.MaxItems != 1 || executed.Nodes[1].MaxItems != 1 || mapFetch.DefaultProjection != kernel.ProjectionContent || mapFetch.ProjectionByKind[kernel.KindDocument] != kernel.ProjectionStructure|kernel.ProjectionContent {
		t.Fatalf("planner expanded fetch scope: request=%+v node=%+v", mapFetch, executed.Nodes[1])
	}

	extra := malicious
	extra.Nodes = []kernel.PlanNode{{ID: "search", Op: kernel.PlanSearch, Request: json.RawMessage(`{"query":"why","sources":["messages"]}`)}}
	kernelStub.executedPlans = nil
	_, err := (Service{Kernel: kernelStub, Planner: &researchPlanner{plan: extra}}).Run(context.Background(), request)
	if detail := kernel.DetailFromError(err); err == nil || detail.Subtype != "invalid_plan" || len(kernelStub.executedPlans) != 0 {
		t.Fatalf("extra source err=%v detail=%+v executed=%d", err, detail, len(kernelStub.executedPlans))
	}

	directFetch := malicious
	directFetch.Nodes = []kernel.PlanNode{{ID: "fetch", Op: kernel.PlanFetch, Request: json.RawMessage(`{"items":[{"ref":{"source":"docs","kind":"document","native_id":"dox_secret"},"projection":["content"]}]}`)}}
	kernelStub.executedPlans = nil
	_, err = (Service{Kernel: kernelStub, Planner: &researchPlanner{plan: directFetch}}).Run(context.Background(), request)
	if detail := kernel.DetailFromError(err); err == nil || detail.Subtype != "invalid_plan" || len(kernelStub.executedPlans) != 0 {
		t.Fatalf("root fetch err=%v detail=%+v executed=%d", err, detail, len(kernelStub.executedPlans))
	}

	dummyFetch := malicious
	dummyFetch.Nodes = []kernel.PlanNode{
		{ID: "dummy", Op: kernel.PlanMerge},
		{ID: "fetch", Op: kernel.PlanFetch, DependsOn: []string{"dummy"}, Request: json.RawMessage(`{"items":[{"ref":{"source":"docs","kind":"document","native_id":"dox_secret"},"projection":["content"]}]}`)},
	}
	kernelStub.executedPlans = nil
	_, err = (Service{Kernel: kernelStub, Planner: &researchPlanner{plan: dummyFetch}}).Run(context.Background(), request)
	if detail := kernel.DetailFromError(err); err == nil || detail.Subtype != "invalid_plan" || len(kernelStub.executedPlans) != 0 {
		t.Fatalf("dummy fetch err=%v detail=%+v executed=%d", err, detail, len(kernelStub.executedPlans))
	}
}

func TestServiceRejectsDeepOperationsWithoutCallerAuthorization(t *testing.T) {
	docsCapability := kernel.ProviderCapability{Descriptor: kernel.ProviderDescriptor{ID: "test.docs", Source: kernel.SourceDocs, Operations: kernel.OperationSet{kernel.OpSearch: true, kernel.OpFetch: true}}, Status: kernel.StatusOK}
	kernelStub := &researchKernel{capabilities: kernel.CapabilitySnapshot{Providers: []kernel.ProviderCapability{docsCapability}}, planResult: kernel.PlanResult{Output: map[string]kernel.NodeResult{}}}
	plan := kernel.RetrievalPlan{
		Version: "retrieval-plan/v1",
		Nodes: []kernel.PlanNode{
			{ID: "search", Op: kernel.PlanSearch, Request: json.RawMessage(`{"query":"why","sources":["docs"]}`)},
			{ID: "fetch", Op: kernel.PlanMapFetch, DependsOn: []string{"search"}, Request: json.RawMessage(`{"top_k":1,"max_items":1}`)},
		},
		Output: []string{"search", "fetch"},
	}
	_, err := (Service{Kernel: kernelStub, Planner: &researchPlanner{plan: plan}}).Run(context.Background(), Request{UserRequest: planner.UserRequest{Query: "why", Sources: []kernel.SourceID{kernel.SourceDocs}, Deep: false}})
	if detail := kernel.DetailFromError(err); err == nil || detail.Subtype != "invalid_plan" || len(kernelStub.executedPlans) != 0 {
		t.Fatalf("shallow map_fetch err=%v detail=%+v executed=%d", err, detail, len(kernelStub.executedPlans))
	}

	queryPlan := plan
	queryPlan.Nodes = []kernel.PlanNode{{ID: "query", Op: kernel.PlanQuery, Request: json.RawMessage(`{"source":"docs","filter":{"all":true}}`)}}
	queryPlan.Output = []string{"query"}
	_, err = (Service{Kernel: kernelStub, Planner: &researchPlanner{plan: queryPlan}}).Run(context.Background(), Request{UserRequest: planner.UserRequest{Query: "why", Deep: true}})
	if detail := kernel.DetailFromError(err); err == nil || detail.Subtype != "invalid_plan" || len(kernelStub.executedPlans) != 0 {
		t.Fatalf("query op err=%v detail=%+v executed=%d", err, detail, len(kernelStub.executedPlans))
	}
}

func TestServiceUsesSessionFallbackAndDefaultPlanner(t *testing.T) {
	candidate := researchCandidate("session-candidate", kernel.KindDocument, kernel.ProjectionContent)
	artifact := researchArtifact(candidate.Ref, "session evidence")
	k := &researchKernel{
		planResult: kernel.PlanResult{SessionID: "rs_session", Output: map[string]kernel.NodeResult{}},
		session: kernel.SessionSnapshot{
			Candidates: []kernel.Candidate{candidate},
			Artifacts:  []kernel.Artifact{artifact},
			Relations:  []kernel.Relation{{Type: "related"}},
			SourceRuns: []kernel.SourceRun{{Source: kernel.SourceDocs, Status: kernel.StatusOK}},
		},
	}
	result, err := (Service{Kernel: k}).Run(context.Background(), Request{UserRequest: planner.UserRequest{Query: "why"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Planner != "rules" || len(result.CandidatePack.Candidates) != 1 || len(result.ArtifactPack.Artifacts) != 1 || len(result.ArtifactPack.Relations) != 1 || len(result.EvidencePack.Evidence) != 1 {
		t.Fatalf("result=%+v", result)
	}
}

func TestServiceDeduplicatesOutputsAndKeepsOrderOnRerankFailure(t *testing.T) {
	c1 := researchCandidate("one", kernel.KindDocument, kernel.ProjectionContent)
	c2 := researchCandidate("two", kernel.KindMessage, kernel.ProjectionContent)
	a1 := researchArtifact(c1.Ref, "one evidence")
	plan := kernel.RetrievalPlan{Version: "retrieval-plan/v1", Output: []string{"search", "fetch"}}
	k := &researchKernel{
		planResult: kernel.PlanResult{
			SessionID: "rs_output",
			Output: map[string]kernel.NodeResult{
				"search": {Candidates: []kernel.Candidate{c1, c1, c2}},
				"fetch":  {Artifacts: []kernel.Artifact{a1, a1}},
			},
		},
		sessionErr: errors.New("session unavailable"),
	}
	rerankErr := errors.New("model unavailable")
	result, err := (Service{Kernel: k, Planner: &researchPlanner{plan: plan}, Reranker: &researchReranker{err: rerankErr}}).Run(context.Background(), Request{UserRequest: planner.UserRequest{Query: "why"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.CandidatePack.Candidates) != 2 || len(result.ArtifactPack.Artifacts) != 1 {
		t.Fatalf("dedup result=%+v", result)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "保留确定性排序") || result.CandidatePack.Candidates[0].Ref.NativeID != "one" {
		t.Fatalf("warnings/order=%v/%v", result.Warnings, result.CandidatePack.Candidates)
	}
}

func TestServiceSuccessfulRerankFetchesNewArtifactsAndReportsPartial(t *testing.T) {
	c1 := researchCandidate("one", kernel.KindDocument, kernel.ProjectionContent)
	c2 := researchCandidate("two", kernel.KindMessage, kernel.ProjectionContent|kernel.ProjectionContext)
	a1 := researchArtifact(c1.Ref, "existing")
	a2 := researchArtifact(c2.Ref, "fetched")
	plan := kernel.RetrievalPlan{Version: "retrieval-plan/v1", Output: []string{"search", "fetch"}}
	k := &researchKernel{
		planResult: kernel.PlanResult{SessionID: "rs_rerank", Output: map[string]kernel.NodeResult{
			"search": {Candidates: []kernel.Candidate{c1, c2}},
			"fetch":  {Artifacts: []kernel.Artifact{a1}},
		}},
		fetchBatch: kernel.ArtifactBatch{Items: []kernel.ArtifactResult{{Artifact: &a2}, {Error: &kernel.ErrorDetail{Type: kernel.ErrMissingScope, Message: "partial item"}}}},
		fetchErr:   errors.New("partial fetch"),
	}
	reranker := &researchReranker{out: []kernel.Candidate{c2, c1}}
	result, err := (Service{Kernel: k, Planner: &researchPlanner{plan: plan}, Reranker: reranker}).Run(context.Background(), Request{UserRequest: planner.UserRequest{Query: "why", Deep: true, FetchTopK: 5}})
	if err != nil {
		t.Fatal(err)
	}
	if len(k.fetchRequests) != 1 || len(k.fetchRequests[0].Items) != 1 || k.fetchRequests[0].Items[0].Ref.NativeID != "two" {
		t.Fatalf("fetch requests=%+v", k.fetchRequests)
	}
	if len(result.ArtifactPack.Artifacts) != 2 || len(result.Warnings) != 2 || !strings.Contains(strings.Join(result.Warnings, "|"), "partial fetch") || !strings.Contains(strings.Join(result.Warnings, "|"), "partial item") {
		t.Fatalf("result artifacts/warnings=%v/%v", result.ArtifactPack.Artifacts, result.Warnings)
	}
}

func TestServiceRejectsMismatchedRerankAndSkipsUnfetchableCandidates(t *testing.T) {
	c1 := researchCandidate("one", kernel.KindUnknown, 0)
	c2 := researchCandidate("two", kernel.KindDocument, kernel.ProjectionContent)
	plan := kernel.RetrievalPlan{Version: "retrieval-plan/v1", Output: []string{"search"}}
	k := &researchKernel{planResult: kernel.PlanResult{Output: map[string]kernel.NodeResult{"search": {Candidates: []kernel.Candidate{c1, c2}}}}}
	result, err := (Service{Kernel: k, Planner: &researchPlanner{plan: plan}, Reranker: &researchReranker{out: []kernel.Candidate{c1}}}).Run(context.Background(), Request{UserRequest: planner.UserRequest{Query: "why"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "数量不一致") || len(k.fetchRequests) != 0 {
		t.Fatalf("result=%+v fetches=%v", result, k.fetchRequests)
	}
	if artifacts, warnings := (Service{Kernel: k}).fetchReranked(context.Background(), Request{}, "", []kernel.Candidate{c1}, nil); len(artifacts) != 0 || len(warnings) != 0 {
		t.Fatalf("unfetchable candidate artifacts=%v warnings=%v", artifacts, warnings)
	}
}

func TestPreferredProjectionByKind(t *testing.T) {
	tests := []struct {
		kind      kernel.ObjectKind
		available kernel.ProjectionSet
		want      kernel.ProjectionSet
	}{
		{kernel.KindDocument, kernel.ProjectionContent | kernel.ProjectionAttachments, kernel.ProjectionContent},
		{kernel.KindMessage, kernel.ProjectionContext | kernel.ProjectionAttachments, kernel.ProjectionContext},
		{kernel.KindMinute, kernel.ProjectionSummary | kernel.ProjectionAttachments, kernel.ProjectionSummary},
		{kernel.KindMeeting, kernel.ProjectionRelations, kernel.ProjectionRelations},
		{kernel.KindTask, kernel.ProjectionContent, kernel.ProjectionContent},
		{kernel.KindEvent, kernel.ProjectionRelations, kernel.ProjectionRelations},
		{kernel.KindMail, kernel.ProjectionAttachments, kernel.ProjectionAttachments},
		{kernel.KindUnknown, kernel.ProjectionSnippet, kernel.ProjectionSnippet},
	}
	for _, test := range tests {
		if got := preferredProjection(kernel.Candidate{Kind: test.kind, AvailableProjection: test.available}); got != test.want {
			t.Errorf("kind=%s projection=%s want=%s", test.kind, got, test.want)
		}
	}
}

func TestServiceStreamsPipelineAndRetrievalEventsInOrder(t *testing.T) {
	retrieval := kernel.RetrievalEvent{ID: "event-1", Type: kernel.EventNodeStarted}
	k := &researchKernel{
		events:     []kernel.RetrievalEvent{retrieval},
		planResult: kernel.PlanResult{SessionID: "rs_stream", Output: map[string]kernel.NodeResult{}},
	}
	events := []Event{}
	_, err := (Service{Kernel: k, Planner: validResearchPlanner()}).RunWithObserver(
		context.Background(),
		Request{UserRequest: planner.UserRequest{Query: "why"}},
		func(event Event) { events = append(events, event) },
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 6 {
		t.Fatalf("events=%+v", events)
	}
	wantPhases := []Phase{PhaseCapabilities, PhasePlanning, PhaseRetrieving, PhaseRetrieving, PhaseEvidence, PhaseResearchComplete}
	for index, want := range wantPhases {
		if events[index].Phase != want {
			t.Fatalf("event[%d].phase=%s want=%s events=%+v", index, events[index].Phase, want, events)
		}
	}
	if events[3].Type != EventRetrieval || events[3].Retrieval == nil || events[3].Retrieval.ID != retrieval.ID {
		t.Fatalf("retrieval event=%+v", events[3])
	}
}

type researchPlanner struct {
	plan kernel.RetrievalPlan
	err  error
}

func (p *researchPlanner) Name() string { return "test-planner" }
func (p *researchPlanner) Plan(context.Context, planner.UserRequest, kernel.CapabilitySnapshot) (kernel.RetrievalPlan, error) {
	return p.plan, p.err
}

func validResearchPlanner() *researchPlanner {
	return &researchPlanner{plan: kernel.RetrievalPlan{Version: "retrieval-plan/v1", Nodes: []kernel.PlanNode{{ID: "search", Op: kernel.PlanSearch, Request: json.RawMessage(`{"query":"why"}`)}}, Output: []string{"search"}}}
}

type researchReranker struct {
	out []kernel.Candidate
	err error
}

func (*researchReranker) Name() string { return "test-reranker" }
func (r *researchReranker) Rerank(context.Context, string, []kernel.Candidate) ([]kernel.Candidate, error) {
	return r.out, r.err
}

type researchKernel struct {
	capabilities            kernel.CapabilitySnapshot
	capabilityErr           error
	executeErr              error
	closeResultWithoutValue bool
	planResult              kernel.PlanResult
	events                  []kernel.RetrievalEvent
	session                 kernel.SessionSnapshot
	sessionErr              error
	fetchBatch              kernel.ArtifactBatch
	fetchErr                error
	fetchRequests           []kernel.FetchBatchRequest
	executedPlans           []kernel.RetrievalPlan
}

func (k *researchKernel) Capabilities(context.Context, kernel.CapabilityRequest) (kernel.CapabilitySnapshot, error) {
	return k.capabilities, k.capabilityErr
}
func (*researchKernel) Search(context.Context, kernel.SearchRequest) (kernel.SearchSnapshot, error) {
	return kernel.SearchSnapshot{}, nil
}
func (*researchKernel) Continue(context.Context, kernel.ContinueRequest) (kernel.SearchSnapshot, error) {
	return kernel.SearchSnapshot{}, nil
}
func (*researchKernel) Query(context.Context, kernel.QueryRequest) (kernel.QuerySnapshot, error) {
	return kernel.QuerySnapshot{}, nil
}
func (k *researchKernel) Fetch(_ context.Context, request kernel.FetchBatchRequest) (kernel.ArtifactBatch, error) {
	k.fetchRequests = append(k.fetchRequests, request)
	return k.fetchBatch, k.fetchErr
}
func (*researchKernel) Expand(context.Context, kernel.ExpandRequest) (kernel.RelationBatch, error) {
	return kernel.RelationBatch{}, nil
}
func (*researchKernel) Resolve(context.Context, kernel.ResolveRequest) ([]kernel.ObjectRef, error) {
	return nil, nil
}
func (k *researchKernel) Execute(_ context.Context, plan kernel.RetrievalPlan) (<-chan kernel.RetrievalEvent, <-chan kernel.PlanResult, error) {
	k.executedPlans = append(k.executedPlans, plan)
	if k.executeErr != nil {
		return nil, nil, k.executeErr
	}
	events := make(chan kernel.RetrievalEvent, len(k.events))
	for _, event := range k.events {
		events <- event
	}
	close(events)
	results := make(chan kernel.PlanResult, 1)
	if !k.closeResultWithoutValue {
		results <- k.planResult
	}
	close(results)
	return events, results, nil
}
func (k *researchKernel) Session(context.Context, string, kernel.Identity) (kernel.SessionSnapshot, error) {
	return k.session, k.sessionErr
}

func researchCandidate(id string, kind kernel.ObjectKind, available kernel.ProjectionSet) kernel.Candidate {
	return kernel.Candidate{Ref: kernel.ObjectRef{NativeID: id, CanonicalID: "canonical:" + id, Kind: kind}, Kind: kind, AvailableProjection: available}
}

func researchArtifact(ref kernel.ObjectRef, text string) kernel.Artifact {
	return kernel.Artifact{Ref: ref, Projection: kernel.ProjectionContent, Chunks: []kernel.ContentChunk{{ID: "content", Kind: "paragraph", Text: text}}}
}

var _ ai.Reranker = (*researchReranker)(nil)
var _ planner.Planner = (*researchPlanner)(nil)
var _ kernel.Kernel = (*researchKernel)(nil)
