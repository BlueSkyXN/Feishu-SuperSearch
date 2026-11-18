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
	return p.PlanWithPolicy(ctx, request, capabilities, planner.PlanPolicy{})
}

func (p Planner) PlanWithPolicy(ctx context.Context, request planner.UserRequest, capabilities kernel.CapabilitySnapshot, policy planner.PlanPolicy) (kernel.RetrievalPlan, error) {
	input, _ := json.Marshal(map[string]any{"request": request, "capabilities": capabilities, "output_schema": "retrieval-plan/v1", "allowed_operations": policy.AllowedOperations})
	schema := map[string]any{}
	_ = json.Unmarshal(apiassets.RetrievalPlanSchema(), &schema)
	if len(policy.AllowedOperations) > 0 {
		defs := schema["$defs"].(map[string]any)
		node := defs["node"].(map[string]any)
		properties := node["properties"].(map[string]any)
		properties["op"].(map[string]any)["enum"] = policy.AllowedOperations
	}
	raw, err := p.Client.CompleteJSON(ctx, p.Model, plannerSystemPrompt, string(input), "retrieval_plan", schema, p.MaxOutputTokens)
	var plan kernel.RetrievalPlan
	if err == nil {
		plan, err = decodePlannedResponse(raw, policy)
	}
	if err != nil {
		repairInput := map[string]any{"request": request, "capabilities": capabilities, "allowed_operations": policy.AllowedOperations, "invalid_output": truncateForRepair(raw, 32<<10), "validation_error": err.Error()}
		if detail := kernel.DetailFromError(err); len(detail.Details) > 0 {
			repairInput["validation_details"] = detail.Details
		}
		repairJSON, _ := json.Marshal(repairInput)
		raw, err = p.Client.CompleteJSON(ctx, p.Model, plannerRepairPrompt, string(repairJSON), "retrieval_plan", schema, p.MaxOutputTokens)
		if err == nil {
			plan, err = decodePlannedResponse(raw, policy)
		}
	}
	if err != nil {
		return p.fallback(ctx, request, capabilities, policy, err)
	}
	if plan.Metadata == nil {
		plan.Metadata = map[string]any{}
	}
	plan.Metadata["planner"] = "ai"
	plan.Metadata["model"] = p.Model
	return plan, nil
}

func decodePlannedResponse(raw []byte, policy planner.PlanPolicy) (kernel.RetrievalPlan, error) {
	if err := planvalidate.ValidateJSON(raw); err != nil {
		return kernel.RetrievalPlan{}, err
	}
	var plan kernel.RetrievalPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return plan, err
	}
	return validatePlanPolicy(plan, policy)
}

func validatePlanPolicy(plan kernel.RetrievalPlan, policy planner.PlanPolicy) (kernel.RetrievalPlan, error) {
	if len(policy.AllowedOperations) > 0 {
		allowed := map[kernel.PlanOp]bool{}
		for _, operation := range policy.AllowedOperations {
			allowed[operation] = true
		}
		for _, node := range plan.Nodes {
			if !allowed[node.Op] {
				return plan, fmt.Errorf("operation %s is not allowed by this entry point", node.Op)
			}
		}
	}
	if policy.Validate != nil {
		var err error
		plan, err = policy.Validate(plan)
		if err != nil {
			return plan, err
		}
	}
	return plan, planvalidate.Validate(plan)
}

func (p Planner) fallback(ctx context.Context, request planner.UserRequest, capabilities kernel.CapabilitySnapshot, policy planner.PlanPolicy, cause error) (kernel.RetrievalPlan, error) {
	if p.Fallback == nil {
		return kernel.RetrievalPlan{}, cause
	}
	plan, err := p.Fallback.Plan(ctx, request, capabilities)
	if err == nil {
		plan, err = validatePlanPolicy(plan, policy)
	}
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

const plannerSystemPrompt = `你是 SuperFeishuSearch 的检索计划器。只生成符合 retrieval-plan/v1 的 JSON，不执行命令，不编造 Provider。只能使用 capabilities 中 status=ok 的来源；提供 allowed_operations 时，节点操作必须在该列表中。保留用户的 query、identity、sources、filters 和预算，改写后的检索词放入 source_queries。读取搜索结果使用有搜索祖先的 map_fetch。计划必须有界，优先并行搜索，深度检索最多 Fetch 用户请求指定的 fetch_top_k。`

const plannerRepairPrompt = `按 validation_error 修复 retrieval-plan/v1。只输出修复后的 JSON。遵守 allowed_operations、capabilities 和用户请求的身份、来源、过滤条件及预算，保留 query，改写词使用 source_queries。不要解释。`
