package openapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestDocsFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/open-apis/docs_ai/v1/documents/dox1/fetch" || r.Method != http.MethodPost {
			t.Fatalf("request=%s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"document":{"document_id":"dox1","revision_id":"12","content":"<title>计划</title><p>延期三天</p>"}}}`))
	}))
	defer server.Close()
	client, _ := NewClient(ClientConfig{AppID: "cli_test", BaseURL: server.URL, Token: "x", HTTPClient: server.Client()})
	ref := kernel.ObjectRef{NativeID: "dox1", CanonicalID: "feishu:s:document:dox1", Kind: kernel.KindDocument, Source: kernel.SourceDocs}
	artifacts, err := NewDocsProvider(client, "").Fetch(context.Background(), []kernel.ProviderFetchRequest{{Ref: ref, Projection: kernel.ProjectionContent | kernel.ProjectionStructure}})
	if err != nil || len(artifacts) != 1 || len(artifacts[0].Chunks) != 1 || artifacts[0].Version != "12" {
		t.Fatalf("artifacts=%+v err=%v", artifacts, err)
	}
}

func TestMessagesSearchAndFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/search/v2/message":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["query"] != "延期" || body["chat_ids"] == nil || body["from_ids"] == nil || body["start_time"] != "1700000000" || body["end_time"] != "1700003600" || body["filter"] != nil || r.URL.Query().Get("page_size") != "5" {
				t.Fatalf("body=%v query=%v", body, r.URL.Query())
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"items":["om1"],"has_more":true,"page_token":"next"}}`))
		case "/open-apis/im/v1/messages/om1":
			_, _ = w.Write([]byte(`{"code":0,"data":{"items":[{"message_id":"om1","chat_id":"oc1","create_time":"1760000000","sender":{"id":"ou1"},"body":{"content":"{\"text\":\"测试环境延期三天 https://example.feishu.cn/docx/dox1\"}"}}]}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, _ := NewClient(ClientConfig{AppID: "cli_test", BaseURL: server.URL, Token: "x", HTTPClient: server.Client()})
	provider := NewMessagesProvider(client)
	after := time.Unix(1700000000, 0)
	before := time.Unix(1700003600, 0)
	page, err := provider.Search(context.Background(), kernel.ProviderSearchRequest{Query: "延期", PageSize: 5, Filters: kernel.SearchFilters{ChatIDs: []string{"oc1"}, SenderIDs: []string{"ou1"}, After: &after, Before: &before}, Identity: kernel.Identity{Mode: kernel.IdentityUser, ScopeKey: "s"}})
	if err != nil || len(page.Candidates) != 1 || !strings.Contains(page.Candidates[0].Snippet, "测试环境延期三天") || !page.HasMore || page.NextCursor != "next" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	want := kernel.ProjectionContent | kernel.ProjectionContext | kernel.ProjectionRelations
	artifacts, err := provider.Fetch(context.Background(), []kernel.ProviderFetchRequest{{Ref: page.Candidates[0].Ref, Projection: want}})
	if err != nil || len(artifacts) != 1 || !artifacts[0].Projection.Has(want) || len(artifacts[0].Relations) != 2 || artifacts[0].Relations[0].Type != "message.in_chat" || artifacts[0].Relations[1].Type != "message.links_to_document" {
		t.Fatalf("artifacts=%+v err=%v", artifacts, err)
	}
	if _, ok := artifacts[0].Metadata["context"].(map[string]any); !ok {
		t.Fatalf("context metadata=%v", artifacts[0].Metadata["context"])
	}
	if provider.Descriptor().FetchableProjection.Has(kernel.ProjectionAttachments) {
		t.Fatal("messages must not advertise unimplemented attachment materialization")
	}
	if _, err := provider.Fetch(context.Background(), []kernel.ProviderFetchRequest{{Ref: page.Candidates[0].Ref, Projection: kernel.ProjectionAttachments}}); kernel.DetailFromError(err).Type != kernel.ErrUnsupported {
		t.Fatalf("attachment fetch error=%v", err)
	}
}

func TestPeopleSearchAndResolve(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/open-apis/directory/v1/employees/search" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Query().Get("employee_id_type") != "open_id" || body["required_fields"] == nil {
			t.Fatalf("query=%v body=%v", r.URL.Query(), body)
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"employees":[{"base_info":{"employee_id":"ou1","name":{"name":{"default_value":"张三"}},"enterprise_email":"z@example.invalid"}}],"page_response":{"has_more":false}}}`))
	}))
	defer server.Close()
	client, _ := NewClient(ClientConfig{AppID: "cli_test", BaseURL: server.URL, Token: "x", HTTPClient: server.Client()})
	provider := NewPeopleProvider(client)
	refs, err := provider.Resolve(context.Background(), kernel.ProviderResolveRequest{Text: "张三", Identity: kernel.Identity{Mode: kernel.IdentityUser, ScopeKey: "s"}, Limit: 10})
	if err != nil || len(refs) != 1 || refs[0].NativeID != "ou1" {
		t.Fatalf("refs=%+v err=%v", refs, err)
	}
}

func TestMinutesSearchAndFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/minutes/v1/minutes/search":
			_, _ = w.Write([]byte(`{"code":0,"data":{"items":[{"token":"min1","display_info":"上线评审","meta_data":{"app_link":"https://example.invalid/min1","description":"决定延期"}}]}}`))
		case "/open-apis/minutes/v1/minutes/min1":
			_, _ = w.Write([]byte(`{"code":0,"data":{"minute":{"token":"min1","title":"上线评审"}}}`))
		case "/open-apis/minutes/v1/minutes/min1/artifacts":
			_, _ = w.Write([]byte(`{"code":0,"data":{"summary":"决定延期三天","minute_todos":[{"content":"补测试"}],"minute_chapters":[{"title":"结论"}],"transcript":"测试环境未就绪"}}`))
		default:
			t.Fatalf("unexpected path=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, _ := NewClient(ClientConfig{AppID: "cli_test", BaseURL: server.URL, Token: "x", HTTPClient: server.Client()})
	provider := NewMinutesProvider(client)
	page, err := provider.Search(context.Background(), kernel.ProviderSearchRequest{Query: "延期", Identity: kernel.Identity{Mode: kernel.IdentityUser, ScopeKey: "s"}})
	if err != nil || len(page.Candidates) != 1 || page.Candidates[0].Title != "上线评审" || page.Candidates[0].URL != "https://example.invalid/min1" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	artifacts, err := provider.Fetch(context.Background(), []kernel.ProviderFetchRequest{{Ref: page.Candidates[0].Ref, Projection: kernel.ProjectionSummary | kernel.ProjectionContent | kernel.ProjectionStructure}})
	if err != nil || len(artifacts) != 1 || artifacts[0].Summary == nil || artifacts[0].Summary.Text != "决定延期三天" || len(artifacts[0].Chunks) < 2 {
		t.Fatalf("artifacts=%+v err=%v", artifacts, err)
	}
}

func TestMinutesFetchRejectsMissingRequestedContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/minutes/v1/minutes/min1":
			_, _ = w.Write([]byte(`{"code":0,"data":{"minute":{"minute_token":"min1"}}}`))
		case "/open-apis/minutes/v1/minutes/min1/artifacts":
			_, _ = w.Write([]byte(`{"code":0,"data":{"summary":"只有摘要"}}`))
		default:
			t.Fatalf("unexpected path=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, _ := NewClient(ClientConfig{AppID: "cli_test", BaseURL: server.URL, Token: "x", HTTPClient: server.Client()})
	ref := kernel.ObjectRef{NativeID: "min1", CanonicalID: "feishu:s:minute:min1", Kind: kernel.KindMinute, Source: kernel.SourceMinutes}
	_, err := NewMinutesProvider(client).Fetch(context.Background(), []kernel.ProviderFetchRequest{{Ref: ref, Projection: kernel.ProjectionContent}})
	if detail := kernel.DetailFromError(err); err == nil || detail.Type != kernel.ErrVersionIncompatible {
		t.Fatalf("error=%v detail=%+v", err, detail)
	}
}

func TestDirectDescriptorsDoNotAdvertiseUnparsedProjection(t *testing.T) {
	client, err := NewClient(ClientConfig{AppID: "cli_test", BaseURL: "https://open.feishu.invalid", Token: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if NewDocsProvider(client, "").Descriptor().FetchableProjection.Has(kernel.ProjectionRelations) {
		t.Fatal("docs relations are not materialized by the direct fetch parser")
	}
	if NewMinutesProvider(client).Descriptor().FetchableProjection.Has(kernel.ProjectionRelations) {
		t.Fatal("minutes relations are not materialized by the direct fetch parser")
	}
}

func TestDirectSearchRejectsMissingStableObjectIDs(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		response string
		search   func(*Client) error
		want     kernel.ErrorType
	}{
		{
			name: "docs", path: "/open-apis/search/v2/doc_wiki/search",
			response: `{"code":0,"data":{"res_units":[{"title_highlighted":"无 token"}]}}`,
			search: func(client *Client) error {
				_, err := NewDocsProvider(client, "").Search(context.Background(), kernel.ProviderSearchRequest{Query: "x", Identity: kernel.Identity{Mode: kernel.IdentityUser}})
				return err
			},
			want: kernel.ErrVersionIncompatible,
		},
		{
			name: "minutes", path: "/open-apis/minutes/v1/minutes/search",
			response: `{"code":0,"data":{"items":[{"display_info":"无 token"}]}}`,
			search: func(client *Client) error {
				_, err := NewMinutesProvider(client).Search(context.Background(), kernel.ProviderSearchRequest{Query: "x", Identity: kernel.Identity{Mode: kernel.IdentityUser}})
				return err
			},
			want: kernel.ErrVersionIncompatible,
		},
		{
			name: "people field permission", path: "/open-apis/directory/v1/employees/search",
			response: `{"code":0,"data":{"employees":[{"base_info":{"name":{"name":{"default_value":"张三"}}}}],"abnormals":[{"id":"redacted","field_errors":{"base_info.employee_id":1}}]}}`,
			search: func(client *Client) error {
				_, err := NewPeopleProvider(client).Search(context.Background(), kernel.ProviderSearchRequest{Query: "张三", Identity: kernel.Identity{Mode: kernel.IdentityUser}})
				return err
			},
			want: kernel.ErrMissingScope,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path != test.path {
					t.Fatalf("path=%s want=%s", r.URL.Path, test.path)
				}
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()
			client, _ := NewClient(ClientConfig{AppID: "cli_test", BaseURL: server.URL, Token: "x", HTTPClient: server.Client()})
			if err := test.search(client); kernel.DetailFromError(err).Type != test.want {
				t.Fatalf("error=%v detail=%+v", err, kernel.DetailFromError(err))
			}
		})
	}
}
