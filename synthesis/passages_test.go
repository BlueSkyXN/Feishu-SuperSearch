package synthesis

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func docArtifact(id, text string) kernel.Artifact {
	ref := kernel.ObjectRef{CanonicalID: "feishu:scope:document:" + id, NativeID: id, Kind: kernel.KindDocument}
	return kernel.Artifact{Ref: ref, Projection: kernel.ProjectionContent, Chunks: []kernel.ContentChunk{{ID: id + "-content", Kind: "content", Text: text}}}
}

// assertQuotesMatchSource checks that every passage quote is the exact chunk
// text at its span and respects the passage size limit.
func assertQuotesMatchSource(t *testing.T, text string, evidence []Evidence) {
	t.Helper()
	r := []rune(text)
	for _, e := range evidence {
		if e.Quote != string(r[e.SourceSpan.Start:e.SourceSpan.End]) {
			t.Fatalf("quote does not match span %d..%d", e.SourceSpan.Start, e.SourceSpan.End)
		}
		if n := len([]rune(e.Quote)); n == 0 || n > maxPassageRunes {
			t.Fatalf("passage length %d outside 1..%d", n, maxPassageRunes)
		}
	}
}

func TestExtractEvidenceSelectsMatchingPassagesOfLongDocument(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&b, "## 第%d节\n%s\n\n", i, strings.Repeat(string(rune('甲'+i)), 200))
		if i == 6 {
			b.WriteString("本季度上线存在延期风险，原因是接口联调未完成。\n\n")
		}
	}
	text := b.String()
	pack := ExtractEvidence("上线 延期", ArtifactPack{Artifacts: []kernel.Artifact{docArtifact("d1", text)}})
	if len(pack.Evidence) == 0 || len(pack.Evidence) > maxPassagesPerChunk {
		t.Fatalf("got %d passages, want 1..%d", len(pack.Evidence), maxPassagesPerChunk)
	}
	found := false
	for _, e := range pack.Evidence {
		found = found || strings.Contains(e.Quote, "延期风险")
	}
	if !found {
		t.Fatal("the passage matching the query was not selected")
	}
	assertQuotesMatchSource(t, text, pack.Evidence)
	for i := 1; i < len(pack.Evidence); i++ {
		if pack.Evidence[i].SourceSpan.Start < pack.Evidence[i-1].SourceSpan.End {
			t.Fatal("passages must be returned in text order without overlap")
		}
	}
}

func TestExtractEvidenceSplitsTranscriptAtLines(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "00:%02d:%02d 发言人%d: 讨论第%d项议题的安排\n", i/60, i%60, i%3, i)
		if i == 77 {
			b.WriteString("00:01:17 发言人2: 第七十七项需要追加预算\n")
		}
	}
	text := b.String()
	pack := ExtractEvidence("追加预算", ArtifactPack{Artifacts: []kernel.Artifact{docArtifact("m1", text)}})
	assertQuotesMatchSource(t, text, pack.Evidence)
	found := false
	for _, e := range pack.Evidence {
		found = found || strings.Contains(e.Quote, "追加预算")
		if strings.HasPrefix(e.Quote, "\n") || !strings.HasPrefix(e.Quote, "00:") {
			t.Fatalf("transcript passage should start at a line: %q", e.Quote[:20])
		}
	}
	if !found {
		t.Fatal("matching transcript line was not selected")
	}
}

func TestExtractEvidenceHardSplitsLongLineAndShiftsSpanByChunkStart(t *testing.T) {
	text := strings.Repeat("长", 2000)
	artifact := docArtifact("d2", text)
	artifact.Chunks[0].Start = 100
	pack := ExtractEvidence("无关", ArtifactPack{Artifacts: []kernel.Artifact{artifact}})
	if len(pack.Evidence) != maxPassagesPerChunk {
		t.Fatalf("got %d passages, want %d", len(pack.Evidence), maxPassagesPerChunk)
	}
	if first := pack.Evidence[0].SourceSpan; first.Start != 100 || first.End != 100+maxPassageRunes {
		t.Fatalf("first span=%+v", first)
	}
}

func TestExtractEvidenceCapsTotalPassageBytes(t *testing.T) {
	var artifacts []kernel.Artifact
	for i := 0; i < 300; i++ {
		var b strings.Builder
		for j := 0; j < 4; j++ {
			b.WriteString(strings.Repeat(string(rune('乙'+j)), maxPassageRunes))
			b.WriteString("\n\n")
		}
		artifacts = append(artifacts, docArtifact(fmt.Sprintf("d%d", i), b.String()))
	}
	pack := ExtractEvidence("q", ArtifactPack{Artifacts: artifacts})
	total := 0
	for _, e := range pack.Evidence {
		total += len(e.Text)
	}
	if total > maxChunkEvidenceBytes || total < maxChunkEvidenceBytes/2 {
		t.Fatalf("passage bytes=%d, want at most %d and close to it", total, maxChunkEvidenceBytes)
	}
}

func TestQueryTerms(t *testing.T) {
	if got, want := queryTerms("A 项目延期, API"), []string{"项目", "目延", "延期", "api"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("terms=%v want %v", got, want)
	}
	if got, want := queryTerms("会 议"), []string{"会", "议"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("terms=%v want %v", got, want)
	}
}
