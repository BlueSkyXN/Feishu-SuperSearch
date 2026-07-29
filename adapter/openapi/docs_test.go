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

func TestDocsSearch(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/search/v2/doc_wiki/search" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("auth=%q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"success","data":{"res_units":[{"title_highlighted":"A <h>项目</h> 计划","summary_highlighted":"延期原因","result_meta":{"token":"dox123","url":"https://example/doc","update_time":1760000000}}],"has_more":true,"page_token":"next"}}`))
	}))
	defer srv.Close()
	client, _ := NewClient(ClientConfig{AppID: "cli_test", BaseURL: srv.URL, Token: "test-token", HTTPClient: srv.Client()})
	provider := NewDocsProvider(client, "ou_me")
	after := time.Unix(1750000000, 0)
	page, err := provider.Search(context.Background(), kernel.ProviderSearchRequest{Query: "A 项目", PageSize: 8, Filters: kernel.SearchFilters{Mine: true, After: &after, DocTypes: []string{"docx"}, OnlyTitle: true}, Identity: kernel.Identity{Mode: kernel.IdentityUser, ScopeKey: "scope"}})
	if err != nil {
		t.Fatal(err)
	}
	if got["page_size"].(float64) != 8 {
		t.Fatalf("body=%#v", got)
	}
	docFilter := got["doc_filter"].(map[string]any)
	if onlyTitle, _ := docFilter["only_title"].(bool); !onlyTitle {
		t.Fatalf("only_title not compiled: %#v", docFilter)
	}
	if len(page.Candidates) != 1 || page.Candidates[0].Ref.NativeID != "dox123" || page.Candidates[0].Title != "A 项目 计划" {
		t.Fatalf("page=%#v", page)
	}
	if !page.HasMore || page.NextCursor != "next" {
		t.Fatalf("pagination=%#v", page)
	}
}

func TestDocsSearchRequiresUserOpenIDForMine(t *testing.T) {
	client, _ := NewClient(ClientConfig{AppID: "cli_test", BaseURL: "http://127.0.0.1", Token: "x"})
	_, err := NewDocsProvider(client, "").Search(context.Background(), kernel.ProviderSearchRequest{Query: "x", Filters: kernel.SearchFilters{Mine: true}, Identity: kernel.Identity{ScopeKey: "s"}})
	if kernel.DetailFromError(err).Type != kernel.ErrInvalidRequest {
		t.Fatalf("err=%v", err)
	}
}

func TestDocsSearchMarksBaseHitAsNotFetchable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"res_units":[{"title_highlighted":"数据表","result_meta":{"token":"base1","doc_types":"BITABLE","url":"https://tenant.feishu.cn/base/base1"}}]}}`))
	}))
	defer server.Close()
	client, _ := NewClient(ClientConfig{AppID: "cli_test", BaseURL: server.URL, Token: "x", HTTPClient: server.Client()})
	provider := NewDocsProvider(client, "")
	page, err := provider.Search(context.Background(), kernel.ProviderSearchRequest{Query: "数据", Identity: kernel.Identity{Mode: kernel.IdentityUser, ScopeKey: "scope"}})
	if err != nil || len(page.Candidates) != 1 || page.Candidates[0].Kind != kernel.KindBase || !page.Candidates[0].AvailableProjection.Empty() {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	_, err = provider.Fetch(context.Background(), []kernel.ProviderFetchRequest{{Ref: page.Candidates[0].Ref, Projection: kernel.ProjectionContent}})
	if kernel.DetailFromError(err).Type != kernel.ErrUnsupported {
		t.Fatalf("fetch error=%v", err)
	}
}

func TestClientMapsRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"msg":"too many requests"}`))
	}))
	defer srv.Close()
	c, _ := NewClient(ClientConfig{AppID: "cli_test", BaseURL: srv.URL, Token: "x", HTTPClient: srv.Client()})
	_, err := c.Call(context.Background(), "GET", "/open-apis/test", nil)
	if kernel.DetailFromError(err).Type != kernel.ErrRateLimited {
		t.Fatalf("err=%v", err)
	}
}

func TestClientRedactsOpenAPIErrorPayloads(t *testing.T) {
	const sentinel = "tenant-secret-sentinel"
	tests := []struct {
		name      string
		status    int
		body      string
		wantType  kernel.ErrorType
		wantCode  int
		wantLogID string
	}{
		{
			name:      "http status",
			status:    http.StatusForbidden,
			body:      `{"msg":"` + sentinel + `","error":{"token":"` + sentinel + `"},"data":{"tenant":"` + sentinel + `"},"log_id":"log_123"}`,
			wantType:  kernel.ErrMissingScope,
			wantCode:  http.StatusForbidden,
			wantLogID: "log_123",
		},
		{
			name:      "business error",
			status:    http.StatusOK,
			body:      `{"code":12345,"msg":"permission ` + sentinel + `","error":{"token":"` + sentinel + `"},"data":{"tenant":"` + sentinel + `"},"log_id":"log_456"}`,
			wantType:  kernel.ErrMissingScope,
			wantCode:  12345,
			wantLogID: "log_456",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, _ := NewClient(ClientConfig{AppID: "cli_test", BaseURL: server.URL, Token: "x", HTTPClient: server.Client()})
			_, err := client.Call(context.Background(), http.MethodGet, "/open-apis/test", nil)
			detail := kernel.DetailFromError(err)
			encoded, marshalErr := json.Marshal(detail)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if detail.Type != test.wantType || detail.Code != test.wantCode || detail.Details["log_id"] != test.wantLogID {
				t.Fatalf("detail=%+v", detail)
			}
			if strings.Contains(string(encoded), sentinel) {
				t.Fatalf("public error leaked upstream payload: %s", encoded)
			}
		})
	}
}

func TestDocsFetchRejectsPathTraversalBeforeHTTPCall(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	client, _ := NewClient(ClientConfig{AppID: "cli_test", BaseURL: server.URL, Token: "x", HTTPClient: server.Client()})
	provider := NewDocsProvider(client, "")
	ref := kernel.ObjectRef{Kind: kernel.KindDocument, NativeID: "../im/v1/messages/om_x", Source: kernel.SourceDocs}
	_, err := provider.Fetch(context.Background(), []kernel.ProviderFetchRequest{{Ref: ref, Projection: kernel.ProjectionContent}})
	if detail := kernel.DetailFromError(err); err == nil || detail.Type != kernel.ErrInvalidRequest || calls != 0 {
		t.Fatalf("error=%v detail=%+v calls=%d", err, detail, calls)
	}
}

func TestOfficialSDKEnforcesResponseLimitAndMapsScopeErrors(t *testing.T) {
	t.Run("response limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"res_units":[],"padding":"` + string(make([]byte, 256)) + `"}}`))
		}))
		defer server.Close()
		client, _ := NewClient(ClientConfig{AppID: "cli_test", BaseURL: server.URL, Token: "x", HTTPClient: server.Client(), MaxResponseBytes: 64})
		_, err := NewDocsProvider(client, "").Search(context.Background(), kernel.ProviderSearchRequest{Query: "x", Identity: kernel.Identity{Mode: kernel.IdentityUser}})
		if detail := kernel.DetailFromError(err); detail.Type != kernel.ErrParse || !strings.Contains(detail.Message, "size limit") {
			t.Fatalf("error=%v detail=%+v", err, detail)
		}
	})

	t.Run("missing scope", func(t *testing.T) {
		const sentinel = "sdk-tenant-secret-sentinel"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":99991672,"msg":"missing scope ` + sentinel + `","data":{"token":"` + sentinel + `"},"log_id":"sdk_log_123"}`))
		}))
		defer server.Close()
		client, _ := NewClient(ClientConfig{AppID: "cli_test", BaseURL: server.URL, Token: "x", HTTPClient: server.Client()})
		_, err := NewDocsProvider(client, "").Search(context.Background(), kernel.ProviderSearchRequest{Query: "x", Identity: kernel.Identity{Mode: kernel.IdentityUser}})
		detail := kernel.DetailFromError(err)
		encoded, marshalErr := json.Marshal(detail)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if detail.Type != kernel.ErrMissingScope || detail.Code != http.StatusForbidden || detail.Details["log_id"] != "sdk_log_123" || strings.Contains(string(encoded), sentinel) {
			t.Fatalf("error=%v detail=%+v", err, detail)
		}
	})
}
