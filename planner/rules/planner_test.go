package rules

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/planvalidate"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
)

func TestPlannerSelectsSemanticSourcesAndBuildsDeepPlan(t *testing.T) {
	caps := capabilities(kernel.SourceDocs, kernel.SourceMessages, kernel.SourceMinutes, kernel.SourceMeetings, kernel.SourceTasks, kernel.SourceCalendar, kernel.SourcePeople, kernel.SourceChats, kernel.SourceMail)
	p := Planner{}
	plan, err := p.Plan(context.Background(), planner.UserRequest{Query: "请帮我查找会议任务邮件联系人群聊文档", Deep: true}, caps)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "rules" || len(plan.Nodes) != 2 || plan.Nodes[1].MaxItems != 8 {
		t.Fatalf("plan=%+v", plan)
	}
	var request kernel.SearchRequest
	if err := json.Unmarshal(plan.Nodes[0].Request, &request); err != nil {
		t.Fatal(err)
	}
	want := []kernel.SourceID{kernel.SourceDocs, kernel.SourceMessages, kernel.SourceMinutes, kernel.SourceMeetings, kernel.SourceTasks, kernel.SourceCalendar, kernel.SourcePeople, kernel.SourceChats, kernel.SourceMail}
	if !reflect.DeepEqual(request.Sources, want) {
		t.Fatalf("sources=%v want=%v", request.Sources, want)
	}
	if strings.Contains(request.Query, "请帮我") || request.Limit != 40 {
		t.Fatalf("request=%+v", request)
	}
	if err := planvalidate.Validate(plan); err != nil {
		t.Fatalf("rules plan is invalid: %v", err)
	}
}

func TestSelectSourcesFallsBackToAvailableGlobalSource(t *testing.T) {
	got := selectSources("没有命中任何规则", capabilities(kernel.SourceTasks))
	if !reflect.DeepEqual(got, []kernel.SourceID{kernel.SourceTasks}) {
		t.Fatalf("fallback sources=%v", got)
	}
	if got := selectSources("任务负责人", kernel.CapabilitySnapshot{Providers: []kernel.ProviderCapability{{Descriptor: kernel.ProviderDescriptor{Source: kernel.SourceTasks}, Status: kernel.StatusFailed}}}); len(got) != 0 {
		t.Fatalf("failed provider should not be selected: %v", got)
	}
}

func TestSourceQueriesAndCompactionBoundaries(t *testing.T) {
	long := strings.Repeat("文", 60)
	queries := sourceQueries(long, []kernel.SourceID{kernel.SourceDocs, kernel.SourceMail, kernel.SourceTasks})
	if len([]rune(queries[kernel.SourceDocs])) != 30 || len([]rune(queries[kernel.SourceMail])) != 50 || queries[kernel.SourceTasks] != "" {
		t.Fatalf("queries=%v", queries)
	}
	if got := sourceQueries("short", []kernel.SourceID{kernel.SourceDocs}); got != nil {
		t.Fatalf("short query override=%v", got)
	}
	if got := compactQuery(" 请帮我  搜索 A 项目 最近一周 "); got != "A 项目" {
		t.Fatalf("compact=%q", got)
	}
	if got := compactQuery("请帮我"); got != "请帮我" {
		t.Fatalf("empty compaction should retain original, got %q", got)
	}
	if !containsAny("abc", "x", "b") || containsAny("abc", "x", "y") {
		t.Fatal("containsAny mismatch")
	}
}

func capabilities(sources ...kernel.SourceID) kernel.CapabilitySnapshot {
	out := kernel.CapabilitySnapshot{}
	for _, source := range sources {
		out.Providers = append(out.Providers, kernel.ProviderCapability{Descriptor: kernel.ProviderDescriptor{Source: source}, Status: kernel.StatusOK})
	}
	return out
}
