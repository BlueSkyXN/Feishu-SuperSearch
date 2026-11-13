package larkcli

import (
	"encoding/json"
	"testing"
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
