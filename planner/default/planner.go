package defaultplanner

import (
	"context"
	"encoding/json"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
)

type Planner struct{}

func (Planner) Name() string { return "default" }
func (Planner) Plan(_ context.Context, r planner.UserRequest, _ kernel.CapabilitySnapshot) (kernel.RetrievalPlan, error) {
	search := kernel.SearchRequest{Query: r.Query, Sources: r.Sources, Filters: r.Filters, Identity: r.Identity, Limit: r.Limit, Budget: r.Budget}
	raw, _ := json.Marshal(search)
	nodes := []kernel.PlanNode{{ID: "search", Op: kernel.PlanSearch, Request: raw, Priority: 100}}
	output := []string{"search"}
	if r.Deep {
		max := r.FetchTopK
		if max <= 0 {
			max = 8
		}
		fetchRaw, _ := json.Marshal(map[string]any{"top_k": max, "max_items": max, "projection_by_kind": map[kernel.ObjectKind]kernel.ProjectionSet{kernel.KindDocument: kernel.ProjectionStructure | kernel.ProjectionContent, kernel.KindMessage: kernel.ProjectionContent | kernel.ProjectionContext, kernel.KindMinute: kernel.ProjectionSummary | kernel.ProjectionRelations, kernel.KindMeeting: kernel.ProjectionContent | kernel.ProjectionRelations, kernel.KindTask: kernel.ProjectionContent, kernel.KindMail: kernel.ProjectionContent | kernel.ProjectionContext}})
		nodes = append(nodes, kernel.PlanNode{ID: "fetch_top", Op: kernel.PlanMapFetch, DependsOn: []string{"search"}, Request: fetchRaw, Priority: 50, MaxItems: max})
		output = []string{"search", "fetch_top"}
	}
	return kernel.RetrievalPlan{Version: "retrieval-plan/v1", Identity: r.Identity, Budget: r.Budget, Nodes: nodes, Output: output}, nil
}
