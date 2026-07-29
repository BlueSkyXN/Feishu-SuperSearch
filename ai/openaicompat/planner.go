package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	apiassets "github.com/BlueSkyXN/Feishu-SuperSearch/api"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/planvalidate"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
)

type Planner struct {
	Client          *Client
	Model           string
	MaxOutputTokens int
	Fallback        planner.Planner
}

func (Planner) Name() string { return "ai" }

func (p Planner) Plan(ctx context.Context, request planner.UserRequest, capabilities kernel.CapabilitySnapshot) (kernel.RetrievalPlan, error) {
	input, _ := json.Marshal(map[string]any{"request": request, "capabilities": capabilities, "output_schema": "retrieval-plan/v1"})
	schema := map[string]any{}
	_ = json.Unmarshal(apiassets.RetrievalPlanSchema(), &schema)
	raw, err := p.Client.CompleteJSON(ctx, p.Model, plannerSystemPrompt, string(input), "retrieval_plan", schema, p.MaxOutputTokens)
	if err == nil {
		err = planvalidate.ValidateJSON(raw)
	}
	if err != nil {
		repairInput := map[string]any{"request": request, "capabilities": capabilities, "invalid_output": truncateForRepair(raw, 32<<10), "validation_error": err.Error()}
		repairJSON, _ := json.Marshal(repairInput)
		raw, err = p.Client.CompleteJSON(ctx, p.Model, plannerRepairPrompt, string(repairJSON), "retrieval_plan", schema, p.MaxOutputTokens)
		if err == nil {
			err = planvalidate.ValidateJSON(raw)
		}
	}
	if err != nil {
		return p.fallback(ctx, request, capabilities, err)
	}
	var plan kernel.RetrievalPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return p.fallback(ctx, request, capabilities, err)
	}
	if plan.Metadata == nil {
		plan.Metadata = map[string]any{}
	}
	plan.Metadata["planner"] = "ai"
	plan.Metadata["model"] = p.Model
	return plan, nil
}

func (p Planner) fallback(ctx context.Context, request planner.UserRequest, capabilities kernel.CapabilitySnapshot, cause error) (kernel.RetrievalPlan, error) {
	if p.Fallback == nil {
		return kernel.RetrievalPlan{}, cause
	}
	plan, err := p.Fallback.Plan(ctx, request, capabilities)
	if err != nil {
		return kernel.RetrievalPlan{}, fmt.Errorf("AI planner failed (%v), fallback failed: %w", cause, err)
	}
	if plan.Metadata == nil {
		plan.Metadata = map[string]any{}
	}
	plan.Metadata["planner"] = p.Fallback.Name()
	plan.Metadata["fallback_from"] = "ai"
	plan.Metadata["fallback_reason"] = cause.Error()
	return plan, nil
}

func truncateForRepair(raw []byte, max int) string {
	if len(raw) > max {
		return string(raw[:max]) + "…"
	}
	return strings.TrimSpace(string(raw))
}

const plannerSystemPrompt = `你是 SuperFeishuSearch 的检索计划器。只生成符合 retrieval-plan/v1 的 JSON，不执行命令，不编造 Provider。只能使用 capabilities 中 status=ok 的来源和操作。计划必须有界，优先并行搜索，深度检索最多 Fetch 用户请求指定的 fetch_top_k。`

const plannerRepairPrompt = `修复一份不合法的 retrieval-plan/v1。只输出修复后的 JSON。不要增加 capabilities 未提供的来源或操作，不要解释。`
