package fusion

import (
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestFuseDeduplicatesAndCombinesDiscoveries(t *testing.T) {
	now := time.Now().UTC()
	ref := kernel.ObjectRef{Kind: kernel.KindDocument, NativeID: "d1", CanonicalID: "feishu:test:document:d1"}
	a := kernel.Candidate{Ref: ref, Source: kernel.SourceDocs, Kind: kernel.KindDocument, Title: "A 项目延期", NativeRank: 1, Timestamp: &now, DiscoveredBy: []kernel.Discovery{{ProviderID: "docs", Source: kernel.SourceDocs, Rank: 1}}}
	b := kernel.Candidate{Ref: ref, Source: kernel.SourceDocs, Kind: kernel.KindDocument, Title: "A 项目延期完整方案", Snippet: "测试环境延迟", NativeRank: 2, DiscoveredBy: []kernel.Discovery{{ProviderID: "messages", Source: kernel.SourceMessages, Rank: 2}}}
	got, unique := Fuse("A 项目延期", []kernel.Candidate{a, b}, 10, Config{UseQuota: false}, "test")
	if unique != 1 || len(got) != 1 {
		t.Fatalf("unique=%d len=%d", unique, len(got))
	}
	if len(got[0].DiscoveredBy) != 2 {
		t.Fatalf("discoveries=%v", got[0].DiscoveredBy)
	}
	if got[0].Title != "A 项目延期完整方案" {
		t.Fatalf("merge did not retain richer title: %q", got[0].Title)
	}
	if got[0].FusedScore <= 0 {
		t.Fatal("score not assigned")
	}
}

func TestCanonicalizeRebindsProviderScopeToCallerIdentity(t *testing.T) {
	candidate := kernel.Candidate{
		Ref:    kernel.ObjectRef{Platform: "feishu", ScopeKey: "provider-selected", Kind: kernel.KindDocument, NativeID: "dox_1", CanonicalID: "feishu:provider-selected:document:dox_1"},
		Source: kernel.SourceDocs,
		Kind:   kernel.KindDocument,
	}
	one := Canonicalize(candidate, "scope-one")
	two := Canonicalize(candidate, "scope-two")
	if one.Ref.ScopeKey != "scope-one" || one.Ref.CanonicalID != "feishu:scope-one:document:dox_1" {
		t.Fatalf("one=%+v", one.Ref)
	}
	if two.Ref.ScopeKey != "scope-two" || two.Ref.CanonicalID != "feishu:scope-two:document:dox_1" || one.Ref.CanonicalID == two.Ref.CanonicalID {
		t.Fatalf("two=%+v", two.Ref)
	}
}
