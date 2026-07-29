package kernel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type SourceID string
type ProviderID string
type ObjectKind string
type IdentityMode string
type Operation string

type SourceStatus string

const (
	SourceDocs     SourceID = "docs"
	SourceMessages SourceID = "messages"
	SourceChats    SourceID = "chats"
	SourcePeople   SourceID = "people"
	SourceMinutes  SourceID = "minutes"
	SourceMeetings SourceID = "meetings"
	SourceCalendar SourceID = "calendar"
	SourceTasks    SourceID = "tasks"
	SourceMail     SourceID = "mail"
	SourceBase     SourceID = "base"
	SourceSheets   SourceID = "sheets"
)

var GlobalSources = []SourceID{
	SourceDocs, SourceMessages, SourceChats, SourcePeople, SourceMinutes,
	SourceMeetings, SourceCalendar, SourceTasks, SourceMail,
}

const (
	KindUnknown    ObjectKind = "unknown"
	KindDocument   ObjectKind = "document"
	KindMessage    ObjectKind = "message"
	KindChat       ObjectKind = "chat"
	KindPerson     ObjectKind = "person"
	KindMinute     ObjectKind = "minute"
	KindMeeting    ObjectKind = "meeting"
	KindEvent      ObjectKind = "event"
	KindTask       ObjectKind = "task"
	KindTaskList   ObjectKind = "tasklist"
	KindMail       ObjectKind = "mail"
	KindBase       ObjectKind = "base"
	KindBaseTable  ObjectKind = "base_table"
	KindRecord     ObjectKind = "record"
	KindSheet      ObjectKind = "sheet"
	KindCell       ObjectKind = "cell"
	KindAttachment ObjectKind = "attachment"
)

const (
	IdentityAuto IdentityMode = "auto"
	IdentityUser IdentityMode = "user"
	IdentityBot  IdentityMode = "bot"
)

const (
	OpResolve  Operation = "resolve"
	OpSearch   Operation = "search"
	OpQuery    Operation = "query"
	OpFetch    Operation = "fetch"
	OpExpand   Operation = "expand"
	OpContinue Operation = "continue"
)

const (
	StatusOK           SourceStatus = "ok"
	StatusPartial      SourceStatus = "partial"
	StatusEmpty        SourceStatus = "empty"
	StatusUnavailable  SourceStatus = "unavailable"
	StatusMissingScope SourceStatus = "missing_scope"
	StatusFailed       SourceStatus = "failed"
	StatusDeadline     SourceStatus = "deadline_exceeded"
	StatusBudget       SourceStatus = "budget_exhausted"
)

type Identity struct {
	Profile  string       `json:"profile,omitempty"`
	Mode     IdentityMode `json:"mode,omitempty"`
	ScopeKey string       `json:"scope_key,omitempty"`
}

func (i Identity) Normalized() Identity {
	if i.Mode == "" {
		i.Mode = IdentityAuto
	}
	if i.ScopeKey == "" {
		raw := i.Profile + ":" + string(i.Mode)
		h := sha256.Sum256([]byte(raw))
		i.ScopeKey = hex.EncodeToString(h[:8])
	}
	return i
}

type ObjectRef struct {
	Platform    string     `json:"platform"`
	ScopeKey    string     `json:"scope_key"`
	Kind        ObjectKind `json:"kind"`
	NativeID    string     `json:"native_id"`
	CanonicalID string     `json:"canonical_id"`
	ProviderID  ProviderID `json:"provider_id"`
	Source      SourceID   `json:"source,omitempty"`
	URL         string     `json:"url,omitempty"`
}

func (r ObjectRef) Validate() error {
	if r.CanonicalID == "" && r.NativeID == "" {
		return fmt.Errorf("object ref requires canonical_id or native_id")
	}
	return nil
}

func BuildCanonicalID(scope string, kind ObjectKind, nativeID string) string {
	if scope == "" {
		scope = "default"
	}
	return fmt.Sprintf("feishu:%s:%s:%s", scope, kind, strings.TrimSpace(nativeID))
}

type Actor struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	Email  string `json:"email,omitempty"`
	Avatar string `json:"avatar,omitempty"`
}

type Container struct {
	Kind  ObjectKind `json:"kind"`
	ID    string     `json:"id"`
	Title string     `json:"title,omitempty"`
	URL   string     `json:"url,omitempty"`
}

type Discovery struct {
	ProviderID ProviderID `json:"provider_id"`
	Source     SourceID   `json:"source"`
	Rank       int        `json:"rank"`
	Score      *float64   `json:"score,omitempty"`
	Cursor     string     `json:"cursor,omitempty"`
}

type RelationHint struct {
	Type       string     `json:"type"`
	TargetKind ObjectKind `json:"target_kind,omitempty"`
	TargetID   string     `json:"target_id,omitempty"`
}

type Provenance struct {
	ProviderID  ProviderID `json:"provider_id"`
	Backend     string     `json:"backend"`
	Operation   string     `json:"operation"`
	RetrievedAt time.Time  `json:"retrieved_at"`
	SourceRank  int        `json:"source_rank,omitempty"`
	RawRef      any        `json:"-"`
}

type Candidate struct {
	Ref                 ObjectRef      `json:"ref"`
	Source              SourceID       `json:"source"`
	Kind                ObjectKind     `json:"kind"`
	Title               string         `json:"title"`
	Snippet             string         `json:"snippet,omitempty"`
	URL                 string         `json:"url,omitempty"`
	Timestamp           *time.Time     `json:"timestamp,omitempty"`
	Actors              []Actor        `json:"actors,omitempty"`
	Container           *Container     `json:"container,omitempty"`
	NativeRank          int            `json:"native_rank"`
	NativeScore         *float64       `json:"native_score,omitempty"`
	FusedScore          float64        `json:"fused_score"`
	Projection          ProjectionSet  `json:"projection"`
	AvailableProjection ProjectionSet  `json:"available_projection"`
	RelationHints       []RelationHint `json:"relation_hints,omitempty"`
	DiscoveredBy        []Discovery    `json:"discovered_by"`
	Provenance          Provenance     `json:"provenance"`
}

type ContentChunk struct {
	ID        string         `json:"id,omitempty"`
	Kind      string         `json:"kind,omitempty"`
	Text      string         `json:"text"`
	Start     int            `json:"start,omitempty"`
	End       int            `json:"end,omitempty"`
	Timestamp *time.Time     `json:"timestamp,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type NativeSummary struct {
	Text     string           `json:"text,omitempty"`
	Todos    []map[string]any `json:"todos,omitempty"`
	Chapters []map[string]any `json:"chapters,omitempty"`
	Keywords []string         `json:"keywords,omitempty"`
}

type Attachment struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	MimeType  string `json:"mime_type,omitempty"`
	Size      int64  `json:"size,omitempty"`
	URL       string `json:"url,omitempty"`
	LocalPath string `json:"local_path,omitempty"`
}

type Artifact struct {
	Ref         ObjectRef      `json:"ref"`
	Projection  ProjectionSet  `json:"projection"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Chunks      []ContentChunk `json:"chunks,omitempty"`
	Summary     *NativeSummary `json:"summary,omitempty"`
	Relations   []Relation     `json:"relations,omitempty"`
	Attachments []Attachment   `json:"attachments,omitempty"`
	Version     string         `json:"version,omitempty"`
	Provenance  Provenance     `json:"provenance"`
}

type Relation struct {
	From       ObjectRef  `json:"from"`
	Type       string     `json:"type"`
	To         ObjectRef  `json:"to"`
	Confidence float64    `json:"confidence"`
	Provenance Provenance `json:"provenance"`
}

type SearchFilters struct {
	After        *time.Time     `json:"after,omitempty"`
	Before       *time.Time     `json:"before,omitempty"`
	SenderIDs    []string       `json:"sender_ids,omitempty"`
	PersonIDs    []string       `json:"person_ids,omitempty"`
	ChatIDs      []string       `json:"chat_ids,omitempty"`
	DocTypes     []string       `json:"doc_types,omitempty"`
	FolderTokens []string       `json:"folder_tokens,omitempty"`
	SpaceIDs     []string       `json:"space_ids,omitempty"`
	Mine         bool           `json:"mine,omitempty"`
	Completed    *bool          `json:"completed,omitempty"`
	OnlyTitle    bool           `json:"only_title,omitempty"`
	Extra        map[string]any `json:"extra,omitempty"`
}

type SearchBudget struct {
	DeadlineMS        int   `json:"deadline_ms,omitempty"`
	MaxCalls          int   `json:"max_calls,omitempty"`
	MaxPagesPerSource int   `json:"max_pages_per_source,omitempty"`
	MaxFetches        int   `json:"max_fetches,omitempty"`
	MaxExpandedNodes  int   `json:"max_expanded_nodes,omitempty"`
	MaxBytes          int64 `json:"max_bytes,omitempty"`
}

func (b SearchBudget) WithDefaults() SearchBudget {
	if b.DeadlineMS <= 0 {
		b.DeadlineMS = 8000
	}
	if b.MaxCalls <= 0 {
		b.MaxCalls = 24
	}
	if b.MaxPagesPerSource <= 0 {
		b.MaxPagesPerSource = 2
	}
	if b.MaxFetches <= 0 {
		b.MaxFetches = 12
	}
	if b.MaxExpandedNodes <= 0 {
		b.MaxExpandedNodes = 20
	}
	if b.MaxBytes <= 0 {
		b.MaxBytes = 32 << 20
	}
	return b
}

type SearchStrategy struct {
	Profile            string               `json:"profile,omitempty"`
	Fusion             string               `json:"fusion,omitempty"`
	Pagination         string               `json:"pagination,omitempty"`
	SourceQuota        bool                 `json:"source_quota,omitempty"`
	DisableSourceQuota bool                 `json:"disable_source_quota,omitempty"`
	Weights            map[SourceID]float64 `json:"weights,omitempty"`
	Quotas             map[SourceID]int     `json:"quotas,omitempty"`
	K0                 float64              `json:"k0,omitempty"`
}

func (s SearchStrategy) WithDefaults() SearchStrategy {
	s.Profile = strings.ToLower(strings.TrimSpace(s.Profile))
	if s.Profile == "" {
		s.Profile = "balanced"
	}
	if s.Fusion == "" {
		s.Fusion = "weighted_rrf"
	}
	if s.Pagination == "" {
		if s.Profile == "fast" {
			s.Pagination = "none"
		} else {
			s.Pagination = "adaptive"
		}
	}
	if s.K0 <= 0 {
		s.K0 = 60
	}
	if s.DisableSourceQuota {
		s.SourceQuota = false
	} else if !s.SourceQuota {
		// Source diversity is the safe default for federated search. Set
		// disable_source_quota=true to intentionally turn it off.
		s.SourceQuota = true
	}
	if s.Weights == nil {
		s.Weights = map[SourceID]float64{}
	}
	if s.Quotas == nil {
		s.Quotas = map[SourceID]int{}
	}
	return s
}

type SearchRequest struct {
	Query          string              `json:"query"`
	SourceQueries  map[SourceID]string `json:"source_queries,omitempty"`
	Sources        []SourceID          `json:"sources,omitempty"`
	Filters        SearchFilters       `json:"filters,omitempty"`
	Identity       Identity            `json:"identity"`
	Limit          int                 `json:"limit,omitempty"`
	LimitPerSource int                 `json:"limit_per_source,omitempty"`
	Budget         SearchBudget        `json:"budget,omitempty"`
	Strategy       SearchStrategy      `json:"strategy,omitempty"`
	SessionID      string              `json:"session_id,omitempty"`
}

func (r SearchRequest) WithDefaults() SearchRequest {
	r.Identity = r.Identity.Normalized()
	r.Strategy = r.Strategy.WithDefaults()
	switch r.Strategy.Profile {
	case "fast":
		if r.Limit <= 0 {
			r.Limit = 25
		}
		if r.LimitPerSource <= 0 {
			r.LimitPerSource = 5
		}
		if r.Budget.DeadlineMS <= 0 {
			r.Budget.DeadlineMS = 4000
		}
		if r.Budget.MaxPagesPerSource <= 0 {
			r.Budget.MaxPagesPerSource = 1
		}
	case "deep":
		if r.Limit <= 0 {
			r.Limit = 60
		}
		if r.LimitPerSource <= 0 {
			r.LimitPerSource = 10
		}
		if r.Budget.DeadlineMS <= 0 {
			r.Budget.DeadlineMS = 15000
		}
		if r.Budget.MaxPagesPerSource <= 0 {
			r.Budget.MaxPagesPerSource = 2
		}
		if r.Budget.MaxFetches <= 0 {
			r.Budget.MaxFetches = 20
		}
	default:
		if r.Limit <= 0 {
			r.Limit = 40
		}
		if r.LimitPerSource <= 0 {
			r.LimitPerSource = 8
		}
		if r.Budget.DeadlineMS <= 0 {
			r.Budget.DeadlineMS = 8000
		}
		if r.Budget.MaxPagesPerSource <= 0 {
			r.Budget.MaxPagesPerSource = 2
		}
	}
	r.Budget = r.Budget.WithDefaults()
	return r
}

func (r SearchRequest) Validate() error {
	if strings.TrimSpace(r.Query) == "" {
		return fmt.Errorf("query must not be empty")
	}
	if r.Limit < 0 || r.LimitPerSource < 0 {
		return fmt.Errorf("limits must not be negative")
	}
	switch r.Strategy.Profile {
	case "fast", "balanced", "deep":
	default:
		return fmt.Errorf("unsupported strategy profile %q", r.Strategy.Profile)
	}
	switch r.Strategy.Fusion {
	case "weighted_rrf":
	default:
		return fmt.Errorf("unsupported fusion strategy %q", r.Strategy.Fusion)
	}
	switch r.Strategy.Pagination {
	case "none", "adaptive", "fixed":
	default:
		return fmt.Errorf("unsupported pagination strategy %q", r.Strategy.Pagination)
	}
	return nil
}

type ContinueRequest struct {
	SessionID          string     `json:"session_id"`
	Sources            []SourceID `json:"sources,omitempty"`
	MaxAdditionalPages int        `json:"max_additional_pages,omitempty"`
	Identity           Identity   `json:"identity"`
}

type QueryRequest struct {
	Source    SourceID       `json:"source"`
	Container *ObjectRef     `json:"container,omitempty"`
	Filter    map[string]any `json:"filter,omitempty"`
	Sort      []SortSpec     `json:"sort,omitempty"`
	Limit     int            `json:"limit,omitempty"`
	Cursor    string         `json:"cursor,omitempty"`
	Identity  Identity       `json:"identity"`
	SessionID string         `json:"session_id,omitempty"`
	Budget    SearchBudget   `json:"budget,omitempty"`
}

type SortSpec struct {
	Field string `json:"field"`
	Order string `json:"order,omitempty"`
}

type FetchRequest struct {
	Ref        ObjectRef     `json:"ref"`
	Projection ProjectionSet `json:"projection"`
}

type FetchBatchRequest struct {
	SessionID string         `json:"session_id,omitempty"`
	Identity  Identity       `json:"identity"`
	Items     []FetchRequest `json:"items"`
	Budget    SearchBudget   `json:"budget,omitempty"`
}

type ExpandRequest struct {
	SessionID string       `json:"session_id,omitempty"`
	Identity  Identity     `json:"identity"`
	Ref       ObjectRef    `json:"ref"`
	Relations []string     `json:"relations,omitempty"`
	MaxDepth  int          `json:"max_depth,omitempty"`
	Budget    SearchBudget `json:"budget,omitempty"`
}

type ResolveRequest struct {
	Source   SourceID   `json:"source,omitempty"`
	Text     string     `json:"text"`
	Kind     ObjectKind `json:"kind,omitempty"`
	Identity Identity   `json:"identity"`
	Limit    int        `json:"limit,omitempty"`
}

type SourceRun struct {
	ProviderID ProviderID   `json:"provider_id"`
	Source     SourceID     `json:"source"`
	Status     SourceStatus `json:"status"`
	Pages      int          `json:"pages"`
	HitCount   int          `json:"hit_count"`
	ElapsedMS  int64        `json:"elapsed_ms"`
	Error      *ErrorDetail `json:"error,omitempty"`
}

type Continuation struct {
	ProviderID   ProviderID `json:"provider_id"`
	Source       SourceID   `json:"source"`
	Cursor       string     `json:"cursor"`
	HasMore      bool       `json:"has_more"`
	PagesFetched int        `json:"pages_fetched"`
}

type BudgetState struct {
	MaxCalls     int       `json:"max_calls"`
	CallsUsed    int       `json:"calls_used"`
	MaxFetches   int       `json:"max_fetches"`
	FetchesUsed  int       `json:"fetches_used"`
	MaxExpanded  int       `json:"max_expanded"`
	ExpandedUsed int       `json:"expanded_used"`
	MaxBytes     int64     `json:"max_bytes"`
	BytesUsed    int64     `json:"bytes_used"`
	Deadline     time.Time `json:"deadline"`
}

type SearchStats struct {
	RawCandidates    int   `json:"raw_candidates"`
	UniqueCandidates int   `json:"unique_candidates"`
	Returned         int   `json:"returned"`
	SourcesCompleted int   `json:"sources_completed"`
	SourcesFailed    int   `json:"sources_failed"`
	ElapsedMS        int64 `json:"elapsed_ms"`
}

type SearchSnapshot struct {
	SessionID     string         `json:"session_id"`
	Query         string         `json:"query"`
	Candidates    []Candidate    `json:"candidates"`
	Sources       []SourceRun    `json:"sources"`
	Continuations []Continuation `json:"continuations,omitempty"`
	Partial       bool           `json:"partial"`
	Budget        BudgetState    `json:"budget"`
	Stats         SearchStats    `json:"stats"`
}

type QuerySnapshot struct {
	SessionID    string        `json:"session_id"`
	Source       SourceID      `json:"source"`
	Candidates   []Candidate   `json:"candidates"`
	Continuation *Continuation `json:"continuation,omitempty"`
	SourceRun    SourceRun     `json:"source_run"`
	Budget       BudgetState   `json:"budget"`
}

type ArtifactResult struct {
	Ref      ObjectRef    `json:"ref"`
	Artifact *Artifact    `json:"artifact,omitempty"`
	Error    *ErrorDetail `json:"error,omitempty"`
	Cached   bool         `json:"cached,omitempty"`
	Shared   bool         `json:"shared,omitempty"`
}

type ArtifactBatch struct {
	SessionID string           `json:"session_id"`
	Items     []ArtifactResult `json:"items"`
	Partial   bool             `json:"partial"`
	Budget    BudgetState      `json:"budget"`
}

type RelationBatch struct {
	SessionID string       `json:"session_id"`
	Relations []Relation   `json:"relations"`
	Partial   bool         `json:"partial"`
	Error     *ErrorDetail `json:"error,omitempty"`
	Budget    BudgetState  `json:"budget"`
}

type CapabilityRequest struct {
	Identity Identity `json:"identity"`
	Probe    bool     `json:"probe,omitempty"`
}

type CapabilitySnapshot struct {
	GeneratedAt time.Time            `json:"generated_at"`
	Providers   []ProviderCapability `json:"providers"`
}

type ProviderCapability struct {
	Descriptor ProviderDescriptor                        `json:"descriptor"`
	Status     SourceStatus                              `json:"status"`
	Version    string                                    `json:"version,omitempty"`
	Error      *ErrorDetail                              `json:"error,omitempty"`
	Operations map[Operation]ProviderOperationCapability `json:"operations,omitempty"`
}

type ProviderOperationCapability struct {
	Status  SourceStatus `json:"status"`
	Version string       `json:"version,omitempty"`
	Error   *ErrorDetail `json:"error,omitempty"`
}

type SessionSnapshot struct {
	ID         string                    `json:"id"`
	ScopeKey   string                    `json:"scope_key"`
	CreatedAt  time.Time                 `json:"created_at"`
	ExpiresAt  time.Time                 `json:"expires_at"`
	Request    *SearchRequest            `json:"request,omitempty"`
	Candidates []Candidate               `json:"candidates"`
	Artifacts  []Artifact                `json:"artifacts"`
	Relations  []Relation                `json:"relations"`
	Frontiers  map[SourceID]Continuation `json:"frontiers"`
	SourceRuns []SourceRun               `json:"source_runs"`
	Budget     BudgetState               `json:"budget"`
	Events     []RetrievalEvent          `json:"events,omitempty"`
}

func CloneJSON[T any](in T) T {
	var out T
	b, _ := json.Marshal(in)
	_ = json.Unmarshal(b, &out)
	return out
}
