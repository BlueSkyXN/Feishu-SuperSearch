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

// Two search nodes share the plan's session. The later search must not remove
// the earlier one's candidates, otherwise map_fetch rejects their refs as not
// present in the session.
func TestPlanTwoSearchNodesKeepBothResultsForMapFetch(t *testing.T) {
	plan := `{
	  "version": "retrieval-plan/v1",
	  "identity": {"mode": "user"},
	  "budget": {"deadline_ms": 15000, "max_calls": 24, "max_fetches": 8},
	  "nodes": [
	    {"id": "s_docs", "op": "search", "priority": 100, "request": {"query": "A 项目", "sources": ["docs"], "limit": 10}},
	    {"id": "s_msgs", "op": "search", "priority": 90, "request": {"query": "A 项目", "sources": ["messages"], "limit": 10}},
	    {"id": "merge", "op": "merge", "depends_on": ["s_docs", "s_msgs"]},
	    {"id": "fetch", "op": "map_fetch", "depends_on": ["merge"], "max_items": 6, "request": {"top_k": 6, "max_items": 6}}
	  ],
	  "output": ["s_docs", "s_msgs", "fetch"]
	}`
	for _, store := range []struct {
		name string
		open func(t *testing.T) session.Store
	}{
		{"memory", func(t *testing.T) session.Store { return session.NewMemoryStore(time.Hour) }},
		{"sqlite", func(t *testing.T) session.Store {
			s, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sfs.db"), time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}},
	} {
		t.Run(store.name, func(t *testing.T) {
			registry := provider.NewRegistry()
			for _, p := range mock.NewProviders(mock.DemoDataset()) {
				if err := registry.Register(p, true); err != nil {
					t.Fatal(err)
				}
			}
			e := engine.New(registry, store.open(t), engine.Config{GlobalConcurrency: 4, ProviderTimeout: time.Second, SessionTTL: time.Hour})
			defer e.Close()
			var p kernel.RetrievalPlan
			if err := json.Unmarshal([]byte(plan), &p); err != nil {
				t.Fatal(err)
			}
			events, results, err := e.Execute(context.Background(), p)
			if err != nil {
				t.Fatal(err)
			}
			for range events {
			}
			result := <-results
			fetch := result.Output["fetch"]
			if fetch.State != kernel.NodeSucceeded || fetch.Error != nil {
				t.Fatalf("map_fetch state=%s error=%+v", fetch.State, fetch.Error)
			}
			sources := map[kernel.SourceID]bool{}
			for _, a := range fetch.Artifacts {
				sources[a.Ref.Source] = true
			}
			if !sources[kernel.SourceDocs] || !sources[kernel.SourceMessages] {
				t.Fatalf("map_fetch read sources %v, want docs and messages", sources)
			}
		})
	}
}
