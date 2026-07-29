package replay

import (
	"context"
	"errors"
	"fmt"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type RecordingProvider struct {
	base  kernel.Provider
	store *Store
}

func Wrap(base kernel.Provider, store *Store) (*RecordingProvider, error) {
	if base == nil || store == nil {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "recording provider requires base provider and store"}
	}
	if err := store.Register(base.Descriptor()); err != nil {
		return nil, err
	}
	return &RecordingProvider{base: base, store: store}, nil
}
func (p *RecordingProvider) Descriptor() kernel.ProviderDescriptor { return p.base.Descriptor() }
func (p *RecordingProvider) Health(ctx context.Context, identity kernel.Identity) (string, error) {
	if checker, ok := p.base.(kernel.HealthChecker); ok {
		return checker.Health(ctx, identity)
	}
	return p.base.Descriptor().Version, nil
}
func (p *RecordingProvider) Search(ctx context.Context, req kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	base, ok := p.base.(kernel.Searcher)
	if !ok {
		return kernel.CandidatePage{}, unsupported(p.base, kernel.OpSearch)
	}
	out, err := base.Search(ctx, req)
	saveErr := p.store.Save(p.base.Descriptor().ID, kernel.OpSearch, req, out, err)
	return out, recordingError(err, saveErr)
}
func (p *RecordingProvider) Query(ctx context.Context, req kernel.ProviderQueryRequest) (kernel.CandidatePage, error) {
	base, ok := p.base.(kernel.Querier)
	if !ok {
		return kernel.CandidatePage{}, unsupported(p.base, kernel.OpQuery)
	}
	out, err := base.Query(ctx, req)
	saveErr := p.store.Save(p.base.Descriptor().ID, kernel.OpQuery, req, out, err)
	return out, recordingError(err, saveErr)
}
func (p *RecordingProvider) Fetch(ctx context.Context, req []kernel.ProviderFetchRequest) ([]kernel.Artifact, error) {
	base, ok := p.base.(kernel.Fetcher)
	if !ok {
		return nil, unsupported(p.base, kernel.OpFetch)
	}
	out, err := base.Fetch(ctx, req)
	saveErr := p.store.Save(p.base.Descriptor().ID, kernel.OpFetch, req, out, err)
	return out, recordingError(err, saveErr)
}
func (p *RecordingProvider) Expand(ctx context.Context, req kernel.ProviderExpandRequest) ([]kernel.Relation, error) {
	base, ok := p.base.(kernel.Expander)
	if !ok {
		return nil, unsupported(p.base, kernel.OpExpand)
	}
	out, err := base.Expand(ctx, req)
	saveErr := p.store.Save(p.base.Descriptor().ID, kernel.OpExpand, req, out, err)
	return out, recordingError(err, saveErr)
}
func (p *RecordingProvider) Resolve(ctx context.Context, req kernel.ProviderResolveRequest) ([]kernel.ObjectRef, error) {
	base, ok := p.base.(kernel.Resolver)
	if !ok {
		return nil, unsupported(p.base, kernel.OpResolve)
	}
	out, err := base.Resolve(ctx, req)
	saveErr := p.store.Save(p.base.Descriptor().ID, kernel.OpResolve, req, out, err)
	return out, recordingError(err, saveErr)
}

func recordingError(callErr, saveErr error) error {
	if saveErr == nil {
		return callErr
	}
	recordErr := fmt.Errorf("persist replay fixture: %w", saveErr)
	if callErr != nil {
		return errors.Join(callErr, recordErr)
	}
	return recordErr
}

// Provider is a deterministic fixture-backed provider.
type Provider struct {
	descriptor kernel.ProviderDescriptor
	store      *Store
}

func NewProvider(descriptor kernel.ProviderDescriptor, store *Store) *Provider {
	descriptor.Backend = "replay"
	return &Provider{descriptor: descriptor, store: store}
}
func (p *Provider) Descriptor() kernel.ProviderDescriptor { return p.descriptor }
func (p *Provider) Health(context.Context, kernel.Identity) (string, error) {
	return "replay/1", nil
}
func (p *Provider) Search(_ context.Context, req kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	var out kernel.CandidatePage
	return out, p.store.Load(p.descriptor.ID, kernel.OpSearch, req, &out)
}
func (p *Provider) Query(_ context.Context, req kernel.ProviderQueryRequest) (kernel.CandidatePage, error) {
	var out kernel.CandidatePage
	return out, p.store.Load(p.descriptor.ID, kernel.OpQuery, req, &out)
}
func (p *Provider) Fetch(_ context.Context, req []kernel.ProviderFetchRequest) ([]kernel.Artifact, error) {
	var out []kernel.Artifact
	return out, p.store.Load(p.descriptor.ID, kernel.OpFetch, req, &out)
}
func (p *Provider) Expand(_ context.Context, req kernel.ProviderExpandRequest) ([]kernel.Relation, error) {
	var out []kernel.Relation
	return out, p.store.Load(p.descriptor.ID, kernel.OpExpand, req, &out)
}
func (p *Provider) Resolve(_ context.Context, req kernel.ProviderResolveRequest) ([]kernel.ObjectRef, error) {
	var out []kernel.ObjectRef
	return out, p.store.Load(p.descriptor.ID, kernel.OpResolve, req, &out)
}
func unsupported(provider kernel.Provider, op kernel.Operation) error {
	d := provider.Descriptor()
	return &kernel.ErrorDetail{Type: kernel.ErrUnsupported, ProviderID: d.ID, Source: d.Source, Message: "provider does not implement " + string(op)}
}
