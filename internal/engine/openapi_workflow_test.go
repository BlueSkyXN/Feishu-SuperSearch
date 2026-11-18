package engine_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BlueSkyXN/Feishu-SuperSearch/adapter/openapi"
	"github.com/BlueSkyXN/Feishu-SuperSearch/adapter/replay"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestOpenAPIPartialMessagesSurviveKernelAndReplay(t *testing.T) {
	var failPage atomic.Bool
	var failedPageCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/open-apis/search/v2/message" {
			if r.URL.Query().Get("page_token") == "next" {
				if failPage.Load() {
					failedPageCalls.Add(1)
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"code":503,"msg":"temporary page failure"}`))
					return
				}
				_, _ = w.Write([]byte(`{"code":0,"data":{"items":["good2"],"has_more":false}}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"items":["good","forbidden","missing"],"page_token":"next","has_more":true}}`))
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/open-apis/im/v1/messages/")
		switch id {
		case "good", "good2":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"items": []any{map[string]any{"message_id": id, "chat_id": "chat_test", "body": map[string]any{"content": `{"text":"available evidence"}`}}}}})
		case "forbidden":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":403,"msg":"scope missing"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":404,"msg":"message missing"}`))
		}
	}))
	defer server.Close()
	client, err := openapi.NewClient(openapi.ClientConfig{AppID: "cli_test", BaseURL: server.URL, Token: "test-token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	base := openapi.NewMessagesProvider(client)
	store, err := replay.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recording, err := replay.Wrap(base, store)
	if err != nil {
		t.Fatal(err)
	}
	for _, replaying := range []bool{false, true} {
		name := "record"
		var p kernel.Provider = recording
		if replaying {
			name = "replay"
			server.Close()
			p = replay.NewProvider(base.Descriptor(), store)
		}
		t.Run(name, func(t *testing.T) {
			e := relationTestEngine(t, p, "memory")
			ctx := context.Background()
			identity := kernel.Identity{Mode: kernel.IdentityUser}
			search, err := e.Search(ctx, kernel.SearchRequest{Query: "evidence", Sources: []kernel.SourceID{kernel.SourceMessages}, Identity: identity, Limit: 10, LimitPerSource: 10, Strategy: kernel.SearchStrategy{Pagination: "none"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}})
			if err != nil || len(search.Candidates) != 3 || !search.Partial || len(search.Sources) != 1 {
				t.Fatalf("partial search=%+v err=%v", search, err)
			}
			run := search.Sources[0]
			if run.Status != kernel.StatusPartial || run.Pages != 1 || run.Error == nil || run.Error.Subtype != "partial_enrichment" {
				t.Fatalf("enrichment status=%+v", run)
			}
			items := make([]kernel.FetchRequest, 0, 3)
			for _, c := range search.Candidates {
				if c.Ref.NativeID == "good" && c.Snippet != "available evidence" {
					t.Fatalf("successful enrichment lost: %+v", c)
				}
				items = append(items, kernel.FetchRequest{Ref: c.Ref, Projection: kernel.ProjectionContent})
			}
			batch, err := e.Fetch(ctx, kernel.FetchBatchRequest{SessionID: search.SessionID, Identity: identity, Items: items})
			if err != nil || !batch.Partial || len(batch.Items) != 3 {
				t.Fatalf("partial Fetch=%+v err=%v", batch, err)
			}
			for _, item := range batch.Items {
				if item.Ref.NativeID == "good" {
					if item.Artifact == nil || item.Error != nil || len(item.Artifact.Chunks) != 1 || item.Artifact.Chunks[0].Text != "available evidence" {
						t.Fatalf("successful Fetch lost: %+v", item)
					}
					continue
				}
				want := kernel.ErrMissingScope
				if item.Ref.NativeID == "missing" {
					want = kernel.ErrNotFound
				}
				if item.Artifact != nil || item.Error == nil || item.Error.Type != want {
					t.Fatalf("per-object error=%+v want=%s", item, want)
				}
			}
			continuation := kernel.ContinueRequest{SessionID: search.SessionID, Identity: identity, MaxAdditionalPages: 1}
			if !replaying {
				failPage.Store(true)
				failed, err := e.Continue(ctx, continuation)
				failPage.Store(false)
				if err != nil || !failed.Partial || len(failed.Candidates) != 3 || failedPageCalls.Load() < 2 {
					t.Fatalf("page failure=%+v calls=%d err=%v", failed, failedPageCalls.Load(), err)
				}
				run := failed.Sources[0]
				if run.Pages != 1 || run.Error == nil || run.Error.Type != kernel.ErrUpstreamTransient || run.Error.Details["enrichment_error"] == nil {
					t.Fatalf("previous enrichment lost during page failure: %+v", run)
				}
				snap, err := e.Session(ctx, search.SessionID, identity)
				if err != nil || snap.Frontiers[kernel.SourceMessages].Cursor != "next" || snap.Frontiers[kernel.SourceMessages].PagesFetched != 1 {
					t.Fatalf("failed page advanced cursor: %+v err=%v", snap.Frontiers, err)
				}
			}
			continued, err := e.Continue(ctx, continuation)
			if err != nil || len(continued.Candidates) != 4 || !continued.Partial || len(continued.Sources) != 1 {
				t.Fatalf("continue=%+v err=%v", continued, err)
			}
			run = continued.Sources[0]
			if run.Pages != 2 || run.HitCount != 4 || run.Status != kernel.StatusPartial || run.Error == nil || run.Error.Subtype != "partial_enrichment" {
				t.Fatalf("successful page erased unresolved enrichment: %+v", run)
			}
			snap, err := e.Session(ctx, search.SessionID, identity)
			if err != nil || snap.Frontiers[kernel.SourceMessages].HasMore || snap.Frontiers[kernel.SourceMessages].PagesFetched != 2 {
				t.Fatalf("final cursor=%+v err=%v", snap.Frontiers, err)
			}
		})
	}
}
