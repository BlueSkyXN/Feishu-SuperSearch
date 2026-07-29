package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BlueSkyXN/Feishu-SuperSearch/ai"
)

type Answerer struct {
	Client          *Client
	Model           string
	MaxOutputTokens int
}

func (Answerer) Name() string { return "ai" }

func (a Answerer) Answer(ctx context.Context, request ai.AnswerRequest) (ai.Answer, error) {
	if len(request.EvidencePack.Evidence) == 0 {
		return ai.Answer{Text: "没有获取到足够证据，无法回答这个问题。", Warnings: []string{"evidence_pack 为空"}, Partial: true}, nil
	}
	input, _ := json.Marshal(request)
	schema := map[string]any{
		"type": "object", "required": []string{"answer", "claims", "insufficient_evidence"}, "additionalProperties": false,
		"properties": map[string]any{
			"answer": map[string]any{"type": "string", "minLength": 1},
			"claims": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object", "required": []string{"text", "evidence_ids"}, "additionalProperties": false,
					"properties": map[string]any{
						"text":         map[string]any{"type": "string", "minLength": 1},
						"evidence_ids": map[string]any{"type": "array", "minItems": 1, "uniqueItems": true, "items": map[string]any{"type": "string"}},
					},
				},
			},
			"warnings":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"insufficient_evidence": map[string]any{"type": "boolean"},
		},
	}
	raw, err := a.Client.CompleteJSON(ctx, a.Model, answerSystemPrompt, string(input), "grounded_answer", schema, a.MaxOutputTokens)
	if err != nil {
		return ai.Answer{}, err
	}
	var response struct {
		Answer               string     `json:"answer"`
		Claims               []ai.Claim `json:"claims"`
		Warnings             []string   `json:"warnings"`
		InsufficientEvidence bool       `json:"insufficient_evidence"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return ai.Answer{}, err
	}
	response.Answer = strings.TrimSpace(response.Answer)
	if response.Answer == "" {
		return ai.Answer{}, fmt.Errorf("AI answer is empty")
	}
	if response.InsufficientEvidence {
		if len(response.Claims) != 0 {
			return ai.Answer{}, fmt.Errorf("AI refusal must not contain claims")
		}
		warnings := append([]string(nil), response.Warnings...)
		warnings = append(warnings, "证据不足，已拒绝生成无来源结论")
		return ai.Answer{Text: response.Answer, Warnings: warnings, Partial: true, Refused: true}, nil
	}
	if len(response.Claims) == 0 {
		return ai.Answer{}, fmt.Errorf("AI answer requires at least one evidence-backed claim")
	}
	byEvidence := map[string]struct {
		Quote string
		URL   string
		Ref   any
	}{}
	for _, evidence := range request.EvidencePack.Evidence {
		byEvidence[evidence.ID] = struct {
			Quote string
			URL   string
			Ref   any
		}{Quote: evidence.Quote, URL: evidence.URL, Ref: evidence.SourceRef}
	}
	used := map[string]bool{}
	for _, claim := range response.Claims {
		if strings.TrimSpace(claim.Text) == "" || len(claim.EvidenceIDs) == 0 {
			return ai.Answer{}, fmt.Errorf("AI claim is missing text or evidence_ids")
		}
		for _, evidenceID := range claim.EvidenceIDs {
			if _, ok := byEvidence[evidenceID]; !ok {
				return ai.Answer{}, fmt.Errorf("AI claim references unknown evidence id %q", evidenceID)
			}
			used[evidenceID] = true
		}
	}
	citations := make([]ai.Citation, 0, len(used))
	for _, evidence := range request.EvidencePack.Evidence {
		if used[evidence.ID] {
			citations = append(citations, ai.Citation{ID: evidence.ID, EvidenceID: evidence.ID, SourceRef: evidence.SourceRef, Quote: evidence.Quote, URL: evidence.URL})
		}
	}
	return ai.Answer{Text: response.Answer, Claims: response.Claims, Citations: citations, Warnings: response.Warnings}, nil
}

const answerSystemPrompt = `你是 SuperFeishuSearch 的证据回答器。只能依据 Evidence Pack 回答。每个可核验结论必须列出至少一个真实 evidence_id；不得引用不存在的 ID，不得补充外部知识。证据不足时设置 insufficient_evidence=true，在 answer 中明确拒答，claims 必须为空并写入 warnings；否则设置为 false。`
