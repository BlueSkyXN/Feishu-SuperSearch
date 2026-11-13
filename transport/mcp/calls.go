package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// responseWriter serializes responses from concurrent tool calls and keeps the
// first write error, after which nothing more is written.
type responseWriter struct {
	mu  sync.Mutex
	enc *json.Encoder
	err error
}

func newResponseWriter(out io.Writer) *responseWriter {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return &responseWriter{enc: enc}
}

func (w *responseWriter) write(resp response) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err == nil {
		w.err = w.enc.Encode(resp)
	}
}

func (w *responseWriter) failed() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// callTracker maps the JSON-RPC id of each running tools/call to its cancel
// function.
type callTracker struct {
	mu    sync.Mutex
	calls map[string]*runningCall
}

type runningCall struct {
	cancel   func()
	byClient atomic.Bool
}

func (c *runningCall) cancelledByClient() bool { return c.byClient.Load() }

func (t *callTracker) start(key string, cancel func()) *runningCall {
	call := &runningCall{cancel: cancel}
	t.mu.Lock()
	t.calls[key] = call
	t.mu.Unlock()
	return call
}

func (t *callTracker) finish(key string, call *runningCall) {
	t.mu.Lock()
	if t.calls[key] == call {
		delete(t.calls, key)
	}
	t.mu.Unlock()
}

func (t *callTracker) cancel(key string) {
	t.mu.Lock()
	call := t.calls[key]
	t.mu.Unlock()
	if call != nil {
		call.byClient.Store(true)
		call.cancel()
	}
}

// cancelledRequestKey returns the id named by a notifications/cancelled
// (MCP, params.requestId) or $/cancelRequest (LSP style, params.id) message.
func cancelledRequestKey(req request) (string, bool) {
	var params struct {
		RequestID json.RawMessage `json:"requestId"`
		ID        json.RawMessage `json:"id"`
	}
	switch req.Method {
	case "notifications/cancelled", "$/cancelRequest":
	default:
		return "", false
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return "", false
	}
	id := params.RequestID
	if req.Method == "$/cancelRequest" {
		id = params.ID
	}
	if len(bytes.TrimSpace(id)) == 0 {
		return "", false
	}
	return requestKey(id), true
}

// requestKey normalizes a JSON-RPC id so that the same number or string
// written with different whitespace maps to one key.
func requestKey(id json.RawMessage) string {
	decoder := json.NewDecoder(bytes.NewReader(id))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return string(id)
	}
	return fmt.Sprintf("%T:%v", value, value)
}
