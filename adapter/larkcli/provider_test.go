package larkcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type fakeRunner struct {
	last    CommandSpec
	calls   []CommandSpec
	result  CommandResult
	results []CommandResult
}

type runnerFunc func(context.Context, CommandSpec) (CommandResult, error)

func (f runnerFunc) Run(ctx context.Context, spec CommandSpec) (CommandResult, error) {
	return f(ctx, spec)
}

func (f *fakeRunner) Run(_ context.Context, s CommandSpec) (CommandResult, error) {
	f.last = s
	f.calls = append(f.calls, s)
	if len(f.results) > 0 {
		result := f.results[0]
		f.results = f.results[1:]
		return result, nil
	}
	return f.result, nil
}

func providerForSource(t *testing.T, runner Runner, cfg Config, source kernel.SourceID) *Provider {
	t.Helper()
	for _, provider := range NewProviders(runner, cfg) {
		if provider.Descriptor().Source == source {
			return provider
		}
	}
	t.Fatalf("provider for %s not found", source)
	return nil
}

func TestDocsProviderBuildsStructuredCommandAndParsesResult(t *testing.T) {
	r := &fakeRunner{result: CommandResult{ExitCode: 0, Stdout: []byte(`{"ok":true,"data":{"results":[{"token":"dox1","title":"A 项目方案","summary":"延期原因","url":"https://x/docx/dox1"}],"has_more":true,"page_token":"p2"}}`)}}
	docs := providerForSource(t, r, Config{Executable: "lark-cli"}, kernel.SourceDocs)
	page, err := docs.Search(context.Background(), kernel.ProviderSearchRequest{Query: "A 项目", Identity: kernel.Identity{Mode: kernel.IdentityUser, Profile: "work"}, PageSize: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Candidates) != 1 || page.Candidates[0].Title != "A 项目方案" || !page.HasMore {
		t.Fatalf("page=%+v", page)
	}
	joined := strings.Join(r.last.Args, " ")
	for _, want := range []string{"drive +search", "--query A 项目", "--profile work", "--as user", "--format json"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("command %q missing %q", joined, want)
		}
	}
}

func TestDocsSearchClassifiesStructuredObjectsAsNotFetchable(t *testing.T) {
	runner := &fakeRunner{result: CommandResult{ExitCode: 0, Stdout: []byte(`{"ok":true,"data":{"results":[{"entity_type":"DOC","title_highlighted":"数据表","result_meta":{"token":"base1","doc_types":"BITABLE","url":"https://tenant.feishu.cn/base/base1"}}]}}`)}}
	docs := providerForSource(t, runner, Config{Executable: "lark-cli"}, kernel.SourceDocs)
	page, err := docs.Search(context.Background(), kernel.ProviderSearchRequest{Query: "数据", Identity: kernel.Identity{Mode: kernel.IdentityUser}})
	if err != nil || len(page.Candidates) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	candidate := page.Candidates[0]
	if candidate.Kind != kernel.KindBase || !candidate.AvailableProjection.Empty() {
		t.Fatalf("candidate=%+v", candidate)
	}
	calls := len(runner.calls)
	_, err = docs.Fetch(context.Background(), []kernel.ProviderFetchRequest{{Ref: candidate.Ref, Projection: kernel.ProjectionContent}})
	if detail := kernel.DetailFromError(err); detail.Type != kernel.ErrUnsupported || len(runner.calls) != calls {
		t.Fatalf("error=%v detail=%+v calls=%d", err, detail, len(runner.calls))
	}
}

func TestHealthUsesVersionFlagAndChecksCommandSurface(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{results: []CommandResult{
		{ExitCode: 0, Stdout: []byte("lark-cli version 1.0.79\n")},
		{ExitCode: 0, Stdout: []byte("--query --page-size --page-token --only-title")},
	}}
	docs := providerForSource(t, runner, Config{Executable: executable}, kernel.SourceDocs)
	version, err := docs.Health(context.Background(), kernel.Identity{Mode: kernel.IdentityUser})
	if err != nil {
		t.Fatal(err)
	}
	if version != "lark-cli version 1.0.79" {
		t.Fatalf("version=%q", version)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls=%d", len(runner.calls))
	}
	if got := strings.Join(runner.calls[0].Args, " "); got != "--version" {
		t.Fatalf("version args=%q", got)
	}
	operationVersion, err := docs.HealthOperation(context.Background(), kernel.OpSearch, kernel.Identity{Mode: kernel.IdentityUser})
	if err != nil || operationVersion != version {
		t.Fatalf("operation version=%q err=%v", operationVersion, err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls=%d", len(runner.calls))
	}
	if got := strings.Join(runner.calls[1].Args, " "); got != "drive +search --help" {
		t.Fatalf("probe args=%q", got)
	}
	if docs.Descriptor().Version != version {
		t.Fatalf("descriptor version=%q", docs.Descriptor().Version)
	}
}

func TestHealthRejectsMissingRequiredFlag(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{results: []CommandResult{
		{ExitCode: 0, Stdout: []byte("lark-cli version 1.0.79\n")},
		{ExitCode: 0, Stdout: []byte("--query --page-size --page-token")},
	}}
	docs := providerForSource(t, runner, Config{Executable: executable}, kernel.SourceDocs)
	if _, err = docs.Health(context.Background(), kernel.Identity{Mode: kernel.IdentityUser}); err != nil {
		t.Fatal(err)
	}
	_, err = docs.HealthOperation(context.Background(), kernel.OpSearch, kernel.Identity{Mode: kernel.IdentityUser})
	if detail := kernel.DetailFromError(err); err == nil || detail.Type != kernel.ErrVersionIncompatible || !strings.Contains(err.Error(), "--only-title") {
		t.Fatalf("expected missing flag error, got %v", err)
	}
}

func TestMinuteFetchOperationProbeRequiresIsolatedTranscriptFlags(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{results: []CommandResult{
		{ExitCode: 0, Stdout: []byte("lark-cli version 1.0.79\n")},
		{ExitCode: 0, Stdout: []byte("--minute-tokens --summary --chapter --transcript --output-dir --overwrite")},
	}}
	minutes := providerForSource(t, runner, Config{Executable: executable}, kernel.SourceMinutes)
	if _, err := minutes.Health(context.Background(), kernel.Identity{Mode: kernel.IdentityUser}); err != nil {
		t.Fatal(err)
	}
	if _, err := minutes.HealthOperation(context.Background(), kernel.OpFetch, kernel.Identity{Mode: kernel.IdentityUser}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(runner.calls[1].Args, " "); got != "minutes +detail --help" {
		t.Fatalf("probe args=%q", got)
	}
}

func TestSearchRejectsTruncatedCLIOutput(t *testing.T) {
	runner := &fakeRunner{result: CommandResult{ExitCode: 0, Stdout: []byte(`{"ok":true}`), StdoutTruncated: true}}
	docs := providerForSource(t, runner, Config{Executable: "lark-cli"}, kernel.SourceDocs)
	_, err := docs.Search(context.Background(), kernel.ProviderSearchRequest{Query: "test", Identity: kernel.Identity{Mode: kernel.IdentityUser}})
	if detail := kernel.DetailFromError(err); err == nil || detail.Type != kernel.ErrBudgetExhausted {
		t.Fatalf("error=%v detail=%+v", err, detail)
	}
}

func TestMessageFetchMaterializesOnlySupportedProjection(t *testing.T) {
	runner := &fakeRunner{result: CommandResult{ExitCode: 0, Stdout: []byte(`{"ok":true,"data":{"message_id":"om1","chat_id":"oc1","thread_id":"omt1","content":"{\"text\":\"延期说明 https://example.feishu.cn/docx/dox1\"}"}}`)}}
	messages := providerForSource(t, runner, Config{Executable: "lark-cli"}, kernel.SourceMessages)
	ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: "scope", Kind: kernel.KindMessage, NativeID: "om1", CanonicalID: kernel.BuildCanonicalID("scope", kernel.KindMessage, "om1"), ProviderID: messages.Descriptor().ID, Source: kernel.SourceMessages}
	want := kernel.ProjectionContent | kernel.ProjectionContext | kernel.ProjectionRelations
	artifacts, err := messages.Fetch(context.Background(), []kernel.ProviderFetchRequest{{Ref: ref, Projection: want, Identity: kernel.Identity{Mode: kernel.IdentityUser, ScopeKey: "scope"}}})
	if err != nil || len(artifacts) != 1 || !artifacts[0].Projection.Has(want) || len(artifacts[0].Chunks) != 1 || len(artifacts[0].Relations) != 2 {
		t.Fatalf("artifacts=%+v err=%v", artifacts, err)
	}
	if messages.Descriptor().FetchableProjection.Has(kernel.ProjectionAttachments) {
		t.Fatal("messages must not advertise unimplemented attachment materialization")
	}
	calls := len(runner.calls)
	_, err = messages.Fetch(context.Background(), []kernel.ProviderFetchRequest{{Ref: ref, Projection: kernel.ProjectionAttachments}})
	if detail := kernel.DetailFromError(err); err == nil || detail.Type != kernel.ErrUnsupported || len(runner.calls) != calls {
		t.Fatalf("error=%v detail=%+v calls=%d want=%d", err, detail, len(runner.calls), calls)
	}
}

func TestRawAPIFetchRejectsPathTraversalBeforeRunnerCall(t *testing.T) {
	runner := &fakeRunner{}
	messages := providerForSource(t, runner, Config{Executable: "lark-cli"}, kernel.SourceMessages)
	ref := kernel.ObjectRef{Kind: kernel.KindMessage, NativeID: "../tasks/secret", Source: kernel.SourceMessages}
	_, err := messages.Fetch(context.Background(), []kernel.ProviderFetchRequest{{Ref: ref, Projection: kernel.ProjectionContent}})
	if err == nil || len(runner.calls) != 0 {
		t.Fatalf("path traversal reached runner: err=%v calls=%d", err, len(runner.calls))
	}
}

func TestFetchDoesNotTreatArbitraryJSONAsContent(t *testing.T) {
	runner := &fakeRunner{result: CommandResult{ExitCode: 0, Stdout: []byte(`{"ok":true,"data":{"task":{"guid":"task1","status":"todo"}}}`)}}
	tasks := providerForSource(t, runner, Config{Executable: "lark-cli"}, kernel.SourceTasks)
	ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: "scope", Kind: kernel.KindTask, NativeID: "task1", CanonicalID: kernel.BuildCanonicalID("scope", kernel.KindTask, "task1"), ProviderID: tasks.Descriptor().ID, Source: kernel.SourceTasks}
	_, err := tasks.Fetch(context.Background(), []kernel.ProviderFetchRequest{{Ref: ref, Projection: kernel.ProjectionContent}})
	if detail := kernel.DetailFromError(err); err == nil || detail.Type != kernel.ErrVersionIncompatible {
		t.Fatalf("error=%v detail=%+v", err, detail)
	}
}

func TestTaskFetchUsesDetailSummaryAsContentWithoutClaimingNativeSummary(t *testing.T) {
	runner := &fakeRunner{result: CommandResult{ExitCode: 0, Stdout: []byte(`{"ok":true,"data":{"task":{"guid":"task1","summary":"回归测试","description":""}}}`)}}
	tasks := providerForSource(t, runner, Config{Executable: "lark-cli"}, kernel.SourceTasks)
	ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: "scope", Kind: kernel.KindTask, NativeID: "task1", CanonicalID: kernel.BuildCanonicalID("scope", kernel.KindTask, "task1"), ProviderID: tasks.Descriptor().ID, Source: kernel.SourceTasks}
	artifacts, err := tasks.Fetch(context.Background(), []kernel.ProviderFetchRequest{{Ref: ref, Projection: kernel.ProjectionContent | kernel.ProjectionRelations}})
	if err != nil || len(artifacts) != 1 || !artifacts[0].Projection.Has(kernel.ProjectionContent|kernel.ProjectionRelations) || artifacts[0].Projection.Has(kernel.ProjectionSummary) || len(artifacts[0].Chunks) != 1 {
		t.Fatalf("artifacts=%+v err=%v", artifacts, err)
	}
}

func TestMinuteFetchReadsTranscriptFromIsolatedOutput(t *testing.T) {
	var outputDir, workingDir string
	runner := runnerFunc(func(_ context.Context, spec CommandSpec) (CommandResult, error) {
		workingDir = spec.Dir
		for index, arg := range spec.Args {
			if arg == "--output-dir" && index+1 < len(spec.Args) {
				outputDir = spec.Args[index+1]
			}
		}
		if outputDir != "." || spec.Dir == "" {
			t.Fatalf("command is not using an explicit isolated output directory: %+v", spec)
		}
		if err := os.WriteFile(filepath.Join(spec.Dir, "transcript.txt"), []byte("真实转写内容"), 0o600); err != nil {
			t.Fatal(err)
		}
		return CommandResult{ExitCode: 0, Stdout: []byte(`{"ok":true,"data":{"minutes":[{"minute_token":"minute1","artifacts":{"summary":"会议结论","todos":[{"text":"补测试"}],"chapters":[],"keywords":["延期"],"transcript_file":"transcript.txt"}}]}}`)}, nil
	})
	minutes := providerForSource(t, runner, Config{Executable: "lark-cli"}, kernel.SourceMinutes)
	ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: "scope", Kind: kernel.KindMinute, NativeID: "minute1", CanonicalID: kernel.BuildCanonicalID("scope", kernel.KindMinute, "minute1"), ProviderID: minutes.Descriptor().ID, Source: kernel.SourceMinutes}
	want := kernel.ProjectionSummary | kernel.ProjectionStructure | kernel.ProjectionContent
	artifacts, err := minutes.Fetch(context.Background(), []kernel.ProviderFetchRequest{{Ref: ref, Projection: want, Identity: kernel.Identity{Mode: kernel.IdentityUser, ScopeKey: "scope"}}})
	if err != nil || len(artifacts) != 1 || !artifacts[0].Projection.Has(want) || len(artifacts[0].Chunks) != 1 || artifacts[0].Chunks[0].Text != "真实转写内容" {
		t.Fatalf("artifacts=%+v err=%v", artifacts, err)
	}
	if outputDir != "." {
		t.Fatal("output directory was not captured")
	}
	if _, err := os.Stat(workingDir); !os.IsNotExist(err) {
		t.Fatalf("temporary transcript directory still exists: %v", err)
	}
}

func TestMinuteFetchDoesNotClaimMissingTranscriptAsContent(t *testing.T) {
	runner := runnerFunc(func(_ context.Context, spec CommandSpec) (CommandResult, error) {
		return CommandResult{ExitCode: 0, Stdout: []byte(`{"ok":true,"data":{"summary":"only metadata","transcript":"transcript.txt"}}`)}, nil
	})
	minutes := providerForSource(t, runner, Config{Executable: "lark-cli"}, kernel.SourceMinutes)
	ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: "scope", Kind: kernel.KindMinute, NativeID: "minute1", CanonicalID: kernel.BuildCanonicalID("scope", kernel.KindMinute, "minute1"), ProviderID: minutes.Descriptor().ID, Source: kernel.SourceMinutes}
	_, err := minutes.Fetch(context.Background(), []kernel.ProviderFetchRequest{{Ref: ref, Projection: kernel.ProjectionContent, Identity: kernel.Identity{Mode: kernel.IdentityUser, ScopeKey: "scope"}}})
	if detail := kernel.DetailFromError(err); err == nil || detail.Type != kernel.ErrVersionIncompatible {
		t.Fatalf("error=%v detail=%+v", err, detail)
	}
}

func TestBaseQueryUsesCurrentCLIFlagsAndParsesRecordMatrix(t *testing.T) {
	runner := &fakeRunner{result: CommandResult{ExitCode: 0, Stdout: []byte(`{"ok":true,"data":{"fields":["Title","Status"],"record_id_list":["rec_1"],"data":[["Launch","Todo"]],"has_more":true}}`)}}
	base := providerForSource(t, runner, Config{Executable: "lark-cli"}, kernel.SourceBase)
	page, err := base.Query(context.Background(), kernel.ProviderQueryRequest{
		Container: &kernel.ObjectRef{NativeID: "app_x/tbl_x"},
		Filter:    map[string]any{"keyword": "Launch", "search_field": "Title"},
		Identity:  kernel.Identity{Mode: kernel.IdentityUser},
		Limit:     20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Candidates) != 1 || page.Candidates[0].Ref.NativeID != "rec_1" || page.Candidates[0].Title != "Launch" {
		t.Fatalf("page=%+v", page)
	}
	if page.NextCursor != "1" || !page.HasMore {
		t.Fatalf("pagination=%+v", page)
	}
	joined := strings.Join(runner.last.Args, " ")
	for _, want := range []string{"base +record-search", "--base-token app_x", "--table-id tbl_x", "--keyword Launch", "--search-field Title", "--limit 20"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("command %q missing %q", joined, want)
		}
	}
	for _, forbidden := range []string{"--app-token", "--page-size"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("command %q contains stale flag %q", joined, forbidden)
		}
	}
}
