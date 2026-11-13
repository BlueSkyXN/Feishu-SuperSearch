package engine_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/engine"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/session"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/provider"
)

// pagedSearchProvider returns pageSize documents per page and, like the real
// adapters, reports ranks relative to the current page.
type pagedSearchProvider struct {
	pageSize int
	total    int
}

func (p *pagedSearchProvider) Descriptor() kernel.ProviderDescriptor {
	return kernel.ProviderDescriptor{ID: "test.paged.docs", Source: kernel.SourceDocs, ObjectKinds: []kernel.ObjectKind{kernel.KindDocument}, Operations: kernel.OperationSet{kernel.OpSearch: true}, ReturnedProjection: kernel.ProjectionHead, SupportsPagination: true, Backend: "test"}
}

func (p *pagedSearchProvider) Search(_ context.Context, req kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	start := 0
	if req.Cursor != "" {
		n, err := strconv.Atoi(req.Cursor)
		if err != nil {
			return kernel.CandidatePage{}, err
		}
		start = n
	}
	end := start + p.pageSize
	if end > p.total {
		end = p.total
	}
	page := kernel.CandidatePage{}
	for i := start; i < end; i++ {
		local := i - start + 1
		id := fmt.Sprintf("doc_%d", i+1)
		ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: req.Identity.Normalized().ScopeKey, Kind: kernel.KindDocument, NativeID: id, ProviderID: "test.paged.docs", Source: kernel.SourceDocs}
		page.Candidates = append(page.Candidates, kernel.Candidate{
			Ref: ref, Source: kernel.SourceDocs, Kind: kernel.KindDocument, Title: id, NativeRank: local, Projection: kernel.ProjectionHead,
			DiscoveredBy: []kernel.Discovery{{ProviderID: "test.paged.docs", Source: kernel.SourceDocs, Rank: local}},
			Provenance:   kernel.Provenance{ProviderID: "test.paged.docs", Operation: "search", SourceRank: local},
		})
	}
	if end < p.total {
		page.HasMore = true
		page.NextCursor = strconv.Itoa(end)
	}
	page.RawCount = len(page.Candidates)
	return page, nil
}

func TestSearchRanksContinueAcrossPages(t *testing.T) {
	registry := provider.NewRegistry()
	if err := registry.Register(&pagedSearchProvider{pageSize: 3, total: 9}, true); err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e.Close()
	ctx := context.Background()
	id := kernel.Identity{Mode: kernel.IdentityAuto}

	snap, err := e.Search(ctx, kernel.SearchRequest{Query: "无关查询", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: id, Limit: 20, LimitPerSource: 3, Strategy: kernel.SearchStrategy{Pagination: "fixed"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 2}})
	if err != nil {
		t.Fatal(err)
	}
	assertRankOrder(t, snap.Candidates, 6)

	more, err := e.Continue(ctx, kernel.ContinueRequest{SessionID: snap.SessionID, Identity: id, MaxAdditionalPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	assertRankOrder(t, more.Candidates, 9)
}

// assertRankOrder checks that candidates are doc_1..doc_n in order and that
// every rank field equals the position in the whole result list.
func assertRankOrder(t *testing.T, candidates []kernel.Candidate, n int) {
	t.Helper()
	if len(candidates) != n {
		t.Fatalf("got %d candidates, want %d", len(candidates), n)
	}
	for i, c := range candidates {
		want := i + 1
		if c.Title != fmt.Sprintf("doc_%d", want) {
			t.Fatalf("position %d holds %s, want doc_%d", want, c.Title, want)
		}
		if c.NativeRank != want || c.Provenance.SourceRank != want || len(c.DiscoveredBy) != 1 || c.DiscoveredBy[0].Rank != want {
			t.Fatalf("%s ranks native=%d provenance=%d discovered=%+v, want %d", c.Title, c.NativeRank, c.Provenance.SourceRank, c.DiscoveredBy, want)
		}
	}
}

// failingSecondPageProvider returns one page with a cursor and fails the next.
type failingSecondPageProvider struct{ pagedSearchProvider }

func (p *failingSecondPageProvider) Search(ctx context.Context, req kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	if req.Cursor != "" {
		return kernel.CandidatePage{}, &kernel.ErrorDetail{Type: kernel.ErrDeadlineExceeded, Message: "page 2 timed out"}
	}
	return p.pagedSearchProvider.Search(ctx, req)
}

func TestSearchMarksSourcePartialWhenLaterPageFails(t *testing.T) {
	registry := provider.NewRegistry()
	if err := registry.Register(&failingSecondPageProvider{pagedSearchProvider{pageSize: 3, total: 9}}, true); err != nil {
		t.Fatal(err)
	}
	e := engine.New(registry, session.NewMemoryStore(time.Hour), engine.Config{ProviderTimeout: time.Second, SessionTTL: time.Hour})
	defer e.Close()
	snap, err := e.Search(context.Background(), kernel.SearchRequest{Query: "q", Sources: []kernel.SourceID{kernel.SourceDocs}, Identity: kernel.Identity{Mode: kernel.IdentityAuto}, Limit: 20, LimitPerSource: 3, Strategy: kernel.SearchStrategy{Pagination: "fixed"}, Budget: kernel.SearchBudget{MaxPagesPerSource: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Candidates) != 3 || len(snap.Sources) != 1 {
		t.Fatalf("snapshot=%+v", snap)
	}
	run := snap.Sources[0]
	if run.Status != kernel.StatusPartial || run.Error == nil || run.Error.Type != kernel.ErrDeadlineExceeded {
		t.Fatalf("source run=%+v", run)
	}
	if !snap.Partial || snap.Stats.SourcesFailed != 0 || snap.Stats.SourcesCompleted != 1 {
		t.Fatalf("partial=%v stats=%+v", snap.Partial, snap.Stats)
	}
}
