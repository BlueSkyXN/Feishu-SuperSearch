package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	appcore "github.com/BlueSkyXN/Feishu-SuperSearch/internal/app"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/engine"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/session"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/provider"
)

// blockingSearchProvider blocks every search until its context ends.
type blockingSearchProvider struct {
	started   chan struct{}
	cancelled chan struct{}
}

func (p *blockingSearchProvider) Descriptor() kernel.ProviderDescriptor {
	return kernel.ProviderDescriptor{ID: "test.blocking.docs", Source: kernel.SourceDocs, ObjectKinds: []kernel.ObjectKind{kernel.KindDocument}, Operations: kernel.OperationSet{kernel.OpSearch: true}, ReturnedProjection: kernel.ProjectionHead, Backend: "test"}
}

func (p *blockingSearchProvider) Search(ctx context.Context, _ kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	close(p.started)
	<-ctx.Done()
	close(p.cancelled)
	return kernel.CandidatePage{}, ctx.Err()
}

func TestMCPAnswersOtherRequestsDuringToolCallAndHonorsCancellation(t *testing.T) {
	blocking := &blockingSearchProvider{started: make(chan struct{}), cancelled: make(chan struct{})}
	registry := provider.NewRegistry()
	if err := registry.Register(blocking, true); err != nil {
		t.Fatal(err)
	}
	cfg := appcore.DefaultConfig()
	cfg.Backend = "mock"
	cfg.Storage = appcore.StorageConfig{Type: "memory", TTL: "45m"}
	a, err := appcore.Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.Engine = engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{ProviderTimeout: time.Minute, SessionTTL: time.Hour})

	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- New(a).Serve(context.Background(), inReader, outWriter)
		_ = outWriter.Close()
	}()
	responses := make(chan map[string]any, 8)
	go func() {
		scanner := bufio.NewScanner(outReader)
		for scanner.Scan() {
			var msg map[string]any
			if json.Unmarshal(scanner.Bytes(), &msg) == nil {
				responses <- msg
			}
		}
		close(responses)
	}()
	send := func(line string) {
		t.Helper()
		if _, err := io.WriteString(inWriter, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	waitFor := func(ch <-chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %s", what)
		}
	}

	send(`{"jsonrpc":"2.0","id":"slow","method":"tools/call","params":{"name":"feishu_search","arguments":{"query":"A 项目","sources":["docs"]}}}`)
	waitFor(blocking.started, "search to start")
	send(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	select {
	case msg := <-responses:
		if msg["id"] != float64(2) {
			t.Fatalf("first response should answer the ping while the search runs, got %v", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ping was blocked by the running tool call")
	}
	send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"slow","reason":"user interrupted"}}`)
	waitFor(blocking.cancelled, "search to observe cancellation")
	_ = inWriter.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for msg := range responses {
		t.Fatalf("cancelled call must not get a response, got %v", msg)
	}
}

func TestRequestKeyNormalizesIDs(t *testing.T) {
	if requestKey(json.RawMessage(` 7 `)) != requestKey(json.RawMessage(`7`)) {
		t.Fatal("whitespace changed numeric key")
	}
	if requestKey(json.RawMessage(`"7"`)) == requestKey(json.RawMessage(`7`)) {
		t.Fatal("string and number ids must differ")
	}
	if key, ok := cancelledRequestKey(request{Method: "$/cancelRequest", Params: json.RawMessage(`{"id":7}`)}); !ok || key != requestKey(json.RawMessage(`7`)) {
		t.Fatalf("$/cancelRequest key=%q ok=%v", key, ok)
	}
	if _, ok := cancelledRequestKey(request{Method: "tools/call", Params: json.RawMessage(`{"id":7}`)}); ok {
		t.Fatal("only cancellation messages carry a cancelled id")
	}
}
