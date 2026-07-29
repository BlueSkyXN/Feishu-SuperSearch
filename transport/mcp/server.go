package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	appcore "github.com/BlueSkyXN/Feishu-SuperSearch/internal/app"
	askcore "github.com/BlueSkyXN/Feishu-SuperSearch/internal/ask"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/research"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
)

const (
	ServerName      = "super-feishu-search"
	ProtocolVersion = "2025-11-25"
)

type Server struct {
	App     *appcore.App
	Version string
}

type Option func(*Server)

func WithVersion(version string) Option {
	return func(server *Server) {
		server.Version = strings.TrimSpace(version)
	}
}

func New(a *appcore.App, options ...Option) *Server {
	server := &Server{App: a, Version: "dev"}
	for _, option := range options {
		option(server)
	}
	return server
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}
type tool struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
}
type toolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	if s.App == nil {
		return fmt.Errorf("mcp app is nil")
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = enc.Encode(response{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error", Data: err.Error()}})
			continue
		}
		// Notifications intentionally have no response.
		notification := len(req.ID) == 0 || string(req.ID) == "null"
		result, rerr := s.handle(ctx, req)
		if notification {
			continue
		}
		resp := response{JSONRPC: "2.0", ID: req.ID, Result: result}
		if rerr != nil {
			resp.Result = nil
			resp.Error = rerr
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func (s *Server) handle(ctx context.Context, req request) (any, *rpcError) {
	if req.JSONRPC != "2.0" {
		return nil, &rpcError{Code: -32600, Message: "invalid request: jsonrpc must be 2.0"}
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &p); err != nil {
				return nil, &rpcError{Code: -32602, Message: "invalid initialize params"}
			}
		}
		// MCP version negotiation requires the server to return a version it
		// actually supports. Never echo an arbitrary client value.
		return map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]any{"name": ServerName, "version": s.Version}, "instructions": "Use feishu_search for broad retrieval, then feishu_fetch for selected objects. Use feishu_research for the bounded search-and-fetch workflow."}, nil
	case "server/discover":
		return map[string]any{"protocolVersion": ProtocolVersion, "serverInfo": map[string]any{"name": ServerName, "version": s.Version}, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "instructions": "Search, query, fetch, expand and inspect retrieval sessions."}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.tools()}, nil
	case "tools/call":
		var call toolCall
		if err := decodeStrict(req.Params, &call); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid tools/call params", Data: err.Error()}
		}
		value, err := s.callTool(ctx, call)
		if err != nil {
			detail := kernel.DetailFromError(err)
			return toolResult(map[string]any{"ok": false, "error": detail}, true), nil
		}
		return toolResult(value, false), nil
	case "notifications/initialized", "notifications/cancelled", "$/cancelRequest":
		return map[string]any{}, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
}

func toolResult(value any, isError bool) map[string]any {
	b, _ := json.MarshalIndent(value, "", "  ")
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "structuredContent": value, "isError": isError}
}

func (s *Server) callTool(ctx context.Context, call toolCall) (any, error) {
	switch call.Name {
	case "feishu_search":
		var req kernel.SearchRequest
		if err := decodeStrict(call.Arguments, &req); err != nil {
			return nil, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: err.Error()}
		}
		req.Identity.ScopeKey = ""
		req = s.App.ApplyDefaults(req)
		return s.App.Engine.Search(ctx, req)
	case "feishu_continue":
		var req kernel.ContinueRequest
		if err := decodeStrict(call.Arguments, &req); err != nil {
			return nil, err
		}
		req.Identity.ScopeKey = ""
		return s.App.Engine.Continue(ctx, req)
	case "feishu_query":
		var req kernel.QueryRequest
		if err := decodeStrict(call.Arguments, &req); err != nil {
			return nil, err
		}
		req.Identity.ScopeKey = ""
		return s.App.Engine.Query(ctx, req)
	case "feishu_fetch":
		var req kernel.FetchBatchRequest
		if err := decodeStrict(call.Arguments, &req); err != nil {
			return nil, err
		}
		req.Identity.ScopeKey = ""
		return s.App.Engine.Fetch(ctx, req)
	case "feishu_expand":
		var req kernel.ExpandRequest
		if err := decodeStrict(call.Arguments, &req); err != nil {
			return nil, err
		}
		req.Identity.ScopeKey = ""
		return s.App.Engine.Expand(ctx, req)
	case "feishu_resolve":
		var req kernel.ResolveRequest
		if err := decodeStrict(call.Arguments, &req); err != nil {
			return nil, err
		}
		req.Identity.ScopeKey = ""
		refs, err := s.App.Engine.Resolve(ctx, req)
		return map[string]any{"refs": refs}, err
	case "feishu_research":
		var req planner.UserRequest
		if err := decodeStrict(call.Arguments, &req); err != nil {
			return nil, err
		}
		req.Identity.ScopeKey = ""
		if !req.Deep {
			req.Deep = true
		}
		return (research.Service{Kernel: s.App.Engine, Planner: s.App.Planner, Reranker: s.App.Reranker}).Run(ctx, research.Request{UserRequest: req})
	case "feishu_ask":
		var req planner.UserRequest
		if err := decodeStrict(call.Arguments, &req); err != nil {
			return nil, err
		}
		req.Identity.ScopeKey = ""
		req.Deep = true
		service := askcore.Service{Research: research.Service{Kernel: s.App.Engine, Planner: s.App.Planner, Reranker: s.App.Reranker}, Answerer: s.App.Answerer}
		return service.Run(ctx, askcore.Request{UserRequest: req})
	case "feishu_get_session":
		var req struct {
			SessionID string          `json:"session_id"`
			Identity  kernel.Identity `json:"identity"`
		}
		if err := decodeStrict(call.Arguments, &req); err != nil {
			return nil, err
		}
		req.Identity.ScopeKey = ""
		return s.App.Engine.Session(ctx, req.SessionID, req.Identity)
	case "feishu_capabilities":
		var req kernel.CapabilityRequest
		if len(bytes.TrimSpace(call.Arguments)) > 0 {
			if err := decodeStrict(call.Arguments, &req); err != nil {
				return nil, err
			}
		}
		req.Identity.ScopeKey = ""
		return s.App.Engine.Capabilities(ctx, req)
	default:
		return nil, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: "unknown tool: " + call.Name}
	}
}

func decodeStrict(raw json.RawMessage, out any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "invalid tool arguments: " + err.Error()}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "tool arguments must contain exactly one JSON value"}
	}
	return nil
}

func obj(properties map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}
func (s *Server) tools() []tool {
	sourceEnum := []string{"docs", "messages", "chats", "people", "minutes", "meetings", "calendar", "tasks", "mail", "base", "sheets"}
	identity := obj(map[string]any{"profile": map[string]any{"type": "string"}, "mode": map[string]any{"type": "string", "enum": []string{"auto", "user", "bot"}}})
	sources := map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": sourceEnum}}
	return []tool{
		{Name: "feishu_search", Title: "飞书多源搜索", Description: "在启用的飞书业务域并发检索，返回统一候选、来源状态、游标和 session_id。先搜索再按需读取。", InputSchema: obj(map[string]any{"query": map[string]any{"type": "string", "minLength": 1}, "sources": sources, "source_queries": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}, "filters": map[string]any{"type": "object"}, "identity": identity, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200}, "limit_per_source": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}}, "query"), Annotations: map[string]any{"readOnlyHint": true}},
		{Name: "feishu_continue", Title: "继续搜索分页", Description: "使用同一 identity scope 下已有 session 的来源游标继续获取下一页。", InputSchema: obj(map[string]any{"session_id": map[string]any{"type": "string"}, "sources": sources, "max_additional_pages": map[string]any{"type": "integer", "minimum": 1, "maximum": 5}, "identity": identity}, "session_id"), Annotations: map[string]any{"readOnlyHint": true}},
		{Name: "feishu_query", Title: "结构化查询", Description: "对任务、Base、Sheets 等来源执行结构化或对象内查询。", InputSchema: obj(map[string]any{"source": map[string]any{"type": "string", "enum": sourceEnum}, "container": map[string]any{"type": "object"}, "filter": map[string]any{"type": "object"}, "sort": map[string]any{"type": "array"}, "limit": map[string]any{"type": "integer"}, "cursor": map[string]any{"type": "string"}, "identity": identity, "session_id": map[string]any{"type": "string"}}, "source"), Annotations: map[string]any{"readOnlyHint": true}},
		{Name: "feishu_fetch", Title: "读取搜索对象", Description: "读取候选对象的正文、上下文、原生摘要、结构或关系。ref 应直接来自 feishu_search。", InputSchema: obj(map[string]any{"session_id": map[string]any{"type": "string"}, "identity": identity, "items": map[string]any{"type": "array", "minItems": 1, "items": obj(map[string]any{"ref": map[string]any{"type": "object"}, "projection": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"head", "snippet", "summary", "structure", "content", "context", "relations", "attachments"}}}}, "ref", "projection")}}, "items"), Annotations: map[string]any{"readOnlyHint": true}},
		{Name: "feishu_expand", Title: "展开对象关系", Description: "沿 meeting→minute、message→document 等已支持关系发现关联对象，最大深度 2。", InputSchema: obj(map[string]any{"session_id": map[string]any{"type": "string"}, "identity": identity, "ref": map[string]any{"type": "object"}, "relations": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "max_depth": map[string]any{"type": "integer", "minimum": 1, "maximum": 2}}, "ref"), Annotations: map[string]any{"readOnlyHint": true}},
		{Name: "feishu_resolve", Title: "解析名称或链接", Description: "把人员名称、URL 或别名解析成稳定 ObjectRef。", InputSchema: obj(map[string]any{"source": map[string]any{"type": "string", "enum": sourceEnum}, "text": map[string]any{"type": "string"}, "kind": map[string]any{"type": "string"}, "identity": identity, "limit": map[string]any{"type": "integer"}}, "text"), Annotations: map[string]any{"readOnlyHint": true}},
		{Name: "feishu_research", Title: "有界深度检索", Description: "使用当前配置 Planner 执行 Search→Fetch→Evidence 的有界流程，返回候选包、已读对象、证据包和确定性摘要；可再交给 LLM 综合。", InputSchema: obj(map[string]any{"query": map[string]any{"type": "string"}, "sources": sources, "filters": map[string]any{"type": "object"}, "identity": identity, "limit": map[string]any{"type": "integer"}, "deep": map[string]any{"type": "boolean"}, "fetch_top_k": map[string]any{"type": "integer", "minimum": 1, "maximum": 20}}, "query"), Annotations: map[string]any{"readOnlyHint": true}},
		{Name: "feishu_ask", Title: "基于证据问答", Description: "执行有界检索与读取，再由启用的 AI Answerer 仅依据 Evidence Pack 生成带引用答案；未启用 AI 时返回 unsupported。", InputSchema: obj(map[string]any{"query": map[string]any{"type": "string", "minLength": 1}, "sources": sources, "filters": map[string]any{"type": "object"}, "identity": identity, "limit": map[string]any{"type": "integer"}, "fetch_top_k": map[string]any{"type": "integer", "minimum": 1, "maximum": 20}}, "query"), Annotations: map[string]any{"readOnlyHint": true}},
		{Name: "feishu_get_session", Title: "读取检索会话", Description: "返回同一 identity scope 下的候选、Artifact、关系、来源状态、游标和事件。", InputSchema: obj(map[string]any{"session_id": map[string]any{"type": "string"}, "identity": identity}, "session_id"), Annotations: map[string]any{"readOnlyHint": true}},
		{Name: "feishu_capabilities", Title: "检索能力", Description: "查看当前 Provider、操作、身份和 Projection 能力；probe=true 会实际探测后端。", InputSchema: obj(map[string]any{"identity": identity, "probe": map[string]any{"type": "boolean"}}), Annotations: map[string]any{"readOnlyHint": true}},
	}
}
