package planexec

import (
	"container/heap"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	budgetctl "github.com/BlueSkyXN/Feishu-SuperSearch/internal/budget"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/fusion"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type Executor struct {
	kernel      kernel.Kernel
	concurrency int
}

func New(k kernel.Kernel, concurrency int) *Executor {
	if concurrency <= 0 {
		concurrency = 4
	}
	return &Executor{kernel: k, concurrency: concurrency}
}

type runtimeNode struct {
	node    kernel.PlanNode
	state   kernel.NodeState
	result  kernel.NodeResult
	index   int
	attempt int
}

type nodeItem struct {
	id       string
	priority float64
	seq      int
}
type readyHeap []nodeItem

func (h readyHeap) Len() int { return len(h) }
func (h readyHeap) Less(i, j int) bool {
	if h[i].priority == h[j].priority {
		return h[i].seq < h[j].seq
	}
	return h[i].priority > h[j].priority
}
func (h readyHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *readyHeap) Push(x any)   { *h = append(*h, x.(nodeItem)) }
func (h *readyHeap) Pop() any     { old := *h; n := len(old); x := old[n-1]; *h = old[:n-1]; return x }

type retryItem struct {
	id  string
	due time.Time
	seq int
}
type retryHeap []retryItem

func (h retryHeap) Len() int { return len(h) }
func (h retryHeap) Less(i, j int) bool {
	if h[i].due.Equal(h[j].due) {
		return h[i].seq < h[j].seq
	}
	return h[i].due.Before(h[j].due)
}
func (h retryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *retryHeap) Push(x any)   { *h = append(*h, x.(retryItem)) }
func (h *retryHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

type completion struct {
	id        string
	result    kernel.NodeResult
	sessionID string
}

func (e *Executor) Execute(ctx context.Context, plan kernel.RetrievalPlan) (<-chan kernel.RetrievalEvent, <-chan kernel.PlanResult, error) {
	if err := validatePlan(plan); err != nil {
		return nil, nil, err
	}
	events := make(chan kernel.RetrievalEvent, 256)
	results := make(chan kernel.PlanResult, 1)
	go e.run(ctx, plan, events, results)
	return events, results, nil
}

func (e *Executor) run(ctx context.Context, plan kernel.RetrievalPlan, events chan<- kernel.RetrievalEvent, results chan<- kernel.PlanResult) {
	defer close(events)
	defer close(results)
	account := budgetctl.New(plan.Budget)
	ctx, cancel := context.WithDeadline(budgetctl.WithAccount(ctx, account), account.Deadline())
	defer cancel()
	runtime := map[string]*runtimeNode{}
	for i, n := range plan.Nodes {
		state := kernel.NodeBlocked
		if len(n.DependsOn) == 0 {
			state = kernel.NodeReady
		}
		runtime[n.ID] = &runtimeNode{node: n, state: state, index: i}
	}
	ready := &readyHeap{}
	heap.Init(ready)
	retries := &retryHeap{}
	heap.Init(retries)
	seq := 0
	for id, n := range runtime {
		if n.state == kernel.NodeReady {
			heap.Push(ready, nodeItem{id: id, priority: n.node.Priority, seq: seq})
			seq++
		}
	}
	completed := make(chan completion, len(plan.Nodes))
	running := 0
	terminal := 0
	sessionID := plan.Session.ID
	var mu sync.Mutex
	emit := func(t kernel.EventType, nodeID string, data map[string]any) {
		select {
		case events <- kernel.RetrievalEvent{ID: fmt.Sprintf("pev_%d", time.Now().UnixNano()), SessionID: sessionID, Type: t, Time: time.Now().UTC(), NodeID: nodeID, Data: data}:
		case <-ctx.Done():
		}
	}
	emit(kernel.EventSessionStarted, "", map[string]any{"plan_version": plan.Version})
	for terminal < len(plan.Nodes) {
		now := time.Now()
		for retries.Len() > 0 && !(*retries)[0].due.After(now) {
			r := heap.Pop(retries).(retryItem)
			rn := runtime[r.id]
			if rn.state == kernel.NodeRetryWait {
				rn.state = kernel.NodeReady
				heap.Push(ready, nodeItem{id: r.id, priority: rn.node.Priority, seq: seq})
				seq++
			}
		}
		for running < e.concurrency && ready.Len() > 0 {
			// A plan without an explicit session serializes the first stateful node.
			// Once that node returns a session ID, the remaining roots can fan out
			// into the same retrieval session.
			if sessionID == "" && running > 0 {
				break
			}
			item := heap.Pop(ready).(nodeItem)
			rn := runtime[item.id]
			if rn.state != kernel.NodeReady {
				continue
			}
			rn.state = kernel.NodeRunning
			rn.attempt++
			running++
			emit(kernel.EventNodeStarted, rn.node.ID, map[string]any{"op": rn.node.Op, "attempt": rn.attempt})
			deps := collectDeps(rn.node, runtime)
			go func(rn *runtimeNode, deps map[string]kernel.NodeResult, currentSession string) {
				res, sid := e.executeNode(ctx, plan, rn.node, deps, currentSession)
				completed <- completion{id: rn.node.ID, result: res, sessionID: sid}
			}(rn, deps, sessionID)
		}
		if running == 0 && ready.Len() == 0 && retries.Len() == 0 {
			// Remaining nodes are blocked by failed dependencies or a malformed state.
			for _, rn := range runtime {
				if !isTerminal(rn.state) {
					rn.state = kernel.NodeSkipped
					rn.result = kernel.NodeResult{NodeID: rn.node.ID, State: kernel.NodeSkipped, Attempts: rn.attempt, Error: &kernel.ErrorDetail{Type: kernel.ErrInternal, Message: "node could not be scheduled"}}
					terminal++
				}
			}
			break
		}
		var retryTimer <-chan time.Time
		var timer *time.Timer
		if retries.Len() > 0 {
			d := time.Until((*retries)[0].due)
			if d < 0 {
				d = 0
			}
			timer = time.NewTimer(d)
			retryTimer = timer.C
		}
		select {
		case <-retryTimer:
			// The next loop promotes all due retry nodes to READY.
			continue
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			for _, rn := range runtime {
				if !isTerminal(rn.state) {
					rn.state = kernel.NodeCancelled
					rn.result = kernel.NodeResult{NodeID: rn.node.ID, State: kernel.NodeCancelled, Attempts: rn.attempt, Error: &kernel.ErrorDetail{Type: kernel.ErrCancelled, Message: ctx.Err().Error()}}
					terminal++
				}
			}
			running = 0
		case done := <-completed:
			if timer != nil {
				timer.Stop()
			}
			mu.Lock()
			if sessionID == "" && done.sessionID != "" {
				sessionID = done.sessionID
			}
			rn := runtime[done.id]
			done.result.Attempts = rn.attempt
			running--
			if done.result.State == kernel.NodeFailed && shouldRetry(done.result.Error) && rn.attempt < maxAttempts(rn.node) {
				rn.result = done.result
				rn.state = kernel.NodeRetryWait
				delay := retryDelay(rn.node, rn.attempt)
				heap.Push(retries, retryItem{id: rn.node.ID, due: time.Now().Add(delay), seq: seq})
				seq++
				mu.Unlock()
				emit(kernel.EventNodeRetrying, done.id, map[string]any{"attempt": rn.attempt, "next_attempt": rn.attempt + 1, "backoff_ms": delay.Milliseconds(), "error": done.result.Error})
				continue
			}
			rn.result = done.result
			rn.state = done.result.State
			terminal++
			mu.Unlock()
			emit(kernel.EventNodeCompleted, done.id, map[string]any{"state": done.result.State, "attempts": rn.attempt, "error": done.result.Error})
			for _, candidate := range done.result.Candidates {
				emit(kernel.EventCandidateUpsert, done.id, map[string]any{"candidate": candidate})
			}
			for _, artifact := range done.result.Artifacts {
				emit(kernel.EventArtifactUpsert, done.id, map[string]any{"artifact": artifact})
			}
			for _, candidate := range runtime {
				if candidate.state != kernel.NodeBlocked {
					continue
				}
				readyNow, skip := dependencyState(candidate.node, runtime)
				if skip {
					candidate.state = kernel.NodeSkipped
					candidate.result = kernel.NodeResult{NodeID: candidate.node.ID, State: kernel.NodeSkipped, Attempts: candidate.attempt, Error: &kernel.ErrorDetail{Type: kernel.ErrUpstreamPermanent, Message: "required dependency failed"}}
					terminal++
					emit(kernel.EventNodeCompleted, candidate.node.ID, map[string]any{"state": kernel.NodeSkipped})
					continue
				}
				if readyNow {
					candidate.state = kernel.NodeReady
					heap.Push(ready, nodeItem{id: candidate.node.ID, priority: candidate.node.Priority, seq: seq})
					seq++
				}
			}
		}
	}
	out := map[string]kernel.NodeResult{}
	nodes := map[string]kernel.NodeResult{}
	partial := false
	for id, rn := range runtime {
		nodes[id] = rn.result
		if rn.result.State == kernel.NodeFailed || rn.result.State == kernel.NodePartial || rn.result.State == kernel.NodeSkipped || rn.result.State == kernel.NodeCancelled {
			partial = true
		}
	}
	for _, id := range plan.Output {
		if r, ok := nodes[id]; ok {
			out[id] = r
		}
	}
	if len(plan.Output) == 0 {
		for id, r := range nodes {
			out[id] = r
		}
	}
	state := account.Snapshot()
	result := kernel.PlanResult{SessionID: sessionID, Nodes: nodes, Output: out, Partial: partial, Budget: state}
	if partial {
		emit(kernel.EventSessionPartial, "", map[string]any{"partial": true})
	} else {
		emit(kernel.EventSessionCompleted, "", map[string]any{"partial": false})
	}
	results <- result
}

func (e *Executor) executeNode(ctx context.Context, plan kernel.RetrievalPlan, node kernel.PlanNode, deps map[string]kernel.NodeResult, sessionID string) (kernel.NodeResult, string) {
	base := kernel.NodeResult{NodeID: node.ID, State: kernel.NodeSucceeded}
	switch node.Op {
	case kernel.PlanSearch:
		var raw struct {
			Source         kernel.SourceID            `json:"source"`
			Query          string                     `json:"query"`
			SourceQueries  map[kernel.SourceID]string `json:"source_queries"`
			Sources        []kernel.SourceID          `json:"sources"`
			Filters        kernel.SearchFilters       `json:"filters"`
			Limit          int                        `json:"limit"`
			LimitPerSource int                        `json:"limit_per_source"`
			Strategy       kernel.SearchStrategy      `json:"strategy"`
		}
		if err := json.Unmarshal(node.Request, &raw); err != nil {
			return failed(node.ID, err), sessionID
		}
		sources := raw.Sources
		if raw.Source != "" {
			sources = []kernel.SourceID{raw.Source}
		}
		req := kernel.SearchRequest{Query: raw.Query, SourceQueries: raw.SourceQueries, Sources: sources, Filters: raw.Filters, Identity: plan.Identity, Limit: raw.Limit, LimitPerSource: raw.LimitPerSource, Strategy: raw.Strategy, Budget: plan.Budget, SessionID: sessionID}
		snap, err := e.kernel.Search(ctx, req)
		if err != nil && len(snap.Candidates) == 0 {
			return failedDetail(node.ID, err), snap.SessionID
		}
		base.Candidates = snap.Candidates
		if snap.Partial {
			base.State = kernel.NodePartial
		}
		return base, snap.SessionID
	case kernel.PlanQuery:
		var req kernel.QueryRequest
		if err := json.Unmarshal(node.Request, &req); err != nil {
			return failed(node.ID, err), sessionID
		}
		req.Identity = plan.Identity
		req.SessionID = sessionID
		req.Budget = plan.Budget
		snap, err := e.kernel.Query(ctx, req)
		if err != nil && len(snap.Candidates) == 0 {
			return failedDetail(node.ID, err), snap.SessionID
		}
		base.Candidates = snap.Candidates
		if err != nil {
			base.State = kernel.NodePartial
			base.Error = kernel.DetailFromError(err)
		}
		return base, snap.SessionID
	case kernel.PlanFetch:
		var req kernel.FetchBatchRequest
		if err := json.Unmarshal(node.Request, &req); err != nil {
			return failed(node.ID, err), sessionID
		}
		req.Identity = plan.Identity
		req.SessionID = sessionID
		req.Budget = plan.Budget
		batch, err := e.kernel.Fetch(ctx, req)
		if err != nil && len(batch.Items) == 0 {
			return failedDetail(node.ID, err), batch.SessionID
		}
		for _, it := range batch.Items {
			if it.Artifact != nil {
				base.Artifacts = append(base.Artifacts, *it.Artifact)
			}
			if it.Error != nil {
				base.State = kernel.NodePartial
				base.Error = it.Error
			}
		}
		return base, batch.SessionID
	case kernel.PlanExpand:
		var req kernel.ExpandRequest
		if err := json.Unmarshal(node.Request, &req); err != nil {
			return failed(node.ID, err), sessionID
		}
		req.Identity = plan.Identity
		req.SessionID = sessionID
		req.Budget = plan.Budget
		batch, err := e.kernel.Expand(ctx, req)
		if err != nil && len(batch.Relations) == 0 {
			return failedDetail(node.ID, err), batch.SessionID
		}
		base.Relations = batch.Relations
		if batch.Partial {
			base.State = kernel.NodePartial
			base.Error = batch.Error
		}
		return base, batch.SessionID
	case kernel.PlanResolve:
		var req kernel.ResolveRequest
		if err := json.Unmarshal(node.Request, &req); err != nil {
			return failed(node.ID, err), sessionID
		}
		req.Identity = plan.Identity
		refs, err := e.kernel.Resolve(ctx, req)
		if err != nil {
			return failedDetail(node.ID, err), sessionID
		}
		base.Value = refs
		return base, sessionID
	case kernel.PlanMerge:
		for _, d := range deps {
			base.Candidates = append(base.Candidates, d.Candidates...)
			base.Artifacts = append(base.Artifacts, d.Artifacts...)
			base.Relations = append(base.Relations, d.Relations...)
		}
		return base, sessionID
	case kernel.PlanDedup:
		cands := depCandidates(deps)
		scored, _ := fusion.Fuse("", cands, len(cands), fusion.Config{UseQuota: false}, plan.Identity.Normalized().ScopeKey)
		base.Candidates = scored
		return base, sessionID
	case kernel.PlanRank:
		var r struct {
			Query       string                      `json:"query"`
			Limit       int                         `json:"limit"`
			K0          float64                     `json:"k0"`
			Weights     map[kernel.SourceID]float64 `json:"weights"`
			Quotas      map[kernel.SourceID]int     `json:"quotas"`
			SourceQuota bool                        `json:"source_quota"`
		}
		_ = json.Unmarshal(node.Request, &r)
		cands := depCandidates(deps)
		if r.Limit <= 0 {
			r.Limit = len(cands)
		}
		base.Candidates, _ = fusion.Fuse(r.Query, cands, r.Limit, fusion.Config{K0: r.K0, Weights: r.Weights, Quotas: r.Quotas, UseQuota: r.SourceQuota}, plan.Identity.Normalized().ScopeKey)
		return base, sessionID
	case kernel.PlanLimit:
		var r struct {
			TopK int `json:"top_k"`
		}
		_ = json.Unmarshal(node.Request, &r)
		if r.TopK <= 0 {
			r.TopK = 20
		}
		cands := depCandidates(deps)
		if len(cands) > r.TopK {
			cands = cands[:r.TopK]
		}
		base.Candidates = cands
		arts := depArtifacts(deps)
		if len(arts) > r.TopK {
			arts = arts[:r.TopK]
		}
		base.Artifacts = arts
		return base, sessionID
	case kernel.PlanProject:
		base.Candidates = depCandidates(deps)
		base.Artifacts = depArtifacts(deps)
		base.Relations = depRelations(deps)
		return base, sessionID
	case kernel.PlanMapFetch:
		var r struct {
			TopK              int                                        `json:"top_k"`
			MaxItems          int                                        `json:"max_items"`
			ProjectionByKind  map[kernel.ObjectKind]kernel.ProjectionSet `json:"projection_by_kind"`
			DefaultProjection kernel.ProjectionSet                       `json:"default_projection"`
		}
		if err := json.Unmarshal(node.Request, &r); err != nil {
			return failed(node.ID, err), sessionID
		}
		if r.MaxItems <= 0 {
			r.MaxItems = node.MaxItems
		}
		if r.MaxItems <= 0 {
			r.MaxItems = 8
		}
		if r.TopK <= 0 || r.TopK > r.MaxItems {
			r.TopK = r.MaxItems
		}
		cands := depCandidates(deps)
		if len(cands) > r.TopK {
			cands = cands[:r.TopK]
		}
		items := []kernel.FetchRequest{}
		for _, c := range cands {
			p := r.ProjectionByKind[c.Kind]
			if p == 0 {
				p = r.DefaultProjection
			}
			if p == 0 {
				p = defaultProjection(c.Kind)
			}
			items = append(items, kernel.FetchRequest{Ref: c.Ref, Projection: p})
		}
		batch, err := e.kernel.Fetch(ctx, kernel.FetchBatchRequest{SessionID: sessionID, Identity: plan.Identity, Items: items, Budget: plan.Budget})
		if err != nil && len(batch.Items) == 0 {
			return failedDetail(node.ID, err), batch.SessionID
		}
		for _, it := range batch.Items {
			if it.Artifact != nil {
				base.Artifacts = append(base.Artifacts, *it.Artifact)
			}
			if it.Error != nil {
				base.State = kernel.NodePartial
				base.Error = it.Error
			}
		}
		return base, batch.SessionID
	default:
		return failed(node.ID, fmt.Errorf("unsupported plan op %q", node.Op)), sessionID
	}
}

func maxAttempts(node kernel.PlanNode) int {
	if node.Retry.MaxAttempts > 0 {
		return node.Retry.MaxAttempts
	}
	switch node.Op {
	case kernel.PlanResolve, kernel.PlanSearch, kernel.PlanQuery, kernel.PlanFetch, kernel.PlanExpand, kernel.PlanMapFetch:
		return 2
	default:
		return 1
	}
}

func retryDelay(node kernel.PlanNode, completedAttempts int) time.Duration {
	base := node.Retry.BackoffMS
	if base <= 0 {
		base = 200
	}
	max := node.Retry.MaxBackoffMS
	if max <= 0 {
		max = 2000
	}
	shift := completedAttempts - 1
	if shift < 0 {
		shift = 0
	}
	delay := base
	for i := 0; i < shift && delay < max; i++ {
		delay *= 2
	}
	if delay > max {
		delay = max
	}
	return time.Duration(delay) * time.Millisecond
}

func shouldRetry(detail *kernel.ErrorDetail) bool {
	if detail == nil {
		return false
	}
	if detail.Retryable {
		return true
	}
	switch detail.Type {
	case kernel.ErrRateLimited, kernel.ErrUpstreamTransient:
		return true
	default:
		return false
	}
}

func validatePlan(plan kernel.RetrievalPlan) error {
	if plan.Version == "" {
		plan.Version = "retrieval-plan/v1"
	}
	if plan.Version != "retrieval-plan/v1" {
		return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "unsupported plan version: " + plan.Version}
	}
	if len(plan.Nodes) == 0 {
		return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "plan has no nodes"}
	}
	if len(plan.Nodes) > 200 {
		return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "plan exceeds 200 nodes"}
	}
	nodes := map[string]kernel.PlanNode{}
	for _, n := range plan.Nodes {
		if n.ID == "" {
			return fmt.Errorf("plan node id is required")
		}
		if _, ok := nodes[n.ID]; ok {
			return fmt.Errorf("duplicate plan node %q", n.ID)
		}
		nodes[n.ID] = n
	}
	for _, n := range plan.Nodes {
		for _, d := range n.DependsOn {
			if _, ok := nodes[d]; !ok {
				return fmt.Errorf("node %q depends on unknown node %q", n.ID, d)
			}
		}
	}
	state := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return fmt.Errorf("plan contains a cycle at %q", id)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		for _, d := range nodes[id].DependsOn {
			if err := visit(d); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for id := range nodes {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}
func collectDeps(n kernel.PlanNode, runtime map[string]*runtimeNode) map[string]kernel.NodeResult {
	out := map[string]kernel.NodeResult{}
	for _, id := range n.DependsOn {
		out[id] = runtime[id].result
	}
	return out
}
func dependencyState(n kernel.PlanNode, runtime map[string]*runtimeNode) (ready bool, skip bool) {
	for _, id := range n.DependsOn {
		d := runtime[id]
		if !isTerminal(d.state) {
			return false, false
		}
		if (d.state == kernel.NodeFailed || d.state == kernel.NodeCancelled || d.state == kernel.NodeSkipped) && !n.Optional && !acceptsPartial(n.Op) {
			return false, true
		}
	}
	return true, false
}
func acceptsPartial(op kernel.PlanOp) bool {
	switch op {
	case kernel.PlanMerge, kernel.PlanDedup, kernel.PlanRank, kernel.PlanLimit, kernel.PlanProject, kernel.PlanMapFetch:
		return true
	}
	return false
}
func isTerminal(s kernel.NodeState) bool {
	switch s {
	case kernel.NodeSucceeded, kernel.NodePartial, kernel.NodeEmpty, kernel.NodeFailed, kernel.NodeSkipped, kernel.NodeCancelled:
		return true
	}
	return false
}
func failed(id string, err error) kernel.NodeResult {
	return kernel.NodeResult{NodeID: id, State: kernel.NodeFailed, Error: &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: err.Error()}}
}
func failedDetail(id string, err error) kernel.NodeResult {
	return kernel.NodeResult{NodeID: id, State: kernel.NodeFailed, Error: kernel.DetailFromError(err)}
}
func depCandidates(deps map[string]kernel.NodeResult) []kernel.Candidate {
	keys := make([]string, 0, len(deps))
	for k := range deps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []kernel.Candidate{}
	for _, k := range keys {
		out = append(out, deps[k].Candidates...)
	}
	return out
}
func depArtifacts(deps map[string]kernel.NodeResult) []kernel.Artifact {
	keys := make([]string, 0, len(deps))
	for k := range deps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []kernel.Artifact{}
	for _, k := range keys {
		out = append(out, deps[k].Artifacts...)
	}
	return out
}
func depRelations(deps map[string]kernel.NodeResult) []kernel.Relation {
	keys := make([]string, 0, len(deps))
	for k := range deps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []kernel.Relation{}
	for _, k := range keys {
		out = append(out, deps[k].Relations...)
	}
	return out
}
func defaultProjection(kind kernel.ObjectKind) kernel.ProjectionSet {
	switch kind {
	case kernel.KindDocument:
		return kernel.ProjectionStructure | kernel.ProjectionContent
	case kernel.KindMessage:
		return kernel.ProjectionContext | kernel.ProjectionContent
	case kernel.KindMinute:
		return kernel.ProjectionSummary | kernel.ProjectionRelations
	case kernel.KindMeeting:
		return kernel.ProjectionContent | kernel.ProjectionRelations
	case kernel.KindTask:
		return kernel.ProjectionContent
	default:
		return kernel.ProjectionContent
	}
}
