package defaultplanner

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/planvalidate"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
)

func TestPlannerBuildsShallowAndDeepPlans(t *testing.T) {
	p := Planner{}
	if p.Name() != "default" {
		t.Fatalf("name=%q", p.Name())
	}
	shallow, err := p.Plan(context.Background(), planner.UserRequest{Query: "A 项目", Sources: []kernel.SourceID{kernel.SourceDocs}, Limit: 3}, kernel.CapabilitySnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(shallow.Nodes) != 1 || shallow.Nodes[0].Op != kernel.PlanSearch || len(shallow.Output) != 1 {
		t.Fatalf("shallow plan=%+v", shallow)
	}
	var search kernel.SearchRequest
	if err := json.Unmarshal(shallow.Nodes[0].Request, &search); err != nil {
		t.Fatal(err)
	}
	if search.Query != "A 项目" || search.Limit != 3 || len(search.Sources) != 1 {
		t.Fatalf("search request=%+v", search)
	}
	if err := planvalidate.Validate(shallow); err != nil {
		t.Fatalf("shallow plan is invalid: %v", err)
	}

	for _, fetchTop := range []int{0, 3} {
		deep, err := p.Plan(context.Background(), planner.UserRequest{Query: "A 项目", Deep: true, FetchTopK: fetchTop}, kernel.CapabilitySnapshot{})
		if err != nil {
			t.Fatal(err)
		}
		want := fetchTop
		if want == 0 {
			want = 8
		}
		if len(deep.Nodes) != 2 || deep.Nodes[1].Op != kernel.PlanMapFetch || deep.Nodes[1].MaxItems != want || len(deep.Output) != 2 {
			t.Fatalf("deep plan=%+v", deep)
		}
		var request struct {
			TopK     int `json:"top_k"`
			MaxItems int `json:"max_items"`
		}
		if err := json.Unmarshal(deep.Nodes[1].Request, &request); err != nil {
			t.Fatal(err)
		}
		if request.TopK != want || request.MaxItems != want {
			t.Fatalf("map_fetch request=%+v want=%d", request, want)
		}
		if err := planvalidate.Validate(deep); err != nil {
			t.Fatalf("deep plan is invalid: %v", err)
		}
	}
}
