package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/ai"
	appcore "github.com/BlueSkyXN/Feishu-SuperSearch/internal/app"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
	"github.com/BlueSkyXN/Feishu-SuperSearch/transport/httpapi"
)

func TestClientSearchAndFetch(t *testing.T) {
	cfg := appcore.DefaultConfig()
	cfg.Backend = "mock"
	cfg.Storage = appcore.StorageConfig{Type: "file", Path: t.TempDir(), TTL: "45m"}
	a, err := appcore.Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	srv := httptest.NewServer(httpapi.New(a).Handler())
	defer srv.Close()
	c := New(srv.URL)
	snap, err := c.Search(context.Background(), kernel.SearchRequest{Query: "A 项目 延期", Sources: []kernel.SourceID{kernel.SourceDocs, kernel.SourceMessages}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Candidates) == 0 {
		t.Fatal("no candidates")
	}
	batch, err := c.Fetch(context.Background(), kernel.FetchBatchRequest{SessionID: snap.SessionID, Items: []kernel.FetchRequest{{Ref: snap.Candidates[0].Ref, Projection: kernel.ProjectionContent}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Items) == 0 || batch.Items[0].Artifact == nil {
		t.Fatalf("batch=%+v", batch)
	}
}

func TestClientAsk(t *testing.T) {
	cfg := appcore.DefaultConfig()
	cfg.Backend = "mock"
	cfg.Storage = appcore.StorageConfig{Type: "memory", TTL: "45m"}
	a, err := appcore.Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a.Answerer = staticAnswerer{}
	defer a.Close()
	srv := httptest.NewServer(httpapi.New(a).Handler())
	defer srv.Close()

	result, err := New(srv.URL).Ask(context.Background(), planner.UserRequest{Query: "A 项目为什么延期", Sources: []kernel.SourceID{kernel.SourceDocs, kernel.SourceMessages}, FetchTopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer.Text != "基于测试证据的回答" || result.Answerer != "static-test" {
		t.Fatalf("unexpected ask result: %+v", result)
	}
	if len(result.Research.EvidencePack.Evidence) == 0 {
		t.Fatal("client Ask response did not decode research evidence")
	}
}

func TestTypedClientMethodsUseExpectedRoutes(t *testing.T) {
	responses := map[string]string{
		"POST /v1/search/continue":             `{"session_id":"continued"}`,
		"POST /v1/query":                       `{"session_id":"query"}`,
		"POST /v1/expand":                      `{"session_id":"expand"}`,
		"POST /v1/resolve":                     `{"refs":[{"native_id":"person"}]}`,
		"GET /v1/capabilities":                 `{"providers":[]}`,
		"GET /v1/capabilities?probe=true":      `{"providers":[]}`,
		"GET /v1/sessions/session-1?mode=auto": `{"id":"session-1"}`,
		"POST /v1/research":                    `{"planner":"rules","summary":"summary"}`,
		"POST /v1/plans:execute":               `{"result":{"session_id":"plan"},"events":[{"type":"session.completed"}]}`,
	}
	seen := map[string]int{}
	c := New("http://example.test///")
	if c.BaseURL != "http://example.test" || c.HTTP.Timeout != 30*time.Second {
		t.Fatalf("new client=%+v", c)
	}
	c.HTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		key := req.Method + " " + req.URL.RequestURI()
		payload, ok := responses[key]
		if !ok {
			t.Fatalf("unexpected request %s", key)
		}
		seen[key]++
		if req.Method == http.MethodPost && req.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("missing JSON content type for %s", key)
		}
		if req.Header.Get("Accept") != "application/json" {
			t.Fatalf("missing accept header for %s", key)
		}
		return response(http.StatusOK, payload), nil
	})}
	ctx := context.Background()
	if out, err := c.Continue(ctx, kernel.ContinueRequest{SessionID: "session"}); err != nil || out.SessionID != "continued" {
		t.Fatalf("continue=%+v err=%v", out, err)
	}
	if out, err := c.Query(ctx, kernel.QueryRequest{Source: kernel.SourceTasks}); err != nil || out.SessionID != "query" {
		t.Fatalf("query=%+v err=%v", out, err)
	}
	if out, err := c.Expand(ctx, kernel.ExpandRequest{Ref: kernel.ObjectRef{NativeID: "doc"}}); err != nil || out.SessionID != "expand" {
		t.Fatalf("expand=%+v err=%v", out, err)
	}
	if out, err := c.Resolve(ctx, kernel.ResolveRequest{Text: "person"}); err != nil || len(out) != 1 || out[0].NativeID != "person" {
		t.Fatalf("resolve=%+v err=%v", out, err)
	}
	if _, err := c.Capabilities(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Capabilities(ctx, true); err != nil {
		t.Fatal(err)
	}
	if out, err := c.Session(ctx, "session-1"); err != nil || out.ID != "session-1" {
		t.Fatalf("session=%+v err=%v", out, err)
	}
	if out, err := c.Research(ctx, planner.UserRequest{Query: "q"}); err != nil || out.Planner != "rules" || out.Summary != "summary" {
		t.Fatalf("research=%+v err=%v", out, err)
	}
	if result, events, err := c.ExecutePlan(ctx, kernel.RetrievalPlan{}); err != nil || result.SessionID != "plan" || len(events) != 1 {
		t.Fatalf("execute result=%+v events=%+v err=%v", result, events, err)
	}
	if len(seen) != len(responses) {
		t.Fatalf("seen routes=%v", seen)
	}
	if (&Client{}).httpClient() != http.DefaultClient {
		t.Fatal("nil HTTP client did not fall back to http.DefaultClient")
	}
}

func TestClientTransportAndResponseErrors(t *testing.T) {
	ctx := context.Background()
	if err := (&Client{}).do(ctx, http.MethodGet, "/", nil, nil); err == nil || !strings.Contains(err.Error(), "base URL") {
		t.Fatalf("empty base URL error=%v", err)
	}
	if err := (&Client{BaseURL: "://"}).do(ctx, http.MethodGet, "/", nil, nil); err == nil {
		t.Fatal("invalid request URL accepted")
	}
	if err := (&Client{BaseURL: "http://example.test"}).do(ctx, http.MethodPost, "/", func() {}, nil); err == nil {
		t.Fatal("unencodable request body accepted")
	}

	transportErr := errors.New("transport failed")
	c := &Client{BaseURL: "http://example.test", HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, transportErr
	})}}
	if err := c.do(ctx, http.MethodGet, "/", nil, nil); !errors.Is(err, transportErr) {
		t.Fatalf("transport error=%v", err)
	}

	c.HTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: readErrorBody{}}, nil
	})
	if err := c.do(ctx, http.MethodGet, "/", nil, nil); err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("read error=%v", err)
	}

	tests := []struct {
		name    string
		status  int
		payload string
		out     any
		check   func(error) bool
	}{
		{"structured error", http.StatusForbidden, `{"error":{"type":"missing_scope","message":"denied"}}`, nil, func(err error) bool {
			var detail *kernel.ErrorDetail
			return errors.As(err, &detail) && detail.Type == kernel.ErrMissingScope
		}},
		{"plain error", http.StatusBadGateway, ` upstream failed `, nil, func(err error) bool { return strings.Contains(err.Error(), "HTTP 502: upstream failed") }},
		{"invalid JSON", http.StatusOK, `{`, &kernel.SearchSnapshot{}, func(err error) bool { return strings.Contains(err.Error(), "decode sfs response") }},
		{"empty body", http.StatusNoContent, ``, &kernel.SearchSnapshot{}, func(err error) bool { return err == nil }},
		{"nil output", http.StatusOK, `{"ignored":true}`, nil, func(err error) bool { return err == nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c.HTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(test.status, test.payload), nil
			})
			if err := c.do(ctx, http.MethodGet, "/", nil, test.out); !test.check(err) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(body))}
}

type readErrorBody struct{}

func (readErrorBody) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (readErrorBody) Close() error             { return nil }

type staticAnswerer struct{}

func (staticAnswerer) Name() string { return "static-test" }

func (staticAnswerer) Answer(context.Context, ai.AnswerRequest) (ai.Answer, error) {
	return ai.Answer{Text: "基于测试证据的回答"}, nil
}
