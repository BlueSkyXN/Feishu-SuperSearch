package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestParsingHelpers(t *testing.T) {
	var values StringList
	if err := values.Set("a"); err != nil {
		t.Fatal(err)
	}
	_ = values.Set("b")
	if values.String() != "a,b" {
		t.Fatalf("list=%q", values.String())
	}
	sources := ParseSources(" docs, messages,docs, ,minutes ")
	if len(sources) != 3 || sources[0] != kernel.SourceDocs || sources[2] != kernel.SourceMinutes {
		t.Fatalf("sources=%v", sources)
	}
	all := "head,snippet,summary,structure,content,context,relations,attachments,"
	projection, err := ParseProjection(all)
	if err != nil {
		t.Fatal(err)
	}
	want := kernel.ProjectionHead | kernel.ProjectionSnippet | kernel.ProjectionSummary | kernel.ProjectionStructure | kernel.ProjectionContent | kernel.ProjectionContext | kernel.ProjectionRelations | kernel.ProjectionAttachments
	if projection != want {
		t.Fatalf("projection=%s", projection)
	}
	if defaultProjection, err := ParseProjection(" "); err != nil || defaultProjection != kernel.ProjectionContent {
		t.Fatalf("default projection=%v err=%v", defaultProjection, err)
	}
	if _, err := ParseProjection("content,unknown"); err == nil {
		t.Fatal("unknown projection accepted")
	}
	identity := ParseIdentity("work", "user")
	if identity.Profile != "work" || identity.Mode != kernel.IdentityUser || identity.ScopeKey == "" {
		t.Fatalf("identity=%+v", identity)
	}
}

func TestParseTimeFormatsAndErrors(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.Local)
	for input, duration := range map[string]time.Duration{"5m": 5 * time.Minute, "2h": 2 * time.Hour, "3d": 72 * time.Hour, "2w": 14 * 24 * time.Hour} {
		got, err := ParseTime(input, now)
		if err != nil || got == nil || !got.Equal(now.Add(-duration)) {
			t.Fatalf("ParseTime(%q)=%v err=%v", input, got, err)
		}
	}
	for _, input := range []string{"2026-07-29T12:30:00+08:00", "2026-07-29T12:30:00", "2026-07-29"} {
		if got, err := ParseTime(input, now); err != nil || got == nil {
			t.Fatalf("ParseTime(%q)=%v err=%v", input, got, err)
		}
	}
	if got, err := ParseTime("", now); err != nil || got != nil {
		t.Fatalf("empty time=%v err=%v", got, err)
	}
	for _, input := range []string{"0h", "3x", "nonsense"} {
		if _, err := ParseTime(input, now); err == nil {
			t.Fatalf("invalid time %q accepted", input)
		}
	}
}

func TestJSONAndSearchOutput(t *testing.T) {
	var decoded map[string]any
	if err := ReadJSON(strings.NewReader(`{"value":1}`), &decoded); err != nil || decoded["value"] != float64(1) {
		t.Fatalf("decoded=%v err=%v", decoded, err)
	}
	if err := ReadJSON(strings.NewReader(`{`), &decoded); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	var compact, pretty bytes.Buffer
	value := map[string]string{"html": "<tag>"}
	if err := PrintJSON(&compact, value, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(compact.String(), `\u003c`) || !strings.Contains(compact.String(), "<tag>") {
		t.Fatalf("HTML was escaped: %s", compact.String())
	}
	if err := PrintJSON(&pretty, value, true); err != nil || !strings.Contains(pretty.String(), "\n  ") {
		t.Fatalf("pretty=%q err=%v", pretty.String(), err)
	}

	longTitle := strings.Repeat("题", 80)
	longSnippet := strings.Repeat("片", 120)
	snapshot := kernel.SearchSnapshot{
		SessionID: "rs_test",
		Query:     "A 项目",
		Partial:   true,
		Stats:     kernel.SearchStats{RawCandidates: 2, ElapsedMS: 9},
		Candidates: []kernel.Candidate{
			{Ref: kernel.ObjectRef{NativeID: "native", CanonicalID: "canonical"}, Source: kernel.SourceDocs, Kind: kernel.KindDocument, FusedScore: 1, Snippet: longSnippet},
			{Ref: kernel.ObjectRef{CanonicalID: "long"}, Source: kernel.SourceMessages, Kind: kernel.KindMessage, Title: longTitle, URL: "https://example.test"},
		},
		Sources: []kernel.SourceRun{{Source: kernel.SourceDocs, Status: kernel.StatusFailed, Error: &kernel.ErrorDetail{Message: "denied"}}},
	}
	var out bytes.Buffer
	PrintSearch(&out, snapshot)
	text := out.String()
	for _, want := range []string{"rs_test", "native", "…", "https://example.test", "Sources:", "denied"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
}
