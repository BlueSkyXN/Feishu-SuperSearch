package planvalidate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestValidateJSONAcceptsValidPlan(t *testing.T) {
	raw := []byte(`{
		"version":"retrieval-plan/v1",
		"budget":{"deadline_ms":5000,"max_calls":4},
		"nodes":[
			{"id":"search","op":"search","request":{"query":"A 项目","sources":["docs"]}},
			{"id":"fetch","op":"map_fetch","depends_on":["search"],"max_items":2,"request":{"top_k":2,"default_projection":["content"]}}
		],
		"output":["search","fetch"],
		"metadata":{"planner":"ai","fallback":{"used":false}}
	}`)
	if err := ValidateJSON(raw); err != nil {
		t.Fatal(err)
	}
}

func TestValidateJSONRejectsSchemaViolations(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "unknown field",
			raw:  `{"version":"retrieval-plan/v1","nodes":[{"id":"search","op":"search","request":{"query":"x"}}],"surprise":true}`,
		},
		{
			name: "illegal op",
			raw:  `{"version":"retrieval-plan/v1","nodes":[{"id":"shell","op":"shell","request":{}}]}`,
		},
		{
			name: "illegal retry",
			raw:  `{"version":"retrieval-plan/v1","nodes":[{"id":"search","op":"search","request":{"query":"x"},"retry":{"max_attempts":11}}]}`,
		},
		{
			name: "illegal budget",
			raw:  `{"version":"retrieval-plan/v1","budget":{"max_calls":0},"nodes":[{"id":"search","op":"search","request":{"query":"x"}}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateJSON([]byte(test.raw)); err == nil || !strings.Contains(err.Error(), "schema validation") {
				t.Fatalf("expected schema validation error, got %v", err)
			}
		})
	}
}

func TestValidateRejectsDanglingDependency(t *testing.T) {
	plan := validTypedPlan()
	plan.Nodes[0].DependsOn = []string{"missing"}
	if err := Validate(plan); err == nil || !strings.Contains(err.Error(), "depends on unknown node") {
		t.Fatalf("expected dangling dependency error, got %v", err)
	}
}

func TestValidateRejectsDependencyCycle(t *testing.T) {
	plan := validTypedPlan()
	plan.Nodes = append(plan.Nodes, kernel.PlanNode{ID: "rank", Op: kernel.PlanRank, DependsOn: []string{"search"}})
	plan.Nodes[0].DependsOn = []string{"rank"}
	if err := Validate(plan); err == nil || !strings.Contains(err.Error(), "dependency cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestValidateRejectsDanglingOutput(t *testing.T) {
	plan := validTypedPlan()
	plan.Output = []string{"missing"}
	if err := Validate(plan); err == nil || !strings.Contains(err.Error(), "output references unknown node") {
		t.Fatalf("expected dangling output error, got %v", err)
	}
}

func TestValidateRejectsDuplicateNode(t *testing.T) {
	plan := validTypedPlan()
	plan.Nodes = append(plan.Nodes, plan.Nodes[0])
	if err := Validate(plan); err == nil || !strings.Contains(err.Error(), "duplicate node") {
		t.Fatalf("expected duplicate node error, got %v", err)
	}
}

func TestValidateJSONRejectsInvalidRequestShape(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "unknown request field",
			raw:  `{"version":"retrieval-plan/v1","nodes":[{"id":"search","op":"search","request":{"query":"x","command":"rm"}}]}`,
			want: "unknown field",
		},
		{
			name: "missing search query",
			raw:  `{"version":"retrieval-plan/v1","nodes":[{"id":"search","op":"search","request":{"sources":["docs"]}}]}`,
			want: "non-empty query",
		},
		{
			name: "wrong request type",
			raw:  `{"version":"retrieval-plan/v1","nodes":[{"id":"search","op":"search","request":{"query":42}}]}`,
			want: "request shape",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateJSON([]byte(test.raw))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected error containing %q, got %v", test.want, err)
			}
		})
	}
}

func TestValidateNodeRequestAcceptsSupportedOperations(t *testing.T) {
	tests := []kernel.PlanNode{
		{ID: "search", Op: kernel.PlanSearch, Request: json.RawMessage(`{"source":"docs","query":"A 项目","limit":1}`)},
		{ID: "query", Op: kernel.PlanQuery, Request: json.RawMessage(`{"source":"tasks","container":{"native_id":"task-list"},"limit":1}`)},
		{ID: "fetch", Op: kernel.PlanFetch, Request: json.RawMessage(`{"items":[{"ref":{"native_id":"doc-1"},"projection":["content"]}]}`)},
		{ID: "expand", Op: kernel.PlanExpand, Request: json.RawMessage(`{"ref":{"native_id":"doc-1"},"max_depth":2}`)},
		{ID: "resolve", Op: kernel.PlanResolve, Request: json.RawMessage(`{"text":"张三","limit":1}`)},
		{ID: "rank", Op: kernel.PlanRank, Request: json.RawMessage(`{"limit":2,"k0":30,"weights":{"docs":1},"quotas":{"docs":1},"source_quota":true}`)},
		{ID: "limit", Op: kernel.PlanLimit, Request: json.RawMessage(`{"top_k":2}`)},
		{ID: "map", Op: kernel.PlanMapFetch, Request: json.RawMessage(`{"top_k":2,"max_items":3,"projection_by_kind":{"document":["summary"]},"default_projection":["content"]}`)},
		{ID: "merge", Op: kernel.PlanMerge},
		{ID: "dedup", Op: kernel.PlanDedup, Request: json.RawMessage(`null`)},
		{ID: "project", Op: kernel.PlanProject, Request: json.RawMessage(`{}`)},
	}
	for _, node := range tests {
		t.Run(string(node.Op), func(t *testing.T) {
			if err := validateNodeRequest(node); err != nil {
				t.Fatalf("validateNodeRequest(%s): %v", node.Op, err)
			}
		})
	}
}

func TestValidateNodeRequestRejectsOperationSpecificErrors(t *testing.T) {
	tests := []struct {
		name    string
		op      kernel.PlanOp
		request string
		want    string
	}{
		{"required search request", kernel.PlanSearch, `null`, "request is required"},
		{"empty search query", kernel.PlanSearch, `{}`, "non-empty query"},
		{"conflicting search sources", kernel.PlanSearch, `{"query":"x","source":"docs","sources":["tasks"]}`, "both source and sources"},
		{"invalid search strategy", kernel.PlanSearch, `{"query":"x","strategy":{"profile":"turbo"}}`, "invalid search request"},
		{"required query request", kernel.PlanQuery, ``, "request is required"},
		{"query source", kernel.PlanQuery, `{}`, "requires source"},
		{"query limit", kernel.PlanQuery, `{"source":"tasks","limit":-1}`, "must not be negative"},
		{"query container", kernel.PlanQuery, `{"source":"tasks","container":{}}`, "invalid query container"},
		{"fetch items", kernel.PlanFetch, `{"items":[]}`, "at least one item"},
		{"fetch ref", kernel.PlanFetch, `{"items":[{"ref":{},"projection":["content"]}]}`, "invalid fetch item"},
		{"fetch projection", kernel.PlanFetch, `{"items":[{"ref":{"native_id":"doc-1"},"projection":[]}]}`, "requires a projection"},
		{"expand ref", kernel.PlanExpand, `{"ref":{}}`, "invalid expand ref"},
		{"expand depth", kernel.PlanExpand, `{"ref":{"native_id":"doc-1"},"max_depth":3}`, "between 0 and 2"},
		{"resolve text", kernel.PlanResolve, `{}`, "non-empty text"},
		{"resolve limit", kernel.PlanResolve, `{"text":"person","limit":-1}`, "must not be negative"},
		{"rank limit", kernel.PlanRank, `{"limit":-1}`, "must not be negative"},
		{"rank weight", kernel.PlanRank, `{"weights":{"docs":-1}}`, "weight for source"},
		{"rank quota", kernel.PlanRank, `{"quotas":{"docs":-1}}`, "quota for source"},
		{"limit top k", kernel.PlanLimit, `{"top_k":-1}`, "must not be negative"},
		{"map fetch negative", kernel.PlanMapFetch, `{"top_k":-1}`, "must not be negative"},
		{"map fetch maximum", kernel.PlanMapFetch, `{"max_items":101}`, "must not exceed 100"},
		{"merge fields", kernel.PlanMerge, `{"unexpected":true}`, "does not accept request fields"},
		{"merge malformed", kernel.PlanMerge, `{`, "invalid merge request shape"},
		{"multiple values", kernel.PlanLimit, `{} {}`, "multiple JSON values"},
		{"unsupported", kernel.PlanOp("shell"), `{}`, "unsupported operation"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node := kernel.PlanNode{ID: "node", Op: test.op, Request: json.RawMessage(test.request)}
			err := validateNodeRequest(node)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected error containing %q, got %v", test.want, err)
			}
		})
	}
}

func TestValidationRejectsMalformedInputAndLocalGraphErrors(t *testing.T) {
	if err := ValidateJSON([]byte(`{"version"`)); err == nil || !strings.Contains(err.Error(), "invalid retrieval plan JSON") {
		t.Fatalf("malformed JSON error=%v", err)
	}
	plan := validTypedPlan()
	plan.Metadata["unencodable"] = func() {}
	if err := Validate(plan); err == nil || !strings.Contains(err.Error(), "encode retrieval plan") {
		t.Fatalf("marshal error=%v", err)
	}

	graphTests := []struct {
		name string
		plan kernel.RetrievalPlan
		want string
	}{
		{"self dependency", kernel.RetrievalPlan{Nodes: []kernel.PlanNode{{ID: "self", Op: kernel.PlanMerge, DependsOn: []string{"self"}}}}, "depends on itself"},
		{"empty dependency", kernel.RetrievalPlan{Nodes: []kernel.PlanNode{{ID: "node", Op: kernel.PlanMerge, DependsOn: []string{" "}}}}, "empty dependency"},
		{"empty id", kernel.RetrievalPlan{Nodes: []kernel.PlanNode{{ID: " ", Op: kernel.PlanMerge}}}, "empty id"},
	}
	for _, test := range graphTests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateSemantics(test.plan); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected error containing %q, got %v", test.want, err)
			}
		})
	}

	diamond := kernel.RetrievalPlan{Nodes: []kernel.PlanNode{
		{ID: "root", Op: kernel.PlanMerge},
		{ID: "left", Op: kernel.PlanMerge, DependsOn: []string{"root"}},
		{ID: "right", Op: kernel.PlanMerge, DependsOn: []string{"root"}},
		{ID: "output", Op: kernel.PlanMerge, DependsOn: []string{"left", "right"}},
	}}
	if err := validateSemantics(diamond); err != nil {
		t.Fatalf("valid shared dependency graph: %v", err)
	}

	var target struct{}
	if err := decodeStrict([]byte(`{]`), &target); err == nil {
		t.Fatalf("expected malformed strict decode error, got %v", err)
	}
}

func validTypedPlan() kernel.RetrievalPlan {
	request, _ := json.Marshal(map[string]any{"query": "A 项目", "sources": []string{"docs"}})
	return kernel.RetrievalPlan{
		Version: "retrieval-plan/v1",
		Nodes:   []kernel.PlanNode{{ID: "search", Op: kernel.PlanSearch, Request: request}},
		Output:  []string{"search"},
		Metadata: map[string]any{
			"planner":  "ai",
			"fallback": map[string]any{"used": false},
		},
	}
}
