package demo

import (
	"context"
	"testing"

	"github.com/BlueSkyXN/Feishu-SuperSearch/ai"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/synthesis"
)

func TestAnswerUsesOnlyKnownEvidence(t *testing.T) {
	evidence := synthesis.Evidence{ID: "ev_demo", Text: "测试环境晚交付三天", Quote: "测试环境晚交付三天", SourceRef: kernel.ObjectRef{Kind: kernel.KindDocument, NativeID: "doc_demo"}, URL: "https://example.invalid/doc"}
	answer, err := (Answerer{}).Answer(context.Background(), ai.AnswerRequest{Query: "为什么延期", EvidencePack: synthesis.EvidencePack{Evidence: []synthesis.Evidence{evidence}}})
	if err != nil {
		t.Fatal(err)
	}
	if answer.Refused || answer.Partial || len(answer.Claims) != 1 || len(answer.Citations) != 1 {
		t.Fatalf("answer=%+v", answer)
	}
	if answer.Claims[0].EvidenceIDs[0] != evidence.ID || answer.Citations[0].EvidenceID != evidence.ID {
		t.Fatalf("answer references=%+v %+v", answer.Claims, answer.Citations)
	}
}

func TestAnswerRefusesEmptyEvidenceAndHonorsCancellation(t *testing.T) {
	answer, err := (Answerer{}).Answer(context.Background(), ai.AnswerRequest{})
	if err != nil || !answer.Refused || !answer.Partial {
		t.Fatalf("answer=%+v err=%v", answer, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Answerer{}).Answer(ctx, ai.AnswerRequest{EvidencePack: synthesis.EvidencePack{Evidence: []synthesis.Evidence{{ID: "ev"}}}}); err == nil {
		t.Fatal("cancelled context accepted")
	}
}
