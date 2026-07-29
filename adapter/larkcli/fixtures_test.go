package larkcli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("testdata", "lark-cli", "1.0.79", name))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestLarkCLI179SuccessFixtureMatrix(t *testing.T) {
	var fixtures map[kernel.SourceID]json.RawMessage
	if err := json.Unmarshal(readFixture(t, "success-matrix.json"), &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, spec := range BuiltinSpecs() {
		spec := spec
		t.Run(string(spec.Descriptor.Source), func(t *testing.T) {
			raw := fixtures[spec.Descriptor.Source]
			if len(raw) == 0 {
				t.Fatalf("missing fixture for %s", spec.Descriptor.Source)
			}
			envelope, data, err := ParseEnvelope(CommandResult{ExitCode: 0, Stdout: raw})
			if err != nil {
				t.Fatal(err)
			}
			provider := Provider{spec: spec}
			page := provider.parsePage(data, envelope, kernel.Identity{Mode: kernel.IdentityUser, ScopeKey: "fixture"})
			if len(page.Candidates) != 1 || page.Candidates[0].Ref.NativeID == "" {
				t.Fatalf("page=%+v", page)
			}
			if spec.Descriptor.SupportsPagination && spec.Descriptor.Source != kernel.SourcePeople && spec.Descriptor.Source != kernel.SourceSheets && !page.HasMore {
				t.Fatalf("expected pagination: %+v", page)
			}
		})
	}
}

func TestLarkCLI179EmptyFixtureAllSources(t *testing.T) {
	envelope, data, err := ParseEnvelope(CommandResult{ExitCode: 0, Stdout: readFixture(t, "empty.json")})
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range BuiltinSpecs() {
		page := (&Provider{spec: spec}).parsePage(data, envelope, kernel.Identity{Mode: kernel.IdentityUser, ScopeKey: "fixture"})
		if page.RawCount != 0 || len(page.Candidates) != 0 || page.HasMore {
			t.Fatalf("source=%s page=%+v", spec.Descriptor.Source, page)
		}
	}
}

func TestLarkCLI179ErrorFixtureMatrix(t *testing.T) {
	var fixtures map[string]json.RawMessage
	if err := json.Unmarshal(readFixture(t, "error-matrix.json"), &fixtures); err != nil {
		t.Fatal(err)
	}
	want := map[string]kernel.ErrorType{"missing_scope": kernel.ErrMissingScope, "identity": kernel.ErrIdentityRequired, "rate_limit": kernel.ErrRateLimited, "validation": kernel.ErrInvalidRequest, "not_found": kernel.ErrNotFound, "timeout": kernel.ErrUpstreamTransient, "server_error": kernel.ErrUpstreamTransient, "schema": kernel.ErrVersionIncompatible}
	for name, expected := range want {
		_, _, err := ParseEnvelope(CommandResult{ExitCode: 1, Stderr: fixtures[name]})
		if detail := kernel.DetailFromError(err); detail.Type != expected {
			t.Fatalf("fixture=%s type=%s err=%v", name, detail.Type, err)
		}
	}
}

func TestLarkCLI179ErrorMatrixAnnotatesEverySource(t *testing.T) {
	var errorsByCase map[string]json.RawMessage
	if err := json.Unmarshal(readFixture(t, "error-matrix.json"), &errorsByCase); err != nil {
		t.Fatal(err)
	}
	want := map[string]kernel.ErrorType{"missing_scope": kernel.ErrMissingScope, "identity": kernel.ErrIdentityRequired, "rate_limit": kernel.ErrRateLimited, "timeout": kernel.ErrUpstreamTransient, "server_error": kernel.ErrUpstreamTransient, "schema": kernel.ErrVersionIncompatible}
	for _, spec := range BuiltinSpecs() {
		for name, expected := range want {
			t.Run(string(spec.Descriptor.Source)+"/"+name, func(t *testing.T) {
				provider := &Provider{spec: spec, runner: &fakeRunner{result: CommandResult{ExitCode: 1, Stderr: errorsByCase[name]}}, cfg: Config{Executable: "lark-cli"}}
				err := invokeFixtureProvider(provider)
				detail := kernel.DetailFromError(err)
				if detail.Type != expected || detail.Source != spec.Descriptor.Source || detail.ProviderID != spec.Descriptor.ID {
					t.Fatalf("detail=%+v", detail)
				}
			})
		}
	}
}

func TestLarkCLI179MissingStableIDsFailEverySource(t *testing.T) {
	var fixtures map[kernel.SourceID]json.RawMessage
	if err := json.Unmarshal(readFixture(t, "missing-fields-matrix.json"), &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, spec := range BuiltinSpecs() {
		t.Run(string(spec.Descriptor.Source), func(t *testing.T) {
			provider := &Provider{spec: spec, runner: &fakeRunner{result: CommandResult{ExitCode: 0, Stdout: fixtures[spec.Descriptor.Source]}}, cfg: Config{Executable: "lark-cli"}}
			detail := kernel.DetailFromError(invokeFixtureProvider(provider))
			if detail.Type != kernel.ErrVersionIncompatible {
				t.Fatalf("detail=%+v", detail)
			}
		})
	}
}

func TestLarkCLI179UnknownFieldsRemainCompatibleForEverySource(t *testing.T) {
	var fixtures map[kernel.SourceID]map[string]any
	if err := json.Unmarshal(readFixture(t, "success-matrix.json"), &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, spec := range BuiltinSpecs() {
		t.Run(string(spec.Descriptor.Source), func(t *testing.T) {
			fixture := fixtures[spec.Descriptor.Source]
			fixture["future_envelope_field"] = map[string]any{"nested": true}
			if data, ok := fixture["data"].(map[string]any); ok {
				data["future_data_field"] = []any{"opaque"}
			}
			raw, _ := json.Marshal(fixture)
			provider := &Provider{spec: spec, runner: &fakeRunner{result: CommandResult{ExitCode: 0, Stdout: raw}}, cfg: Config{Executable: "lark-cli"}}
			if err := invokeFixtureProvider(provider); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLarkCLI179PaginationRequiresCursor(t *testing.T) {
	var fixtures map[kernel.SourceID]map[string]any
	if err := json.Unmarshal(readFixture(t, "success-matrix.json"), &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, spec := range BuiltinSpecs() {
		if !spec.Descriptor.SupportsPagination || spec.Descriptor.Source == kernel.SourceBase {
			continue
		}
		t.Run(string(spec.Descriptor.Source), func(t *testing.T) {
			fixture := fixtures[spec.Descriptor.Source]
			data, _ := fixture["data"].(map[string]any)
			delete(data, "page_token")
			raw, _ := json.Marshal(fixture)
			provider := &Provider{spec: spec, runner: &fakeRunner{result: CommandResult{ExitCode: 0, Stdout: raw}}, cfg: Config{Executable: "lark-cli"}}
			detail := kernel.DetailFromError(invokeFixtureProvider(provider))
			if detail.Type != kernel.ErrVersionIncompatible {
				t.Fatalf("detail=%+v", detail)
			}
		})
	}
}

func invokeFixtureProvider(provider *Provider) error {
	identity := kernel.Identity{Mode: kernel.IdentityUser, ScopeKey: "fixture"}
	if provider.spec.SearchArgs != nil {
		_, err := provider.Search(context.Background(), kernel.ProviderSearchRequest{Query: "fixture", Identity: identity, PageSize: 10})
		return err
	}
	container := &kernel.ObjectRef{NativeID: "spreadsheet"}
	filter := map[string]any{"find": "fixture"}
	if provider.spec.Descriptor.Source == kernel.SourceBase {
		container.NativeID = "app/table"
		filter = map[string]any{"keyword": "fixture", "search_field": "名称"}
	}
	_, err := provider.Query(context.Background(), kernel.ProviderQueryRequest{Container: container, Filter: filter, Identity: identity, Limit: 10})
	return err
}

func TestLarkCLI179SchemaDriftIgnoresUnknownFields(t *testing.T) {
	envelope, data, err := ParseEnvelope(CommandResult{ExitCode: 0, Stdout: readFixture(t, "schema-drift.json")})
	if err != nil {
		t.Fatal(err)
	}
	spec := BuiltinSpecs()[0]
	page := (&Provider{spec: spec}).parsePage(data, envelope, kernel.Identity{Mode: kernel.IdentityUser, ScopeKey: "fixture"})
	if len(page.Candidates) != 1 || page.Candidates[0].Ref.NativeID != "dox_drift" {
		t.Fatalf("page=%+v", page)
	}
}
