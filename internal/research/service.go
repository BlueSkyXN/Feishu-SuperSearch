package research

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/BlueSkyXN/Feishu-SuperSearch/ai"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
	rulesplanner "github.com/BlueSkyXN/Feishu-SuperSearch/planner/rules"
	"github.com/BlueSkyXN/Feishu-SuperSearch/synthesis"
)

// Service runs the bounded retrieval workflow: plan, execute, fetch selected
// candidates, extract an evidence pack, and produce a deterministic digest.
// A caller may replace Planner or feed EvidencePack into an external LLM.
type Service struct {
	Kernel   kernel.Kernel
	Planner  planner.Planner
	Reranker ai.Reranker
}

type Request struct {
	planner.UserRequest
}

type Result struct {
	Planner       string                  `json:"planner"`
	Plan          kernel.RetrievalPlan    `json:"plan"`
	PlanResult    kernel.PlanResult       `json:"plan_result"`
	CandidatePack synthesis.CandidatePack `json:"candidate_pack"`
	ArtifactPack  synthesis.ArtifactPack  `json:"artifact_pack"`
	EvidencePack  synthesis.EvidencePack  `json:"evidence_pack"`
	Summary       string                  `json:"summary"`
	Events        []kernel.RetrievalEvent `json:"events,omitempty"`
	Warnings      []string                `json:"warnings,omitempty"`
}

func (s Service) Run(ctx context.Context, req Request) (Result, error) {
	return s.RunWithObserver(ctx, req, nil)
}

func (s Service) RunWithObserver(ctx context.Context, req Request, observer Observer) (Result, error) {
	if s.Kernel == nil {
		return Result{}, fmt.Errorf("research kernel is nil")
	}
	emit := func(event Event) {
		if observer != nil {
			observer(event)
		}
	}
	p := s.Planner
	if p == nil {
		p = rulesplanner.Planner{}
	}
	req.Identity = req.Identity.Normalized()
	emit(progressEvent(PhaseCapabilities, "正在读取 Provider 能力"))
	caps, err := s.Kernel.Capabilities(ctx, kernel.CapabilityRequest{Identity: req.Identity})
	if err != nil {
		return Result{}, err
	}
	emit(progressEvent(PhasePlanning, "正在生成受限检索计划"))
	plan, err := p.Plan(ctx, req.UserRequest, caps)
	if err != nil {
		return Result{}, err
	}
	plan, err = constrainPlan(plan, req.UserRequest, caps)
	if err != nil {
		return Result{}, err
	}
	emit(progressEvent(PhaseRetrieving, "正在检索并读取候选内容"))
	eventsCh, resultCh, err := s.Kernel.Execute(ctx, plan)
	if err != nil {
		return Result{}, err
	}
	events := make([]kernel.RetrievalEvent, 0, 32)
	for ev := range eventsCh {
		events = append(events, ev)
		emit(retrievalEvent(ev))
	}
	planResult, ok := <-resultCh
	if !ok {
		return Result{}, fmt.Errorf("plan executor closed without a result")
	}

	candidates := make([]kernel.Candidate, 0)
	artifacts := make([]kernel.Artifact, 0)
	seenCandidates := map[string]bool{}
	seenArtifacts := map[string]bool{}
	outputIDs := plan.Output
	if len(outputIDs) == 0 {
		outputIDs = make([]string, 0, len(planResult.Output))
		for id := range planResult.Output {
			outputIDs = append(outputIDs, id)
		}
	}
	for _, id := range outputIDs {
		n, exists := planResult.Output[id]
		if !exists {
			n = planResult.Nodes[id]
		}
		for _, c := range n.Candidates {
			key := c.Ref.CanonicalID
			if key == "" {
				key = c.Ref.NativeID
			}
			if !seenCandidates[key] {
				seenCandidates[key] = true
				candidates = append(candidates, c)
			}
		}
		for _, a := range n.Artifacts {
			key := a.Ref.CanonicalID
			if key == "" {
				key = a.Ref.NativeID
			}
			if !seenArtifacts[key] {
				seenArtifacts[key] = true
				artifacts = append(artifacts, a)
			}
		}
	}
	var sourceRuns []kernel.SourceRun
	var relations []kernel.Relation
	warnings := []string{}
	if planResult.SessionID != "" {
		if snap, e := s.Kernel.Session(ctx, planResult.SessionID, req.Identity); e == nil {
			sourceRuns = snap.SourceRuns
			relations = snap.Relations
			if len(candidates) == 0 {
				candidates = snap.Candidates
			}
			if len(artifacts) == 0 {
				artifacts = snap.Artifacts
			}
		}
	}
	for _, artifact := range artifacts {
		key := artifact.Ref.CanonicalID
		if key == "" {
			key = artifact.Ref.NativeID
		}
		seenArtifacts[key] = true
	}
	if s.Reranker != nil && len(candidates) > 1 {
		emit(progressEvent(PhaseReranking, "正在重排高价值候选"))
		reranked, rerankErr := s.Reranker.Rerank(ctx, req.Query, candidates)
		if rerankErr != nil {
			warnings = append(warnings, "AI rerank 失败，保留确定性排序："+rerankErr.Error())
		} else if len(reranked) == len(candidates) {
			candidates = reranked
			if req.Deep {
				additional, fetchWarnings := s.fetchReranked(ctx, req, planResult.SessionID, candidates, artifacts)
				warnings = append(warnings, fetchWarnings...)
				for _, artifact := range additional {
					key := artifact.Ref.CanonicalID
					if key == "" {
						key = artifact.Ref.NativeID
					}
					if !seenArtifacts[key] {
						seenArtifacts[key] = true
						artifacts = append(artifacts, artifact)
					}
				}
			}
		} else {
			warnings = append(warnings, "AI rerank 返回候选数量不一致，保留确定性排序")
		}
	}
	cp := synthesis.CandidatePack{Query: req.Query, SessionID: planResult.SessionID, Candidates: candidates, Sources: sourceRuns}
	ap := synthesis.ArtifactPack{SessionID: planResult.SessionID, Artifacts: artifacts, Relations: relations}
	emit(progressEvent(PhaseEvidence, "正在提取可追溯证据"))
	ep := synthesis.ExtractEvidence(req.Query, ap)
	result := Result{Planner: p.Name(), Plan: plan, PlanResult: planResult, CandidatePack: cp, ArtifactPack: ap, EvidencePack: ep, Summary: synthesis.Summarize(ep), Events: events, Warnings: warnings}
	emit(progressEvent(PhaseResearchComplete, "证据包已生成"))
	return result, nil
}

func constrainPlan(plan kernel.RetrievalPlan, request planner.UserRequest, capabilities kernel.CapabilitySnapshot) (kernel.RetrievalPlan, error) {
	plan.Identity = request.Identity.Normalized()
	plan.Budget = clampBudget(plan.Budget, request.Budget.WithDefaults())
	allowed := map[kernel.SourceID]bool{}
	for _, source := range request.Sources {
		if source != "" {
			allowed[source] = true
		}
	}
	nodesByID := make(map[string]kernel.PlanNode, len(plan.Nodes))
	for _, node := range plan.Nodes {
		nodesByID[node.ID] = node
	}
	for index := range plan.Nodes {
		node := &plan.Nodes[index]
		if !researchPlannerOperationAllowed(node.Op) {
			return plan, invalidPlan(node.ID, fmt.Sprintf("operation %s is not authorized for research planning", node.Op))
		}
		operation := planOperation(node.Op)
		if operation == "" {
			continue
		}
		var sources []kernel.SourceID
		switch node.Op {
		case kernel.PlanSearch:
			var raw struct {
				Source        kernel.SourceID            `json:"source"`
				Sources       []kernel.SourceID          `json:"sources"`
				SourceQueries map[kernel.SourceID]string `json:"source_queries"`
			}
			if err := json.Unmarshal(node.Request, &raw); err != nil {
				return plan, invalidPlan(node.ID, err.Error())
			}
			if raw.Source != "" {
				sources = []kernel.SourceID{raw.Source}
			} else {
				sources = raw.Sources
			}
			if len(allowed) > 0 && len(sources) == 0 {
				var object map[string]any
				if err := json.Unmarshal(node.Request, &object); err != nil {
					return plan, invalidPlan(node.ID, err.Error())
				}
				object["sources"] = request.Sources
				delete(object, "source")
				rewritten, err := json.Marshal(object)
				if err != nil {
					return plan, invalidPlan(node.ID, err.Error())
				}
				node.Request = rewritten
				sources = append([]kernel.SourceID(nil), request.Sources...)
			}
			for source := range raw.SourceQueries {
				if len(allowed) > 0 && !allowed[source] {
					return plan, invalidPlan(node.ID, fmt.Sprintf("source query %s is outside the caller source set", source))
				}
			}
			var object map[string]any
			if err := json.Unmarshal(node.Request, &object); err != nil {
				return plan, invalidPlan(node.ID, err.Error())
			}
			object["query"] = request.Query
			object["filters"] = request.Filters
			object["limit"] = normalizedResearchLimit(request)
			object["limit_per_source"] = normalizedResearchLimitPerSource(request)
			rewritten, err := json.Marshal(object)
			if err != nil {
				return plan, invalidPlan(node.ID, err.Error())
			}
			node.Request = rewritten
		case kernel.PlanMapFetch:
			if !hasRetrievalAncestor(*node, nodesByID) {
				return plan, invalidPlan(node.ID, "map_fetch nodes require a search ancestor")
			}
			if !request.Deep {
				return plan, invalidPlan(node.ID, "map_fetch requires deep retrieval to be requested")
			}
			var raw struct {
				TopK     int `json:"top_k"`
				MaxItems int `json:"max_items"`
			}
			if err := json.Unmarshal(node.Request, &raw); err != nil {
				return plan, invalidPlan(node.ID, err.Error())
			}
			fetchLimit := normalizedResearchFetchTopK(request, plan.Budget)
			if raw.TopK <= 0 || raw.TopK > fetchLimit {
				raw.TopK = fetchLimit
			}
			if raw.MaxItems <= 0 || raw.MaxItems > fetchLimit {
				raw.MaxItems = fetchLimit
			}
			if node.MaxItems <= 0 || node.MaxItems > fetchLimit {
				node.MaxItems = fetchLimit
			}
			var object map[string]any
			if err := json.Unmarshal(node.Request, &object); err != nil {
				return plan, invalidPlan(node.ID, err.Error())
			}
			object["top_k"] = raw.TopK
			object["max_items"] = raw.MaxItems
			object["projection_by_kind"] = researchProjectionByKind()
			object["default_projection"] = kernel.ProjectionContent
			rewritten, err := json.Marshal(object)
			if err != nil {
				return plan, invalidPlan(node.ID, err.Error())
			}
			node.Request = rewritten
		}
		for _, source := range sources {
			if source == "" {
				continue
			}
			if len(allowed) > 0 && !allowed[source] {
				return plan, invalidPlan(node.ID, fmt.Sprintf("source %s is outside the caller source set", source))
			}
			if !capabilitySupports(capabilities, source, operation) {
				return plan, invalidPlan(node.ID, fmt.Sprintf("source %s does not advertise operation %s", source, operation))
			}
		}
	}
	return plan, nil
}

func researchPlannerOperationAllowed(op kernel.PlanOp) bool {
	switch op {
	case kernel.PlanSearch, kernel.PlanMapFetch, kernel.PlanMerge, kernel.PlanDedup, kernel.PlanRank, kernel.PlanLimit, kernel.PlanProject:
		return true
	default:
		return false
	}
}

func normalizedResearchLimit(request planner.UserRequest) int {
	if request.Limit > 0 {
		return request.Limit
	}
	return 40
}

func normalizedResearchLimitPerSource(request planner.UserRequest) int {
	limit := normalizedResearchLimit(request)
	if limit < 8 {
		return limit
	}
	return 8
}

func normalizedResearchFetchTopK(request planner.UserRequest, budget kernel.SearchBudget) int {
	limit := request.FetchTopK
	if limit <= 0 {
		limit = 8
	}
	maxFetches := budget.WithDefaults().MaxFetches
	if maxFetches > 0 && limit > maxFetches {
		limit = maxFetches
	}
	return limit
}

func researchProjectionByKind() map[kernel.ObjectKind]kernel.ProjectionSet {
	return map[kernel.ObjectKind]kernel.ProjectionSet{
		kernel.KindDocument: kernel.ProjectionStructure | kernel.ProjectionContent,
		kernel.KindMessage:  kernel.ProjectionContent | kernel.ProjectionContext | kernel.ProjectionRelations,
		kernel.KindMinute:   kernel.ProjectionSummary | kernel.ProjectionStructure | kernel.ProjectionContent | kernel.ProjectionRelations,
		kernel.KindMeeting:  kernel.ProjectionContent | kernel.ProjectionRelations,
		kernel.KindTask:     kernel.ProjectionContent | kernel.ProjectionRelations,
		kernel.KindEvent:    kernel.ProjectionContent | kernel.ProjectionRelations,
		kernel.KindMail:     kernel.ProjectionContent | kernel.ProjectionContext,
	}
}

func clampBudget(candidate, limit kernel.SearchBudget) kernel.SearchBudget {
	clampInt := func(value, maximum int) int {
		if value <= 0 || value > maximum {
			return maximum
		}
		return value
	}
	clampInt64 := func(value, maximum int64) int64 {
		if value <= 0 || value > maximum {
			return maximum
		}
		return value
	}
	return kernel.SearchBudget{
		DeadlineMS:        clampInt(candidate.DeadlineMS, limit.DeadlineMS),
		MaxCalls:          clampInt(candidate.MaxCalls, limit.MaxCalls),
		MaxPagesPerSource: clampInt(candidate.MaxPagesPerSource, limit.MaxPagesPerSource),
		MaxFetches:        clampInt(candidate.MaxFetches, limit.MaxFetches),
		MaxExpandedNodes:  clampInt(candidate.MaxExpandedNodes, limit.MaxExpandedNodes),
		MaxBytes:          clampInt64(candidate.MaxBytes, limit.MaxBytes),
	}
}

func planOperation(op kernel.PlanOp) kernel.Operation {
	switch op {
	case kernel.PlanSearch:
		return kernel.OpSearch
	case kernel.PlanQuery:
		return kernel.OpQuery
	case kernel.PlanFetch, kernel.PlanMapFetch:
		return kernel.OpFetch
	case kernel.PlanExpand:
		return kernel.OpExpand
	case kernel.PlanResolve:
		return kernel.OpResolve
	default:
		return ""
	}
}

func capabilitySupports(snapshot kernel.CapabilitySnapshot, source kernel.SourceID, operation kernel.Operation) bool {
	for _, capability := range snapshot.Providers {
		if capability.Descriptor.Source != source || !capability.Descriptor.Operations[operation] {
			continue
		}
		if capability.Status == kernel.StatusOK {
			return true
		}
		if operationCapability, ok := capability.Operations[operation]; ok && operationCapability.Status == kernel.StatusOK {
			return true
		}
	}
	return false
}

func hasRetrievalAncestor(node kernel.PlanNode, nodes map[string]kernel.PlanNode) bool {
	visited := map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if visited[id] {
			return false
		}
		visited[id] = true
		dependency, ok := nodes[id]
		if !ok {
			return false
		}
		if dependency.Op == kernel.PlanSearch {
			return true
		}
		for _, parent := range dependency.DependsOn {
			if visit(parent) {
				return true
			}
		}
		return false
	}
	for _, dependency := range node.DependsOn {
		if visit(dependency) {
			return true
		}
	}
	return false
}

func invalidPlan(nodeID, message string) error {
	return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Subtype: "invalid_plan", Message: "planner produced an unauthorized retrieval plan", Details: map[string]any{"node_id": nodeID, "reason": message}}
}

func (s Service) fetchReranked(ctx context.Context, req Request, sessionID string, candidates []kernel.Candidate, artifacts []kernel.Artifact) ([]kernel.Artifact, []string) {
	limit := normalizedResearchFetchTopK(req.UserRequest, req.Budget)
	if limit > len(candidates) {
		limit = len(candidates)
	}
	existing := map[string]bool{}
	for _, artifact := range artifacts {
		key := artifact.Ref.CanonicalID
		if key == "" {
			key = artifact.Ref.NativeID
		}
		existing[key] = true
	}
	items := make([]kernel.FetchRequest, 0, limit)
	for _, candidate := range candidates[:limit] {
		key := candidate.Ref.CanonicalID
		if key == "" {
			key = candidate.Ref.NativeID
		}
		if existing[key] {
			continue
		}
		projection := preferredProjection(candidate)
		if projection == 0 {
			continue
		}
		items = append(items, kernel.FetchRequest{Ref: candidate.Ref, Projection: projection})
	}
	if len(items) == 0 {
		return nil, nil
	}
	batch, err := s.Kernel.Fetch(ctx, kernel.FetchBatchRequest{SessionID: sessionID, Identity: req.Identity, Items: items, Budget: req.Budget})
	warnings := []string{}
	if err != nil {
		warnings = append(warnings, "rerank 后补充读取失败："+err.Error())
	}
	out := make([]kernel.Artifact, 0, len(batch.Items))
	for _, item := range batch.Items {
		if item.Artifact != nil {
			out = append(out, *item.Artifact)
		}
		if item.Error != nil {
			warnings = append(warnings, item.Error.Message)
		}
	}
	return out, warnings
}

func preferredProjection(candidate kernel.Candidate) kernel.ProjectionSet {
	preferred := researchProjectionByKind()[candidate.Kind]
	projection := preferred & candidate.AvailableProjection
	if projection == 0 {
		projection = candidate.AvailableProjection
	}
	return projection
}
