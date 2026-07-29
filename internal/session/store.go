package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type Record struct {
	mu sync.RWMutex

	ID        string
	ScopeKey  string
	CreatedAt time.Time
	ExpiresAt time.Time
	Request   *kernel.SearchRequest

	Candidates map[string]kernel.Candidate
	Artifacts  map[string]kernel.Artifact
	Relations  []kernel.Relation
	Frontiers  map[kernel.SourceID]kernel.Continuation
	SourceRuns []kernel.SourceRun
	Budget     kernel.BudgetState
	Events     []kernel.RetrievalEvent
}

func NewRecord(scope string, ttl time.Duration) *Record {
	now := time.Now().UTC()
	return &Record{
		ID: newID("rs"), ScopeKey: scope, CreatedAt: now, ExpiresAt: now.Add(ttl),
		Candidates: map[string]kernel.Candidate{}, Artifacts: map[string]kernel.Artifact{},
		Frontiers: map[kernel.SourceID]kernel.Continuation{},
	}
}

func Restore(s kernel.SessionSnapshot) *Record {
	r := &Record{
		ID: s.ID, ScopeKey: s.ScopeKey, CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt,
		Request: s.Request, Candidates: map[string]kernel.Candidate{}, Artifacts: map[string]kernel.Artifact{},
		Relations: append([]kernel.Relation(nil), s.Relations...), Frontiers: map[kernel.SourceID]kernel.Continuation{},
		SourceRuns: append([]kernel.SourceRun(nil), s.SourceRuns...), Budget: s.Budget,
		Events: append([]kernel.RetrievalEvent(nil), s.Events...),
	}
	for _, c := range s.Candidates {
		r.Candidates[c.Ref.CanonicalID] = c
	}
	for _, a := range s.Artifacts {
		r.Artifacts[a.Ref.CanonicalID] = a
	}
	for k, v := range s.Frontiers {
		r.Frontiers[k] = v
	}
	return r
}

func (r *Record) WithLock(fn func(*Record))  { r.mu.Lock(); defer r.mu.Unlock(); fn(r) }
func (r *Record) WithRLock(fn func(*Record)) { r.mu.RLock(); defer r.mu.RUnlock(); fn(r) }

func (r *Record) Snapshot(includeEvents bool) kernel.SessionSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	candidates := make([]kernel.Candidate, 0, len(r.Candidates))
	for _, c := range r.Candidates {
		candidates = append(candidates, c)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].FusedScore == candidates[j].FusedScore {
			return candidates[i].Ref.CanonicalID < candidates[j].Ref.CanonicalID
		}
		return candidates[i].FusedScore > candidates[j].FusedScore
	})
	artifacts := make([]kernel.Artifact, 0, len(r.Artifacts))
	for _, a := range r.Artifacts {
		artifacts = append(artifacts, a)
	}
	sort.SliceStable(artifacts, func(i, j int) bool { return artifacts[i].Ref.CanonicalID < artifacts[j].Ref.CanonicalID })
	frontiers := make(map[kernel.SourceID]kernel.Continuation, len(r.Frontiers))
	for k, v := range r.Frontiers {
		frontiers[k] = v
	}
	var events []kernel.RetrievalEvent
	if includeEvents {
		events = append(events, r.Events...)
	}
	var req *kernel.SearchRequest
	if r.Request != nil {
		copyReq := kernel.CloneJSON(*r.Request)
		req = &copyReq
	}
	return kernel.SessionSnapshot{
		ID: r.ID, ScopeKey: r.ScopeKey, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt,
		Request: req, Candidates: candidates, Artifacts: artifacts,
		Relations: append([]kernel.Relation(nil), r.Relations...), Frontiers: frontiers,
		SourceRuns: append([]kernel.SourceRun(nil), r.SourceRuns...), Budget: r.Budget, Events: events,
	}
}

func (r *Record) AddEvent(event kernel.RetrievalEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if event.ID == "" {
		event.ID = newID("ev")
	}
	if event.SessionID == "" {
		event.SessionID = r.ID
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	r.Events = append(r.Events, event)
	if len(r.Events) > 2000 {
		r.Events = append([]kernel.RetrievalEvent(nil), r.Events[len(r.Events)-2000:]...)
	}
}

func NewEventID() string { return newID("ev") }

func newID(prefix string) string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

type Store interface {
	Create(context.Context, string) (*Record, error)
	Get(context.Context, string) (*Record, error)
	Save(context.Context, *Record) error
	Delete(context.Context, string) error
	List(context.Context) ([]kernel.SessionSnapshot, error)
	Close() error
}

type CapabilityStore interface {
	SaveCapabilities(context.Context, kernel.CapabilitySnapshot) error
	LoadCapabilities(context.Context) (kernel.CapabilitySnapshot, error)
}

type QueryHistoryEntry struct {
	ID        int64             `json:"id"`
	SessionID string            `json:"session_id"`
	ScopeKey  string            `json:"scope_key"`
	Query     string            `json:"query"`
	Sources   []kernel.SourceID `json:"sources,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	ExpiresAt time.Time         `json:"expires_at"`
}

type QueryHistoryStore interface {
	RecordQuery(context.Context, string, kernel.SearchRequest) error
	ListQueryHistory(context.Context, int) ([]QueryHistoryEntry, error)
}

type MemoryStore struct {
	mu      sync.RWMutex
	ttl     time.Duration
	records map[string]*Record
}

func NewMemoryStore(ttl time.Duration) *MemoryStore {
	if ttl <= 0 {
		ttl = 45 * time.Minute
	}
	return &MemoryStore{ttl: ttl, records: map[string]*Record{}}
}

func (s *MemoryStore) Create(_ context.Context, scope string) (*Record, error) {
	r := NewRecord(scope, s.ttl)
	s.mu.Lock()
	s.records[r.ID] = r
	s.mu.Unlock()
	return r, nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (*Record, error) {
	s.mu.RLock()
	r := s.records[id]
	s.mu.RUnlock()
	if r == nil {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrNotFound, Message: "session not found"}
	}
	if time.Now().After(r.ExpiresAt) {
		_ = s.Delete(context.Background(), id)
		return nil, &kernel.ErrorDetail{Type: kernel.ErrNotFound, Message: "session expired"}
	}
	return r, nil
}

func (s *MemoryStore) Save(_ context.Context, r *Record) error {
	if r == nil {
		return fmt.Errorf("nil session")
	}
	s.mu.Lock()
	s.records[r.ID] = r
	s.mu.Unlock()
	return nil
}
func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	delete(s.records, id)
	s.mu.Unlock()
	return nil
}
func (s *MemoryStore) List(_ context.Context) ([]kernel.SessionSnapshot, error) {
	s.cleanupExpired(time.Now())
	s.mu.RLock()
	records := make([]*Record, 0, len(s.records))
	for _, r := range s.records {
		records = append(records, r)
	}
	s.mu.RUnlock()
	out := make([]kernel.SessionSnapshot, 0, len(records))
	for _, r := range records {
		out = append(out, r.Snapshot(false))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}
func (s *MemoryStore) cleanupExpired(now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var expired []string
	for id, record := range s.records {
		if !record.ExpiresAt.After(now) {
			delete(s.records, id)
			expired = append(expired, id)
		}
	}
	return expired
}
func (s *MemoryStore) Close() error { return nil }
