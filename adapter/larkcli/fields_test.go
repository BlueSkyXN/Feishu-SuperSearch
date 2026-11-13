package larkcli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
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

// Message details measured with lark-cli 1.0.100 include interactive cards
// whose elements are only images. Reading one must succeed with empty content
// instead of failing as version_incompatible.
func TestMessageFetchAcceptsCardWithoutText(t *testing.T) {
	spec := specFor(t, "messages")
	raw := `{"ok":true,"identity":"user","data":{"items":[{"message_id":"om_sample","chat_id":"oc_sample","msg_type":"interactive","body":{"content":"{\"title\":null,\"elements\":[[{\"tag\":\"img\",\"image_key\":\"img_sample\"}]]}"}}]}}`
	_, data, err := ParseEnvelope(CommandResult{ExitCode: 0, Stdout: []byte(raw)})
	if err != nil {
		t.Fatal(err)
	}
	ref := kernel.ObjectRef{Kind: kernel.KindMessage, NativeID: "om_sample", Source: kernel.SourceMessages}
	projection := kernel.ProjectionContent | kernel.ProjectionContext | kernel.ProjectionRelations
	artifact, err := (&Provider{spec: spec}).parseArtifact(kernel.ProviderFetchRequest{Ref: ref, Projection: projection}, data, "")
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if !artifact.Projection.Has(projection) || len(artifact.Chunks) != 0 {
		t.Fatalf("projection=%v chunks=%d", artifact.Projection.Strings(), len(artifact.Chunks))
	}

	// A card with a title still yields its text.
	withText := `{"ok":true,"data":{"items":[{"message_id":"om_text","msg_type":"interactive","body":{"content":"{\"title\":\"项目周报\",\"elements\":[[{\"tag\":\"text\",\"text\":\"进度正常\"}]]}"}}]}}`
	_, data, err = ParseEnvelope(CommandResult{ExitCode: 0, Stdout: []byte(withText)})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err = (&Provider{spec: spec}).parseArtifact(kernel.ProviderFetchRequest{Ref: ref, Projection: projection}, data, "")
	if err != nil || len(artifact.Chunks) != 1 || artifact.Chunks[0].Text != "项目周报\n进度正常" {
		t.Fatalf("artifact=%+v err=%v", artifact.Chunks, err)
	}
}

// p2p messages from lark-cli 1.0.100 search have no chat_name; the sender's
// name is the title, as in the OpenAPI adapter.
func TestMessageTitleFallsBackToSenderName(t *testing.T) {
	spec := specFor(t, "messages")
	item := map[string]any{"message_id": "om_p2p", "chat_type": "p2p", "chat_partner": map[string]any{"open_id": "ou_partner"}, "sender": map[string]any{"id": "ou_sender", "id_type": "open_id", "name": "示例同事"}, "content": "你好"}
	if c := (&Provider{spec: spec}).itemToCandidate(item, 1, kernel.Identity{Mode: kernel.IdentityUser}); c.Title != "示例同事" {
		t.Fatalf("title=%q", c.Title)
	}
	item["chat_name"] = "项目群"
	if c := (&Provider{spec: spec}).itemToCandidate(item, 1, kernel.Identity{Mode: kernel.IdentityUser}); c.Title != "项目群" {
		t.Fatalf("group title=%q", c.Title)
	}
}

// docs +fetch of a wiki page reports the underlying docx as document.document_id;
// that is the document itself, not a link.
func TestDocsFetchDoesNotRelateDocumentToItself(t *testing.T) {
	spec := specFor(t, "docs")
	data := map[string]any{"document": map[string]any{"document_id": "doxUNDERLYING", "content": "# 标题\n正文"}}
	ref := kernel.ObjectRef{Kind: kernel.KindDocument, NativeID: "wikSAMPLE", Source: kernel.SourceDocs}
	if relations := (&Provider{spec: spec}).parseRelations(kernel.ProviderExpandRequest{Ref: ref}, data); len(relations) != 0 {
		t.Fatalf("relations=%+v", relations)
	}
}

func TestCalendarFetchArgsAcceptCompoundIDWithoutURL(t *testing.T) {
	args, err := calendarFetchArgs(kernel.ProviderFetchRequest{Ref: kernel.ObjectRef{NativeID: "cal_sample/evt_sample"}})
	if err != nil || len(args) != 5 || args[4] != `{"calendar_id":"cal_sample","event_id":"evt_sample"}` {
		t.Fatalf("args=%v err=%v", args, err)
	}
	if _, err := calendarFetchArgs(kernel.ProviderFetchRequest{Ref: kernel.ObjectRef{NativeID: "evt_sample"}}); err == nil {
		t.Fatal("event id without calendar must be rejected")
	}
}
