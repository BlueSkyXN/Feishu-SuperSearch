package ask

import (
	"context"
	"fmt"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/ai"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/research"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
)

type Service struct {
	Research research.Service
	Answerer ai.Answerer
}

type Request struct {
	planner.UserRequest
}

type Result struct {
	Research research.Result `json:"research"`
	Answer   ai.Answer       `json:"answer"`
	Answerer string          `json:"answerer"`
	Partial  bool            `json:"partial"`
	Warnings []string        `json:"warnings,omitempty"`
}

func (s Service) Run(ctx context.Context, request Request) (Result, error) {
	return s.RunWithObserver(ctx, request, nil)
}

func (s Service) RunWithObserver(ctx context.Context, request Request, observer research.Observer) (Result, error) {
	if s.Answerer == nil {
		return Result{}, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: "AI answer is disabled; enable ai.answer or use research"}
	}
	if s.Research.Kernel == nil {
		return Result{}, fmt.Errorf("ask research kernel is nil")
	}
	request.Deep = true
	researchResult, err := s.Research.RunWithObserver(ctx, research.Request{UserRequest: request.UserRequest}, observer)
	if err != nil {
		return Result{}, err
	}
	if observer != nil {
		observer(research.Event{Type: research.EventProgress, Phase: research.PhaseAnswering, Message: "正在基于证据生成回答", Time: time.Now().UTC()})
	}
	answer, answerErr := s.Answerer.Answer(ctx, ai.AnswerRequest{Query: request.Query, EvidencePack: researchResult.EvidencePack})
	result := Result{Research: researchResult, Answer: answer, Answerer: s.Answerer.Name(), Partial: researchResult.PlanResult.Partial || answer.Partial, Warnings: append([]string(nil), researchResult.Warnings...)}
	if answerErr != nil {
		result.Answer = ai.Answer{Text: researchResult.Summary, Warnings: []string{"AI 回答失败，返回确定性证据摘要"}, Partial: true}
		result.Partial = true
		result.Warnings = append(result.Warnings, answerErr.Error())
	}
	if observer != nil {
		observer(research.Event{Type: research.EventProgress, Phase: research.PhaseComplete, Message: "带引用回答已生成", Time: time.Now().UTC()})
	}
	return result, nil
}
