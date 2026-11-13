package engine_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/adapter/mock"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/engine"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/session"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/provider"
)

// Two query nodes run in parallel after a search and add candidates to the same
// session. With the default SQLite store both nodes' candidates must be saved.
func TestPlanParallelQueryNodesPersistBothResultsWithSQLite(t *testing.T) {
	plan := `{
	  "version": "retrieval-plan/v1",
	  "identity": {"mode": "user"},
	  "budget": {"deadline_ms": 15000, "max_calls": 24, "max_fetches": 8},
	  "nodes": [
	    {"id": "root", "op": "search", "request": {"query": "A 项目", "sources": ["minutes"], "limit": 10}},
	    {"id": "q_docs", "op": "query", "depends_on": ["root"], "request": {"source": "docs", "filter": {"query": "A 项目"}}},
	    {"id": "q_msgs", "op": "query", "depends_on": ["root"], "request": {"source": "messages", "filter": {"query": "A 项目"}}}
	  ],
	  "output": ["q_docs", "q_msgs"]
	}`
	path := filepath.Join(t.TempDir(), "sfs.db")
	for run := 0; run < 5; run++ {
		sessionID, want := runPlanWithSQLite(t, path, plan)
		store, err := session.NewSQLiteStore(path, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		record, err := store.Get(context.Background(), sessionID)
		if err != nil {
			t.Fatal(err)
		}
		persisted := map[kernel.SourceID]bool{}
		for _, c := range record.Snapshot(false).Candidates {
			persisted[c.Source] = true
		}
		_ = store.Close()
		for source := range want {
			if !persisted[source] {
				t.Fatalf("run %d: candidates from %s were not persisted; got sources %v", run+1, source, persisted)
			}
		}
	}
}

// runPlanWithSQLite executes the plan and returns its session and the sources
// whose query nodes returned candidates.
func runPlanWithSQLite(t *testing.T, path, raw string) (string, map[kernel.SourceID]bool) {
	t.Helper()
	registry := provider.NewRegistry()
	for _, p := range mock.NewProviders(mock.DemoDataset()) {
		if err := registry.Register(p, true); err != nil {
			t.Fatal(err)
		}
	}
	store, err := session.NewSQLiteStore(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, store, engine.Config{GlobalConcurrency: 4, ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e.Close()
	var plan kernel.RetrievalPlan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatal(err)
	}
	events, results, err := e.Execute(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	result := <-results
	want := map[kernel.SourceID]bool{}
	for _, node := range []string{"q_docs", "q_msgs"} {
		for _, c := range result.Output[node].Candidates {
			want[c.Source] = true
		}
	}
	if len(want) != 2 {
		t.Fatalf("query nodes should return docs and messages candidates, got %v (output %+v)", want, result.Output)
	}
	return result.SessionID, want
}
