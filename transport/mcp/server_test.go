package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	appcore "github.com/BlueSkyXN/Feishu-SuperSearch/internal/app"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestMCPInitializeListAndSearch(t *testing.T) {
	cfg := appcore.DefaultConfig()
	cfg.Backend = "mock"
	cfg.Storage = appcore.StorageConfig{Type: "file", Path: t.TempDir(), TTL: "45m"}
	a, err := appcore.Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"feishu_search","arguments":{"query":"A 项目 延期","sources":["docs","messages"]}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := New(a, WithVersion("1.0.1")).Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("responses=%d\n%s", len(lines), out.String())
	}
	var initialized map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &initialized); err != nil {
		t.Fatal(err)
	}
	serverInfo := initialized["result"].(map[string]any)["serverInfo"].(map[string]any)
	if serverInfo["version"] != "1.0.1" {
		t.Fatalf("server version=%v", serverInfo["version"])
	}
	var listed struct {
		Result struct {
			Tools []tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Result.Tools) != 10 {
		t.Fatalf("tool count=%d want 10", len(listed.Result.Tools))
	}
	foundAsk := false
	for _, listedTool := range listed.Result.Tools {
		if listedTool.Name == "feishu_ask" {
			foundAsk = true
			break
		}
	}
	if !foundAsk {
		t.Fatal("feishu_ask is missing from tools/list")
	}
	var search map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &search); err != nil {
		t.Fatal(err)
	}
	result := search["result"].(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	if structured["candidates"] == nil {
		t.Fatalf("missing candidates: %v", structured)
	}
}

func TestMCPInitializeNegotiatesSupportedProtocolVersion(t *testing.T) {
	tests := []struct {
		name   string
		params string
	}{
		{name: "supported", params: `{"protocolVersion":"` + ProtocolVersion + `"}`},
		{name: "unsupported", params: `{"protocolVersion":"definitely-unsupported"}`},
		{name: "missing", params: `{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, rpcErr := New(nil).handle(context.Background(), request{JSONRPC: "2.0", Method: "initialize", Params: json.RawMessage(test.params)})
			if rpcErr != nil {
				t.Fatalf("rpc error=%+v", rpcErr)
			}
			payload, ok := result.(map[string]any)
			if !ok || payload["protocolVersion"] != ProtocolVersion {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}

func TestMCPAskDisabledReturnsToolError(t *testing.T) {
	cfg := appcore.DefaultConfig()
	cfg.Backend = "mock"
	cfg.Storage = appcore.StorageConfig{Type: "memory", TTL: "45m"}
	a, err := appcore.Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"feishu_ask","arguments":{"query":"A 项目为什么延期"}}}` + "\n"
	var out bytes.Buffer
	if err := New(a).Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result struct {
			StructuredContent struct {
				OK    bool                `json:"ok"`
				Error *kernel.ErrorDetail `json:"error"`
			} `json:"structuredContent"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Result.IsError || response.Result.StructuredContent.OK || response.Result.StructuredContent.Error == nil || response.Result.StructuredContent.Error.Type != kernel.ErrUnsupported {
		t.Fatalf("unexpected ask response: %s", out.String())
	}
}

func TestMCPRejectsUnknownToolArguments(t *testing.T) {
	cfg := appcore.DefaultConfig()
	cfg.Backend = "mock"
	cfg.Storage = appcore.StorageConfig{Type: "memory", TTL: "45m"}
	a, err := appcore.Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"feishu_search","arguments":{"query":"A 项目","unknown":true}}}` + "\n"
	var out bytes.Buffer
	if err := New(a).Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"isError":true`) || !strings.Contains(out.String(), "unknown field") || !strings.Contains(out.String(), string(kernel.ErrInvalidRequest)) {
		t.Fatalf("unknown arguments accepted: %s", out.String())
	}
}
