package kernel

import "context"

type Kernel interface {
	Capabilities(context.Context, CapabilityRequest) (CapabilitySnapshot, error)
	Search(context.Context, SearchRequest) (SearchSnapshot, error)
	Continue(context.Context, ContinueRequest) (SearchSnapshot, error)
	Query(context.Context, QueryRequest) (QuerySnapshot, error)
	Fetch(context.Context, FetchBatchRequest) (ArtifactBatch, error)
	Expand(context.Context, ExpandRequest) (RelationBatch, error)
	Resolve(context.Context, ResolveRequest) ([]ObjectRef, error)
	Execute(context.Context, RetrievalPlan) (<-chan RetrievalEvent, <-chan PlanResult, error)
	Session(context.Context, string, Identity) (SessionSnapshot, error)
}
