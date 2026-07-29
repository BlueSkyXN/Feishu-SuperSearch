package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/ai"
	appcore "github.com/BlueSkyXN/Feishu-SuperSearch/internal/app"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/engine"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/session"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
	rulesplanner "github.com/BlueSkyXN/Feishu-SuperSearch/planner/rules"
	"github.com/BlueSkyXN/Feishu-SuperSearch/provider"
)

func testApp(t *testing.T) *appcore.App {
	t.Helper()
	cfg := appcore.DefaultConfig()
	cfg.Backend = "mock"
	cfg.Storage = appcore.StorageConfig{Type: "file", Path: t.TempDir(), TTL: "45m"}
	a, err := appcore.Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}
func postJSON(t *testing.T, h http.Handler, path string, v any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(v)
	r := loopbackRequest(http.MethodPost, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func loopbackRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.Host = "127.0.0.1:3765"
	return request
}
func TestSearchFetchAndResearchAPI(t *testing.T) {
	a := testApp(t)
	h := New(a).Handler()
	health := httptest.NewRecorder()
	h.ServeHTTP(health, loopbackRequest(http.MethodGet, "/v1/health", nil))
	if health.Code != 200 {
		t.Fatalf("health %d %s", health.Code, health.Body.String())
	}
	w := postJSON(t, h, "/v1/search", kernel.SearchRequest{Query: "A 项目 延期", Sources: []kernel.SourceID{kernel.SourceDocs, kernel.SourceMessages, kernel.SourceMinutes}, Limit: 10, Identity: kernel.Identity{Mode: kernel.IdentityAuto}})
	if w.Code != 200 {
		t.Fatalf("search %d %s", w.Code, w.Body.String())
	}
	var snap kernel.SearchSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Candidates) == 0 {
		t.Fatal("no candidates")
	}
	f := postJSON(t, h, "/v1/fetch", kernel.FetchBatchRequest{SessionID: snap.SessionID, Identity: kernel.Identity{Mode: kernel.IdentityAuto}, Items: []kernel.FetchRequest{{Ref: snap.Candidates[0].Ref, Projection: kernel.ProjectionContent | kernel.ProjectionSummary}}})
	if f.Code != 200 {
		t.Fatalf("fetch %d %s", f.Code, f.Body.String())
	}
	rr := postJSON(t, h, "/v1/research", map[string]any{"query": "A 项目 延期", "sources": []string{"docs", "messages", "minutes"}, "deep": true, "fetch_top_k": 3})
	if rr.Code != 200 {
		t.Fatalf("research %d %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["evidence_pack"] == nil {
		t.Fatalf("missing evidence: %v", body)
	}
}

func TestSessionEndpointsEnforceIdentityScope(t *testing.T) {
	a := testApp(t)
	h := New(a).Handler()
	search := postJSON(t, h, "/v1/search", kernel.SearchRequest{Query: "A 项目", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: kernel.Identity{Profile: "one", Mode: kernel.IdentityUser}})
	if search.Code != http.StatusOK {
		t.Fatalf("search=%d %s", search.Code, search.Body.String())
	}
	var snapshot kernel.SearchSnapshot
	if err := json.Unmarshal(search.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	getOther := httptest.NewRecorder()
	h.ServeHTTP(getOther, loopbackRequest(http.MethodGet, "/v1/sessions/"+snapshot.SessionID+"?profile=two&mode=user", nil))
	if getOther.Code != http.StatusForbidden {
		t.Fatalf("cross-scope get=%d %s", getOther.Code, getOther.Body.String())
	}
	listOther := httptest.NewRecorder()
	h.ServeHTTP(listOther, loopbackRequest(http.MethodGet, "/v1/sessions?profile=two&mode=user", nil))
	if listOther.Code != http.StatusOK || !strings.Contains(listOther.Body.String(), `"sessions":[]`) {
		t.Fatalf("cross-scope list=%d %s", listOther.Code, listOther.Body.String())
	}
	continued := postJSON(t, h, "/v1/search/continue", kernel.ContinueRequest{SessionID: snapshot.SessionID, Identity: kernel.Identity{Profile: "two", Mode: kernel.IdentityUser}})
	if continued.Code != http.StatusForbidden {
		t.Fatalf("cross-scope continue=%d %s", continued.Code, continued.Body.String())
	}
	deleteOther := httptest.NewRecorder()
	h.ServeHTTP(deleteOther, loopbackRequest(http.MethodDelete, "/v1/sessions/"+snapshot.SessionID+"?profile=two&mode=user", nil))
	if deleteOther.Code != http.StatusForbidden {
		t.Fatalf("cross-scope delete=%d %s", deleteOther.Code, deleteOther.Body.String())
	}
	getOwner := httptest.NewRecorder()
	h.ServeHTTP(getOwner, loopbackRequest(http.MethodGet, "/v1/sessions/"+snapshot.SessionID+"?profile=one&mode=user", nil))
	if getOwner.Code != http.StatusOK {
		t.Fatalf("owner get=%d %s", getOwner.Code, getOwner.Body.String())
	}
}
func TestStaticUI(t *testing.T) {
	a := testApp(t)
	w := httptest.NewRecorder()
	New(a).Handler().ServeHTTP(w, loopbackRequest(http.MethodGet, "/", nil))
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("SuperFeishuSearch")) || !bytes.Contains(w.Body.Bytes(), []byte("/v1/capabilities")) {
		t.Fatalf("ui %d", w.Code)
	}
}

func TestHTTPRequiresLoopbackHostAndSameBrowserOrigin(t *testing.T) {
	a := testApp(t)
	h := New(a).Handler()

	blockedRequest := loopbackRequest(http.MethodGet, "/v1/health", nil)
	blockedRequest.Header.Set("Origin", "https://untrusted.example")
	blocked := httptest.NewRecorder()
	h.ServeHTTP(blocked, blockedRequest)
	if blocked.Code != http.StatusForbidden || blocked.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("blocked origin status=%d headers=%v", blocked.Code, blocked.Header())
	}

	crossOriginRequest := loopbackRequest(http.MethodGet, "/v1/health", nil)
	crossOriginRequest.Header.Set("Origin", "http://127.0.0.1:5173")
	crossOrigin := httptest.NewRecorder()
	h.ServeHTTP(crossOrigin, crossOriginRequest)
	if crossOrigin.Code != http.StatusForbidden || crossOrigin.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("cross origin status=%d headers=%v", crossOrigin.Code, crossOrigin.Header())
	}

	allowedRequest := loopbackRequest(http.MethodGet, "/v1/health", nil)
	allowedRequest.Header.Set("Origin", "http://127.0.0.1:3765")
	allowed := httptest.NewRecorder()
	h.ServeHTTP(allowed, allowedRequest)
	if allowed.Code != http.StatusOK || allowed.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:3765" {
		t.Fatalf("allowed origin status=%d headers=%v", allowed.Code, allowed.Header())
	}

	badHostRequest := loopbackRequest(http.MethodGet, "/v1/health", nil)
	badHostRequest.Host = "attacker.example"
	badHost := httptest.NewRecorder()
	h.ServeHTTP(badHost, badHostRequest)
	if badHost.Code != http.StatusForbidden {
		t.Fatalf("bad Host status=%d body=%s", badHost.Code, badHost.Body.String())
	}
}

func TestHTTPPanicResponseDoesNotExposeRecoveredValue(t *testing.T) {
	const sentinel = "sentinel-secret /private/path"
	handler := New(nil).middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(sentinel)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, loopbackRequest(http.MethodGet, "/panic", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), sentinel) || strings.Contains(recorder.Body.String(), "panic:") || !strings.Contains(recorder.Body.String(), `"type":"internal"`) {
		t.Fatalf("panic value leaked or error missing: %s", recorder.Body.String())
	}
}

func TestHealthExposesStorageAndAICapabilities(t *testing.T) {
	a := testApp(t)
	w := httptest.NewRecorder()
	New(a).Handler().ServeHTTP(w, loopbackRequest(http.MethodGet, "/v1/health", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("health %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Storage string `json:"storage"`
		AI      struct {
			Enabled bool `json:"enabled"`
			Planner bool `json:"planner"`
			Rerank  bool `json:"rerank"`
			Answer  bool `json:"answer"`
		} `json:"ai"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Storage != "file" {
		t.Fatalf("storage=%q want file", body.Storage)
	}
	if body.AI.Enabled || body.AI.Planner || body.AI.Rerank || body.AI.Answer {
		t.Fatalf("AI capabilities should be disabled: %+v", body.AI)
	}
}

func TestAskDisabledReturnsUnsupported(t *testing.T) {
	a := testApp(t)
	body := bytes.NewBufferString(`{"query":"A 项目为什么延期"}`)
	req := loopbackRequest(http.MethodPost, "/v1/ask", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()
	New(a).Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("ask status=%d body=%s", w.Code, w.Body.String())
	}
	var envelope struct {
		Error *kernel.ErrorDetail `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error == nil || envelope.Error.Type != kernel.ErrUnsupported || !strings.Contains(envelope.Error.Message, "AI answer is disabled") {
		t.Fatalf("unexpected ask error: %+v", envelope.Error)
	}
}

type failingPlanner struct{}

func (failingPlanner) Name() string { return "failing" }
func (failingPlanner) Plan(context.Context, planner.UserRequest, kernel.CapabilitySnapshot) (kernel.RetrievalPlan, error) {
	return kernel.RetrievalPlan{}, errors.New("planner unavailable")
}

type streamAnswerer struct{}

func (streamAnswerer) Name() string { return "stream-test" }
func (streamAnswerer) Answer(_ context.Context, request ai.AnswerRequest) (ai.Answer, error) {
	return ai.Answer{Text: "基于证据回答", Partial: len(request.EvidencePack.Evidence) == 0}, nil
}

func TestResearchAndAskSSE(t *testing.T) {
	a := testApp(t)
	a.Answerer = streamAnswerer{}
	h := New(a).Handler()

	for _, test := range []struct {
		path       string
		wantPhases []string
	}{
		{path: "/v1/research", wantPhases: []string{`"phase":"capabilities"`, `"phase":"planning"`, `"phase":"research_complete"`}},
		{path: "/v1/ask", wantPhases: []string{`"phase":"capabilities"`, `"phase":"answering"`, `"phase":"complete"`}},
	} {
		body := bytes.NewBufferString(`{"query":"A 项目为什么延期","sources":["docs","messages"],"fetch_top_k":2}`)
		req := loopbackRequest(http.MethodPost, test.path, body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
			t.Fatalf("%s status=%d content-type=%q body=%s", test.path, w.Code, w.Header().Get("Content-Type"), w.Body.String())
		}
		stream := w.Body.String()
		for _, phase := range test.wantPhases {
			if !strings.Contains(stream, phase) {
				t.Fatalf("%s missing %s in %s", test.path, phase, stream)
			}
		}
		if !strings.Contains(stream, "event: retrieval") || !strings.Contains(stream, "event: result") {
			t.Fatalf("%s incomplete SSE stream: %s", test.path, stream)
		}
		if strings.LastIndex(stream, "event: result") < strings.LastIndex(stream, test.wantPhases[len(test.wantPhases)-1]) {
			t.Fatalf("%s result arrived before final progress: %s", test.path, stream)
		}
	}
}

func TestResearchSSEUsesErrorAsTheOnlyTerminalEvent(t *testing.T) {
	a := testApp(t)
	a.Planner = failingPlanner{}
	req := loopbackRequest(http.MethodPost, "/v1/research", strings.NewReader(`{"query":"A 项目延期"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()
	New(a).Handler().ServeHTTP(w, req)
	stream := w.Body.String()
	if w.Code != http.StatusOK || strings.Count(stream, "event: error") != 1 || strings.Contains(stream, "event: result") {
		t.Fatalf("status=%d stream=%s", w.Code, stream)
	}
}

func TestExecutePlanSSEDrainsRetrievalBeforeUniqueResult(t *testing.T) {
	a := testApp(t)
	plan := `{"version":"retrieval-plan/v1","nodes":[{"id":"search","op":"search","request":{"query":"A 项目延期","sources":["docs"]}}],"output":["search"]}`
	req := loopbackRequest(http.MethodPost, "/v1/plans:execute", strings.NewReader(plan))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()
	New(a).Handler().ServeHTTP(w, req)
	stream := w.Body.String()
	if w.Code != http.StatusOK || strings.Count(stream, "event: result") != 1 || !strings.Contains(stream, "event: retrieval") {
		t.Fatalf("status=%d stream=%s", w.Code, stream)
	}
	if strings.LastIndex(stream, "event: retrieval") > strings.LastIndex(stream, "event: result") {
		t.Fatalf("retrieval arrived after result: %s", stream)
	}
}

func TestExecutePlanRejectsUnknownFieldBeforeProviderCall(t *testing.T) {
	searchProvider := &countingSearchProvider{}
	a := countingProviderApp(t, searchProvider)
	raw := `{"version":"retrieval-plan/v1","nodes":[{"id":"search","op":"search","request":{"query":"A 项目","sources":["docs"]}}],"unknown":true}`
	req := loopbackRequest(http.MethodPost, "/v1/plans:execute", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	New(a).Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("plan status=%d body=%s", w.Code, w.Body.String())
	}
	if calls := searchProvider.calls.Load(); calls != 0 {
		t.Fatalf("provider executed %d times for an invalid plan", calls)
	}
}

type countingSearchProvider struct {
	calls atomic.Int32
}

func (p *countingSearchProvider) Descriptor() kernel.ProviderDescriptor {
	return kernel.ProviderDescriptor{
		ID:               "counting.docs",
		Source:           kernel.SourceDocs,
		ObjectKinds:      []kernel.ObjectKind{kernel.KindDocument},
		Operations:       kernel.OperationSet{kernel.OpSearch: true},
		RequiredIdentity: []kernel.IdentityMode{kernel.IdentityAuto, kernel.IdentityUser, kernel.IdentityBot},
		Backend:          "test",
		Version:          "test/1",
	}
}

func (p *countingSearchProvider) Search(context.Context, kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	p.calls.Add(1)
	return kernel.CandidatePage{}, nil
}

func countingProviderApp(t *testing.T, searchProvider *countingSearchProvider) *appcore.App {
	t.Helper()
	registry := provider.NewRegistry()
	if err := registry.Register(searchProvider, true); err != nil {
		t.Fatal(err)
	}
	eng := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{GlobalConcurrency: 1, ProviderTimeout: time.Second, SessionTTL: time.Hour})
	t.Cleanup(func() { _ = eng.Close() })
	cfg := appcore.DefaultConfig()
	cfg.Backend = "mock"
	cfg.Storage = appcore.StorageConfig{Type: "memory", TTL: "45m"}
	return &appcore.App{Config: cfg, Registry: registry, Engine: eng, Planner: rulesplanner.Planner{}, Backend: "test"}
}
