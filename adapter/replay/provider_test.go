package replay

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestRecordingAndReplayProviderOperations(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := replayFixtureProvider{descriptor: replayDescriptor()}
	recording, err := Wrap(base, store)
	if err != nil {
		t.Fatal(err)
	}
	if recording.Descriptor().ID != base.descriptor.ID {
		t.Fatalf("descriptor=%+v", recording.Descriptor())
	}
	if health, err := recording.Health(context.Background(), kernel.Identity{}); err != nil || health != "fixture/2" {
		t.Fatalf("health=%q err=%v", health, err)
	}

	searchReq := kernel.ProviderSearchRequest{Query: "project", SessionID: "recorded"}
	queryReq := kernel.ProviderQueryRequest{Filter: map[string]any{"query": "task"}, SessionID: "recorded"}
	fetchReq := []kernel.ProviderFetchRequest{{Ref: kernel.ObjectRef{NativeID: "doc"}, Projection: kernel.ProjectionContent, SessionID: "recorded"}}
	expandReq := kernel.ProviderExpandRequest{Ref: kernel.ObjectRef{NativeID: "doc"}, SessionID: "recorded"}
	resolveReq := kernel.ProviderResolveRequest{Text: "person"}

	if out, err := recording.Search(context.Background(), searchReq); err != nil || len(out.Candidates) != 1 {
		t.Fatalf("record search=%+v err=%v", out, err)
	}
	if out, err := recording.Query(context.Background(), queryReq); err != nil || len(out.Candidates) != 1 {
		t.Fatalf("record query=%+v err=%v", out, err)
	}
	if out, err := recording.Fetch(context.Background(), fetchReq); err != nil || len(out) != 1 {
		t.Fatalf("record fetch=%+v err=%v", out, err)
	}
	if out, err := recording.Expand(context.Background(), expandReq); err != nil || len(out) != 1 {
		t.Fatalf("record expand=%+v err=%v", out, err)
	}
	if out, err := recording.Resolve(context.Background(), resolveReq); err != nil || len(out) != 1 {
		t.Fatalf("record resolve=%+v err=%v", out, err)
	}

	replay := NewProvider(base.descriptor, store)
	if replay.Descriptor().Backend != "replay" {
		t.Fatalf("replay descriptor=%+v", replay.Descriptor())
	}
	if health, err := replay.Health(context.Background(), kernel.Identity{}); err != nil || health != "replay/1" {
		t.Fatalf("replay health=%q err=%v", health, err)
	}
	searchReq.SessionID = "different"
	queryReq.SessionID = "different"
	fetchReq[0].SessionID = "different"
	expandReq.SessionID = "different"
	if out, err := replay.Search(context.Background(), searchReq); err != nil || len(out.Candidates) != 1 || out.Candidates[0].Title != "search result" {
		t.Fatalf("replay search=%+v err=%v", out, err)
	}
	if out, err := replay.Query(context.Background(), queryReq); err != nil || len(out.Candidates) != 1 || out.Candidates[0].Title != "query result" {
		t.Fatalf("replay query=%+v err=%v", out, err)
	}
	if out, err := replay.Fetch(context.Background(), fetchReq); err != nil || len(out) != 1 || out[0].Ref.NativeID != "doc" {
		t.Fatalf("replay fetch=%+v err=%v", out, err)
	}
	if out, err := replay.Expand(context.Background(), expandReq); err != nil || len(out) != 1 || out[0].Type != "related" {
		t.Fatalf("replay expand=%+v err=%v", out, err)
	}
	if out, err := replay.Resolve(context.Background(), resolveReq); err != nil || len(out) != 1 || out[0].NativeID != "person" {
		t.Fatalf("replay resolve=%+v err=%v", out, err)
	}
}

func TestRecordingProviderValidationFallbackAndUnsupportedOperations(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Wrap(nil, store); err == nil {
		t.Fatal("nil base provider accepted")
	}
	if _, err := Wrap(replayBareProvider{descriptor: replayDescriptor()}, nil); err == nil {
		t.Fatal("nil store accepted")
	}
	if _, err := Wrap(replayBareProvider{descriptor: kernel.ProviderDescriptor{}}, store); err == nil {
		t.Fatal("provider with empty descriptor id accepted")
	}

	bare := replayBareProvider{descriptor: replayDescriptor()}
	recording, err := Wrap(bare, store)
	if err != nil {
		t.Fatal(err)
	}
	if health, err := recording.Health(context.Background(), kernel.Identity{}); err != nil || health != bare.descriptor.Version {
		t.Fatalf("fallback health=%q err=%v", health, err)
	}
	assertUnsupported := func(t *testing.T, err error) {
		t.Helper()
		var detail *kernel.ErrorDetail
		if !errors.As(err, &detail) || detail.Type != kernel.ErrUnsupported || detail.ProviderID != bare.descriptor.ID || detail.Source != bare.descriptor.Source {
			t.Fatalf("unsupported error=%v", err)
		}
	}
	_, err = recording.Search(context.Background(), kernel.ProviderSearchRequest{})
	assertUnsupported(t, err)
	_, err = recording.Query(context.Background(), kernel.ProviderQueryRequest{})
	assertUnsupported(t, err)
	_, err = recording.Fetch(context.Background(), nil)
	assertUnsupported(t, err)
	_, err = recording.Expand(context.Background(), kernel.ProviderExpandRequest{})
	assertUnsupported(t, err)
	_, err = recording.Resolve(context.Background(), kernel.ProviderResolveRequest{})
	assertUnsupported(t, err)
}

func TestRecordingProviderReturnsFixturePersistenceFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "recordings")
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	recording, err := Wrap(replayFixtureProvider{descriptor: replayDescriptor()}, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = recording.Search(context.Background(), kernel.ProviderSearchRequest{Query: "project"})
	if err == nil || !strings.Contains(err.Error(), "persist replay fixture") {
		t.Fatalf("recording error=%v", err)
	}
}

type replayBareProvider struct {
	descriptor kernel.ProviderDescriptor
}

func (p replayBareProvider) Descriptor() kernel.ProviderDescriptor { return p.descriptor }

type replayFixtureProvider struct {
	descriptor kernel.ProviderDescriptor
}

func (p replayFixtureProvider) Descriptor() kernel.ProviderDescriptor { return p.descriptor }
func (replayFixtureProvider) Health(context.Context, kernel.Identity) (string, error) {
	return "fixture/2", nil
}
func (replayFixtureProvider) Search(context.Context, kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	return kernel.CandidatePage{Candidates: []kernel.Candidate{{Title: "search result"}}, RawCount: 1}, nil
}
func (replayFixtureProvider) Query(context.Context, kernel.ProviderQueryRequest) (kernel.CandidatePage, error) {
	return kernel.CandidatePage{Candidates: []kernel.Candidate{{Title: "query result"}}, RawCount: 1}, nil
}
func (replayFixtureProvider) Fetch(context.Context, []kernel.ProviderFetchRequest) ([]kernel.Artifact, error) {
	return []kernel.Artifact{{Ref: kernel.ObjectRef{NativeID: "doc"}}}, nil
}
func (replayFixtureProvider) Expand(context.Context, kernel.ProviderExpandRequest) ([]kernel.Relation, error) {
	return []kernel.Relation{{Type: "related"}}, nil
}
func (replayFixtureProvider) Resolve(context.Context, kernel.ProviderResolveRequest) ([]kernel.ObjectRef, error) {
	return []kernel.ObjectRef{{NativeID: "person"}}, nil
}

func replayDescriptor() kernel.ProviderDescriptor {
	return kernel.ProviderDescriptor{ID: "fixture.docs", Source: kernel.SourceDocs, Backend: "fixture", Version: "2"}
}
