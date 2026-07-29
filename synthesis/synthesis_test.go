package synthesis

import (
	"strings"
	"testing"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestExtractEvidenceUsesStableIDsAndSourceSpans(t *testing.T) {
	ref := kernel.ObjectRef{CanonicalID: "feishu:scope:document:d1", NativeID: "d1", Kind: kernel.KindDocument, URL: "https://example.invalid/d1"}
	artifact := kernel.Artifact{Ref: ref, Projection: kernel.ProjectionContent, Chunks: []kernel.ContentChunk{{ID: "chunk-1", Kind: "decision", Text: "决定延期三天", Start: 10, End: 16}}}
	first := ExtractEvidence("延期", ArtifactPack{Artifacts: []kernel.Artifact{artifact}})
	second := ExtractEvidence("延期", ArtifactPack{Artifacts: []kernel.Artifact{artifact}})
	if len(first.Evidence) != 1 || len(second.Evidence) != 1 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	evidence := first.Evidence[0]
	if evidence.ID != second.Evidence[0].ID || evidence.ID == "" || evidence.Quote != evidence.Text || evidence.ClaimType != "decision" {
		t.Fatalf("evidence=%+v", evidence)
	}
	if evidence.SourceSpan.ChunkID != "chunk-1" || evidence.SourceSpan.Start != 10 || evidence.SourceSpan.End != 16 || evidence.Projection != kernel.ProjectionContent {
		t.Fatalf("source span=%+v projection=%v", evidence.SourceSpan, evidence.Projection)
	}
}

func TestPackHelpersAndSummaryFormatting(t *testing.T) {
	candidate := kernel.Candidate{Title: "candidate"}
	snapshot := kernel.SearchSnapshot{SessionID: "rs_test", Query: "why", Candidates: []kernel.Candidate{candidate}, Sources: []kernel.SourceRun{{Source: kernel.SourceDocs}}}
	if pack := Candidates(snapshot); pack.SessionID != "rs_test" || pack.Query != "why" || len(pack.Candidates) != 1 || len(pack.Sources) != 1 {
		t.Fatalf("candidate pack=%+v", pack)
	}
	artifact := kernel.Artifact{Ref: kernel.ObjectRef{CanonicalID: "doc", Kind: kernel.KindDocument}}
	pack := Artifacts("rs_test", kernel.ArtifactBatch{Items: []kernel.ArtifactResult{{Artifact: &artifact}, {Error: &kernel.ErrorDetail{Message: "failed"}}}}, []kernel.Relation{{Type: "related"}})
	if len(pack.Artifacts) != 1 || len(pack.Relations) != 1 {
		t.Fatalf("artifact pack=%+v", pack)
	}
	if got := Summarize(EvidencePack{}); !strings.Contains(got, "未获取到") {
		t.Fatalf("empty summary=%q", got)
	}
	long := strings.Repeat("证", 300)
	summary := Summarize(EvidencePack{Evidence: []Evidence{{Text: long, SourceRef: kernel.ObjectRef{Kind: kernel.KindDocument}, URL: "https://example.test"}, {Text: "unknown", SourceRef: kernel.ObjectRef{Kind: kernel.KindUnknown}}}})
	for _, want := range []string{"1. [document]", "…", "https://example.test", "2. [unknown]"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %s", want, summary)
		}
	}
}

func TestEvidenceTodoAndClaimClassification(t *testing.T) {
	artifact := kernel.Artifact{
		Ref: kernel.ObjectRef{CanonicalID: "task", Kind: kernel.KindTask},
		Summary: &kernel.NativeSummary{Todos: []map[string]any{
			{"text": "修复", "assignee": "张三", "completed": false},
			{"ignored": "value"},
		}},
		Chunks: []kernel.ContentChunk{{ID: "empty", Kind: "abstract", Text: "   "}, {ID: "task", Kind: "TASK", Text: "处理事项"}, {ID: "abstract", Kind: "abstract", Text: "摘要"}, {ID: "plain", Kind: "paragraph", Text: "事实"}},
	}
	pack := ExtractEvidence("q", ArtifactPack{Artifacts: []kernel.Artifact{artifact}})
	if len(pack.Evidence) != 4 {
		t.Fatalf("evidence=%+v", pack.Evidence)
	}
	got := map[string]bool{}
	for _, evidence := range pack.Evidence {
		got[evidence.ClaimType] = true
	}
	for _, claim := range []string{"action_item", "summary", "fact"} {
		if !got[claim] {
			t.Fatalf("missing claim type %q in %+v", claim, pack.Evidence)
		}
	}
	if todos := summaryTodos(kernel.Artifact{}); todos != nil {
		t.Fatalf("nil summary todos=%v", todos)
	}
	if intString(0) != "0" || intString(12345) != "12345" {
		t.Fatal("intString mismatch")
	}
}

func TestExtractEvidenceDeduplicatesIdenticalChunks(t *testing.T) {
	ref := kernel.ObjectRef{CanonicalID: "feishu:scope:message:m1", NativeID: "m1", Kind: kernel.KindMessage}
	chunk := kernel.ContentChunk{ID: "same", Kind: "content", Text: "相同内容"}
	pack := ExtractEvidence("query", ArtifactPack{Artifacts: []kernel.Artifact{{Ref: ref, Projection: kernel.ProjectionContent, Chunks: []kernel.ContentChunk{chunk}}, {Ref: ref, Projection: kernel.ProjectionContent, Chunks: []kernel.ContentChunk{chunk}}}})
	if len(pack.Evidence) != 1 {
		t.Fatalf("evidence=%+v", pack.Evidence)
	}
}
