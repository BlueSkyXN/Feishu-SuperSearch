package openaicompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/ai"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
	rulesplanner "github.com/BlueSkyXN/Feishu-SuperSearch/planner/rules"
	"github.com/BlueSkyXN/Feishu-SuperSearch/synthesis"
)

func newTestClient(t *testing.T, responses ...string) (*Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		index := int(calls.Add(1)) - 1
		if index >= len(responses) {
			index = len(responses) - 1
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": responses[index]}}}})
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(ClientConfig{BaseURL: server.URL, APIKey: "test-key", Timeout: 2 * time.Second, JSONSchema: true})
	if err != nil {
		t.Fatal(err)
	}
	return client, &calls
}

func TestPlannerRepairsInvalidPlanOnce(t *testing.T) {
	valid := `{"version":"retrieval-plan/v1","nodes":[{"id":"search","op":"search","request":{"query":"A 项目","sources":["docs"],"identity":{"mode":"user"}}}],"output":["search"]}`
	client, calls := newTestClient(t, `{"version":"retrieval-plan/v1","nodes":[]}`, valid)
	p := Planner{Client: client, Model: "test", MaxOutputTokens: 1000, Fallback: rulesplanner.Planner{}}
	plan, err := p.Plan(context.Background(), planner.UserRequest{Query: "A 项目"}, kernel.CapabilitySnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || plan.Metadata["planner"] != "ai" || len(plan.Nodes) != 1 {
		t.Fatalf("calls=%d plan=%+v", calls.Load(), plan)
	}
}

func TestPlannerFallsBackAfterTwoInvalidResponses(t *testing.T) {
	client, calls := newTestClient(t, `{}`, `{}`)
	p := Planner{Client: client, Model: "test", MaxOutputTokens: 1000, Fallback: rulesplanner.Planner{}}
	plan, err := p.Plan(context.Background(), planner.UserRequest{Query: "A 项目", Sources: []kernel.SourceID{kernel.SourceDocs}}, kernel.CapabilitySnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || plan.Metadata["fallback_from"] != "ai" || plan.Metadata["planner"] != "rules" {
		t.Fatalf("calls=%d metadata=%v", calls.Load(), plan.Metadata)
	}
}

func TestAnswererValidatesEvidenceIDs(t *testing.T) {
	response := `{"answer":"项目延期。","claims":[{"text":"项目延期","evidence_ids":["ev_known"]}],"warnings":[],"insufficient_evidence":false}`
	client, _ := newTestClient(t, response)
	answerer := Answerer{Client: client, Model: "test", MaxOutputTokens: 1000}
	evidence := synthesis.Evidence{ID: "ev_known", Text: "测试环境晚交付。", Quote: "测试环境晚交付。", SourceRef: kernel.ObjectRef{CanonicalID: "feishu:s:document:d1", Kind: kernel.KindDocument}, URL: "https://example.invalid/doc"}
	answer, err := answerer.Answer(context.Background(), ai.AnswerRequest{Query: "为什么延期", EvidencePack: synthesis.EvidencePack{Evidence: []synthesis.Evidence{evidence}}})
	if err != nil {
		t.Fatal(err)
	}
	if answer.Text == "" || len(answer.Citations) != 1 || answer.Citations[0].ID != "ev_known" || answer.Citations[0].EvidenceID != "ev_known" {
		t.Fatalf("answer=%+v", answer)
	}
}

func TestAnswererRejectsUnknownEvidenceID(t *testing.T) {
	response := `{"answer":"项目延期。","claims":[{"text":"项目延期","evidence_ids":["ev_missing"]}],"insufficient_evidence":false}`
	client, _ := newTestClient(t, response)
	answerer := Answerer{Client: client, Model: "test"}
	_, err := answerer.Answer(context.Background(), ai.AnswerRequest{Query: "为什么延期", EvidencePack: synthesis.EvidencePack{Evidence: []synthesis.Evidence{{ID: "ev_known", Text: "证据", Quote: "证据"}}}})
	if err == nil {
		t.Fatal("expected unknown evidence error")
	}
}

func TestAnswererReturnsExplicitEvidenceRefusal(t *testing.T) {
	client, _ := newTestClient(t, `{"answer":"现有证据不足，无法回答。","claims":[],"warnings":["缺少决策记录"],"insufficient_evidence":true}`)
	answerer := Answerer{Client: client, Model: "test"}
	answer, err := answerer.Answer(context.Background(), ai.AnswerRequest{Query: "谁做了决定", EvidencePack: synthesis.EvidencePack{Evidence: []synthesis.Evidence{{ID: "ev_known", Text: "只有背景信息", Quote: "只有背景信息"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !answer.Refused || !answer.Partial || len(answer.Claims) != 0 || len(answer.Citations) != 0 || len(answer.Warnings) < 2 {
		t.Fatalf("answer=%+v", answer)
	}
}

func TestAnswererRejectsUncitedNonRefusal(t *testing.T) {
	client, _ := newTestClient(t, `{"answer":"项目延期。","claims":[],"warnings":[],"insufficient_evidence":false}`)
	answerer := Answerer{Client: client, Model: "test"}
	_, err := answerer.Answer(context.Background(), ai.AnswerRequest{Query: "为什么延期", EvidencePack: synthesis.EvidencePack{Evidence: []synthesis.Evidence{{ID: "ev_known", Text: "证据", Quote: "证据"}}}})
	if err == nil {
		t.Fatal("expected uncited answer rejection")
	}
}

func TestRerankerRejectsUnknownCandidate(t *testing.T) {
	client, _ := newTestClient(t, `{"ordered_ids":["missing"]}`)
	reranker := Reranker{Client: client, Model: "test", TopN: 30}
	_, err := reranker.Rerank(context.Background(), "query", []kernel.Candidate{{Ref: kernel.ObjectRef{CanonicalID: "a"}}, {Ref: kernel.ObjectRef{CanonicalID: "b"}}})
	if err == nil {
		t.Fatal("expected unknown candidate error")
	}
}
