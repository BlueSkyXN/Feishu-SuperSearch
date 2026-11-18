package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	aiadapter "github.com/BlueSkyXN/Feishu-SuperSearch/ai/openaicompat"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/engine"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/research"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/session"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
	rulesplanner "github.com/BlueSkyXN/Feishu-SuperSearch/planner/rules"
	"github.com/BlueSkyXN/Feishu-SuperSearch/provider"
)

type relationTestRelationsProvider struct {
	workflowProvider
	entered     chan string
	release     map[string]chan struct{}
	beforeFetch func(context.Context) error
}

func (p *relationTestRelationsProvider) Descriptor() kernel.ProviderDescriptor {
	d := p.workflowProvider.Descriptor()
	d.Operations[kernel.OpExpand] = true
	d.FetchableProjection |= kernel.ProjectionRelations
	return d
}
func (p *relationTestRelationsProvider) Search(ctx context.Context, req kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	page, err := p.workflowProvider.Search(ctx, req)
	for i := range page.Candidates {
		page.Candidates[i].AvailableProjection |= kernel.ProjectionRelations
	}
	return page, err
}

func (p *relationTestRelationsProvider) relation(ref kernel.ObjectRef) kernel.Relation {
	child := ref
	child.NativeID = "child_" + ref.NativeID
	child.CanonicalID = kernel.BuildCanonicalID(ref.ScopeKey, child.Kind, child.NativeID)
	return kernel.Relation{From: ref, To: child, Type: "document.links_to_document", Confidence: 1}
}
func (p *relationTestRelationsProvider) Expand(ctx context.Context, r kernel.ProviderExpandRequest) ([]kernel.Relation, error) {
	if release := p.release[r.Ref.NativeID]; release != nil {
		p.entered <- r.Ref.NativeID
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return []kernel.Relation{p.relation(r.Ref)}, nil
}
func (p *relationTestRelationsProvider) Fetch(ctx context.Context, rs []kernel.ProviderFetchRequest) ([]kernel.Artifact, error) {
	if p.beforeFetch != nil {
		if err := p.beforeFetch(ctx); err != nil {
			return nil, err
		}
	}
	out, err := p.workflowProvider.Fetch(ctx, rs)
	for i := range out {
		out[i].Relations = []kernel.Relation{p.relation(out[i].Ref)}
	}
	return out, err
}
func relationTestEngine(t *testing.T, p kernel.Provider, kind string) *engine.Engine {
	e, _ := relationTestEngineAndStore(t, p, kind)
	return e
}
func relationTestEngineAndStore(t *testing.T, p kernel.Provider, kind string, paths ...string) (*engine.Engine, session.Store) {
	t.Helper()
	registry := provider.NewRegistry()
	if err := registry.Register(p, true); err != nil {
		t.Fatal(err)
	}
	var store session.Store = session.NewMemoryStore(time.Hour)
	if kind == "sqlite" {
		path := filepath.Join(t.TempDir(), "sessions.db")
		if len(paths) > 0 {
			path = paths[0]
		}
		s, err := session.NewSQLiteStore(path, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		store = s
	}
	e := engine.New(registry, store, engine.Config{ProviderTimeout: 3 * time.Second})
	t.Cleanup(func() { _ = e.Close() })
	return e, store
}
func TestConcurrentExpandPreservesBothRelations(t *testing.T) {
	for _, kind := range []string{"memory", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			p := &relationTestRelationsProvider{entered: make(chan string, 2), release: map[string]chan struct{}{}}
			e := relationTestEngine(t, p, kind)
			search, err := e.Search(ctx, workflowSearch("root", ""))
			if err != nil {
				t.Fatal(err)
			}
			refs := []kernel.ObjectRef{search.Candidates[0].Ref, search.Candidates[1].Ref}
			for _, ref := range refs {
				p.release[ref.NativeID] = make(chan struct{})
			}
			type outcome struct {
				batch kernel.RelationBatch
				err   error
			}
			done := make(chan outcome, 2)
			for _, ref := range refs {
				go func(ref kernel.ObjectRef) {
					batch, err := e.Expand(ctx, kernel.ExpandRequest{SessionID: search.SessionID, Ref: ref, MaxDepth: 1})
					done <- outcome{batch, err}
				}(ref)
			}
			for range 2 {
				select {
				case <-p.entered:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			close(p.release[refs[0].NativeID])
			one := <-done
			if one.err != nil || len(one.batch.Relations) != 1 {
				t.Fatalf("first=%+v", one)
			}
			close(p.release[refs[1].NativeID])
			two := <-done
			if two.err != nil || len(two.batch.Relations) != 1 {
				t.Fatalf("second=%+v", two)
			}
			snap, err := e.Session(ctx, search.SessionID, kernel.Identity{})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("successful expansions=2, returned relations=2, persisted relations=%d", len(snap.Relations))
			if len(snap.Relations) != 2 {
				t.Fatalf("one successful Expand overwrote the other result")
			}
		})
	}
}
func TestFetchRegistersRelationTargets(t *testing.T) {
	p := &relationTestRelationsProvider{}
	e := relationTestEngine(t, p, "memory")
	ctx := context.Background()
	search, err := e.Search(ctx, workflowSearch("root", ""))
	if err != nil {
		t.Fatal(err)
	}
	ref := search.Candidates[0].Ref
	fetched, err := e.Fetch(ctx, kernel.FetchBatchRequest{SessionID: search.SessionID, Items: []kernel.FetchRequest{{Ref: ref, Projection: kernel.ProjectionContent | kernel.ProjectionRelations}}})
	if err != nil || len(fetched.Items) != 1 || fetched.Items[0].Artifact == nil {
		t.Fatalf("root fetch=%+v err=%v", fetched, err)
	}
	child := fetched.Items[0].Artifact.Relations[0].To
	childReq := kernel.FetchBatchRequest{SessionID: search.SessionID, Items: []kernel.FetchRequest{{Ref: child, Projection: kernel.ProjectionContent}}}
	before, beforeErr := e.Fetch(ctx, childReq)
	snap, _ := e.Session(ctx, search.SessionID, kernel.Identity{})
	t.Logf("Artifact.Relations=%d, Session.Relations=%d, child Fetch error=%v, item error=%v", len(fetched.Items[0].Artifact.Relations), len(snap.Relations), beforeErr, before.Items[0].Error)
	expanded, err := e.Expand(ctx, kernel.ExpandRequest{SessionID: search.SessionID, Ref: ref, MaxDepth: 1})
	if err != nil || len(expanded.Relations) != 1 {
		t.Fatalf("control expand=%+v err=%v", expanded, err)
	}
	after, err := e.Fetch(ctx, childReq)
	if err != nil || after.Items[0].Artifact == nil {
		t.Fatalf("control child Fetch=%+v err=%v", after, err)
	}
	t.Log("the same child Fetch succeeds after a separate Expand call")
	if beforeErr != nil || before.Items[0].Artifact == nil {
		t.Fatal("relation returned by Fetch was not registered as a discovered object")
	}
}

type relationTestHistoryProvider struct{ relationTestRelationsProvider }

func (p *relationTestHistoryProvider) Search(ctx context.Context, r kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	if r.Query != "old" {
		return kernel.CandidatePage{}, nil
	}
	return p.relationTestRelationsProvider.Search(ctx, r)
}

type relationTestReusePlanner struct{ sessionID string }

func (relationTestReusePlanner) Name() string { return "reuse-test" }
func (p relationTestReusePlanner) Plan(ctx context.Context, r planner.UserRequest, caps kernel.CapabilitySnapshot) (kernel.RetrievalPlan, error) {
	plan, err := (rulesplanner.Planner{}).Plan(ctx, r, caps)
	plan.Session = kernel.PlanSession{ID: p.sessionID, Reuse: true}
	return plan, err
}
func TestResearchKeepsEvidenceWithinCurrentPlan(t *testing.T) {
	for _, deep := range []bool{false, true} {
		t.Run(fmt.Sprintf("deep=%v", deep), func(t *testing.T) {
			p := &relationTestHistoryProvider{}
			e := relationTestEngine(t, p, "memory")
			ctx := context.Background()
			old, err := e.Search(ctx, workflowSearch("old", ""))
			if err != nil {
				t.Fatal(err)
			}
			oldFetch, err := e.Fetch(ctx, kernel.FetchBatchRequest{SessionID: old.SessionID, Items: []kernel.FetchRequest{{Ref: old.Candidates[0].Ref, Projection: kernel.ProjectionContent}}})
			if err != nil || oldFetch.Items[0].Artifact == nil {
				t.Fatal("seed Fetch failed", err)
			}
			req := research.Request{UserRequest: planner.UserRequest{Query: "unrelated", Sources: []kernel.SourceID{kernel.SourceDocs}, Deep: deep, FetchTopK: 1, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}}}
			result, err := (research.Service{Kernel: e, Planner: relationTestReusePlanner{old.SessionID}}).Run(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("current search results=%d, reported candidates=%d, artifacts=%d, evidence=%d, partial=%v", len(result.PlanResult.Nodes["search"].Candidates), len(result.CandidatePack.Candidates), len(result.ArtifactPack.Artifacts), len(result.EvidencePack.Evidence), result.PlanResult.Partial)
			if len(result.EvidencePack.Evidence) > 0 {
				t.Logf("stale evidence=%q", result.EvidencePack.Evidence[0].Text)
			}
			if len(result.CandidatePack.Candidates) > 0 || len(result.EvidencePack.Evidence) > 0 || len(result.ArtifactPack.Artifacts) > 0 || len(result.ArtifactPack.Relations) > 0 || result.PlanResult.Partial {
				t.Fatal("empty current search inherited unrelated historical content")
			}
			if deep && result.PlanResult.Nodes["fetch_top"].State != kernel.NodeEmpty {
				t.Fatalf("empty fetch node=%+v", result.PlanResult.Nodes["fetch_top"])
			}
		})
	}
}
func TestAIResearchFallsBackWithinPolicy(t *testing.T) {
	var calls atomic.Int32
	bodies := make(chan map[string]any, 2)
	modelPlan := `{"version":"retrieval-plan/v1","nodes":[{"id":"query","op":"query","request":{"source":"docs","filter":{"query":"budget"}}}],"output":["query"]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies <- body
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": modelPlan}}}})
	}))
	defer server.Close()
	client, err := aiadapter.NewClient(aiadapter.ClientConfig{BaseURL: server.URL, APIKey: "audit-key", Timeout: time.Second, HTTPClient: server.Client(), JSONSchema: true})
	if err != nil {
		t.Fatal(err)
	}
	aiPlanner := aiadapter.Planner{Client: client, Model: "audit", Fallback: rulesplanner.Planner{}}
	p := &workflowProvider{}
	e := relationTestEngine(t, p, "memory")
	req := research.Request{UserRequest: planner.UserRequest{Query: "budget", Sources: []kernel.SourceID{kernel.SourceDocs}, Deep: true, FetchTopK: 1, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}}}
	result, aiErr := (research.Service{Kernel: e, Planner: aiPlanner}).Run(context.Background(), req)
	t.Logf("AI requests=%d, Research error=%v", calls.Load(), aiErr)
	control, err := (research.Service{Kernel: e, Planner: rulesplanner.Planner{}}).Run(context.Background(), req)
	if err != nil || len(control.EvidencePack.Evidence) == 0 {
		t.Fatalf("rules control result=%+v err=%v", control, err)
	}
	if aiErr != nil {
		t.Fatal("schema-valid and advertised Query operation aborted Research; configured rules fallback was not used")
	}
	if calls.Load() != 2 || result.Planner != "rules" || result.Plan.Metadata["fallback_from"] != "ai" || len(result.EvidencePack.Evidence) == 0 {
		t.Fatalf("fallback calls=%d result=%+v", calls.Load(), result)
	}
	for range 2 {
		body := <-bodies
		schema := body["response_format"].(map[string]any)["json_schema"].(map[string]any)["schema"].(map[string]any)
		enum := schema["$defs"].(map[string]any)["node"].(map[string]any)["properties"].(map[string]any)["op"].(map[string]any)["enum"].([]any)
		want := []any{"search", "merge", "dedup", "rank", "limit", "project", "map_fetch"}
		if !reflect.DeepEqual(enum, want) {
			t.Fatalf("advertised schema operations=%v", enum)
		}
		messages := body["messages"].([]any)
		var input map[string]any
		if err := json.Unmarshal([]byte(messages[1].(map[string]any)["content"].(string)), &input); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(input["allowed_operations"], want) {
			t.Fatalf("advertised prompt operations=%v", input["allowed_operations"])
		}
	}
}

func TestResearchPreservesBusinessKeywords(t *testing.T) {
	for _, query := range []string{"请假申请制度", "API 请求错误", "需求分析报告"} {
		t.Run(query, func(t *testing.T) {
			p := &workflowProvider{}
			e := relationTestEngine(t, p, "memory")
			_, err := (research.Service{Kernel: e}).Run(context.Background(), research.Request{UserRequest: planner.UserRequest{Query: query, Sources: []kernel.SourceID{kernel.SourceDocs}, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			if len(p.searches) != 1 {
				t.Fatalf("provider calls=%d", len(p.searches))
			}
			actual := p.searches[0].Query
			t.Logf("business query=%q, actual provider query=%q", query, actual)
			if actual != query {
				t.Fatal("business keyword was altered by instruction-word removal")
			}
		})
	}
}

func TestConcurrentFetchAndExpandPreserveRelations(t *testing.T) {
	for _, kind := range []string{"memory", "sqlite"} {
		for _, fetchFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fetch_first=%v", kind, fetchFirst), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				fetchEntered, fetchRelease := make(chan struct{}), make(chan struct{})
				p := &relationTestRelationsProvider{entered: make(chan string, 1), release: map[string]chan struct{}{}, beforeFetch: func(ctx context.Context) error {
					close(fetchEntered)
					select {
					case <-fetchRelease:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}}
				path := filepath.Join(t.TempDir(), "sessions.db")
				e, store := relationTestEngineAndStore(t, p, kind, path)
				search, err := e.Search(ctx, workflowSearch("root", ""))
				if err != nil {
					t.Fatal(err)
				}
				fetchRef, expandRef := search.Candidates[0].Ref, search.Candidates[1].Ref
				expandRelease := make(chan struct{})
				p.release[expandRef.NativeID] = expandRelease
				fetchDone, expandDone := make(chan error, 1), make(chan error, 1)
				go func() {
					batch, err := e.Fetch(ctx, kernel.FetchBatchRequest{SessionID: search.SessionID, Items: []kernel.FetchRequest{{Ref: fetchRef, Projection: kernel.ProjectionRelations}}})
					if err == nil && (batch.Partial || len(batch.Items) != 1 || batch.Items[0].Artifact == nil) {
						err = fmt.Errorf("fetch=%+v", batch)
					}
					fetchDone <- err
				}()
				go func() {
					_, err := e.Expand(ctx, kernel.ExpandRequest{SessionID: search.SessionID, Ref: expandRef, MaxDepth: 1})
					expandDone <- err
				}()
				select {
				case <-fetchEntered:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				select {
				case <-p.entered:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				firstRelease, lastRelease, firstDone, lastDone := expandRelease, fetchRelease, expandDone, fetchDone
				if fetchFirst {
					firstRelease, lastRelease, firstDone, lastDone = fetchRelease, expandRelease, fetchDone, expandDone
				}
				close(firstRelease)
				if err := <-firstDone; err != nil {
					t.Fatal(err)
				}
				close(lastRelease)
				if err := <-lastDone; err != nil {
					t.Fatal(err)
				}
				// SQLite must retain the union after reopening, not just in its live Record.
				if kind == "sqlite" {
					if err := e.Close(); err != nil {
						t.Fatal(err)
					}
					_, store = relationTestEngineAndStore(t, p, kind, path)
				}
				snap, err := store.Get(ctx, search.SessionID)
				if err != nil || len(snap.Snapshot(false).Relations) != 2 {
					t.Fatalf("relations were lost: record=%+v err=%v", snap, err)
				}
			})
		}
	}
}

func TestLegacyArtifactRelationsRemainDiscoverable(t *testing.T) {
	for _, kind := range []string{"memory", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			p := &relationTestRelationsProvider{}
			e, store := relationTestEngineAndStore(t, p, kind)
			ctx := context.Background()
			search, err := e.Search(ctx, workflowSearch("root", ""))
			if err != nil {
				t.Fatal(err)
			}
			root := search.Candidates[0].Ref
			child := p.relation(root).To
			rec, err := store.Get(ctx, search.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			rec.WithLock(func(r *session.Record) {
				unscoped := child
				unscoped.ScopeKey, unscoped.CanonicalID = "", ""
				r.Artifacts[root.CanonicalID] = kernel.Artifact{Ref: root, Projection: kernel.ProjectionRelations, Relations: []kernel.Relation{{Type: "document.links_to_document", To: unscoped}}}
				r.Relations = nil
			})
			if err := store.Save(ctx, rec); err != nil {
				t.Fatal(err)
			}
			batch, err := e.Fetch(ctx, kernel.FetchBatchRequest{SessionID: search.SessionID, Items: []kernel.FetchRequest{{Ref: child, Projection: kernel.ProjectionContent}}})
			if err != nil || len(batch.Items) != 1 || batch.Items[0].Artifact == nil || batch.Items[0].Artifact.Ref.ScopeKey != root.ScopeKey {
				t.Fatalf("legacy target Fetch=%+v err=%v", batch, err)
			}
			if _, err := e.Expand(ctx, kernel.ExpandRequest{SessionID: search.SessionID, Ref: child}); err != nil {
				t.Fatal(err)
			}
			unknown := child
			unknown.NativeID = "undiscovered"
			unknown.CanonicalID = kernel.BuildCanonicalID(root.ScopeKey, unknown.Kind, unknown.NativeID)
			batch, err = e.Fetch(ctx, kernel.FetchBatchRequest{SessionID: search.SessionID, Items: []kernel.FetchRequest{{Ref: unknown, Projection: kernel.ProjectionContent}}})
			if err == nil || len(batch.Items) != 1 || batch.Items[0].Error == nil || batch.Items[0].Error.Type != kernel.ErrInvalidRequest {
				t.Fatalf("unknown target was accepted: batch=%+v err=%v", batch, err)
			}
		})
	}
}

func TestResearchReusesOnlySelectedCachedArtifacts(t *testing.T) {
	for _, deep := range []bool{false, true} {
		t.Run(fmt.Sprintf("deep=%v", deep), func(t *testing.T) {
			p := &relationTestHistoryProvider{}
			e := relationTestEngine(t, p, "memory")
			ctx := context.Background()
			search, err := e.Search(ctx, workflowSearch("old", ""))
			if err != nil {
				t.Fatal(err)
			}
			items := []kernel.FetchRequest{}
			for _, c := range search.Candidates {
				items = append(items, kernel.FetchRequest{Ref: c.Ref, Projection: kernel.ProjectionContent | kernel.ProjectionStructure | kernel.ProjectionRelations})
			}
			seed, err := e.Fetch(ctx, kernel.FetchBatchRequest{SessionID: search.SessionID, Items: items})
			if err != nil || seed.Partial || len(seed.Items) != 2 {
				t.Fatalf("seed=%+v err=%v", seed, err)
			}
			result, err := (research.Service{Kernel: e, Planner: relationTestReusePlanner{search.SessionID}}).Run(ctx, research.Request{UserRequest: planner.UserRequest{Query: "old", Sources: []kernel.SourceID{kernel.SourceDocs}, Deep: deep, FetchTopK: 1, Budget: kernel.SearchBudget{MaxPagesPerSource: 1}}})
			if err != nil || result.PlanResult.Partial || len(result.CandidatePack.Candidates) != 2 {
				t.Fatalf("research=%+v err=%v", result, err)
			}
			want := 0
			if deep {
				want = 1
			}
			if len(result.ArtifactPack.Artifacts) != want || len(result.EvidencePack.Evidence) != want || len(result.ArtifactPack.Relations) != want {
				t.Fatalf("selected cache artifacts=%+v evidence=%+v", result.ArtifactPack, result.EvidencePack)
			}
			if deep && result.ArtifactPack.Artifacts[0].Ref.CanonicalID != search.Candidates[0].Ref.CanonicalID {
				t.Fatal("returned an artifact outside current top_k")
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			if len(p.fetched) != 2 {
				t.Fatalf("cached artifact was fetched again: %v", p.fetched)
			}
		})
	}
}

func TestAIResearchRepairsDisallowedSource(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		var body struct{ Messages []struct{ Content string } }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if call == 2 && (len(body.Messages) < 2 || !strings.Contains(body.Messages[1].Content, "source messages is outside the caller source set")) {
			t.Errorf("repair omitted the concrete policy failure: %+v", body)
		}
		source := "messages"
		if call > 1 {
			source = "docs"
		}
		plan := fmt.Sprintf(`{"version":"retrieval-plan/v1","identity":{"mode":"bot"},"budget":{"max_calls":999},"nodes":[{"id":"search","op":"search","request":{"query":"changed","sources":[%q],"filters":{}}}],"output":["search"]}`, source)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": plan}}}})
	}))
	defer server.Close()
	client, err := aiadapter.NewClient(aiadapter.ClientConfig{BaseURL: server.URL, APIKey: "test-key", Timeout: time.Second, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	p := &workflowProvider{}
	e := relationTestEngine(t, p, "memory")
	request := planner.UserRequest{Query: "budget", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: kernel.Identity{Mode: kernel.IdentityUser, Profile: "test"}, Filters: kernel.SearchFilters{ChatIDs: []string{"chat_filter"}}, Budget: kernel.SearchBudget{MaxCalls: 2, MaxPagesPerSource: 1}}
	result, err := (research.Service{Kernel: e, Planner: aiadapter.Planner{Client: client, Model: "test", Fallback: rulesplanner.Planner{}}}).Run(context.Background(), research.Request{UserRequest: request})
	if err != nil || calls.Load() != 2 || result.Planner != "ai" || result.Plan.Metadata["fallback_from"] != nil || result.Plan.Budget.MaxCalls != 2 {
		t.Fatalf("calls=%d result=%+v err=%v", calls.Load(), result, err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.searches) != 1 || p.searches[0].Query != request.Query || p.searches[0].Identity != request.Identity.Normalized() || !reflect.DeepEqual(p.searches[0].Filters, request.Filters) {
		t.Fatalf("caller constraints were lost: %+v", p.searches)
	}
}
