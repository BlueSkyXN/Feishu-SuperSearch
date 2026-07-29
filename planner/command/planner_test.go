package command

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
)

func TestExternalCommandPlanner(t *testing.T) {
	p := Planner{
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestPlannerHelperProcess", "--", "--planner-helper"},
		Timeout:        2 * time.Second,
		MaxStdoutBytes: 1 << 20,
		MaxStderrBytes: 1 << 16,
	}
	plan, err := p.Plan(context.Background(), planner.UserRequest{Query: "A 项目", Identity: kernel.Identity{Mode: kernel.IdentityUser}}, kernel.CapabilitySnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != "retrieval-plan/v1" || len(plan.Nodes) != 1 || plan.Nodes[0].Op != kernel.PlanSearch {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestExternalCommandPlannerRejectsInvalidJSON(t *testing.T) {
	p := Planner{
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestPlannerHelperProcess", "--", "--planner-helper-invalid"},
		Timeout:        2 * time.Second,
		MaxStdoutBytes: 1 << 20,
		MaxStderrBytes: 1 << 16,
	}
	_, err := p.Plan(context.Background(), planner.UserRequest{Query: "A 项目"}, kernel.CapabilitySnapshot{})
	if err == nil || !strings.Contains(err.Error(), "invalid retrieval plan JSON") {
		t.Fatalf("expected invalid JSON error, got %v", err)
	}
}

func TestExternalCommandPlannerRejectsInvalidPlan(t *testing.T) {
	p := Planner{
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestPlannerHelperProcess", "--", "--planner-helper-invalid-plan"},
		Timeout:        2 * time.Second,
		MaxStdoutBytes: 1 << 20,
		MaxStderrBytes: 1 << 16,
	}
	_, err := p.Plan(context.Background(), planner.UserRequest{Query: "A 项目"}, kernel.CapabilitySnapshot{})
	if err == nil || !strings.Contains(err.Error(), "invalid plan") || !strings.Contains(err.Error(), "schema validation") {
		t.Fatalf("expected schema validation error, got %v", err)
	}
}

func TestPlannerHelperProcess(t *testing.T) {
	mode := ""
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "--planner-helper") {
			mode = arg
		}
	}
	if mode == "" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	if mode == "--planner-helper-invalid" {
		_, _ = os.Stdout.WriteString("not-json")
		os.Exit(0)
	}
	if mode == "--planner-helper-invalid-plan" {
		_, _ = os.Stdout.WriteString(`{"version":"retrieval-plan/v1","nodes":[{"id":"search","op":"search","request":{"query":"x"}}],"unexpected":true}`)
		os.Exit(0)
	}
	req, _ := json.Marshal(kernel.SearchRequest{Query: "A 项目", Sources: []kernel.SourceID{kernel.SourceDocs}})
	plan := kernel.RetrievalPlan{
		Version: "retrieval-plan/v1",
		Nodes:   []kernel.PlanNode{{ID: "search", Op: kernel.PlanSearch, Request: req}},
		Output:  []string{"search"},
	}
	_ = json.NewEncoder(os.Stdout).Encode(plan)
	os.Exit(0)
}
