package budget

import (
	"context"
	"sync"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

// Account is a concurrency-safe request/plan-wide budget ledger. Engine
// operations reuse an Account from context when one is present; standalone
// calls create their own ledger.
type Account interface {
	Reserve(cost kernel.CostEstimate) bool
	ReserveCall() bool
	ReserveFetch(n int) bool
	ReserveExpand(n int) bool
	AddBytes(n int64) bool
	Snapshot() kernel.BudgetState
	Deadline() time.Time
}

type Ledger struct {
	mu    sync.Mutex
	state kernel.BudgetState
}

func New(spec kernel.SearchBudget) *Ledger {
	spec = spec.WithDefaults()
	return &Ledger{state: kernel.BudgetState{
		MaxCalls:    spec.MaxCalls,
		MaxFetches:  spec.MaxFetches,
		MaxExpanded: spec.MaxExpandedNodes,
		MaxBytes:    spec.MaxBytes,
		Deadline:    time.Now().Add(time.Duration(spec.DeadlineMS) * time.Millisecond),
	}}
}

// FromState is primarily useful for replay and continuation machinery.
func FromState(state kernel.BudgetState) *Ledger { return &Ledger{state: state} }

func (l *Ledger) Reserve(cost kernel.CostEstimate) bool {
	if cost.Calls < 0 || cost.Fetches < 0 || cost.Expanded < 0 || cost.Bytes < 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.expiredLocked() ||
		l.state.CallsUsed+cost.Calls > l.state.MaxCalls ||
		l.state.FetchesUsed+cost.Fetches > l.state.MaxFetches ||
		l.state.ExpandedUsed+cost.Expanded > l.state.MaxExpanded ||
		l.state.BytesUsed+cost.Bytes > l.state.MaxBytes {
		return false
	}
	l.state.CallsUsed += cost.Calls
	l.state.FetchesUsed += cost.Fetches
	l.state.ExpandedUsed += cost.Expanded
	l.state.BytesUsed += cost.Bytes
	return true
}

func (l *Ledger) ReserveCall() bool {
	return l.Reserve(kernel.CostEstimate{Calls: 1})
}

func (l *Ledger) ReserveFetch(n int) bool {
	if n <= 0 {
		return true
	}
	return l.Reserve(kernel.CostEstimate{Fetches: n})
}

func (l *Ledger) ReserveExpand(n int) bool {
	if n <= 0 {
		return true
	}
	return l.Reserve(kernel.CostEstimate{Expanded: n})
}

func (l *Ledger) AddBytes(n int64) bool {
	if n <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.expiredLocked() || l.state.BytesUsed+n > l.state.MaxBytes {
		return false
	}
	l.state.BytesUsed += n
	return true
}

func (l *Ledger) Snapshot() kernel.BudgetState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state
}

func (l *Ledger) Deadline() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state.Deadline
}

func (l *Ledger) expiredLocked() bool {
	return !l.state.Deadline.IsZero() && time.Now().After(l.state.Deadline)
}

type contextKey struct{}

func WithAccount(ctx context.Context, account Account) context.Context {
	if account == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, account)
}

func FromContext(ctx context.Context) (Account, bool) {
	if ctx == nil {
		return nil, false
	}
	account, ok := ctx.Value(contextKey{}).(Account)
	return account, ok && account != nil
}

// ForRequest returns a plan-wide account from context, or creates a local
// account for a standalone kernel operation.
func ForRequest(ctx context.Context, spec kernel.SearchBudget) Account {
	if account, ok := FromContext(ctx); ok {
		return account
	}
	return New(spec)
}
