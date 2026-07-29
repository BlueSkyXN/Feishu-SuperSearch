package planner

import (
	"context"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type UserRequest struct {
	Query     string               `json:"query"`
	Sources   []kernel.SourceID    `json:"sources,omitempty"`
	Identity  kernel.Identity      `json:"identity"`
	Filters   kernel.SearchFilters `json:"filters,omitempty"`
	Limit     int                  `json:"limit,omitempty"`
	Deep      bool                 `json:"deep,omitempty"`
	FetchTopK int                  `json:"fetch_top_k,omitempty"`
	Budget    kernel.SearchBudget  `json:"budget,omitempty"`
}

type Planner interface {
	Name() string
	Plan(context.Context, UserRequest, kernel.CapabilitySnapshot) (kernel.RetrievalPlan, error)
}
