package kernel

import "encoding/json"

type PlanOp string

type NodeState string

const (
	PlanResolve  PlanOp = "resolve"
	PlanSearch   PlanOp = "search"
	PlanQuery    PlanOp = "query"
	PlanFetch    PlanOp = "fetch"
	PlanExpand   PlanOp = "expand"
	PlanMerge    PlanOp = "merge"
	PlanDedup    PlanOp = "dedup"
	PlanRank     PlanOp = "rank"
	PlanLimit    PlanOp = "limit"
	PlanProject  PlanOp = "project"
	PlanMapFetch PlanOp = "map_fetch"
)

const (
	NodePlanned   NodeState = "planned"
	NodeBlocked   NodeState = "blocked"
	NodeReady     NodeState = "ready"
	NodeRunning   NodeState = "running"
	NodeSucceeded NodeState = "succeeded"
	NodePartial   NodeState = "partial"
	NodeEmpty     NodeState = "empty"
	NodeRetryWait NodeState = "retry_wait"
	NodeFailed    NodeState = "failed"
	NodeSkipped   NodeState = "skipped"
	NodeCancelled NodeState = "cancelled"
)

type PlanSession struct {
	ID    string `json:"id,omitempty"`
	Reuse bool   `json:"reuse,omitempty"`
}

type RetryPolicy struct {
	MaxAttempts  int `json:"max_attempts,omitempty"`
	BackoffMS    int `json:"backoff_ms,omitempty"`
	MaxBackoffMS int `json:"max_backoff_ms,omitempty"`
}

type PlanNode struct {
	ID        string          `json:"id"`
	Op        PlanOp          `json:"op"`
	DependsOn []string        `json:"depends_on,omitempty"`
	Request   json.RawMessage `json:"request,omitempty"`
	Priority  float64         `json:"priority,omitempty"`
	Optional  bool            `json:"optional,omitempty"`
	MaxItems  int             `json:"max_items,omitempty"`
	Retry     RetryPolicy     `json:"retry,omitempty"`
}

type RetrievalPlan struct {
	Version  string         `json:"version"`
	Session  PlanSession    `json:"session,omitempty"`
	Identity Identity       `json:"identity,omitempty"`
	Budget   SearchBudget   `json:"budget,omitempty"`
	Nodes    []PlanNode     `json:"nodes"`
	Output   []string       `json:"output,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type NodeResult struct {
	NodeID     string       `json:"node_id"`
	State      NodeState    `json:"state"`
	Attempts   int          `json:"attempts,omitempty"`
	Candidates []Candidate  `json:"candidates,omitempty"`
	Artifacts  []Artifact   `json:"artifacts,omitempty"`
	Relations  []Relation   `json:"relations,omitempty"`
	Value      any          `json:"value,omitempty"`
	Error      *ErrorDetail `json:"error,omitempty"`
}

type PlanResult struct {
	SessionID string                `json:"session_id"`
	Nodes     map[string]NodeResult `json:"nodes"`
	Output    map[string]NodeResult `json:"output"`
	Partial   bool                  `json:"partial"`
	Budget    BudgetState           `json:"budget"`
}
