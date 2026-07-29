package budget

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestLedgerReservationIsAtomicAtBudgetBoundary(t *testing.T) {
	ledger := New(kernel.SearchBudget{DeadlineMS: 5000, MaxCalls: 2, MaxFetches: 3, MaxExpandedNodes: 4, MaxBytes: 10})
	if !ledger.Reserve(kernel.CostEstimate{Calls: 1, Fetches: 2, Expanded: 3, Bytes: 6}) {
		t.Fatal("initial reservation failed")
	}
	before := ledger.Snapshot()
	if ledger.Reserve(kernel.CostEstimate{Calls: 1, Fetches: 2}) {
		t.Fatal("reservation exceeding fetch budget succeeded")
	}
	if after := ledger.Snapshot(); after != before {
		t.Fatalf("failed reservation was not atomic: before=%+v after=%+v", before, after)
	}
	if ledger.Reserve(kernel.CostEstimate{Calls: -1}) {
		t.Fatal("negative reservation succeeded")
	}
	if !ledger.ReserveCall() || ledger.ReserveCall() {
		t.Fatal("call budget boundary was not enforced")
	}
	if !ledger.ReserveFetch(0) || !ledger.ReserveExpand(0) || !ledger.AddBytes(0) {
		t.Fatal("zero-cost operations should not consume budget")
	}
	if !ledger.ReserveFetch(1) || ledger.ReserveFetch(1) {
		t.Fatal("fetch budget boundary was not enforced")
	}
	if !ledger.ReserveExpand(1) || ledger.ReserveExpand(1) {
		t.Fatal("expand budget boundary was not enforced")
	}
	if !ledger.AddBytes(4) || ledger.AddBytes(1) {
		t.Fatal("byte budget boundary was not enforced")
	}
	if ledger.Deadline().IsZero() {
		t.Fatal("deadline was not initialized")
	}
}

func TestExpiredLedgerAndContextReuse(t *testing.T) {
	expired := FromState(kernel.BudgetState{MaxCalls: 5, MaxFetches: 5, MaxExpanded: 5, MaxBytes: 50, Deadline: time.Now().Add(-time.Second)})
	if expired.ReserveCall() || expired.AddBytes(1) {
		t.Fatal("expired ledger accepted new work")
	}
	if account, ok := FromContext(nil); ok || account != nil {
		t.Fatal("nil context returned an account")
	}
	ctx := context.Background()
	if got := WithAccount(ctx, nil); got != ctx {
		t.Fatal("nil account should leave context unchanged")
	}
	ctx = WithAccount(ctx, expired)
	if account, ok := FromContext(ctx); !ok || account != expired {
		t.Fatal("context account was not recovered")
	}
	if account := ForRequest(ctx, kernel.SearchBudget{}); account != expired {
		t.Fatal("ForRequest did not reuse plan-wide account")
	}
	if account := ForRequest(context.Background(), kernel.SearchBudget{MaxCalls: 1}); account == nil || account == expired {
		t.Fatal("ForRequest did not create a standalone account")
	}
}

func TestConcurrentReservationsNeverExceedLimit(t *testing.T) {
	ledger := New(kernel.SearchBudget{DeadlineMS: 5000, MaxCalls: 25})
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ledger.ReserveCall()
		}()
	}
	wg.Wait()
	if got := ledger.Snapshot().CallsUsed; got != 25 {
		t.Fatalf("calls_used=%d want 25", got)
	}
}
