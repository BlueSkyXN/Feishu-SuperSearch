package execprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type Config struct {
	Descriptor     kernel.ProviderDescriptor `json:"descriptor"`
	Command        string                    `json:"command"`
	Args           []string                  `json:"args,omitempty"`
	Timeout        time.Duration             `json:"-"`
	MaxStdoutBytes int64                     `json:"max_stdout_bytes,omitempty"`
	MaxStderrBytes int64                     `json:"max_stderr_bytes,omitempty"`
}

type Provider struct{ cfg Config }

func New(cfg Config) (*Provider, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("external provider command is required")
	}
	if err := validateDescriptor(cfg.Descriptor); err != nil {
		return nil, err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.MaxStdoutBytes <= 0 {
		cfg.MaxStdoutBytes = 16 << 20
	}
	if cfg.MaxStderrBytes <= 0 {
		cfg.MaxStderrBytes = 4 << 20
	}
	if cfg.Descriptor.Backend == "" {
		cfg.Descriptor.Backend = "exec-jsonrpc"
	}
	return &Provider{cfg: cfg}, nil
}
func (p *Provider) Descriptor() kernel.ProviderDescriptor { return p.cfg.Descriptor }
func (p *Provider) Health(ctx context.Context, identity kernel.Identity) (string, error) {
	var result struct {
		Version string `json:"version"`
	}
	err := p.call(ctx, "health", map[string]any{"identity": identity}, &result)
	return result.Version, err
}
func (p *Provider) Search(ctx context.Context, r kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	var out kernel.CandidatePage
	return out, p.call(ctx, "search", r, &out)
}
func (p *Provider) Query(ctx context.Context, r kernel.ProviderQueryRequest) (kernel.CandidatePage, error) {
	var out kernel.CandidatePage
	return out, p.call(ctx, "query", r, &out)
}
func (p *Provider) Fetch(ctx context.Context, r []kernel.ProviderFetchRequest) ([]kernel.Artifact, error) {
	var out []kernel.Artifact
	return out, p.call(ctx, "fetch", r, &out)
}
func (p *Provider) Expand(ctx context.Context, r kernel.ProviderExpandRequest) ([]kernel.Relation, error) {
	var out []kernel.Relation
	return out, p.call(ctx, "expand", r, &out)
}
func (p *Provider) Resolve(ctx context.Context, r kernel.ProviderResolveRequest) ([]kernel.ObjectRef, error) {
	var out []kernel.ObjectRef
	return out, p.call(ctx, "resolve", r, &out)
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data,omitempty"`
	} `json:"error,omitempty"`
}

func (p *Provider) call(ctx context.Context, method string, params, out any) error {
	if !supportedMethods[method] {
		return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, ProviderID: p.cfg.Descriptor.ID, Source: p.cfg.Descriptor.Source, Message: "unsupported external provider method " + method}
	}
	callCtx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()
	req := rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params}
	b, _ := json.Marshal(req)
	b = append(b, '\n')
	cmd := exec.CommandContext(callCtx, p.cfg.Command, p.cfg.Args...)
	cmd.Stdin = bytes.NewReader(b)
	stdout := &limitedBuffer{limit: p.cfg.MaxStdoutBytes}
	stderr := &limitedBuffer{limit: p.cfg.MaxStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		typ := kernel.ErrUpstreamPermanent
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			typ = kernel.ErrDeadlineExceeded
		} else if errors.Is(callCtx.Err(), context.Canceled) {
			typ = kernel.ErrCancelled
		}
		return &kernel.ErrorDetail{Type: typ, ProviderID: p.cfg.Descriptor.ID, Source: p.cfg.Descriptor.Source, Message: fmt.Sprintf("external provider process failed: %v", err)}
	}
	if stdout.truncated {
		return &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, ProviderID: p.cfg.Descriptor.ID, Source: p.cfg.Descriptor.Source, Message: "external provider response exceeds max_stdout_bytes"}
	}
	var resp rpcResponse
	if err := decodeStrict(bytes.TrimSpace(stdout.Bytes()), &resp); err != nil {
		return &kernel.ErrorDetail{Type: kernel.ErrParse, ProviderID: p.cfg.Descriptor.ID, Source: p.cfg.Descriptor.Source, Message: "invalid JSON-RPC response: " + err.Error()}
	}
	if resp.JSONRPC != "2.0" || resp.ID != req.ID || (resp.Error == nil) == (len(resp.Result) == 0) {
		return &kernel.ErrorDetail{Type: kernel.ErrParse, ProviderID: p.cfg.Descriptor.ID, Source: p.cfg.Descriptor.Source, Message: "invalid JSON-RPC response envelope"}
	}
	if resp.Error != nil {
		return &kernel.ErrorDetail{Type: kernel.ErrUpstreamPermanent, ProviderID: p.cfg.Descriptor.ID, Source: p.cfg.Descriptor.Source, Code: resp.Error.Code, Message: resp.Error.Message, Details: map[string]any{"data": resp.Error.Data}}
	}
	if out == nil {
		return nil
	}
	if err := decodeStrict(resp.Result, out); err != nil {
		return &kernel.ErrorDetail{Type: kernel.ErrParse, ProviderID: p.cfg.Descriptor.ID, Source: p.cfg.Descriptor.Source, Message: "invalid provider result: " + err.Error()}
	}
	if err := p.validateResult(method, params, out); err != nil {
		return &kernel.ErrorDetail{Type: kernel.ErrParse, ProviderID: p.cfg.Descriptor.ID, Source: p.cfg.Descriptor.Source, Message: "invalid provider result: " + err.Error()}
	}
	return nil
}

var supportedMethods = map[string]bool{
	"health": true, "search": true, "query": true, "fetch": true, "expand": true, "resolve": true,
}

var supportedSources = map[kernel.SourceID]bool{
	kernel.SourceDocs: true, kernel.SourceMessages: true, kernel.SourceChats: true,
	kernel.SourcePeople: true, kernel.SourceMinutes: true, kernel.SourceMeetings: true,
	kernel.SourceCalendar: true, kernel.SourceTasks: true, kernel.SourceMail: true,
	kernel.SourceBase: true, kernel.SourceSheets: true,
}

var supportedKinds = map[kernel.ObjectKind]bool{
	kernel.KindUnknown: true, kernel.KindDocument: true, kernel.KindMessage: true,
	kernel.KindChat: true, kernel.KindPerson: true, kernel.KindMinute: true,
	kernel.KindMeeting: true, kernel.KindEvent: true, kernel.KindTask: true,
	kernel.KindTaskList: true, kernel.KindMail: true, kernel.KindBase: true,
	kernel.KindBaseTable: true, kernel.KindRecord: true, kernel.KindSheet: true,
	kernel.KindCell: true, kernel.KindAttachment: true,
}

const allProjections = kernel.ProjectionHead | kernel.ProjectionSnippet | kernel.ProjectionSummary |
	kernel.ProjectionStructure | kernel.ProjectionContent | kernel.ProjectionContext |
	kernel.ProjectionRelations | kernel.ProjectionAttachments

func validateDescriptor(d kernel.ProviderDescriptor) error {
	if strings.TrimSpace(string(d.ID)) == "" || !supportedSources[d.Source] {
		return fmt.Errorf("external provider descriptor requires a valid id and source")
	}
	if len(d.ObjectKinds) == 0 {
		return fmt.Errorf("external provider descriptor requires object_kinds")
	}
	for _, kind := range d.ObjectKinds {
		if !supportedKinds[kind] || kind == kernel.KindUnknown {
			return fmt.Errorf("external provider descriptor has invalid object kind %q", kind)
		}
	}
	if len(d.Operations) == 0 {
		return fmt.Errorf("external provider descriptor requires operations")
	}
	for operation, enabled := range d.Operations {
		if !enabled || operation == kernel.OpContinue || !supportedMethods[string(operation)] {
			return fmt.Errorf("external provider descriptor has invalid operation %q", operation)
		}
	}
	for _, identity := range d.RequiredIdentity {
		if identity != kernel.IdentityAuto && identity != kernel.IdentityUser && identity != kernel.IdentityBot {
			return fmt.Errorf("external provider descriptor has invalid identity %q", identity)
		}
	}
	if d.SearchLimits.MaxQueryRunes < 0 || d.SearchLimits.MaxPageSize < 0 || d.SearchLimits.MaxPages < 0 || d.BatchLimits.MaxFetchItems < 0 {
		return fmt.Errorf("external provider descriptor limits must not be negative")
	}
	if d.ReturnedProjection&^allProjections != 0 || d.FetchableProjection&^allProjections != 0 {
		return fmt.Errorf("external provider descriptor has an invalid projection")
	}
	return nil
}

func decodeStrict(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func (p *Provider) validateResult(method string, params, out any) error {
	switch method {
	case "health":
		result, ok := out.(*struct {
			Version string `json:"version"`
		})
		if !ok || strings.TrimSpace(result.Version) == "" {
			return fmt.Errorf("health result requires version")
		}
	case "search", "query":
		page, ok := out.(*kernel.CandidatePage)
		if !ok {
			return fmt.Errorf("candidate page has unexpected type")
		}
		if page.RawCount < len(page.Candidates) || page.RawCount < 0 {
			return fmt.Errorf("raw_count is inconsistent with candidates")
		}
		if page.HasMore != (strings.TrimSpace(page.NextCursor) != "") {
			return fmt.Errorf("has_more and next_cursor are inconsistent")
		}
		for i := range page.Candidates {
			if err := p.validateCandidate(page.Candidates[i]); err != nil {
				return fmt.Errorf("candidate %d: %w", i, err)
			}
		}
	case "fetch":
		artifacts, ok := out.(*[]kernel.Artifact)
		requests, reqOK := params.([]kernel.ProviderFetchRequest)
		if !ok || !reqOK || len(*artifacts) > len(requests) {
			return fmt.Errorf("fetch result shape is inconsistent with request")
		}
		requested := make(map[string]kernel.ProjectionSet, len(requests))
		for _, request := range requests {
			requested[refKey(request.Ref)] |= request.Projection
		}
		for i, artifact := range *artifacts {
			if err := p.validateRef(artifact.Ref, true); err != nil {
				return fmt.Errorf("artifact %d: %w", i, err)
			}
			projection, exists := requested[refKey(artifact.Ref)]
			if !exists {
				return fmt.Errorf("artifact %d was not requested", i)
			}
			allowed := p.cfg.Descriptor.ReturnedProjection | p.cfg.Descriptor.FetchableProjection
			if artifact.Projection == 0 || artifact.Projection&^allowed != 0 || !artifact.Projection.Has(projection) {
				return fmt.Errorf("artifact %d has invalid projection", i)
			}
		}
	case "expand":
		relations, ok := out.(*[]kernel.Relation)
		request, reqOK := params.(kernel.ProviderExpandRequest)
		if !ok || !reqOK {
			return fmt.Errorf("expand result shape is invalid")
		}
		for i, relation := range *relations {
			if err := relation.From.Validate(); err != nil {
				return fmt.Errorf("relation %d from: %w", i, err)
			}
			if refKey(relation.From) != refKey(request.Ref) {
				return fmt.Errorf("relation %d starts from an unexpected object", i)
			}
			if err := relation.To.Validate(); err != nil {
				return fmt.Errorf("relation %d to: %w", i, err)
			}
			if strings.TrimSpace(relation.Type) == "" {
				return fmt.Errorf("relation %d requires type", i)
			}
		}
	case "resolve":
		refs, ok := out.(*[]kernel.ObjectRef)
		if !ok {
			return fmt.Errorf("resolve result shape is invalid")
		}
		for i := range *refs {
			if err := p.validateRef((*refs)[i], true); err != nil {
				return fmt.Errorf("ref %d: %w", i, err)
			}
		}
	}
	return nil
}

func (p *Provider) validateCandidate(candidate kernel.Candidate) error {
	if err := p.validateRef(candidate.Ref, true); err != nil {
		return err
	}
	if candidate.Source != p.cfg.Descriptor.Source || candidate.Kind != candidate.Ref.Kind || candidate.NativeRank < 1 {
		return fmt.Errorf("source, kind, or native_rank is invalid")
	}
	if candidate.Projection == 0 || candidate.Projection&^allProjections != 0 || candidate.AvailableProjection&^allProjections != 0 {
		return fmt.Errorf("projection is invalid")
	}
	allowed := p.cfg.Descriptor.ReturnedProjection | p.cfg.Descriptor.FetchableProjection
	if candidate.Projection&^allowed != 0 || candidate.AvailableProjection&^p.cfg.Descriptor.FetchableProjection != 0 {
		return fmt.Errorf("projection exceeds provider descriptor")
	}
	return nil
}

func (p *Provider) validateRef(ref kernel.ObjectRef, owned bool) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if !supportedKinds[ref.Kind] || ref.Kind == kernel.KindUnknown {
		return fmt.Errorf("invalid object kind %q", ref.Kind)
	}
	if ref.Source != "" && !supportedSources[ref.Source] {
		return fmt.Errorf("invalid source %q", ref.Source)
	}
	if owned {
		if ref.ProviderID != p.cfg.Descriptor.ID || ref.Source != p.cfg.Descriptor.Source {
			return fmt.Errorf("provider_id or source does not match descriptor")
		}
		kindAllowed := false
		for _, kind := range p.cfg.Descriptor.ObjectKinds {
			kindAllowed = kindAllowed || kind == ref.Kind
		}
		if !kindAllowed {
			return fmt.Errorf("object kind %q is not declared", ref.Kind)
		}
	}
	return nil
}

func refKey(ref kernel.ObjectRef) string {
	if ref.CanonicalID != "" {
		return "canonical:" + ref.CanonicalID
	}
	return fmt.Sprintf("native:%s:%s:%s", ref.Kind, ref.Source, ref.NativeID)
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int64
	written   int64
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.written
	if remaining > 0 {
		write := int64(len(p))
		if write > remaining {
			write = remaining
		}
		_, _ = b.buf.Write(p[:write])
		b.written += write
	}
	if int64(n) > remaining {
		b.truncated = true
	}
	// Report the full length so os/exec does not treat the cap as a pipe error.
	return n, nil
}

func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }
func (b *limitedBuffer) String() string {
	s := b.buf.String()
	if b.truncated {
		s += "\n...[truncated]"
	}
	return s
}
