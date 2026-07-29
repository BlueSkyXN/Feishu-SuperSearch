package provider

import (
	"context"
	"testing"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type routeProvider struct{ d kernel.ProviderDescriptor }

func (p routeProvider) Descriptor() kernel.ProviderDescriptor { return p.d }
func (p routeProvider) Search(context.Context, kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	return kernel.CandidatePage{}, nil
}
func (p routeProvider) Fetch(context.Context, []kernel.ProviderFetchRequest) ([]kernel.Artifact, error) {
	return nil, nil
}

func TestOperationRoutes(t *testing.T) {
	r := NewRegistry()
	cli := routeProvider{d: kernel.ProviderDescriptor{ID: "cli.docs", Source: kernel.SourceDocs, Operations: kernel.OperationSet{kernel.OpSearch: true, kernel.OpFetch: true}}}
	api := routeProvider{d: kernel.ProviderDescriptor{ID: "api.docs", Source: kernel.SourceDocs, Operations: kernel.OperationSet{kernel.OpSearch: true}}}
	if err := r.Register(cli, true); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(api, false); err != nil {
		t.Fatal(err)
	}
	if err := r.SetPreferredForOperation(kernel.SourceDocs, kernel.OpSearch, "api.docs"); err != nil {
		t.Fatal(err)
	}

	p, ok := r.ForOperation(kernel.SourceDocs, kernel.OpSearch)
	if !ok || p.Descriptor().ID != "api.docs" {
		t.Fatalf("search route = %v %v", p, ok)
	}
	p, ok = r.ForOperation(kernel.SourceDocs, kernel.OpFetch)
	if !ok || p.Descriptor().ID != "cli.docs" {
		t.Fatalf("fetch fallback = %v %v", p, ok)
	}
}

func TestOperationRouteRejectsUnsupportedProvider(t *testing.T) {
	r := NewRegistry()
	p := routeProvider{d: kernel.ProviderDescriptor{ID: "search-only", Source: kernel.SourceDocs, Operations: kernel.OperationSet{kernel.OpSearch: true}}}
	if err := r.Register(p, true); err != nil {
		t.Fatal(err)
	}
	if err := r.SetPreferredForOperation(kernel.SourceDocs, kernel.OpFetch, "search-only"); err == nil {
		t.Fatal("expected route validation error")
	}
}

func TestUnavailablePreferredProviderFallsBackByOperation(t *testing.T) {
	r := NewRegistry()
	primary := routeProvider{d: kernel.ProviderDescriptor{ID: "api.docs", Source: kernel.SourceDocs, Operations: kernel.OperationSet{kernel.OpSearch: true}}}
	backup := routeProvider{d: kernel.ProviderDescriptor{ID: "cli.docs", Source: kernel.SourceDocs, Operations: kernel.OperationSet{kernel.OpSearch: true}}}
	if err := r.Register(primary, true); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(backup, false); err != nil {
		t.Fatal(err)
	}
	if err := r.SetUnavailable("api.docs", &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, Message: "unsupported API version"}); err != nil {
		t.Fatal(err)
	}
	p, ok := r.ForOperation(kernel.SourceDocs, kernel.OpSearch)
	if !ok || p.Descriptor().ID != "cli.docs" {
		t.Fatalf("fallback=%v ok=%v", p, ok)
	}
	if reason := r.UnavailableReason("api.docs"); reason == nil || reason.Type != kernel.ErrVersionIncompatible {
		t.Fatalf("reason=%+v", reason)
	}
	if err := r.SetUnavailable("api.docs", nil); err != nil {
		t.Fatal(err)
	}
	p, ok = r.ForOperation(kernel.SourceDocs, kernel.OpSearch)
	if !ok || p.Descriptor().ID != "api.docs" {
		t.Fatalf("restored=%v ok=%v", p, ok)
	}
}

func TestOperationUnavailableFallsBackWithoutDisablingOtherOperations(t *testing.T) {
	r := NewRegistry()
	primary := routeProvider{d: kernel.ProviderDescriptor{ID: "api.docs", Source: kernel.SourceDocs, Operations: kernel.OperationSet{kernel.OpSearch: true, kernel.OpFetch: true}}}
	backup := routeProvider{d: kernel.ProviderDescriptor{ID: "cli.docs", Source: kernel.SourceDocs, Operations: kernel.OperationSet{kernel.OpSearch: true, kernel.OpFetch: true}}}
	if err := r.Register(primary, true); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(backup, false); err != nil {
		t.Fatal(err)
	}
	reason := &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, Message: "fetch command missing"}
	if err := r.SetOperationUnavailable("api.docs", kernel.OpFetch, reason); err != nil {
		t.Fatal(err)
	}
	if selected, ok := r.ForOperation(kernel.SourceDocs, kernel.OpSearch); !ok || selected.Descriptor().ID != "api.docs" {
		t.Fatalf("search selected=%v ok=%v", selected, ok)
	}
	if selected, ok := r.ForOperation(kernel.SourceDocs, kernel.OpFetch); !ok || selected.Descriptor().ID != "cli.docs" {
		t.Fatalf("fetch selected=%v ok=%v", selected, ok)
	}
	stored := r.OperationUnavailableReason("api.docs", kernel.OpFetch)
	if stored == nil || stored.Message != "fetch command missing" {
		t.Fatalf("stored=%+v", stored)
	}
	reason.Message = "mutated"
	stored.Message = "caller mutation"
	if again := r.OperationUnavailableReason("api.docs", kernel.OpFetch); again == nil || again.Message != "fetch command missing" {
		t.Fatalf("stored operation reason was mutated: %+v", again)
	}
	if err := r.SetOperationUnavailable("api.docs", kernel.OpFetch, nil); err != nil {
		t.Fatal(err)
	}
	if selected, ok := r.ForOperation(kernel.SourceDocs, kernel.OpFetch); !ok || selected.Descriptor().ID != "api.docs" {
		t.Fatalf("restored fetch selected=%v ok=%v", selected, ok)
	}
}

func TestRegistryValidationEnumerationAndAvailability(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil, false); err == nil {
		t.Fatal("nil provider accepted")
	}
	invalid := routeProvider{d: kernel.ProviderDescriptor{ID: "invalid"}}
	if err := r.Register(invalid, false); err == nil {
		t.Fatal("descriptor without source accepted")
	}
	docs := routeProvider{d: kernel.ProviderDescriptor{ID: "docs", Source: kernel.SourceDocs, Operations: kernel.OperationSet{kernel.OpSearch: true}}}
	messages := routeProvider{d: kernel.ProviderDescriptor{ID: "messages", Source: kernel.SourceMessages, Operations: kernel.OperationSet{kernel.OpSearch: true}}}
	if err := r.Register(messages, false); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(docs, true); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(docs, false); err == nil {
		t.Fatal("duplicate provider accepted")
	}
	if err := r.SetPreferred(kernel.SourceDocs, "missing"); err == nil {
		t.Fatal("missing preferred provider accepted")
	}
	if err := r.SetPreferred(kernel.SourceMessages, "docs"); err == nil {
		t.Fatal("cross-source preferred provider accepted")
	}
	if p, ok := r.ByID("docs"); !ok || p.Descriptor().ID != "docs" {
		t.Fatalf("ByID=%v ok=%v", p, ok)
	}
	if p, ok := r.ForSource(kernel.SourceDocs); !ok || p.Descriptor().ID != "docs" {
		t.Fatalf("ForSource=%v ok=%v", p, ok)
	}
	if got := r.Sources(); len(got) != 2 || got[0] != kernel.SourceDocs || got[1] != kernel.SourceMessages {
		t.Fatalf("sources=%v", got)
	}
	if got := r.All(); len(got) != 2 || got[0].Descriptor().ID != "docs" || got[1].Descriptor().ID != "messages" {
		t.Fatalf("all=%v", got)
	}
	if got := r.ProvidersForSource(kernel.SourceDocs); len(got) != 1 || got[0].Descriptor().ID != "docs" {
		t.Fatalf("docs providers=%v", got)
	}

	reason := &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, Message: "bad version"}
	if err := r.SetUnavailable("missing", reason); err == nil {
		t.Fatal("unknown provider marked unavailable")
	}
	if err := r.SetUnavailable("docs", reason); err != nil {
		t.Fatal(err)
	}
	reason.Message = "mutated"
	if _, ok := r.AvailableByID("docs"); ok {
		t.Fatal("unavailable provider reported available")
	}
	gotReason := r.UnavailableReason("docs")
	if gotReason == nil || gotReason.Message != "bad version" {
		t.Fatalf("reason=%+v", gotReason)
	}
	gotReason.Message = "caller mutation"
	if again := r.UnavailableReason("docs"); again == nil || again.Message != "bad version" {
		t.Fatalf("stored reason was mutated: %+v", again)
	}
	if _, ok := r.ForSource(kernel.SourceDocs); ok {
		t.Fatal("unavailable preferred provider selected")
	}
	if err := r.SetUnavailable("docs", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.AvailableByID("docs"); !ok || r.UnavailableReason("docs") != nil {
		t.Fatal("provider was not restored")
	}
}
