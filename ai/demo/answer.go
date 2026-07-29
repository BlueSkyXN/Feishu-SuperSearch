package demo

import (
	"context"
	"fmt"
	"strings"

	"github.com/BlueSkyXN/Feishu-SuperSearch/ai"
)

type Answerer struct {
	MaxEvidence int
}

func (Answerer) Name() string { return "demo" }

func (a Answerer) Answer(ctx context.Context, request ai.AnswerRequest) (ai.Answer, error) {
	if err := ctx.Err(); err != nil {
		return ai.Answer{}, err
	}
	if len(request.EvidencePack.Evidence) == 0 {
		return ai.Answer{
			Text:     "没有获取到足够证据，无法生成演示回答。",
			Warnings: []string{"离线演示没有可用 Evidence"},
			Partial:  true,
			Refused:  true,
		}, nil
	}
	limit := a.MaxEvidence
	if limit <= 0 || limit > len(request.EvidencePack.Evidence) {
		limit = len(request.EvidencePack.Evidence)
	}
	if limit > 4 {
		limit = 4
	}
	claims := make([]ai.Claim, 0, limit)
	citations := make([]ai.Citation, 0, limit)
	lines := []string{"这是离线演示回答，仅用于预览 Evidence、Claim 与 Citation 流程："}
	for _, evidence := range request.EvidencePack.Evidence[:limit] {
		quote := compact(evidence.Quote)
		if quote == "" {
			quote = compact(evidence.Text)
		}
		if quote == "" {
			continue
		}
		claim := fmt.Sprintf("%s [%s]", quote, evidence.ID)
		lines = append(lines, "- "+claim)
		claims = append(claims, ai.Claim{Text: quote, EvidenceIDs: []string{evidence.ID}})
		citations = append(citations, ai.Citation{ID: evidence.ID, EvidenceID: evidence.ID, SourceRef: evidence.SourceRef, Quote: evidence.Quote, URL: evidence.URL})
	}
	if len(claims) == 0 {
		return ai.Answer{
			Text:     "Evidence Pack 不包含可引用文本，无法生成演示回答。",
			Warnings: []string{"离线演示没有可引用文本"},
			Partial:  true,
			Refused:  true,
		}, nil
	}
	return ai.Answer{
		Text:      strings.Join(lines, "\n"),
		Claims:    claims,
		Citations: citations,
		Warnings:  []string{"当前为离线 Demo Answerer，不代表模型生成结果"},
	}, nil
}

func compact(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 180 {
		return string(runes[:180]) + "…"
	}
	return value
}
