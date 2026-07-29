package execprovider

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestExecutableProviderHealthSearchAndFetch(t *testing.T) {
	p, err := New(Config{
		Descriptor: kernel.ProviderDescriptor{
			ID:                  "exec.docs",
			Source:              kernel.SourceDocs,
			ObjectKinds:         []kernel.ObjectKind{kernel.KindDocument},
			Operations:          kernel.OperationSet{kernel.OpSearch: true, kernel.OpFetch: true},
			ReturnedProjection:  kernel.ProjectionHead | kernel.ProjectionSnippet,
			FetchableProjection: kernel.ProjectionContent,
		},
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestExecProviderHelperProcess", "--", "--provider-helper"},
		Timeout:        2 * time.Second,
		MaxStdoutBytes: 1 << 20,
		MaxStderrBytes: 1 << 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	version, err := p.Health(context.Background(), kernel.Identity{Mode: kernel.IdentityUser})
	if err != nil || version != "helper/1" {
		t.Fatalf("version=%q err=%v", version, err)
	}
	page, err := p.Search(context.Background(), kernel.ProviderSearchRequest{Query: "A", Identity: kernel.Identity{Mode: kernel.IdentityUser}, PageSize: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Candidates) != 1 || page.Candidates[0].Title != "A 项目" {
		t.Fatalf("page=%+v", page)
	}
	arts, err := p.Fetch(context.Background(), []kernel.ProviderFetchRequest{{Ref: page.Candidates[0].Ref, Projection: kernel.ProjectionContent}})
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 1 || len(arts[0].Chunks) != 1 || arts[0].Chunks[0].Text != "正文" {
		t.Fatalf("artifacts=%+v", arts)
	}
}

func TestExecProviderHelperProcess(t *testing.T) {
	helper := false
	for _, arg := range os.Args {
		if arg == "--provider-helper" {
			helper = true
		}
	}
	if !helper {
		return
	}
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		os.Exit(2)
	}
	switch os.Getenv("SFS_EXEC_PROVIDER_CASE") {
	case "oversize":
		_, _ = os.Stdout.WriteString(strings.Repeat("x", 4096))
		os.Exit(0)
	case "bad-envelope":
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID + 1, "result": map[string]any{}, "unknown": true})
		os.Exit(0)
	case "bad-page":
		ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: "test", Kind: kernel.KindDocument, NativeID: "d1", CanonicalID: kernel.BuildCanonicalID("test", kernel.KindDocument, "d1"), ProviderID: "wrong.docs", Source: kernel.SourceDocs}
		result := kernel.CandidatePage{Candidates: []kernel.Candidate{{Ref: ref, Source: kernel.SourceDocs, Kind: kernel.KindDocument, Title: "bad", NativeRank: 1, Projection: kernel.ProjectionHead}}, RawCount: 1, HasMore: true}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		os.Exit(0)
	}
	result := any(nil)
	switch req.Method {
	case "health":
		result = map[string]any{"version": "helper/1"}
	case "search":
		ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: "test", Kind: kernel.KindDocument, NativeID: "d1", CanonicalID: kernel.BuildCanonicalID("test", kernel.KindDocument, "d1"), ProviderID: "exec.docs", Source: kernel.SourceDocs}
		result = kernel.CandidatePage{Candidates: []kernel.Candidate{{Ref: ref, Source: kernel.SourceDocs, Kind: kernel.KindDocument, Title: "A 项目", NativeRank: 1, Projection: kernel.ProjectionHead | kernel.ProjectionSnippet, AvailableProjection: kernel.ProjectionContent}}, RawCount: 1}
	case "fetch":
		var items []kernel.ProviderFetchRequest
		_ = json.Unmarshal(req.Params, &items)
		out := make([]kernel.Artifact, 0, len(items))
		for _, item := range items {
			out = append(out, kernel.Artifact{Ref: item.Ref, Projection: kernel.ProjectionHead | kernel.ProjectionContent, Chunks: []kernel.ContentChunk{{Text: "正文"}}})
		}
		result = out
	default:
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "unsupported " + req.Method}})
		os.Exit(0)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	os.Exit(0)
}

func TestNewRejectsMissingDescriptor(t *testing.T) {
	_, err := New(Config{Command: os.Args[0]})
	if err == nil || !strings.Contains(err.Error(), "descriptor") {
		t.Fatalf("expected descriptor error, got %v", err)
	}
}

func TestNewRejectsInvalidDescriptorContracts(t *testing.T) {
	tests := []kernel.ProviderDescriptor{
		{ID: "exec.docs", Source: "unknown", ObjectKinds: []kernel.ObjectKind{kernel.KindDocument}, Operations: kernel.OperationSet{kernel.OpSearch: true}},
		{ID: "exec.docs", Source: kernel.SourceDocs, ObjectKinds: []kernel.ObjectKind{kernel.KindDocument}, Operations: kernel.OperationSet{"shell": true}},
		{ID: "exec.docs", Source: kernel.SourceDocs, ObjectKinds: []kernel.ObjectKind{kernel.KindDocument}, Operations: kernel.OperationSet{kernel.OpSearch: true}, ReturnedProjection: kernel.ProjectionSet(1 << 40)},
	}
	for _, descriptor := range tests {
		if _, err := New(Config{Descriptor: descriptor, Command: os.Args[0]}); err == nil {
			t.Fatalf("expected descriptor rejection: %+v", descriptor)
		}
	}
}

func TestExternalProviderRejectsOversizeAndMalformedResponses(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		limit    int64
		errorTyp kernel.ErrorType
	}{
		{name: "oversize", mode: "oversize", limit: 64, errorTyp: kernel.ErrBudgetExhausted},
		{name: "bad envelope", mode: "bad-envelope", limit: 1 << 20, errorTyp: kernel.ErrParse},
		{name: "bad page", mode: "bad-page", limit: 1 << 20, errorTyp: kernel.ErrParse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SFS_EXEC_PROVIDER_CASE", test.mode)
			p, err := New(Config{
				Descriptor: kernel.ProviderDescriptor{
					ID: "exec.docs", Source: kernel.SourceDocs,
					ObjectKinds:        []kernel.ObjectKind{kernel.KindDocument},
					Operations:         kernel.OperationSet{kernel.OpSearch: true},
					ReturnedProjection: kernel.ProjectionHead,
				},
				Command: os.Args[0], Args: []string{"-test.run=TestExecProviderHelperProcess", "--", "--provider-helper"},
				Timeout: 2 * time.Second, MaxStdoutBytes: test.limit,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.Search(context.Background(), kernel.ProviderSearchRequest{Query: "test", PageSize: 1})
			if detail := kernel.DetailFromError(err); err == nil || detail.Type != test.errorTyp {
				t.Fatalf("error=%v detail=%+v", err, detail)
			}
		})
	}
}
