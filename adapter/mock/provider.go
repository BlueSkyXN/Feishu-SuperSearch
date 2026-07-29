package mock

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type Dataset struct {
	Candidates []kernel.Candidate `json:"candidates"`
	Artifacts  []kernel.Artifact  `json:"artifacts"`
	Relations  []kernel.Relation  `json:"relations"`
}

type Provider struct {
	desc       kernel.ProviderDescriptor
	mu         sync.RWMutex
	candidates []kernel.Candidate
	artifacts  map[string]kernel.Artifact
	relations  []kernel.Relation
}

func NewProviders(data Dataset) []*Provider {
	bySource := map[kernel.SourceID][]kernel.Candidate{}
	for _, c := range data.Candidates {
		bySource[c.Source] = append(bySource[c.Source], c)
	}
	artifacts := map[string]kernel.Artifact{}
	for _, a := range data.Artifacts {
		artifacts[a.Ref.CanonicalID] = a
	}
	sources := make([]kernel.SourceID, 0, len(bySource))
	for s := range bySource {
		sources = append(sources, s)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i] < sources[j] })
	out := make([]*Provider, 0, len(sources))
	for _, source := range sources {
		kinds := []kernel.ObjectKind{}
		seen := map[kernel.ObjectKind]bool{}
		for _, c := range bySource[source] {
			if !seen[c.Kind] {
				seen[c.Kind] = true
				kinds = append(kinds, c.Kind)
			}
		}
		d := kernel.ProviderDescriptor{ID: kernel.ProviderID("mock." + source), Source: source, ObjectKinds: kinds, Operations: kernel.OperationSet{kernel.OpSearch: true, kernel.OpQuery: true, kernel.OpFetch: true, kernel.OpExpand: true, kernel.OpResolve: source == kernel.SourcePeople}, RequiredIdentity: []kernel.IdentityMode{kernel.IdentityAuto, kernel.IdentityUser, kernel.IdentityBot}, SearchLimits: kernel.SearchLimits{MaxQueryRunes: 500, MaxPageSize: 100, MaxPages: 20}, BatchLimits: kernel.BatchLimits{MaxFetchItems: 100}, ReturnedProjection: kernel.ProjectionHead | kernel.ProjectionSnippet, FetchableProjection: kernel.ProjectionSummary | kernel.ProjectionStructure | kernel.ProjectionContent | kernel.ProjectionContext | kernel.ProjectionRelations | kernel.ProjectionAttachments, SupportsPagination: true, Backend: "mock", Version: "1"}
		out = append(out, &Provider{desc: d, candidates: bySource[source], artifacts: artifacts, relations: data.Relations})
	}
	return out
}
func (p *Provider) Descriptor() kernel.ProviderDescriptor                   { return p.desc }
func (p *Provider) Health(context.Context, kernel.Identity) (string, error) { return "mock/1", nil }
func (p *Provider) Search(_ context.Context, r kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	terms := tokenize(r.Query)
	matches := []kernel.Candidate{}
	for _, raw := range p.candidates {
		if !within(raw.Timestamp, r.Filters.After, r.Filters.Before) {
			continue
		}
		score := matchScore(strings.ToLower(raw.Title+" "+raw.Snippet), terms)
		if score <= 0 {
			continue
		}
		c := kernel.CloneJSON(raw)
		c.NativeScore = &score
		c.Provenance = kernel.Provenance{ProviderID: p.desc.ID, Backend: "mock", Operation: "search", RetrievedAt: time.Now().UTC()}
		matches = append(matches, c)
	}
	sort.SliceStable(matches, func(i, j int) bool {
		si, sj := 0.0, 0.0
		if matches[i].NativeScore != nil {
			si = *matches[i].NativeScore
		}
		if matches[j].NativeScore != nil {
			sj = *matches[j].NativeScore
		}
		return si > sj
	})
	for i := range matches {
		matches[i].NativeRank = i + 1
		scope := r.Identity.Normalized().ScopeKey
		matches[i].Ref.ScopeKey = scope
		matches[i].Ref.CanonicalID = kernel.BuildCanonicalID(scope, matches[i].Ref.Kind, matches[i].Ref.NativeID)
		matches[i].Ref.ProviderID = p.desc.ID
		matches[i].DiscoveredBy = []kernel.Discovery{{ProviderID: p.desc.ID, Source: p.desc.Source, Rank: i + 1, Score: matches[i].NativeScore}}
	}
	return paginate(matches, r.PageSize, r.Cursor), nil
}
func (p *Provider) Query(ctx context.Context, r kernel.ProviderQueryRequest) (kernel.CandidatePage, error) {
	query := fmt.Sprint(r.Filter["query"])
	page, err := p.Search(ctx, kernel.ProviderSearchRequest{Query: query, Identity: r.Identity, PageSize: r.Limit, Cursor: r.Cursor})
	if err != nil {
		return page, err
	}
	if completed, ok := r.Filter["completed"].(bool); ok {
		out := page.Candidates[:0]
		for _, c := range page.Candidates {
			a, _ := p.artifactForRef(c.Ref)
			v, _ := a.Metadata["completed"].(bool)
			if v == completed {
				out = append(out, c)
			}
		}
		page.Candidates = out
		page.RawCount = len(out)
	}
	return page, nil
}
func (p *Provider) Fetch(_ context.Context, reqs []kernel.ProviderFetchRequest) ([]kernel.Artifact, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]kernel.Artifact, 0, len(reqs))
	for _, r := range reqs {
		a, ok := p.artifactForRef(r.Ref)
		if !ok {
			return out, &kernel.ErrorDetail{Type: kernel.ErrNotFound, Message: "mock artifact not found: " + r.Ref.CanonicalID, ProviderID: p.desc.ID, Source: p.desc.Source}
		}
		a = kernel.CloneJSON(a)
		a.Projection |= r.Projection
		a.Provenance = kernel.Provenance{ProviderID: p.desc.ID, Backend: "mock", Operation: "fetch", RetrievedAt: time.Now().UTC()}
		out = append(out, a)
	}
	return out, nil
}
func (p *Provider) Expand(_ context.Context, r kernel.ProviderExpandRequest) ([]kernel.Relation, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	wanted := map[string]bool{}
	for _, x := range r.Relations {
		wanted[x] = true
	}
	out := []kernel.Relation{}
	for _, rel := range p.relations {
		if rel.From.NativeID == r.Ref.NativeID && (rel.From.Kind == r.Ref.Kind || rel.From.Kind == "" || r.Ref.Kind == "") && (len(wanted) == 0 || wanted[rel.Type]) {
			out = append(out, kernel.CloneJSON(rel))
		}
	}
	return out, nil
}

func (p *Provider) artifactForRef(ref kernel.ObjectRef) (kernel.Artifact, bool) {
	if artifact, ok := p.artifacts[ref.CanonicalID]; ok {
		return artifact, true
	}
	for _, artifact := range p.artifacts {
		if artifact.Ref.NativeID == ref.NativeID && (artifact.Ref.Kind == ref.Kind || artifact.Ref.Kind == "" || ref.Kind == "") {
			return artifact, true
		}
	}
	return kernel.Artifact{}, false
}
func (p *Provider) Resolve(ctx context.Context, r kernel.ProviderResolveRequest) ([]kernel.ObjectRef, error) {
	page, err := p.Search(ctx, kernel.ProviderSearchRequest{Query: r.Text, Identity: r.Identity, PageSize: r.Limit})
	if err != nil {
		return nil, err
	}
	refs := make([]kernel.ObjectRef, 0, len(page.Candidates))
	for _, c := range page.Candidates {
		refs = append(refs, c.Ref)
	}
	return refs, nil
}

func paginate(items []kernel.Candidate, size int, cursor string) kernel.CandidatePage {
	if size <= 0 {
		size = 10
	}
	offset := decodeCursor(cursor)
	if offset > len(items) {
		offset = len(items)
	}
	end := offset + size
	if end > len(items) {
		end = len(items)
	}
	next := ""
	more := end < len(items)
	if more {
		next = encodeCursor(end)
	}
	out := append([]kernel.Candidate(nil), items[offset:end]...)
	for i := range out {
		out[i].NativeRank = offset + i + 1
		if len(out[i].DiscoveredBy) > 0 {
			out[i].DiscoveredBy[0].Rank = offset + i + 1
		}
	}
	return kernel.CandidatePage{Candidates: out, NextCursor: next, HasMore: more, RawCount: len(out)}
}
func encodeCursor(n int) string { return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(n))) }
func decodeCursor(s string) int {
	if s == "" {
		return 0
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(string(b))
	return n
}
func tokenize(q string) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	parts := strings.FieldsFunc(q, func(r rune) bool { return r == ' ' || r == ',' || r == '，' || r == '/' || r == '|' || r == ';' })
	if len(parts) == 0 {
		return []string{q}
	}
	return parts
}
func matchScore(text string, terms []string) float64 {
	if len(terms) == 0 {
		return 1
	}
	score := 0.0
	for _, t := range terms {
		if strings.Contains(text, t) {
			score += 1
		}
	}
	return score
}
func within(t *time.Time, after, before *time.Time) bool {
	if t == nil {
		return true
	}
	if after != nil && t.Before(*after) {
		return false
	}
	if before != nil && t.After(*before) {
		return false
	}
	return true
}

func DemoDataset() Dataset {
	scope := "demo"
	now := time.Now().UTC()
	ref := func(kind kernel.ObjectKind, source kernel.SourceID, id, url string) kernel.ObjectRef {
		return kernel.ObjectRef{Platform: "feishu", ScopeKey: scope, Kind: kind, NativeID: id, CanonicalID: kernel.BuildCanonicalID(scope, kind, id), Source: source, URL: url}
	}
	doc := ref(kernel.KindDocument, kernel.SourceDocs, "dox_demo_plan", "https://demo.feishu.cn/docx/dox_demo_plan")
	msg := ref(kernel.KindMessage, kernel.SourceMessages, "om_demo_delay", "https://demo.feishu.cn/message/om_demo_delay")
	minute := ref(kernel.KindMinute, kernel.SourceMinutes, "obc_demo_review", "https://demo.feishu.cn/minutes/obc_demo_review")
	meeting := ref(kernel.KindMeeting, kernel.SourceMeetings, "m_demo_review", "")
	task1 := ref(kernel.KindTask, kernel.SourceTasks, "task_demo_env", "")
	task2 := ref(kernel.KindTask, kernel.SourceTasks, "task_demo_regression", "")
	person := ref(kernel.KindPerson, kernel.SourcePeople, "ou_demo_zhangsan", "")
	chat := ref(kernel.KindChat, kernel.SourceChats, "oc_demo_project", "")
	event := ref(kernel.KindEvent, kernel.SourceCalendar, "primary/evt_demo_review", "")
	cands := []kernel.Candidate{
		{Ref: doc, Source: kernel.SourceDocs, Kind: kernel.KindDocument, Title: "A 项目上线计划与风险", Snippet: "测试环境交付延迟，原定上线日期需要调整。", URL: doc.URL, Timestamp: ptrTime(now.Add(-72 * time.Hour)), Projection: kernel.ProjectionHead | kernel.ProjectionSnippet, AvailableProjection: kernel.ProjectionStructure | kernel.ProjectionContent | kernel.ProjectionRelations},
		{Ref: msg, Source: kernel.SourceMessages, Kind: kernel.KindMessage, Title: "A 项目讨论群", Snippet: "测试环境晚交付三天，建议将上线推迟到下周二。", URL: msg.URL, Timestamp: ptrTime(now.Add(-48 * time.Hour)), Projection: kernel.ProjectionHead | kernel.ProjectionSnippet, AvailableProjection: kernel.ProjectionContent | kernel.ProjectionContext | kernel.ProjectionRelations},
		{Ref: minute, Source: kernel.SourceMinutes, Kind: kernel.KindMinute, Title: "A 项目上线评审", Snippet: "会议决定调整上线时间，并明确后续待办。", URL: minute.URL, Timestamp: ptrTime(now.Add(-40 * time.Hour)), Projection: kernel.ProjectionHead | kernel.ProjectionSnippet, AvailableProjection: kernel.ProjectionSummary | kernel.ProjectionStructure | kernel.ProjectionContent | kernel.ProjectionRelations},
		{Ref: meeting, Source: kernel.SourceMeetings, Kind: kernel.KindMeeting, Title: "A 项目上线评审会", Snippet: "评审上线条件与延期方案。", Timestamp: ptrTime(now.Add(-41 * time.Hour)), Projection: kernel.ProjectionHead | kernel.ProjectionSnippet, AvailableProjection: kernel.ProjectionContent | kernel.ProjectionRelations},
		{Ref: task1, Source: kernel.SourceTasks, Kind: kernel.KindTask, Title: "A 项目：完成测试环境部署", Snippet: "负责人张三，尚未完成。", Timestamp: ptrTime(now.Add(-24 * time.Hour)), Projection: kernel.ProjectionHead | kernel.ProjectionSnippet, AvailableProjection: kernel.ProjectionContent | kernel.ProjectionRelations},
		{Ref: task2, Source: kernel.SourceTasks, Kind: kernel.KindTask, Title: "A 项目：执行回归测试", Snippet: "等待测试环境可用后开始。", Timestamp: ptrTime(now.Add(-20 * time.Hour)), Projection: kernel.ProjectionHead | kernel.ProjectionSnippet, AvailableProjection: kernel.ProjectionContent | kernel.ProjectionRelations},
		{Ref: person, Source: kernel.SourcePeople, Kind: kernel.KindPerson, Title: "张三", Snippet: "A 项目测试负责人", Projection: kernel.ProjectionHead | kernel.ProjectionContent, AvailableProjection: kernel.ProjectionRelations},
		{Ref: chat, Source: kernel.SourceChats, Kind: kernel.KindChat, Title: "A 项目讨论群", Snippet: "A 项目研发与上线协作群", Projection: kernel.ProjectionHead | kernel.ProjectionSnippet, AvailableProjection: kernel.ProjectionContent | kernel.ProjectionRelations},
		{Ref: event, Source: kernel.SourceCalendar, Kind: kernel.KindEvent, Title: "A 项目上线评审会", Snippet: "讨论延期原因与新上线日期", Timestamp: ptrTime(now.Add(-41 * time.Hour)), Projection: kernel.ProjectionHead | kernel.ProjectionSnippet, AvailableProjection: kernel.ProjectionContent | kernel.ProjectionRelations},
	}
	arts := []kernel.Artifact{
		{Ref: doc, Projection: kernel.ProjectionHead | kernel.ProjectionStructure | kernel.ProjectionContent, Metadata: map[string]any{"owner": "李四"}, Chunks: []kernel.ContentChunk{{ID: "doc-1", Kind: "markdown", Text: "# A 项目上线计划\n\n测试环境原定周一交付，实际周四完成，导致联调和回归测试整体顺延。建议将上线从 7 月 25 日调整到 7 月 30 日。"}}},
		{Ref: msg, Projection: kernel.ProjectionHead | kernel.ProjectionContent | kernel.ProjectionContext | kernel.ProjectionRelations, Chunks: []kernel.ContentChunk{{ID: "msg-1", Kind: "message", Text: "李四：测试环境比计划晚三天。王五：先推迟到下周二。张三：同意，按 7 月 30 日更新计划。"}}},
		{Ref: minute, Projection: kernel.ProjectionHead | kernel.ProjectionSummary | kernel.ProjectionStructure | kernel.ProjectionRelations, Summary: &kernel.NativeSummary{Text: "因测试环境交付延迟，会议决定将上线调整到 7 月 30 日。最终决策由张三确认。", Todos: []map[string]any{{"text": "完成测试环境部署", "assignee": "张三", "completed": false}, {"text": "执行回归测试", "assignee": "王五", "completed": false}}}},
		{Ref: meeting, Projection: kernel.ProjectionHead | kernel.ProjectionContent | kernel.ProjectionRelations, Metadata: map[string]any{"organizer": "张三", "minute_token": minute.NativeID}, Chunks: []kernel.ContentChunk{{ID: "meeting", Text: "A 项目上线评审会"}}},
		{Ref: task1, Projection: kernel.ProjectionHead | kernel.ProjectionContent, Metadata: map[string]any{"completed": false, "assignee": "张三", "due": "2026-07-29"}, Chunks: []kernel.ContentChunk{{ID: "task", Text: "完成测试环境部署并通知项目群。"}}},
		{Ref: task2, Projection: kernel.ProjectionHead | kernel.ProjectionContent, Metadata: map[string]any{"completed": false, "assignee": "王五", "due": "2026-07-30"}, Chunks: []kernel.ContentChunk{{ID: "task", Text: "执行完整回归测试并输出测试报告。"}}},
		{Ref: person, Projection: kernel.ProjectionHead | kernel.ProjectionContent, Metadata: map[string]any{"department": "质量工程", "email": "zhangsan@example.com"}},
		{Ref: chat, Projection: kernel.ProjectionHead | kernel.ProjectionContent, Metadata: map[string]any{"members": 12}},
		{Ref: event, Projection: kernel.ProjectionHead | kernel.ProjectionContent, Metadata: map[string]any{"start": now.Add(-41 * time.Hour), "attendees": []string{"张三", "李四", "王五"}}},
	}
	rels := []kernel.Relation{
		{From: meeting, Type: "meeting.has_minute", To: minute, Confidence: 1},
		{From: msg, Type: "message.links_to_document", To: doc, Confidence: .95},
		{From: msg, Type: "message.in_chat", To: chat, Confidence: 1},
		{From: minute, Type: "minute.has_task", To: task1, Confidence: .9},
		{From: minute, Type: "minute.has_task", To: task2, Confidence: .9},
		{From: task1, Type: "task.assigned_to", To: person, Confidence: 1},
	}
	return Dataset{Candidates: cands, Artifacts: arts, Relations: rels}
}
func ptrTime(t time.Time) *time.Time { return &t }
