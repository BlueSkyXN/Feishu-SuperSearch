package provider

import (
	"fmt"
	"sort"
	"sync"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type Registry struct {
	mu            sync.RWMutex
	byID          map[kernel.ProviderID]kernel.Provider
	bySource      map[kernel.SourceID][]kernel.ProviderID
	preferred     map[kernel.SourceID]kernel.ProviderID
	preferredByOp map[kernel.SourceID]map[kernel.Operation]kernel.ProviderID
	unavailable   map[kernel.ProviderID]*kernel.ErrorDetail
	unavailableOp map[kernel.ProviderID]map[kernel.Operation]*kernel.ErrorDetail
}

func NewRegistry() *Registry {
	return &Registry{
		byID:          make(map[kernel.ProviderID]kernel.Provider),
		bySource:      make(map[kernel.SourceID][]kernel.ProviderID),
		preferred:     make(map[kernel.SourceID]kernel.ProviderID),
		preferredByOp: make(map[kernel.SourceID]map[kernel.Operation]kernel.ProviderID),
		unavailable:   make(map[kernel.ProviderID]*kernel.ErrorDetail),
		unavailableOp: make(map[kernel.ProviderID]map[kernel.Operation]*kernel.ErrorDetail),
	}
}

func (r *Registry) Register(p kernel.Provider, preferred bool) error {
	if p == nil {
		return fmt.Errorf("provider must not be nil")
	}
	d := p.Descriptor()
	if d.ID == "" || d.Source == "" {
		return fmt.Errorf("provider descriptor requires id and source")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byID[d.ID]; exists {
		return fmt.Errorf("provider %q already registered", d.ID)
	}
	r.byID[d.ID] = p
	r.bySource[d.Source] = append(r.bySource[d.Source], d.ID)
	if preferred || r.preferred[d.Source] == "" {
		r.preferred[d.Source] = d.ID
	}
	return nil
}

func (r *Registry) SetPreferred(source kernel.SourceID, id kernel.ProviderID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validateRouteLocked(source, "", id); err != nil {
		return err
	}
	r.preferred[source] = id
	return nil
}

// SetPreferredForOperation routes one operation independently from the source
// default. This permits, for example, docs/search through direct OpenAPI while
// docs/fetch remains on lark-cli.
func (r *Registry) SetPreferredForOperation(source kernel.SourceID, op kernel.Operation, id kernel.ProviderID) error {
	if op == "" {
		return fmt.Errorf("operation is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validateRouteLocked(source, op, id); err != nil {
		return err
	}
	if r.preferredByOp[source] == nil {
		r.preferredByOp[source] = map[kernel.Operation]kernel.ProviderID{}
	}
	r.preferredByOp[source][op] = id
	return nil
}

func (r *Registry) validateRouteLocked(source kernel.SourceID, op kernel.Operation, id kernel.ProviderID) error {
	p, ok := r.byID[id]
	if !ok {
		return fmt.Errorf("provider %q not registered", id)
	}
	d := p.Descriptor()
	if d.Source != source {
		return fmt.Errorf("provider %q belongs to source %q", id, d.Source)
	}
	if op != "" && !d.Operations[op] {
		return fmt.Errorf("provider %q does not support operation %q", id, op)
	}
	return nil
}

func (r *Registry) ByID(id kernel.ProviderID) (kernel.Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byID[id]
	return p, ok
}

func (r *Registry) ForSource(source kernel.SourceID) (kernel.Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id := r.preferred[source]
	if r.unavailable[id] != nil {
		return nil, false
	}
	p, ok := r.byID[id]
	return p, ok
}

// ExplicitOperationRoute returns only a deliberately configured per-operation
// route. It is useful when an ObjectRef names the provider that discovered it:
// an explicit route wins, otherwise that provider remains eligible.
func (r *Registry) ExplicitOperationRoute(source kernel.SourceID, op kernel.Operation) (kernel.Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id := r.preferredByOp[source][op]
	if !r.operationAvailableLocked(id, op) {
		return nil, false
	}
	p, ok := r.byID[id]
	return p, ok
}

// ForOperation resolves the effective provider for a source and operation. It
// first honors an explicit route, then the source default when compatible, then
// falls back to another registered provider that supports the operation.
func (r *Registry) ForOperation(source kernel.SourceID, op kernel.Operation) (kernel.Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if id := r.preferredByOp[source][op]; id != "" {
		if p := r.byID[id]; p != nil && r.operationAvailableLocked(id, op) {
			return p, true
		}
	}
	if id := r.preferred[source]; id != "" {
		if p := r.byID[id]; p != nil && r.operationAvailableLocked(id, op) && p.Descriptor().Operations[op] {
			return p, true
		}
	}
	for _, id := range r.bySource[source] {
		if p := r.byID[id]; p != nil && r.operationAvailableLocked(id, op) && p.Descriptor().Operations[op] {
			return p, true
		}
	}
	return nil, false
}

func (r *Registry) operationAvailableLocked(id kernel.ProviderID, op kernel.Operation) bool {
	if id == "" || r.unavailable[id] != nil {
		return false
	}
	return r.unavailableOp[id] == nil || r.unavailableOp[id][op] == nil
}

// SetUnavailable records a capability-handshake failure that makes a provider
// implementation unusable before an operation starts. Permission and identity
// errors are intentionally not passed here: those must remain visible from the
// real request and must not be hidden by an automatic fallback.
func (r *Registry) SetUnavailable(id kernel.ProviderID, detail *kernel.ErrorDetail) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byID[id] == nil {
		return fmt.Errorf("provider %q not registered", id)
	}
	if detail == nil {
		delete(r.unavailable, id)
		return nil
	}
	copy := *detail
	r.unavailable[id] = &copy
	return nil
}

func (r *Registry) SetOperationUnavailable(id kernel.ProviderID, op kernel.Operation, detail *kernel.ErrorDetail) error {
	if op == "" {
		return fmt.Errorf("operation is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.byID[id]
	if p == nil {
		return fmt.Errorf("provider %q not registered", id)
	}
	if !p.Descriptor().Operations[op] {
		return fmt.Errorf("provider %q does not support operation %q", id, op)
	}
	if detail == nil {
		if r.unavailableOp[id] != nil {
			delete(r.unavailableOp[id], op)
			if len(r.unavailableOp[id]) == 0 {
				delete(r.unavailableOp, id)
			}
		}
		return nil
	}
	if r.unavailableOp[id] == nil {
		r.unavailableOp[id] = map[kernel.Operation]*kernel.ErrorDetail{}
	}
	copy := *detail
	r.unavailableOp[id][op] = &copy
	return nil
}

func (r *Registry) AvailableByID(id kernel.ProviderID) (kernel.Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.unavailable[id] != nil {
		return nil, false
	}
	p, ok := r.byID[id]
	return p, ok
}

func (r *Registry) UnavailableReason(id kernel.ProviderID) *kernel.ErrorDetail {
	r.mu.RLock()
	defer r.mu.RUnlock()
	detail := r.unavailable[id]
	if detail == nil {
		return nil
	}
	copy := *detail
	return &copy
}

func (r *Registry) OperationUnavailableReason(id kernel.ProviderID, op kernel.Operation) *kernel.ErrorDetail {
	r.mu.RLock()
	defer r.mu.RUnlock()
	detail := r.unavailableOp[id][op]
	if detail == nil {
		return nil
	}
	copy := *detail
	return &copy
}

func (r *Registry) ProvidersForSource(source kernel.SourceID) []kernel.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := r.bySource[source]
	out := make([]kernel.Provider, 0, len(ids))
	for _, id := range ids {
		if p := r.byID[id]; p != nil {
			out = append(out, p)
		}
	}
	return out
}

func (r *Registry) All() []kernel.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.byID))
	for id := range r.byID {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	out := make([]kernel.Provider, len(ids))
	for i, id := range ids {
		out[i] = r.byID[kernel.ProviderID(id)]
	}
	return out
}

func (r *Registry) Sources() []kernel.SourceID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	values := make([]string, 0, len(r.bySource))
	for s := range r.bySource {
		values = append(values, string(s))
	}
	sort.Strings(values)
	out := make([]kernel.SourceID, len(values))
	for i, s := range values {
		out[i] = kernel.SourceID(s)
	}
	return out
}
