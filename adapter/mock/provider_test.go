package mock

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestDemoProvidersSearchPaginationAndFilters(t *testing.T) {
	providers := NewProviders(DemoDataset())
	if len(providers) != 8 {
		t.Fatalf("providers=%d want 8", len(providers))
	}
	for i := 1; i < len(providers); i++ {
		if providers[i-1].Descriptor().Source >= providers[i].Descriptor().Source {
			t.Fatalf("providers are not source-sorted: %s >= %s", providers[i-1].Descriptor().Source, providers[i].Descriptor().Source)
		}
	}
	tasks := providerForSource(t, providers, kernel.SourceTasks)
	descriptor := tasks.Descriptor()
	if descriptor.ID != "mock.tasks" || !descriptor.Operations[kernel.OpQuery] || descriptor.Operations[kernel.OpResolve] {
		t.Fatalf("task descriptor=%+v", descriptor)
	}
	if health, err := tasks.Health(context.Background(), kernel.Identity{}); err != nil || health != "mock/1" {
		t.Fatalf("health=%q err=%v", health, err)
	}

	first, err := tasks.Search(context.Background(), kernel.ProviderSearchRequest{Query: "测试", PageSize: 1})
	if err != nil || len(first.Candidates) != 1 || !first.HasMore || first.NextCursor == "" || first.Candidates[0].NativeRank != 1 {
		t.Fatalf("first page=%+v err=%v", first, err)
	}
	if first.Candidates[0].Ref.ProviderID != descriptor.ID || first.Candidates[0].Provenance.Operation != "search" || len(first.Candidates[0].DiscoveredBy) != 1 {
		t.Fatalf("search metadata=%+v", first.Candidates[0])
	}
	second, err := tasks.Search(context.Background(), kernel.ProviderSearchRequest{Query: "测试", PageSize: 1, Cursor: first.NextCursor})
	if err != nil || len(second.Candidates) != 1 || second.HasMore || second.Candidates[0].NativeRank != 2 {
		t.Fatalf("second page=%+v err=%v", second, err)
	}

	after := time.Now().UTC().Add(-22 * time.Hour)
	filtered, err := tasks.Search(context.Background(), kernel.ProviderSearchRequest{Query: "测试", Filters: kernel.SearchFilters{After: &after}})
	if err != nil || len(filtered.Candidates) != 1 || filtered.Candidates[0].Ref.NativeID != "task_demo_regression" {
		t.Fatalf("after filter=%+v err=%v", filtered, err)
	}
	before := time.Now().UTC().Add(-23 * time.Hour)
	filtered, err = tasks.Search(context.Background(), kernel.ProviderSearchRequest{Query: "测试", Filters: kernel.SearchFilters{Before: &before}})
	if err != nil || len(filtered.Candidates) != 1 || filtered.Candidates[0].Ref.NativeID != "task_demo_env" {
		t.Fatalf("before filter=%+v err=%v", filtered, err)
	}
	empty, err := tasks.Search(context.Background(), kernel.ProviderSearchRequest{Query: "不存在"})
	if err != nil || len(empty.Candidates) != 0 || empty.RawCount != 0 {
		t.Fatalf("empty search=%+v err=%v", empty, err)
	}
}

func TestProviderQueryFetchExpandAndResolve(t *testing.T) {
	providers := NewProviders(DemoDataset())
	tasks := providerForSource(t, providers, kernel.SourceTasks)
	page, err := tasks.Query(context.Background(), kernel.ProviderQueryRequest{Filter: map[string]any{"query": "测试", "completed": false}, Limit: 10})
	if err != nil || len(page.Candidates) != 2 || page.RawCount != 2 {
		t.Fatalf("query incomplete=%+v err=%v", page, err)
	}
	page, err = tasks.Query(context.Background(), kernel.ProviderQueryRequest{Filter: map[string]any{"query": "测试", "completed": true}, Limit: 10})
	if err != nil || len(page.Candidates) != 0 || page.RawCount != 0 {
		t.Fatalf("query completed=%+v err=%v", page, err)
	}

	data := DemoDataset()
	docs := providerForSource(t, providers, kernel.SourceDocs)
	doc := data.Candidates[0]
	artifacts, err := docs.Fetch(context.Background(), []kernel.ProviderFetchRequest{
		{Ref: doc.Ref, Projection: kernel.ProjectionAttachments},
		{Ref: kernel.ObjectRef{NativeID: doc.Ref.NativeID}, Projection: kernel.ProjectionSummary},
	})
	if err != nil || len(artifacts) != 2 {
		t.Fatalf("fetch=%+v err=%v", artifacts, err)
	}
	if !artifacts[0].Projection.Has(kernel.ProjectionAttachments) || artifacts[0].Provenance.Operation != "fetch" || artifacts[0].Provenance.ProviderID != docs.Descriptor().ID {
		t.Fatalf("fetch metadata=%+v", artifacts[0])
	}
	partial, err := docs.Fetch(context.Background(), []kernel.ProviderFetchRequest{
		{Ref: doc.Ref, Projection: kernel.ProjectionContent},
		{Ref: kernel.ObjectRef{CanonicalID: "missing"}, Projection: kernel.ProjectionContent},
	})
	var detail *kernel.ErrorDetail
	if len(partial) != 1 || !errors.As(err, &detail) || detail.Type != kernel.ErrNotFound {
		t.Fatalf("partial=%+v err=%v", partial, err)
	}

	messages := providerForSource(t, providers, kernel.SourceMessages)
	messageRef := data.Candidates[1].Ref
	relations, err := messages.Expand(context.Background(), kernel.ProviderExpandRequest{Ref: messageRef})
	if err != nil || len(relations) != 2 {
		t.Fatalf("all relations=%+v err=%v", relations, err)
	}
	relations, err = messages.Expand(context.Background(), kernel.ProviderExpandRequest{Ref: messageRef, Relations: []string{"message.in_chat"}})
	if err != nil || len(relations) != 1 || relations[0].Type != "message.in_chat" {
		t.Fatalf("filtered relations=%+v err=%v", relations, err)
	}
	relations, err = messages.Expand(context.Background(), kernel.ProviderExpandRequest{Ref: messageRef, Relations: []string{"unknown"}})
	if err != nil || len(relations) != 0 {
		t.Fatalf("unknown relations=%+v err=%v", relations, err)
	}

	people := providerForSource(t, providers, kernel.SourcePeople)
	if !people.Descriptor().Operations[kernel.OpResolve] {
		t.Fatal("people provider should support resolve")
	}
	refs, err := people.Resolve(context.Background(), kernel.ProviderResolveRequest{Text: "张三", Limit: 5})
	if err != nil || len(refs) != 1 || refs[0].NativeID != "ou_demo_zhangsan" {
		t.Fatalf("resolve=%+v err=%v", refs, err)
	}
}

func TestPaginationAndMatchingBoundaries(t *testing.T) {
	data := DemoDataset()
	items := data.Candidates[:2]
	page := paginate(items, 0, encodeCursor(99))
	if len(page.Candidates) != 0 || page.HasMore {
		t.Fatalf("past-end page=%+v", page)
	}
	page = paginate(items, 1, "not-base64")
	if len(page.Candidates) != 1 || !page.HasMore || decodeCursor(page.NextCursor) != 1 {
		t.Fatalf("invalid-cursor fallback=%+v", page)
	}
	if decodeCursor(encodeCursor(12)) != 12 || decodeCursor("bm90LWEtbnVtYmVy") != 0 {
		t.Fatal("cursor round trip mismatch")
	}
	if got := tokenize(" A，B/c|d;e "); len(got) != 5 || got[0] != "a" || tokenize(" ") != nil {
		t.Fatalf("tokens=%v", got)
	}
	if matchScore("alpha beta", nil) != 1 || matchScore("alpha beta", []string{"alpha", "missing"}) != 1 {
		t.Fatal("match score mismatch")
	}
	now := time.Now().UTC()
	before, after := now.Add(-time.Hour), now.Add(time.Hour)
	if !within(nil, &after, &before) || within(&now, &after, nil) || within(&now, nil, &before) || !within(&now, &before, &after) {
		t.Fatal("time boundary mismatch")
	}
	if len(data.Candidates) != 9 || len(data.Artifacts) != 9 || len(data.Relations) != 6 || data.Candidates[0].Timestamp == nil {
		t.Fatalf("demo dataset=%+v", data)
	}
}

func providerForSource(t *testing.T, providers []*Provider, source kernel.SourceID) *Provider {
	t.Helper()
	for _, provider := range providers {
		if provider.Descriptor().Source == source {
			return provider
		}
	}
	t.Fatalf("provider for source %s not found", source)
	return nil
}
