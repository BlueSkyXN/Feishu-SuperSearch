package rules

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
)

type Planner struct{}

func (Planner) Name() string { return "rules" }
func (Planner) Plan(_ context.Context, r planner.UserRequest, caps kernel.CapabilitySnapshot) (kernel.RetrievalPlan, error) {
	sources := r.Sources
	if len(sources) == 0 {
		sources = selectSources(r.Query, caps)
	}
	limit := r.Limit
	if limit <= 0 {
		limit = 40
	}
	query := compactQuery(r.Query)
	searchReq := kernel.SearchRequest{Query: query, SourceQueries: sourceQueries(query, sources), Sources: sources, Filters: r.Filters, Identity: r.Identity, Limit: limit, LimitPerSource: 8, Budget: r.Budget, Strategy: kernel.SearchStrategy{Fusion: "weighted_rrf", Pagination: "adaptive", SourceQuota: true}}
	raw, _ := json.Marshal(searchReq)
	nodes := []kernel.PlanNode{{ID: "search", Op: kernel.PlanSearch, Request: raw, Priority: 100}}
	output := []string{"search"}
	if r.Deep {
		top := r.FetchTopK
		if top <= 0 {
			top = 8
		}
		fetchRaw, _ := json.Marshal(map[string]any{"top_k": top, "max_items": top, "projection_by_kind": map[kernel.ObjectKind]kernel.ProjectionSet{kernel.KindDocument: kernel.ProjectionStructure | kernel.ProjectionContent, kernel.KindMessage: kernel.ProjectionContent | kernel.ProjectionContext | kernel.ProjectionRelations, kernel.KindMinute: kernel.ProjectionSummary | kernel.ProjectionRelations, kernel.KindMeeting: kernel.ProjectionContent | kernel.ProjectionRelations, kernel.KindTask: kernel.ProjectionContent, kernel.KindEvent: kernel.ProjectionContent | kernel.ProjectionRelations, kernel.KindMail: kernel.ProjectionContent | kernel.ProjectionContext}})
		nodes = append(nodes, kernel.PlanNode{ID: "fetch_top", Op: kernel.PlanMapFetch, DependsOn: []string{"search"}, Request: fetchRaw, Priority: 50, MaxItems: top})
		output = []string{"search", "fetch_top"}
	}
	return kernel.RetrievalPlan{Version: "retrieval-plan/v1", Identity: r.Identity, Budget: r.Budget, Nodes: nodes, Output: output}, nil
}

func selectSources(q string, caps kernel.CapabilitySnapshot) []kernel.SourceID {
	available := map[kernel.SourceID]bool{}
	for _, c := range caps.Providers {
		if c.Status == kernel.StatusOK {
			available[c.Descriptor.Source] = true
		}
	}
	lower := strings.ToLower(q)
	wanted := map[kernel.SourceID]bool{}
	add := func(xs ...kernel.SourceID) {
		for _, x := range xs {
			if available[x] {
				wanted[x] = true
			}
		}
	}
	// Broad retrieval is the fallback; explicit semantic clues add authoritative verticals.
	add(kernel.SourceDocs, kernel.SourceMessages, kernel.SourceMinutes, kernel.SourceMeetings)
	if containsAny(lower, "任务", "待办", "todo", "完成", "负责人") {
		add(kernel.SourceTasks)
	}
	if containsAny(lower, "会议", "评审", "纪要", "妙记", "会里") {
		add(kernel.SourceMinutes, kernel.SourceMeetings, kernel.SourceCalendar)
	}
	if containsAny(lower, "邮件", "邮箱", "mail", "email") {
		add(kernel.SourceMail)
	}
	if containsAny(lower, "谁", "联系人", "同事", "成员", "人名") {
		add(kernel.SourcePeople, kernel.SourceChats)
	}
	if containsAny(lower, "群", "聊天", "消息", "讨论") {
		add(kernel.SourceMessages, kernel.SourceChats)
	}
	if containsAny(lower, "文档", "wiki", "方案", "报告", "计划") {
		add(kernel.SourceDocs)
	}
	if len(wanted) == 0 {
		for _, s := range kernel.GlobalSources {
			if available[s] {
				wanted[s] = true
			}
		}
	}
	order := []kernel.SourceID{kernel.SourceDocs, kernel.SourceMessages, kernel.SourceMinutes, kernel.SourceMeetings, kernel.SourceTasks, kernel.SourceCalendar, kernel.SourcePeople, kernel.SourceChats, kernel.SourceMail}
	out := []kernel.SourceID{}
	for _, s := range order {
		if wanted[s] {
			out = append(out, s)
		}
	}
	return out
}
func sourceQueries(query string, sources []kernel.SourceID) map[kernel.SourceID]string {
	out := map[kernel.SourceID]string{}
	for _, source := range sources {
		limit := 0
		switch source {
		case kernel.SourceDocs:
			limit = 30
		case kernel.SourceMail:
			limit = 50
		}
		if limit > 0 && len([]rune(query)) > limit {
			out[source] = string([]rune(query)[:limit])
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func containsAny(s string, terms ...string) bool {
	for _, t := range terms {
		if strings.Contains(s, t) {
			return true
		}
	}
	return false
}
func compactQuery(q string) string {
	original := strings.TrimSpace(q)
	q = original
	repl := []string{"请帮我", "帮我", "请", "搜索", "查找", "找一下", "总结", "分析", "告诉我", "最近两周", "最近一周", "最近一个月"}
	for _, x := range repl {
		q = strings.ReplaceAll(q, x, " ")
	}
	q = strings.Join(strings.Fields(q), " ")
	if q == "" {
		return original
	}
	return q
}
