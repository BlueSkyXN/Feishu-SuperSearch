package kernel

import (
	"context"
	"time"
)

type OperationSet map[Operation]bool

type SearchLimits struct {
	MaxQueryRunes int `json:"max_query_runes,omitempty"`
	MaxPageSize   int `json:"max_page_size,omitempty"`
	MaxPages      int `json:"max_pages,omitempty"`
}

type BatchLimits struct {
	MaxFetchItems int `json:"max_fetch_items,omitempty"`
}

type ProviderDescriptor struct {
	ID                  ProviderID     `json:"id"`
	Source              SourceID       `json:"source"`
	ObjectKinds         []ObjectKind   `json:"object_kinds"`
	Operations          OperationSet   `json:"operations"`
	RequiredIdentity    []IdentityMode `json:"required_identity,omitempty"`
	RequiredScopes      []string       `json:"required_scopes,omitempty"`
	SearchLimits        SearchLimits   `json:"search_limits,omitempty"`
	BatchLimits         BatchLimits    `json:"batch_limits,omitempty"`
	ReturnedProjection  ProjectionSet  `json:"returned_projection"`
	FetchableProjection ProjectionSet  `json:"fetchable_projection"`
	SupportsPagination  bool           `json:"supports_pagination"`
	SupportsStreaming   bool           `json:"supports_streaming"`
	Backend             string         `json:"backend"`
	Version             string         `json:"version,omitempty"`
}

type Provider interface {
	Descriptor() ProviderDescriptor
}

type ProviderSearchRequest struct {
	Query     string        `json:"query"`
	Filters   SearchFilters `json:"filters,omitempty"`
	Identity  Identity      `json:"identity"`
	PageSize  int           `json:"page_size"`
	Cursor    string        `json:"cursor,omitempty"`
	SessionID string        `json:"session_id,omitempty"`
}

type CandidatePage struct {
	Candidates []Candidate    `json:"candidates"`
	NextCursor string         `json:"next_cursor,omitempty"`
	HasMore    bool           `json:"has_more"`
	RawCount   int            `json:"raw_count"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type ProviderQueryRequest struct {
	Container *ObjectRef     `json:"container,omitempty"`
	Filter    map[string]any `json:"filter,omitempty"`
	Sort      []SortSpec     `json:"sort,omitempty"`
	Identity  Identity       `json:"identity"`
	Limit     int            `json:"limit"`
	Cursor    string         `json:"cursor,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
}

type ProviderFetchRequest struct {
	Ref        ObjectRef     `json:"ref"`
	Projection ProjectionSet `json:"projection"`
	Identity   Identity      `json:"identity"`
	SessionID  string        `json:"session_id,omitempty"`
}

type ProviderExpandRequest struct {
	Ref       ObjectRef `json:"ref"`
	Relations []string  `json:"relations,omitempty"`
	Identity  Identity  `json:"identity"`
	MaxDepth  int       `json:"max_depth,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
}

type ProviderResolveRequest struct {
	Text     string     `json:"text"`
	Kind     ObjectKind `json:"kind,omitempty"`
	Identity Identity   `json:"identity"`
	Limit    int        `json:"limit,omitempty"`
}

type Searcher interface {
	Search(context.Context, ProviderSearchRequest) (CandidatePage, error)
}

type Querier interface {
	Query(context.Context, ProviderQueryRequest) (CandidatePage, error)
}

type Fetcher interface {
	Fetch(context.Context, []ProviderFetchRequest) ([]Artifact, error)
}

type Expander interface {
	Expand(context.Context, ProviderExpandRequest) ([]Relation, error)
}

type Resolver interface {
	Resolve(context.Context, ProviderResolveRequest) ([]ObjectRef, error)
}

type HealthChecker interface {
	Health(context.Context, Identity) (version string, err error)
}

type OperationHealthChecker interface {
	HealthOperation(context.Context, Operation, Identity) (version string, err error)
}

type CostEstimate struct {
	Calls    int           `json:"calls"`
	Latency  time.Duration `json:"latency"`
	Bytes    int64         `json:"bytes"`
	Fetches  int           `json:"fetches"`
	Expanded int           `json:"expanded"`
}
