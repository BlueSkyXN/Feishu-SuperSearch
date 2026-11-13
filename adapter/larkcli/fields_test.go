package larkcli

import (
	"encoding/json"
	"testing"
	"time"
)

// The item has no top-level ID or title key. The document token sits in
// result_meta and an owner object carries its own id and name. Lookups below
// the top level must be repeatable and must follow key priority across
// sibling objects, so the higher-priority "token" wins over owner.id.
func TestFindStringIsDeterministicAndFollowsKeyPriority(t *testing.T) {
	raw := `{"entity_type":"DOC","title_highlighted":"<em>A</em> 项目周报",
		"owner":{"id":"ou_owner_1","name":"张三"},
		"result_meta":{"token":"doxREAL","url":"https://example.feishu.cn/docx/doxREAL","doc_types":"DOCX"}}`
	docs := specFor(t, "docs")
	var firstID, firstTitle, firstURL string
	for run := 0; run < 50; run++ {
		var item map[string]any
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			t.Fatal(err)
		}
		id := findString(item, docs.IDKeys)
		title := findString(item, docs.TitleKeys)
		url := findString(item, docs.URLKeys)
		if id != "doxREAL" {
			t.Fatalf("run %d: id=%q want doxREAL", run+1, id)
		}
		if run == 0 {
			firstID, firstTitle, firstURL = id, title, url
			continue
		}
		if id != firstID || title != firstTitle || url != firstURL {
			t.Fatalf("run %d: got (%q,%q,%q), first run (%q,%q,%q)", run+1, id, title, url, firstID, firstTitle, firstURL)
		}
	}
}

func TestFindStringPrefersShallowerMatch(t *testing.T) {
	item := map[string]any{
		"a": map[string]any{"deep": map[string]any{"id": "too-deep"}},
		"b": map[string]any{"id": "shallow"},
	}
	if got := findString(item, []string{"id"}); got != "shallow" {
		t.Fatalf("id=%q want shallow", got)
	}
	if got := findAny(item, "id"); got != "shallow" {
		t.Fatalf("findAny id=%v want shallow", got)
	}
}

func specFor(t *testing.T, source string) SourceSpec {
	t.Helper()
	for _, spec := range BuiltinSpecs() {
		if string(spec.Descriptor.Source) == source {
			return spec
		}
	}
	t.Fatalf("no spec for %s", source)
	return SourceSpec{}
}

// Zone-less lark-cli times are local: parsed as UTC, the newest message
// create_time measured with lark-cli 1.0.100 lay 8 hours in the future on a
// UTC+8 machine.
func TestFindTimeReadsZonelessTimesAsLocal(t *testing.T) {
	cases := map[string]time.Time{
		"2026-07-30 10:05":     time.Date(2026, 7, 30, 10, 5, 0, 0, time.Local),
		"2026-07-30 10:05:09":  time.Date(2026, 7, 30, 10, 5, 9, 0, time.Local),
		"2026-07-30T10:05:09Z": time.Date(2026, 7, 30, 10, 5, 9, 0, time.UTC),
		"1785400000":           time.Unix(1785400000, 0),
	}
	for raw, want := range cases {
		got := findTime(map[string]any{"create_time": raw}, []string{"create_time"})
		if got == nil || !got.Equal(want) {
			t.Fatalf("findTime(%q)=%v want %v", raw, got, want)
		}
	}
}
