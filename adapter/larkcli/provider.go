package larkcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type Config struct {
	Executable  string
	ProfileArgs []string
	Timeout     time.Duration
	StdoutLimit int64
	StderrLimit int64
}

type Provider struct {
	spec      SourceSpec
	runner    Runner
	cfg       Config
	versionMu sync.RWMutex
	version   string
}

func NewProviders(r Runner, cfg Config) []*Provider {
	if r == nil {
		r = ExecRunner{}
	}
	if cfg.Executable == "" {
		cfg.Executable = "lark-cli"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 8 * time.Second
	}
	if cfg.StdoutLimit <= 0 {
		cfg.StdoutLimit = 16 << 20
	}
	if cfg.StderrLimit <= 0 {
		cfg.StderrLimit = 4 << 20
	}
	specs := BuiltinSpecs()
	out := make([]*Provider, 0, len(specs))
	for _, s := range specs {
		out = append(out, &Provider{spec: s, runner: r, cfg: cfg})
	}
	return out
}
func (p *Provider) Descriptor() kernel.ProviderDescriptor {
	descriptor := p.spec.Descriptor
	p.versionMu.RLock()
	descriptor.Version = p.version
	p.versionMu.RUnlock()
	return descriptor
}
func (p *Provider) Health(ctx context.Context, identity kernel.Identity) (string, error) {
	if _, err := exec.LookPath(p.cfg.Executable); err != nil {
		return "", &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: "lark-cli executable not found: " + err.Error(), ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source}
	}
	res, err := p.runProbe(ctx, []string{"--version"})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", p.probeError("version probe failed", res)
	}
	version := strings.TrimSpace(string(res.Stdout))
	if version == "" {
		return "", p.probeError("version probe returned no version", res)
	}
	p.versionMu.Lock()
	p.version = version
	p.versionMu.Unlock()
	return version, nil
}

func (p *Provider) HealthOperation(ctx context.Context, operation kernel.Operation, identity kernel.Identity) (string, error) {
	if !p.spec.Descriptor.Operations[operation] {
		return "", &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: fmt.Sprintf("operation %s is unsupported", operation), ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source}
	}
	p.versionMu.RLock()
	version := p.version
	p.versionMu.RUnlock()
	if version == "" {
		var err error
		version, err = p.Health(ctx, identity)
		if err != nil {
			return "", err
		}
	}
	probe, ok := p.spec.OperationProbes[operation]
	if !ok && ((operation == kernel.OpSearch && p.spec.SearchArgs != nil) || (operation == kernel.OpQuery && p.spec.QueryArgs != nil) || (operation == kernel.OpResolve && p.spec.Descriptor.Source == kernel.SourcePeople)) {
		probe = ProbeSpec{Args: p.spec.ProbeArgs, Flags: p.spec.ProbeFlags}
		ok = len(probe.Args) > 0
	}
	if !ok {
		return "", &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: fmt.Sprintf("operation %s has no verified lark-cli command", operation), ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source}
	}
	result, err := p.runProbe(ctx, probe.Args)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", p.probeError(fmt.Sprintf("required %s command is unavailable", operation), result)
	}
	help := string(result.Stdout) + "\n" + string(result.Stderr)
	for _, flag := range probe.Flags {
		if !strings.Contains(help, flag) {
			return "", &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, Message: fmt.Sprintf("lark-cli %s command is missing required flag %s", operation, flag), ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source}
		}
	}
	return version, nil
}

func (p *Provider) runProbe(ctx context.Context, args []string) (CommandResult, error) {
	final := append([]string{}, p.cfg.ProfileArgs...)
	final = append(final, args...)
	result, err := p.runner.Run(ctx, CommandSpec{Executable: p.cfg.Executable, Args: final, Timeout: 3 * time.Second, StdoutLimit: 1 << 20, StderrLimit: 1 << 20})
	if err == nil && (result.StdoutTruncated || result.StderrTruncated) {
		err = &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, Message: "lark-cli probe output exceeds limit", ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source}
	}
	return result, err
}

func (p *Provider) probeError(message string, result CommandResult) error {
	return &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: message, ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source}
}
func (p *Provider) Search(ctx context.Context, r kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	if p.spec.SearchArgs == nil {
		return kernel.CandidatePage{}, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: "search unsupported", ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source}
	}
	args, err := p.spec.SearchArgs(r)
	if err != nil {
		return kernel.CandidatePage{}, err
	}
	data, env, err := p.call(ctx, args, r.Identity)
	if err != nil {
		return kernel.CandidatePage{}, p.annotate(err)
	}
	if err := p.validateDataShape(data); err != nil {
		return kernel.CandidatePage{}, err
	}
	page := p.parsePage(data, env, r.Identity)
	if err := p.validatePage(page); err != nil {
		return kernel.CandidatePage{}, err
	}
	return page, nil
}
func (p *Provider) Query(ctx context.Context, r kernel.ProviderQueryRequest) (kernel.CandidatePage, error) {
	if p.spec.QueryArgs == nil {
		return kernel.CandidatePage{}, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: "query unsupported", ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source}
	}
	args, err := p.spec.QueryArgs(r)
	if err != nil {
		return kernel.CandidatePage{}, err
	}
	data, env, err := p.call(ctx, args, r.Identity)
	if err != nil {
		return kernel.CandidatePage{}, p.annotate(err)
	}
	if err := p.validateDataShape(data); err != nil {
		return kernel.CandidatePage{}, err
	}
	page := p.parsePage(data, env, r.Identity)
	if p.spec.Descriptor.Source == kernel.SourceBase && page.HasMore && page.NextCursor == "" {
		offset, _ := strconv.Atoi(r.Cursor)
		page.NextCursor = strconv.Itoa(offset + page.RawCount)
	}
	if err := p.validatePage(page); err != nil {
		return kernel.CandidatePage{}, err
	}
	return page, nil
}

func (p *Provider) validatePage(page kernel.CandidatePage) error {
	for index, candidate := range page.Candidates {
		if candidate.Ref.NativeID == "" || candidate.Ref.CanonicalID == "" {
			return &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source, Message: fmt.Sprintf("result %d is missing a stable object identifier", index)}
		}
	}
	if p.spec.Descriptor.SupportsPagination && page.HasMore && page.NextCursor == "" {
		return &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source, Message: "paginated response has_more=true without a cursor"}
	}
	return nil
}

func (p *Provider) validateDataShape(data any) error {
	if p.spec.Descriptor.Source != kernel.SourceBase {
		return nil
	}
	matrix := findRecordMatrix(data, 0)
	if matrix == nil {
		return nil
	}
	recordIDs := stringValues(matrix["record_id_list"])
	rows, _ := matrix["data"].([]any)
	if len(rows) != len(recordIDs) {
		return &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source, Message: "Base response row count does not match record_id_list"}
	}
	return nil
}
func (p *Provider) Fetch(ctx context.Context, reqs []kernel.ProviderFetchRequest) ([]kernel.Artifact, error) {
	if p.spec.FetchArgs == nil {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: "fetch unsupported", ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source}
	}
	out := make([]kernel.Artifact, 0, len(reqs))
	for _, r := range reqs {
		if r.Projection.Empty() {
			return out, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source, Message: "fetch projection is required"}
		}
		if unsupported := r.Projection &^ p.spec.Descriptor.FetchableProjection; unsupported != 0 {
			return out, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source, Message: "requested projection is not supported", Details: map[string]any{"projection": unsupported.Strings()}}
		}
		if p.spec.Descriptor.Source == kernel.SourceDocs && !fetchableDocumentRef(r.Ref) {
			return out, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source, Message: "the search hit is not a docx document; use Base or Sheets query for structured objects"}
		}
		args, err := p.spec.FetchArgs(r)
		if err != nil {
			return out, err
		}
		workDir := ""
		if p.spec.Descriptor.Source == kernel.SourceMinutes && r.Projection.Has(kernel.ProjectionContent) {
			workDir, err = os.MkdirTemp("", "sfs-minute-fetch-")
			if err != nil {
				return out, &kernel.ErrorDetail{Type: kernel.ErrUpstreamTransient, ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source, Message: "create isolated minute transcript directory failed"}
			}
			args = append(args, "--output-dir", ".", "--overwrite")
		}
		data, _, err := p.callInDir(ctx, args, r.Identity, workDir)
		if err != nil {
			if workDir != "" {
				_ = os.RemoveAll(workDir)
			}
			return out, p.annotate(err)
		}
		generatedContent := ""
		if workDir != "" {
			generatedContent, err = readGeneratedTranscript(workDir, p.cfg.StdoutLimit)
			_ = os.RemoveAll(workDir)
			if err != nil {
				return out, p.annotate(err)
			}
		}
		artifact, err := p.parseArtifact(r, data, generatedContent)
		if err != nil {
			return out, err
		}
		out = append(out, artifact)
	}
	return out, nil
}
func (p *Provider) Expand(ctx context.Context, r kernel.ProviderExpandRequest) ([]kernel.Relation, error) {
	if p.spec.ExpandArgs == nil {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: "expand unsupported", ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source}
	}
	args, err := p.spec.ExpandArgs(r)
	if err != nil {
		return nil, err
	}
	data, _, err := p.call(ctx, args, r.Identity)
	if err != nil {
		return nil, p.annotate(err)
	}
	return p.parseRelations(r, data), nil
}
func (p *Provider) Resolve(ctx context.Context, r kernel.ProviderResolveRequest) ([]kernel.ObjectRef, error) {
	if p.spec.Descriptor.Source != kernel.SourcePeople {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: "resolve unsupported"}
	}
	page, err := p.Search(ctx, kernel.ProviderSearchRequest{Query: r.Text, Identity: r.Identity, PageSize: r.Limit})
	if err != nil {
		return nil, err
	}
	out := make([]kernel.ObjectRef, 0, len(page.Candidates))
	for _, c := range page.Candidates {
		out = append(out, c.Ref)
	}
	return out, nil
}

func (p *Provider) call(ctx context.Context, args []string, identity kernel.Identity) (any, Envelope, error) {
	return p.callInDir(ctx, args, identity, "")
}

func (p *Provider) callInDir(ctx context.Context, args []string, identity kernel.Identity, dir string) (any, Envelope, error) {
	final := append([]string{}, p.cfg.ProfileArgs...)
	if profile := strings.TrimSpace(identity.Profile); profile != "" && !hasArg(final, "--profile") {
		final = append(final, "--profile", profile)
	}
	final = append(final, args...)
	if identity.Mode != kernel.IdentityAuto && identity.Mode != "" {
		final = append(final, "--as", string(identity.Mode))
	}
	final = append(final, "--format", "json")
	res, err := p.runner.Run(ctx, CommandSpec{Executable: p.cfg.Executable, Args: final, Dir: dir, Timeout: p.cfg.Timeout, StdoutLimit: p.cfg.StdoutLimit, StderrLimit: p.cfg.StderrLimit})
	if err != nil {
		return nil, Envelope{}, err
	}
	if res.StdoutTruncated || res.StderrTruncated {
		return nil, Envelope{}, &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source, Message: "lark-cli output exceeds configured limit"}
	}
	env, data, err := ParseEnvelope(res)
	return data, env, err
}
func hasArg(args []string, name string) bool {
	for i, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
		if name == "--profile" && arg == "-p" && i+1 < len(args) {
			return true
		}
	}
	return false
}

func (p *Provider) annotate(err error) error {
	var d *kernel.ErrorDetail
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		d = &kernel.ErrorDetail{Type: kernel.ErrDeadlineExceeded, Message: err.Error()}
	case errors.Is(err, context.Canceled):
		d = &kernel.ErrorDetail{Type: kernel.ErrCancelled, Message: err.Error()}
	default:
		d = kernel.DetailFromError(err)
	}
	d.ProviderID = p.spec.Descriptor.ID
	d.Source = p.spec.Descriptor.Source
	return d
}

func (p *Provider) parsePage(data any, env Envelope, identity kernel.Identity) kernel.CandidatePage {
	root := asMap(data)
	items := findItems(root, p.spec.ItemKeys)
	if p.spec.Descriptor.Source == kernel.SourceBase {
		items = baseRecordItems(root)
	}
	page := kernel.CandidatePage{RawCount: len(items), Metadata: map[string]any{}}
	page.NextCursor = findString(root, []string{"page_token", "next_page_token", "next_cursor", "cursor"})
	page.HasMore = findBool(root, []string{"has_more", "has_next"}) || page.NextCursor != ""
	for i, item := range items {
		c := p.itemToCandidate(item, i+1, identity)
		page.Candidates = append(page.Candidates, c)
	}
	if env.Meta != nil {
		page.Metadata["lark_meta"] = env.Meta
	}
	return page
}

func baseRecordItems(root map[string]any) []map[string]any {
	matrix := findRecordMatrix(root, 0)
	if matrix == nil {
		return nil
	}
	fields := stringValues(matrix["fields"])
	recordIDs := stringValues(matrix["record_id_list"])
	rows, _ := matrix["data"].([]any)
	items := make([]map[string]any, 0, len(recordIDs))
	for i, recordID := range recordIDs {
		item := map[string]any{"record_id": recordID}
		values := []any{}
		if i < len(rows) {
			values, _ = rows[i].([]any)
		}
		fieldValues := map[string]any{}
		for fieldIndex, field := range fields {
			if fieldIndex < len(values) {
				fieldValues[field] = values[fieldIndex]
			}
		}
		item["fields"] = fieldValues
		for _, value := range values {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" && text != "<nil>" {
				item["title"] = text
				break
			}
		}
		if encoded, err := json.Marshal(fieldValues); err == nil {
			item["text"] = string(encoded)
		}
		items = append(items, item)
	}
	return items
}

func findRecordMatrix(value any, depth int) map[string]any {
	if depth > 5 {
		return nil
	}
	switch current := value.(type) {
	case map[string]any:
		if _, hasIDs := current["record_id_list"]; hasIDs {
			if _, hasRows := current["data"]; hasRows {
				return current
			}
		}
		for _, child := range current {
			if found := findRecordMatrix(child, depth+1); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range current {
			if found := findRecordMatrix(child, depth+1); found != nil {
				return found
			}
		}
	}
	return nil
}

func stringValues(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text := strings.TrimSpace(fmt.Sprint(item)); text != "" && text != "<nil>" {
			result = append(result, text)
		}
	}
	return result
}
func (p *Provider) itemToCandidate(item map[string]any, rank int, identity kernel.Identity) kernel.Candidate {
	id := findString(item, p.spec.IDKeys)
	// Some fetch APIs require a compound parent/object identifier. Preserve it
	// at candidate creation time so a later Fetch can run from ObjectRef alone.
	switch p.spec.Descriptor.Source {
	case kernel.SourceCalendar:
		if calendarID := findString(item, []string{"calendar_id"}); calendarID != "" && id != "" && !strings.Contains(id, "/") {
			id = calendarID + "/" + id
		}
	case kernel.SourceMail:
		if mailboxID := findString(item, []string{"mailbox_id", "user_mailbox_id"}); mailboxID != "" && id != "" && !strings.Contains(id, "/") {
			id = mailboxID + "/" + id
		}
	}
	title := cleanText(findString(item, p.spec.TitleKeys))
	snippet := cleanText(findString(item, p.spec.SnippetKeys))
	url := findString(item, p.spec.URLKeys)
	when := findTime(item, p.spec.TimeKeys)
	kind := p.spec.Descriptor.ObjectKinds[0]
	availableProjection := p.spec.Descriptor.FetchableProjection
	if p.spec.Descriptor.Source == kernel.SourceDocs {
		var fetchable bool
		kind, fetchable = documentSearchKind(item)
		if !fetchable {
			availableProjection = 0
		}
	}
	if p.spec.Descriptor.Source == kernel.SourceTasks {
		if strings.Contains(strings.ToLower(findString(item, []string{"type", "resource_type"})), "list") {
			kind = kernel.KindTaskList
		}
	}
	ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: identity.Normalized().ScopeKey, Kind: kind, NativeID: id, ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source, URL: url}
	if id != "" {
		ref.CanonicalID = kernel.BuildCanonicalID(ref.ScopeKey, kind, id)
	}
	actors := extractActors(item)
	container := extractContainer(item)
	projection := kernel.ProjectionHead
	if snippet != "" && p.spec.Descriptor.ReturnedProjection.Has(kernel.ProjectionSnippet) {
		projection |= kernel.ProjectionSnippet
	}
	if p.spec.Descriptor.ReturnedProjection.Has(kernel.ProjectionContent) && findString(item, p.spec.SnippetKeys) != "" {
		projection |= kernel.ProjectionContent
	}
	if p.spec.Descriptor.ReturnedProjection.Has(kernel.ProjectionContext) && findString(item, []string{"chat_id", "chat_name", "chat_type", "chat_partner", "thread_id", "root_id", "parent_id"}) != "" {
		projection |= kernel.ProjectionContext
	}
	return kernel.Candidate{Ref: ref, Source: p.spec.Descriptor.Source, Kind: kind, Title: title, Snippet: snippet, URL: url, Timestamp: when, Actors: actors, Container: container, NativeRank: rank, Projection: projection, AvailableProjection: availableProjection, DiscoveredBy: []kernel.Discovery{{ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source, Rank: rank}}, Provenance: kernel.Provenance{ProviderID: p.spec.Descriptor.ID, Backend: "lark-cli", Operation: "search", RetrievedAt: time.Now().UTC(), SourceRank: rank, RawRef: item}}
}

func documentSearchKind(item map[string]any) (kernel.ObjectKind, bool) {
	docType := strings.ToUpper(strings.TrimSpace(findString(item, []string{"doc_types", "doc_type", "file_type"})))
	switch docType {
	case "DOC", "DOCX":
		return kernel.KindDocument, true
	case "SHEET", "SHEETS":
		return kernel.KindSheet, false
	case "BITABLE", "BASE":
		return kernel.KindBase, false
	case "FILE":
		return kernel.KindAttachment, false
	case "SLIDES", "MINDNOTE":
		return kernel.KindDocument, false
	}
	url := strings.ToLower(findString(item, []string{"url", "link"}))
	switch {
	case strings.Contains(url, "/sheets/"):
		return kernel.KindSheet, false
	case strings.Contains(url, "/base/"):
		return kernel.KindBase, false
	case strings.Contains(url, "/file/"):
		return kernel.KindAttachment, false
	default:
		return kernel.KindDocument, true
	}
}

func fetchableDocumentRef(ref kernel.ObjectRef) bool {
	if ref.Kind != "" && ref.Kind != kernel.KindDocument {
		return false
	}
	url := strings.ToLower(ref.URL)
	return !strings.Contains(url, "/sheets/") && !strings.Contains(url, "/base/") && !strings.Contains(url, "/file/")
}
func (p *Provider) parseArtifact(r kernel.ProviderFetchRequest, data any, generatedContent string) (kernel.Artifact, error) {
	if r.Projection.Empty() {
		return kernel.Artifact{}, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source, Message: "fetch projection is required"}
	}
	if unsupported := r.Projection &^ p.spec.Descriptor.FetchableProjection; unsupported != 0 {
		return kernel.Artifact{}, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source, Message: "requested projection is not supported", Details: map[string]any{"projection": unsupported.Strings()}}
	}
	m := asMap(data)
	if len(m) == 0 {
		return kernel.Artifact{}, &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source, Message: "fetch response contains no object data"}
	}
	a := kernel.Artifact{Ref: r.Ref, Projection: kernel.ProjectionHead, Metadata: m, Provenance: kernel.Provenance{ProviderID: p.spec.Descriptor.ID, Backend: "lark-cli", Operation: "fetch", RetrievedAt: time.Now().UTC(), RawRef: m}}
	content := generatedContent
	if p.spec.Descriptor.Source != kernel.SourceMinutes {
		content = extractContentText(data)
	}
	if text := strings.TrimSpace(content); text != "" {
		a.Chunks = []kernel.ContentChunk{{ID: "content", Kind: "text", Text: text}}
		a.Projection |= kernel.ProjectionContent
	}
	if p.spec.Descriptor.FetchableProjection.Has(kernel.ProjectionSummary) {
		if sum := extractSummary(m); sum != nil {
			a.Summary = sum
			if sum.Text != "" || len(sum.Todos) > 0 {
				a.Projection |= kernel.ProjectionSummary
			}
			if len(sum.Chapters) > 0 || len(sum.Keywords) > 0 {
				a.Projection |= kernel.ProjectionStructure
			}
		}
	}
	if r.Projection.Has(kernel.ProjectionStructure) {
		switch p.spec.Descriptor.Source {
		case kernel.SourceDocs:
			if len(a.Chunks) > 0 {
				a.Projection |= kernel.ProjectionStructure
			}
		}
	}
	if r.Projection.Has(kernel.ProjectionContext) && (p.spec.Descriptor.Source == kernel.SourceMessages || p.spec.Descriptor.Source == kernel.SourceMail) {
		a.Projection |= kernel.ProjectionContext
	}
	a.Relations = p.parseRelations(kernel.ProviderExpandRequest{Ref: r.Ref, Identity: r.Identity}, data)
	if r.Projection.Has(kernel.ProjectionRelations) {
		a.Projection |= kernel.ProjectionRelations
	}
	if !a.Projection.Has(r.Projection) {
		missing := r.Projection.Missing(a.Projection)
		return kernel.Artifact{}, &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, ProviderID: p.spec.Descriptor.ID, Source: p.spec.Descriptor.Source, Message: "fetch response did not materialize the requested projection", Details: map[string]any{"missing_projection": missing.Strings()}}
	}
	return a, nil
}

func readGeneratedTranscript(root string, limit int64) (string, error) {
	if limit <= 0 {
		limit = 16 << 20
	}
	paths := make([]string, 0, 1)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() && strings.EqualFold(filepath.Ext(entry.Name()), ".txt") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", &kernel.ErrorDetail{Type: kernel.ErrUpstreamTransient, Source: kernel.SourceMinutes, Message: "read generated minute transcript failed"}
	}
	sort.Strings(paths)
	if len(paths) != 1 {
		return "", &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, Source: kernel.SourceMinutes, Message: "minute transcript output did not contain exactly one text file"}
	}
	file, err := os.Open(paths[0])
	if err != nil {
		return "", &kernel.ErrorDetail{Type: kernel.ErrUpstreamTransient, Source: kernel.SourceMinutes, Message: "open generated minute transcript failed"}
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return "", &kernel.ErrorDetail{Type: kernel.ErrUpstreamTransient, Source: kernel.SourceMinutes, Message: "read generated minute transcript failed"}
	}
	if int64(len(payload)) > limit {
		return "", &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, Source: kernel.SourceMinutes, Message: "minute transcript exceeds configured limit"}
	}
	text := strings.TrimSpace(string(payload))
	if text == "" {
		return "", &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, Source: kernel.SourceMinutes, Message: "minute transcript output is empty"}
	}
	return text, nil
}
func (p *Provider) parseRelations(r kernel.ProviderExpandRequest, data any) []kernel.Relation {
	m := asMap(data)
	tokens := findAllStrings(m, []string{"minute_token", "doc_token", "document_id", "chat_id", "open_id"})
	seen := map[string]bool{}
	out := []kernel.Relation{}
	for key, vals := range tokens {
		for _, id := range vals {
			if id == "" || id == r.Ref.NativeID {
				continue
			}
			kind := kernel.KindUnknown
			rel := "related_to"
			source := kernel.SourceID("")
			switch key {
			case "minute_token":
				kind = kernel.KindMinute
				rel = "meeting.has_minute"
				source = kernel.SourceMinutes
			case "doc_token", "document_id":
				kind = kernel.KindDocument
				rel = "message.links_to_document"
				source = kernel.SourceDocs
			case "chat_id":
				kind = kernel.KindChat
				rel = "message.in_chat"
				source = kernel.SourceChats
			case "open_id":
				kind = kernel.KindPerson
				rel = "artifact.related_person"
				source = kernel.SourcePeople
			}
			k := rel + "|" + id
			if seen[k] {
				continue
			}
			seen[k] = true
			to := kernel.ObjectRef{Platform: "feishu", ScopeKey: r.Identity.Normalized().ScopeKey, Kind: kind, NativeID: id, CanonicalID: kernel.BuildCanonicalID(r.Identity.Normalized().ScopeKey, kind, id), Source: source}
			out = append(out, kernel.Relation{From: r.Ref, Type: rel, To: to, Confidence: .9, Provenance: kernel.Provenance{ProviderID: p.spec.Descriptor.ID, Backend: "lark-cli", Operation: "expand", RetrievedAt: time.Now().UTC()}})
		}
	}
	if p.spec.Descriptor.Source == kernel.SourceMessages {
		for _, u := range extractURLs(extractContentText(data)) {
			kind, id := kindIDFromURL(u)
			if id == "" {
				continue
			}
			k := "message.links_to_document|" + id
			if seen[k] {
				continue
			}
			seen[k] = true
			to := kernel.ObjectRef{Platform: "feishu", ScopeKey: r.Identity.Normalized().ScopeKey, Kind: kind, NativeID: id, CanonicalID: kernel.BuildCanonicalID(r.Identity.Normalized().ScopeKey, kind, id), Source: kernel.SourceDocs, URL: u}
			out = append(out, kernel.Relation{From: r.Ref, Type: "message.links_to_document", To: to, Confidence: .95, Provenance: kernel.Provenance{ProviderID: p.spec.Descriptor.ID, Backend: "lark-cli", Operation: "expand", RetrievedAt: time.Now().UTC()}})
		}
	}
	return out
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func findItems(root map[string]any, keys []string) []map[string]any {
	for _, k := range keys {
		if arr := toMapSlice(root[k]); len(arr) > 0 {
			return arr
		}
	}
	var walk func(any, int) []map[string]any
	walk = func(v any, depth int) []map[string]any {
		if depth > 5 {
			return nil
		}
		switch x := v.(type) {
		case map[string]any:
			for _, k := range keys {
				if arr := toMapSlice(x[k]); len(arr) > 0 {
					return arr
				}
			}
			ks := make([]string, 0, len(x))
			for k := range x {
				ks = append(ks, k)
			}
			sort.Strings(ks)
			for _, k := range ks {
				if r := walk(x[k], depth+1); len(r) > 0 {
					return r
				}
			}
		case []any:
			if arr := toMapSlice(x); len(arr) > 0 {
				return arr
			}
			for _, e := range x {
				if r := walk(e, depth+1); len(r) > 0 {
					return r
				}
			}
		}
		return nil
	}
	return walk(root, 0)
}

func toMapSlice(v any) []map[string]any {
	switch x := v.(type) {
	case []any:
		out := []map[string]any{}
		for _, e := range x {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case []map[string]any:
		return x
	}
	return nil
}

func findString(m map[string]any, keys []string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch x := v.(type) {
			case string:
				if strings.TrimSpace(x) != "" {
					return x
				}
			case json.Number:
				return x.String()
			case float64:
				return strconv.FormatFloat(x, 'f', -1, 64)
			case map[string]any:
				if s := findString(x, []string{"name", "text", "content", "value", "url"}); s != "" {
					return s
				}
			}
		}
	}
	for _, v := range m {
		switch child := v.(type) {
		case map[string]any:
			if s := findString(child, keys); s != "" {
				return s
			}
		case []any:
			for _, item := range child {
				if object, ok := item.(map[string]any); ok {
					if s := findString(object, keys); s != "" {
						return s
					}
				}
			}
		}
	}
	return ""
}

func findBool(m map[string]any, keys []string) bool {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if b, ok := v.(bool); ok {
				return b
			}
		}
	}
	return false
}

func cleanText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	var obj any
	if json.Unmarshal([]byte(s), &obj) == nil {
		if m, ok := obj.(map[string]any); ok {
			if t := findString(m, []string{"text", "content", "title"}); t != "" {
				s = t
			}
		}
	}
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > 500 {
		s = string([]rune(s)[:500]) + "…"
	}
	return s
}

func findTime(m map[string]any, keys []string) *time.Time {
	raw := findString(m, keys)
	if raw == "" {
		return nil
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if n > 1e12 {
			n /= 1000
		}
		t := time.Unix(n, 0).UTC()
		return &t
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return &t
		}
	}
	return nil
}

func extractActors(m map[string]any) []kernel.Actor {
	keys := []string{"sender", "owner", "creator", "organizer", "user"}
	seen := map[string]bool{}
	out := []kernel.Actor{}
	for _, k := range keys {
		if v, ok := m[k].(map[string]any); ok {
			id := findString(v, []string{"open_id", "user_id", "id"})
			name := findString(v, []string{"name", "display_name"})
			unique := id + "|" + name
			if id+name != "" && !seen[unique] {
				seen[unique] = true
				out = append(out, kernel.Actor{
					ID: id, Name: name,
					Email:  findString(v, []string{"email"}),
					Avatar: findString(v, []string{"avatar_url", "avatar"}),
				})
			}
		}
	}
	return out
}

func extractContainer(m map[string]any) *kernel.Container {
	id := findString(m, []string{"chat_id", "calendar_id", "table_id", "mailbox_id"})
	if id == "" {
		return nil
	}
	title := findString(m, []string{"chat_name", "calendar_name", "table_name", "mailbox_name"})
	return &kernel.Container{ID: id, Title: title}
}

func extractContentText(v any) string {
	m := asMap(v)
	for _, k := range []string{"markdown", "content", "text", "body", "transcript", "description", "summary", "title", "name", "subject"} {
		if s := findString(m, []string{k}); s != "" {
			return normalizeContentText(s)
		}
	}
	return ""
}

func normalizeContentText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var decoded any
	if json.Unmarshal([]byte(value), &decoded) != nil {
		return value
	}
	parts := make([]string, 0, 8)
	var walk func(any)
	walk = func(current any) {
		switch item := current.(type) {
		case string:
			if text := strings.TrimSpace(item); text != "" {
				parts = append(parts, text)
			}
		case []any:
			for _, child := range item {
				walk(child)
			}
		case map[string]any:
			for _, key := range []string{"title", "text", "content"} {
				if child, ok := item[key]; ok {
					walk(child)
				}
			}
		}
	}
	walk(decoded)
	return strings.Join(parts, "\n")
}

func extractSummary(m map[string]any) *kernel.NativeSummary {
	var s kernel.NativeSummary
	s.Text = cleanText(findString(m, []string{"summary", "ai_summary"}))
	if todos := toMapSlice(findAny(m, "todos")); len(todos) > 0 {
		s.Todos = todos
	}
	if ch := toMapSlice(findAny(m, "chapters")); len(ch) > 0 {
		s.Chapters = ch
	}
	if kw := toStringSlice(findAny(m, "keywords")); len(kw) > 0 {
		s.Keywords = kw
	}
	if s.Text == "" && len(s.Todos) == 0 && len(s.Chapters) == 0 && len(s.Keywords) == 0 {
		return nil
	}
	return &s
}

func findAny(m map[string]any, key string) any {
	if v, ok := m[key]; ok {
		return v
	}
	for _, v := range m {
		switch child := v.(type) {
		case map[string]any:
			if found := findAny(child, key); found != nil {
				return found
			}
		case []any:
			for _, item := range child {
				if object, ok := item.(map[string]any); ok {
					if found := findAny(object, key); found != nil {
						return found
					}
				}
			}
		}
	}
	return nil
}

func toStringSlice(v any) []string {
	a, ok := v.([]any)
	if !ok {
		return nil
	}
	out := []string{}
	for _, e := range a {
		out = append(out, fmt.Sprint(e))
	}
	return out
}

func findAllStrings(m map[string]any, keys []string) map[string][]string {
	wanted := map[string]bool{}
	for _, k := range keys {
		wanted[k] = true
	}
	out := map[string][]string{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if wanted[k] {
					switch y := val.(type) {
					case string:
						out[k] = append(out[k], y)
					case []any:
						for _, z := range y {
							out[k] = append(out[k], fmt.Sprint(z))
						}
					}
				}
				walk(val)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(m)
	return out
}

var urlRE = regexp.MustCompile(`https?://[^\s"'<>]+`)

func extractURLs(s string) []string { return urlRE.FindAllString(s, -1) }

func kindIDFromURL(u string) (kernel.ObjectKind, string) {
	parts := strings.Split(strings.Trim(strings.Split(u, "?")[0], "/"), "/")
	if len(parts) < 2 {
		return kernel.KindUnknown, ""
	}
	id := parts[len(parts)-1]
	typ := parts[len(parts)-2]
	switch typ {
	case "docx", "docs", "wiki", "sheets", "base", "file":
		return kernel.KindDocument, id
	default:
		return kernel.KindDocument, id
	}
}
