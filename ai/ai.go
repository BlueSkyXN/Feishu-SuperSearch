package ai

import (
	"context"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
	"github.com/BlueSkyXN/Feishu-SuperSearch/synthesis"
)

type PlannerModel interface {
	planner.Planner
}

type Reranker interface {
	Name() string
	Rerank(context.Context, string, []kernel.Candidate) ([]kernel.Candidate, error)
}

type Answerer interface {
	Name() string
	Answer(context.Context, AnswerRequest) (Answer, error)
}

type AnswerRequest struct {
	Query        string                 `json:"query"`
	EvidencePack synthesis.EvidencePack `json:"evidence_pack"`
}

type Claim struct {
	Text        string   `json:"text"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type Citation struct {
	ID         string           `json:"id"`
	EvidenceID string           `json:"evidence_id"`
	SourceRef  kernel.ObjectRef `json:"source_ref"`
	Quote      string           `json:"quote"`
	URL        string           `json:"url,omitempty"`
}

type Answer struct {
	Text      string     `json:"text"`
	Claims    []Claim    `json:"claims,omitempty"`
	Citations []Citation `json:"citations,omitempty"`
	Warnings  []string   `json:"warnings,omitempty"`
	Partial   bool       `json:"partial"`
	Refused   bool       `json:"refused,omitempty"`
}
