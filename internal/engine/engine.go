package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	budgetctl "github.com/BlueSkyXN/Feishu-SuperSearch/internal/budget"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/session"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/provider"
)

type Config struct {
	GlobalConcurrency int
	ProviderTimeout   time.Duration
	SessionTTL        time.Duration
}

type Engine struct {
	registry *provider.Registry
	sessions session.Store
	cfg      Config
	flights  flightGroup[kernel.Artifact]
	capMu    sync.RWMutex
	capCache map[kernel.ProviderID]kernel.ProviderCapability
}

func New(registry *provider.Registry, store session.Store, cfg Config) *Engine {
	if registry == nil {
		registry = provider.NewRegistry()
	}
	if store == nil {
		store = session.NewMemoryStore(cfg.SessionTTL)
	}
	if cfg.GlobalConcurrency <= 0 {
		cfg.GlobalConcurrency = 6
	}
	if cfg.ProviderTimeout <= 0 {
		cfg.ProviderTimeout = 4 * time.Second
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 45 * time.Minute
	}
	engine := &Engine{registry: registry, sessions: store, cfg: cfg, capCache: map[kernel.ProviderID]kernel.ProviderCapability{}}
	if persisted, ok := store.(session.CapabilityStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		snapshot, err := persisted.LoadCapabilities(ctx)
		cancel()
		if err == nil {
			for _, capability := range snapshot.Providers {
				engine.capCache[capability.Descriptor.ID] = capability
				if capability.Error != nil && (capability.Error.Type == kernel.ErrUnsupported || capability.Error.Type == kernel.ErrVersionIncompatible) {
					_ = registry.SetUnavailable(capability.Descriptor.ID, capability.Error)
				}
				for operation, operationCapability := range capability.Operations {
					if operationCapability.Error != nil && (operationCapability.Error.Type == kernel.ErrUnsupported || operationCapability.Error.Type == kernel.ErrVersionIncompatible) {
						_ = registry.SetOperationUnavailable(capability.Descriptor.ID, operation, operationCapability.Error)
					}
				}
			}
		}
	}
	return engine
}

func (e *Engine) Close() error { return e.sessions.Close() }

func (e *Engine) Session(ctx context.Context, id string, identity kernel.Identity) (kernel.SessionSnapshot, error) {
	r, err := e.sessionForIdentity(ctx, id, identity)
	if err != nil {
		return kernel.SessionSnapshot{}, err
	}
	return r.Snapshot(true), nil
}

func (e *Engine) Capabilities(ctx context.Context, req kernel.CapabilityRequest) (kernel.CapabilitySnapshot, error) {
	req.Identity = req.Identity.Normalized()
	providers := e.registry.All()
	out := kernel.CapabilitySnapshot{GeneratedAt: time.Now().UTC(), Providers: make([]kernel.ProviderCapability, len(providers))}
	if !req.Probe {
		for i, p := range providers {
			capability := kernel.ProviderCapability{Descriptor: p.Descriptor(), Status: kernel.StatusOK, Version: p.Descriptor().Version}
			e.capMu.RLock()
			cached, ok := e.capCache[p.Descriptor().ID]
			e.capMu.RUnlock()
			if ok {
				capability = cached
				capability.Descriptor = p.Descriptor()
			}
			out.Providers[i] = capability
		}
		return out, nil
	}
	sem := make(chan struct{}, e.cfg.GlobalConcurrency)
	var wg sync.WaitGroup
	for i, p := range providers {
		i, p := i, p
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			descriptor := p.Descriptor()
			capability := kernel.ProviderCapability{Descriptor: descriptor, Status: kernel.StatusOK, Version: descriptor.Version}
			if checker, ok := p.(kernel.HealthChecker); ok {
				probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
				version, err := checker.Health(probeCtx, req.Identity)
				cancel()
				if err != nil {
					capability.Status = statusForError(err)
					capability.Error = kernel.DetailFromError(err)
				} else {
					capability.Version = version
				}
			}
			if capability.Error == nil {
				_ = e.registry.SetUnavailable(descriptor.ID, nil)
				if checker, ok := p.(kernel.OperationHealthChecker); ok {
					operations := orderedOperations(descriptor.Operations)
					capability.Operations = make(map[kernel.Operation]kernel.ProviderOperationCapability, len(operations))
					failures := 0
					var firstError error
					for _, operation := range operations {
						probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
						version, operationErr := checker.HealthOperation(probeCtx, operation, req.Identity)
						cancel()
						operationCapability := kernel.ProviderOperationCapability{Status: kernel.StatusOK, Version: version}
						if operationErr != nil {
							failures++
							if firstError == nil {
								firstError = operationErr
							}
							operationCapability.Status = statusForError(operationErr)
							operationCapability.Error = kernel.DetailFromError(operationErr)
						}
						if operationCapability.Error != nil && (operationCapability.Error.Type == kernel.ErrUnsupported || operationCapability.Error.Type == kernel.ErrVersionIncompatible) {
							_ = e.registry.SetOperationUnavailable(descriptor.ID, operation, operationCapability.Error)
						} else {
							_ = e.registry.SetOperationUnavailable(descriptor.ID, operation, nil)
						}
						capability.Operations[operation] = operationCapability
					}
					if failures > 0 && failures < len(operations) {
						capability.Status = kernel.StatusPartial
					} else if failures == len(operations) && firstError != nil {
						capability.Status = statusForError(firstError)
					}
				}
			}
			capability.Descriptor = p.Descriptor()
			e.capMu.Lock()
			e.capCache[p.Descriptor().ID] = capability
			e.capMu.Unlock()
			if capability.Error != nil && (capability.Error.Type == kernel.ErrUnsupported || capability.Error.Type == kernel.ErrVersionIncompatible) {
				_ = e.registry.SetUnavailable(p.Descriptor().ID, capability.Error)
			} else if capability.Error != nil {
				_ = e.registry.SetUnavailable(p.Descriptor().ID, nil)
			}
			out.Providers[i] = capability
		}()
	}
	wg.Wait()
	if persisted, ok := e.sessions.(session.CapabilityStore); ok {
		if err := persisted.SaveCapabilities(ctx, out); err != nil {
			return out, sessionPersistenceError(err, "save_capabilities")
		}
	}
	return out, nil
}

func orderedOperations(values kernel.OperationSet) []kernel.Operation {
	out := make([]kernel.Operation, 0, len(values))
	for operation, enabled := range values {
		if enabled {
			out = append(out, operation)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sessionPersistenceError(err error, operation string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return &kernel.ErrorDetail{Type: kernel.ErrCancelled, Subtype: "session_store", Message: "session persistence was cancelled"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &kernel.ErrorDetail{Type: kernel.ErrDeadlineExceeded, Subtype: "session_store", Message: "session persistence exceeded its deadline"}
	}
	subtype := "session_store"
	var sqliteErr *session.SQLiteStoreError
	if errors.As(err, &sqliteErr) {
		subtype = "sqlite_" + string(sqliteErr.Kind)
	}
	return &kernel.ErrorDetail{
		Type:    kernel.ErrInternal,
		Subtype: subtype,
		Message: "session persistence failed",
		Hint:    "check the configured storage path, permissions, free space, and database integrity",
		Details: map[string]any{"operation": operation},
	}
}

func (e *Engine) Resolve(ctx context.Context, req kernel.ResolveRequest) ([]kernel.ObjectRef, error) {
	req.Identity = req.Identity.Normalized()
	if strings.TrimSpace(req.Text) == "" {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "resolve text is required"}
	}
	account, sharedBudget := budgetctl.FromContext(ctx)
	if sharedBudget && !account.Reserve(kernel.CostEstimate{Calls: 1}) {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, Message: "resolve call budget exhausted"}
	}
	if req.Source == "" {
		req.Source = kernel.SourcePeople
	}
	if req.Limit <= 0 {
		req.Limit = 10
	}
	p, ok := e.registry.ForOperation(req.Source, kernel.OpResolve)
	if !ok {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Source: req.Source, Message: "no provider for source"}
	}
	if err := validateProviderOperation(p, kernel.OpResolve, req.Identity); err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, e.cfg.ProviderTimeout)
	defer cancel()
	if resolver, ok := p.(kernel.Resolver); ok {
		refs, err := resolver.Resolve(callCtx, kernel.ProviderResolveRequest{Text: req.Text, Kind: req.Kind, Identity: req.Identity, Limit: req.Limit})
		if err == nil && sharedBudget && !account.AddBytes(encodedSize(refs)) {
			return nil, &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, Message: "resolve response exceeded byte budget"}
		}
		return refs, err
	}
	searcher, ok := p.(kernel.Searcher)
	if !ok {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, ProviderID: p.Descriptor().ID, Source: req.Source, Message: "provider cannot resolve or search"}
	}
	page, err := searcher.Search(callCtx, kernel.ProviderSearchRequest{Query: req.Text, Identity: req.Identity, PageSize: req.Limit})
	if err != nil {
		return nil, err
	}
	refs := make([]kernel.ObjectRef, 0, len(page.Candidates))
	for _, c := range page.Candidates {
		refs = append(refs, c.Ref)
	}
	if sharedBudget && !account.AddBytes(encodedSize(refs)) {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, Message: "resolve response exceeded byte budget"}
	}
	return refs, nil
}

func statusForError(err error) kernel.SourceStatus {
	d := kernel.DetailFromError(err)
	switch d.Type {
	case kernel.ErrMissingScope:
		return kernel.StatusMissingScope
	case kernel.ErrDeadlineExceeded:
		return kernel.StatusDeadline
	case kernel.ErrBudgetExhausted:
		return kernel.StatusBudget
	case kernel.ErrUnsupported, kernel.ErrIdentityRequired:
		return kernel.StatusUnavailable
	default:
		return kernel.StatusFailed
	}
}

func compatibleIdentity(desc kernel.ProviderDescriptor, identity kernel.Identity) bool {
	if identity.Mode == kernel.IdentityAuto || len(desc.RequiredIdentity) == 0 {
		return true
	}
	for _, mode := range desc.RequiredIdentity {
		if mode == identity.Mode {
			return true
		}
	}
	return false
}

func (e *Engine) getOrCreateSession(ctx context.Context, id string, identity kernel.Identity) (*session.Record, error) {
	identity = identity.Normalized()
	if id != "" {
		return e.sessionForIdentity(ctx, id, identity)
	}
	return e.sessions.Create(ctx, identity.ScopeKey)
}

func (e *Engine) sessionForIdentity(ctx context.Context, id string, identity kernel.Identity) (*session.Record, error) {
	rec, err := e.sessions.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	identity = identity.Normalized()
	if rec.ScopeKey == "" || rec.ScopeKey != identity.ScopeKey {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrIdentityRequired, Message: "session belongs to a different identity scope"}
	}
	return rec, nil
}

func orderedSources(values []kernel.SourceID) []kernel.SourceID {
	seen := map[kernel.SourceID]bool{}
	out := make([]kernel.SourceID, 0, len(values))
	for _, s := range values {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func eventData(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{"value": v}
}

func (e *Engine) addEvent(rec *session.Record, typ kernel.EventType, source kernel.SourceID, data map[string]any) {
	if rec == nil {
		return
	}
	rec.AddEvent(kernel.RetrievalEvent{ID: session.NewEventID(), SessionID: rec.ID, Type: typ, Time: time.Now().UTC(), Source: source, Data: data})
}

func validateProviderOperation(p kernel.Provider, op kernel.Operation, identity kernel.Identity) error {
	d := p.Descriptor()
	if !d.Operations[op] {
		return &kernel.ErrorDetail{Type: kernel.ErrUnsupported, ProviderID: d.ID, Source: d.Source, Message: fmt.Sprintf("operation %s is unsupported", op)}
	}
	if !compatibleIdentity(d, identity) {
		return &kernel.ErrorDetail{Type: kernel.ErrIdentityRequired, ProviderID: d.ID, Source: d.Source, Message: fmt.Sprintf("identity %s is unsupported", identity.Mode)}
	}
	return nil
}

// Sessions returns non-expired retrieval sessions in reverse creation order.
func (e *Engine) Sessions(ctx context.Context, identity kernel.Identity) ([]kernel.SessionSnapshot, error) {
	all, err := e.sessions.List(ctx)
	if err != nil {
		return nil, err
	}
	scope := identity.Normalized().ScopeKey
	out := make([]kernel.SessionSnapshot, 0, len(all))
	for _, snapshot := range all {
		if snapshot.ScopeKey == scope {
			out = append(out, snapshot)
		}
	}
	return out, nil
}

// DeleteSession removes a retrieval session and its persisted state.
func (e *Engine) DeleteSession(ctx context.Context, id string, identity kernel.Identity) error {
	if _, err := e.sessionForIdentity(ctx, id, identity); err != nil {
		return err
	}
	return e.sessions.Delete(ctx, id)
}
