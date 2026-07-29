package httpapi

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	appcore "github.com/BlueSkyXN/Feishu-SuperSearch/internal/app"
	askcore "github.com/BlueSkyXN/Feishu-SuperSearch/internal/ask"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/planvalidate"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/research"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
)

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	App             *appcore.App
	MaxRequestBytes int64
}

type workflowOutcome struct {
	Value any
	Err   error
}

func New(a *appcore.App) *Server {
	return &Server{App: a, MaxRequestBytes: 4 << 20}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.health)
	mux.HandleFunc("GET /v1/capabilities", s.capabilities)
	mux.HandleFunc("POST /v1/search", s.search)
	mux.HandleFunc("POST /v1/search/continue", s.continueSearch)
	mux.HandleFunc("POST /v1/query", s.query)
	mux.HandleFunc("POST /v1/fetch", s.fetch)
	mux.HandleFunc("POST /v1/expand", s.expand)
	mux.HandleFunc("POST /v1/resolve", s.resolve)
	mux.HandleFunc("POST /v1/plans:execute", s.executePlan)
	mux.HandleFunc("POST /v1/research", s.runResearch)
	mux.HandleFunc("POST /v1/ask", s.ask)
	mux.HandleFunc("GET /v1/sessions", s.sessions)
	mux.HandleFunc("GET /v1/sessions/{id}", s.session)
	mux.HandleFunc("DELETE /v1/sessions/{id}", s.deleteSession)

	sub, _ := fs.Sub(staticFiles, "static")
	fileServer := http.FileServer(http.FS(sub))
	mux.Handle("GET /", fileServer)
	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if !isLoopbackHost(r.Host) {
			writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": &kernel.ErrorDetail{Type: kernel.ErrIdentityRequired, Message: "request host is not a loopback address"}})
			return
		}
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			if !isSameOrigin(origin, r.Host) {
				writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": &kernel.ErrorDetail{Type: kernel.ErrIdentityRequired, Message: "browser origin must match the local server origin"}})
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				writeError(w, &kernel.ErrorDetail{Type: kernel.ErrInternal, Message: "internal server error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHost(hostport string) bool {
	host := strings.TrimSpace(hostport)
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	} else if strings.Contains(host, ":") {
		return false
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isSameOrigin(origin, requestHost string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	return isLoopbackHost(parsed.Host) && strings.EqualFold(parsed.Host, requestHost)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	caps, err := s.App.Engine.Capabilities(r.Context(), kernel.CapabilityRequest{})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "backend": s.App.Backend, "providers": len(caps.Providers), "ai": map[string]any{"enabled": s.App.Config.AI.Enabled, "planner": s.App.Planner != nil && s.App.Planner.Name() == "ai", "rerank": s.App.Reranker != nil, "answer": s.App.Answerer != nil}, "storage": s.App.Config.Storage.Type, "time": time.Now().UTC()})
}

func (s *Server) capabilities(w http.ResponseWriter, r *http.Request) {
	probe := r.URL.Query().Get("probe") == "1" || strings.EqualFold(r.URL.Query().Get("probe"), "true")
	snap, err := s.App.Engine.Capabilities(r.Context(), kernel.CapabilityRequest{Probe: probe})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	var req kernel.SearchRequest
	if !s.decode(w, r, &req) {
		return
	}
	req.Identity.ScopeKey = ""
	req = s.App.ApplyDefaults(req)
	snap, err := s.App.Engine.Search(r.Context(), req)
	if err != nil && len(snap.Candidates) == 0 {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}
func (s *Server) continueSearch(w http.ResponseWriter, r *http.Request) {
	var req kernel.ContinueRequest
	if !s.decode(w, r, &req) {
		return
	}
	req.Identity.ScopeKey = ""
	snap, err := s.App.Engine.Continue(r.Context(), req)
	if err != nil && len(snap.Candidates) == 0 {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}
func (s *Server) query(w http.ResponseWriter, r *http.Request) {
	var req kernel.QueryRequest
	if !s.decode(w, r, &req) {
		return
	}
	req.Identity.ScopeKey = ""
	snap, err := s.App.Engine.Query(r.Context(), req)
	if err != nil && len(snap.Candidates) == 0 {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}
func (s *Server) fetch(w http.ResponseWriter, r *http.Request) {
	var req kernel.FetchBatchRequest
	if !s.decode(w, r, &req) {
		return
	}
	req.Identity.ScopeKey = ""
	batch, err := s.App.Engine.Fetch(r.Context(), req)
	if err != nil && len(batch.Items) == 0 {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, batch)
}
func (s *Server) expand(w http.ResponseWriter, r *http.Request) {
	var req kernel.ExpandRequest
	if !s.decode(w, r, &req) {
		return
	}
	req.Identity.ScopeKey = ""
	batch, err := s.App.Engine.Expand(r.Context(), req)
	if err != nil && len(batch.Relations) == 0 {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, batch)
}
func (s *Server) resolve(w http.ResponseWriter, r *http.Request) {
	var req kernel.ResolveRequest
	if !s.decode(w, r, &req) {
		return
	}
	req.Identity.ScopeKey = ""
	refs, err := s.App.Engine.Resolve(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"refs": refs})
}

func (s *Server) executePlan(w http.ResponseWriter, r *http.Request) {
	var plan kernel.RetrievalPlan
	if !s.decodePlan(w, r, &plan) {
		return
	}
	plan.Identity.ScopeKey = ""
	events, results, err := s.App.Engine.Execute(r.Context(), plan)
	if err != nil {
		writeError(w, err)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		s.streamPlan(w, r, events, results)
		return
	}
	collected := make([]kernel.RetrievalEvent, 0, 64)
	var result kernel.PlanResult
	eventCh, resultCh := events, results
	for eventCh != nil || resultCh != nil {
		select {
		case ev, ok := <-eventCh:
			if !ok {
				eventCh = nil
				continue
			}
			collected = append(collected, ev)
		case res, ok := <-resultCh:
			if !ok {
				resultCh = nil
				continue
			}
			result = res
		case <-r.Context().Done():
			writeError(w, r.Context().Err())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result, "events": collected})
}

func (s *Server) streamPlan(w http.ResponseWriter, r *http.Request, events <-chan kernel.RetrievalEvent, results <-chan kernel.PlanResult) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	enc := json.NewEncoder(w)
	send := func(event string, value any) bool {
		if _, err := fmt.Fprintf(w, "event: %s\ndata: ", event); err != nil {
			return false
		}
		if err := enc.Encode(value); err != nil {
			return false
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	eventCh, resultCh := events, results
	var finalResult *kernel.PlanResult
	for eventCh != nil || resultCh != nil {
		select {
		case ev, ok := <-eventCh:
			if !ok {
				eventCh = nil
				continue
			}
			if !send("retrieval", ev) {
				return
			}
		case result, ok := <-resultCh:
			if !ok {
				resultCh = nil
				continue
			}
			resultCopy := result
			finalResult = &resultCopy
		case <-r.Context().Done():
			return
		}
	}
	if finalResult == nil {
		_ = send("error", map[string]any{"ok": false, "error": &kernel.ErrorDetail{Type: kernel.ErrInternal, Message: "plan executor closed without a result"}})
		return
	}
	_ = send("result", *finalResult)
}

func (s *Server) runResearch(w http.ResponseWriter, r *http.Request) {
	var req planner.UserRequest
	if !s.decode(w, r, &req) {
		return
	}
	req.Identity.ScopeKey = ""
	if strings.TrimSpace(req.Query) == "" {
		writeError(w, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "query is required"})
		return
	}
	if !req.Deep {
		req.Deep = true
	}
	service := research.Service{Kernel: s.App.Engine, Planner: s.App.Planner, Reranker: s.App.Reranker}
	if acceptsEventStream(r) {
		if !supportsEventStream(w) {
			writeError(w, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: "streaming unsupported"})
			return
		}
		events := make(chan research.Event)
		outcomes := make(chan workflowOutcome, 1)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		go func() {
			defer close(events)
			defer close(outcomes)
			defer func() {
				if recover() != nil {
					outcomes <- workflowOutcome{Err: &kernel.ErrorDetail{Type: kernel.ErrInternal, Message: "research workflow panicked"}}
				}
			}()
			result, err := service.RunWithObserver(ctx, research.Request{UserRequest: req}, func(event research.Event) {
				select {
				case events <- event:
				case <-ctx.Done():
				}
			})
			outcomes <- workflowOutcome{Value: result, Err: err}
		}()
		s.streamWorkflow(w, r, events, outcomes)
		return
	}
	result, err := service.Run(r.Context(), research.Request{UserRequest: req})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) ask(w http.ResponseWriter, r *http.Request) {
	var req planner.UserRequest
	if !s.decode(w, r, &req) {
		return
	}
	req.Identity.ScopeKey = ""
	if strings.TrimSpace(req.Query) == "" {
		writeError(w, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "query is required"})
		return
	}
	req.Deep = true
	service := askcore.Service{Research: research.Service{Kernel: s.App.Engine, Planner: s.App.Planner, Reranker: s.App.Reranker}, Answerer: s.App.Answerer}
	if acceptsEventStream(r) {
		if s.App.Answerer == nil {
			writeError(w, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: "AI answer is disabled; enable ai.answer or use research"})
			return
		}
		if !supportsEventStream(w) {
			writeError(w, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: "streaming unsupported"})
			return
		}
		events := make(chan research.Event)
		outcomes := make(chan workflowOutcome, 1)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		go func() {
			defer close(events)
			defer close(outcomes)
			defer func() {
				if recover() != nil {
					outcomes <- workflowOutcome{Err: &kernel.ErrorDetail{Type: kernel.ErrInternal, Message: "ask workflow panicked"}}
				}
			}()
			result, err := service.RunWithObserver(ctx, askcore.Request{UserRequest: req}, func(event research.Event) {
				select {
				case events <- event:
				case <-ctx.Done():
				}
			})
			outcomes <- workflowOutcome{Value: result, Err: err}
		}()
		s.streamWorkflow(w, r, events, outcomes)
		return
	}
	result, err := service.Run(r.Context(), askcore.Request{UserRequest: req})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func acceptsEventStream(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream")
}

func supportsEventStream(w http.ResponseWriter) bool {
	_, ok := w.(http.Flusher)
	return ok
}

func (s *Server) streamWorkflow(w http.ResponseWriter, r *http.Request, events <-chan research.Event, outcomes <-chan workflowOutcome) {
	flusher := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	enc := json.NewEncoder(w)
	sequence := 0
	send := func(event string, value any) bool {
		sequence++
		if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: ", sequence, event); err != nil {
			return false
		}
		if err := enc.Encode(value); err != nil {
			return false
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	eventCh, outcomeCh := events, outcomes
	for eventCh != nil || outcomeCh != nil {
		select {
		case event, ok := <-eventCh:
			if !ok {
				eventCh = nil
				continue
			}
			if !send(string(event.Type), event) {
				return
			}
		case outcome, ok := <-outcomeCh:
			if !ok {
				outcomeCh = nil
				continue
			}
			if outcome.Err != nil {
				if !send("error", map[string]any{"ok": false, "error": kernel.DetailFromError(outcome.Err)}) {
					return
				}
				continue
			}
			if !send("result", outcome.Value) {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	identity, err := identityFromQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	sessions, err := s.App.Engine.Sessions(r.Context(), identity)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	identity, err := identityFromQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	snap, err := s.App.Engine.Session(r.Context(), id, identity)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}
func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	identity, err := identityFromQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.App.Engine.DeleteSession(r.Context(), r.PathValue("id"), identity); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func identityFromQuery(r *http.Request) (kernel.Identity, error) {
	identity := kernel.Identity{Profile: strings.TrimSpace(r.URL.Query().Get("profile")), Mode: kernel.IdentityMode(strings.TrimSpace(r.URL.Query().Get("mode")))}
	switch identity.Mode {
	case "", kernel.IdentityAuto, kernel.IdentityUser, kernel.IdentityBot:
		return identity.Normalized(), nil
	default:
		return kernel.Identity{}, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "identity mode must be auto, user, or bot"}
	}
}

func (s *Server) decode(w http.ResponseWriter, r *http.Request, out any) bool {
	max := s.MaxRequestBytes
	if max <= 0 {
		max = 4 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		writeError(w, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "invalid JSON request: " + err.Error()})
		return false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		writeError(w, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "request must contain exactly one JSON value"})
		return false
	}
	return true
}

func (s *Server) decodePlan(w http.ResponseWriter, r *http.Request, out *kernel.RetrievalPlan) bool {
	max := s.MaxRequestBytes
	if max <= 0 {
		max = 4 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "read plan request: " + err.Error()})
		return false
	}
	if err := planvalidate.ValidateJSON(raw); err != nil {
		writeError(w, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "invalid plan request: " + err.Error()})
		return false
	}
	if err := json.Unmarshal(raw, out); err != nil {
		writeError(w, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "invalid plan request: " + err.Error()})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, err error) {
	detail := kernel.DetailFromError(err)
	status := http.StatusInternalServerError
	switch detail.Type {
	case kernel.ErrInvalidRequest:
		status = http.StatusBadRequest
	case kernel.ErrNotFound:
		status = http.StatusNotFound
	case kernel.ErrUnsupported:
		status = http.StatusNotImplemented
	case kernel.ErrMissingScope, kernel.ErrIdentityRequired:
		status = http.StatusForbidden
	case kernel.ErrRateLimited:
		status = http.StatusTooManyRequests
	case kernel.ErrDeadlineExceeded:
		status = http.StatusGatewayTimeout
	case kernel.ErrBudgetExhausted:
		status = http.StatusUnprocessableEntity
	case kernel.ErrCancelled:
		status = 499
	}
	// errors.Is preserves context cancellation/deadline when an adapter did not map it.
	if errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusGatewayTimeout
	}
	if errors.Is(err, context.Canceled) {
		status = 499
	}
	writeJSON(w, status, map[string]any{"ok": false, "error": detail})
}
