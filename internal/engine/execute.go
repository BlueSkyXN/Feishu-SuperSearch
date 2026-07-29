package engine

import (
	"context"

	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/planexec"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/planvalidate"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func (e *Engine) Execute(ctx context.Context, plan kernel.RetrievalPlan) (<-chan kernel.RetrievalEvent, <-chan kernel.PlanResult, error) {
	if err := planvalidate.Validate(plan); err != nil {
		return nil, nil, err
	}
	if plan.Identity.Mode == "" {
		plan.Identity = plan.Identity.Normalized()
	}
	if plan.Budget.DeadlineMS == 0 {
		plan.Budget = plan.Budget.WithDefaults()
	}
	return planexec.New(e, e.cfg.GlobalConcurrency).Execute(ctx, plan)
}
