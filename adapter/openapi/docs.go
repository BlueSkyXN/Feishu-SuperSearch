package openapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type DocsProvider struct {
	client     *Client
	userOpenID string
}

func NewDocsProvider(client *Client, userOpenID string) *DocsProvider {
	return &DocsProvider{client: client, userOpenID: strings.TrimSpace(userOpenID)}
}
func (p *DocsProvider) Descriptor() kernel.ProviderDescriptor {
	return kernel.ProviderDescriptor{
		ID: "openapi.docs", Source: kernel.SourceDocs, ObjectKinds: []kernel.ObjectKind{kernel.KindDocument, kernel.KindSheet, kernel.KindBase, kernel.KindAttachment},
		Operations: kernel.OperationSet{kernel.OpSearch: true, kernel.OpFetch: true}, RequiredIdentity: []kernel.IdentityMode{kernel.IdentityUser},
		RequiredScopes: []string{"search:docs:read"}, SearchLimits: kernel.SearchLimits{MaxQueryRunes: 30, MaxPageSize: 20, MaxPages: 5},
		ReturnedProjection: kernel.ProjectionHead | kernel.ProjectionSnippet, FetchableProjection: kernel.ProjectionStructure | kernel.ProjectionContent, SupportsPagination: true,
		Backend: "openapi", Version: "oapi-sdk-go/v3:search-v2/doc_wiki+http:docs_ai/v1",
	}
}
func (p *DocsProvider) Health(context.Context, kernel.Identity) (string, error) {
	if p == nil || p.client == nil || !p.client.HasSDKCredentials() {
		return "", &kernel.ErrorDetail{Type: kernel.ErrIdentityRequired, ProviderID: "openapi.docs", Source: kernel.SourceDocs, Message: "OpenAPI app_id or user token is not configured"}
	}
	return "oapi-sdk-go/v3:search-v2/doc_wiki+http:docs_ai/v1", nil
}

func (p *DocsProvider) Fetch(ctx context.Context, requests []kernel.ProviderFetchRequest) ([]kernel.Artifact, error) {
	artifacts := make([]kernel.Artifact, 0, len(requests))
	for _, request := range requests {
		if err := validateFetchProjection("openapi.docs", kernel.SourceDocs, request.Projection, kernel.ProjectionStructure|kernel.ProjectionContent); err != nil {
			return artifacts, err
		}
		if !fetchableOpenAPIDocumentRef(request.Ref) {
			return artifacts, &kernel.ErrorDetail{Type: kernel.ErrUnsupported, ProviderID: "openapi.docs", Source: kernel.SourceDocs, Message: "the search hit is not a docx document; use Base or Sheets query for structured objects"}
		}
		if request.Ref.NativeID == "" {
			return nil, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, ProviderID: "openapi.docs", Source: kernel.SourceDocs, Message: "document native_id is required"}
		}
		documentID, err := safeOpenAPIPathSegment(request.Ref.NativeID, "document native_id")
		if err != nil {
			return artifacts, annotate(err, "openapi.docs", kernel.SourceDocs)
		}
		body := map[string]any{
			"format": "xml",
			"export_option": map[string]any{
				"export_block_id":        request.Projection.Has(kernel.ProjectionStructure),
				"export_cite_extra_data": true,
				"export_style_attrs":     false,
			},
			"extra_param": `{"enable_user_cite_reference_map":true,"return_html5_block_data":true}`,
		}
		data, err := p.client.Call(ctx, http.MethodPost, "/open-apis/docs_ai/v1/documents/"+documentID+"/fetch", body)
		if err != nil {
			return nil, annotate(err, "openapi.docs", kernel.SourceDocs)
		}
		document := findMap(data, []string{"document", "doc"})
		if document == nil {
			document = data
		}
		content := findString(document, []string{"content", "xml", "markdown"})
		projection := request.Projection & (kernel.ProjectionStructure | kernel.ProjectionContent)
		artifact := kernel.Artifact{Ref: request.Ref, Metadata: map[string]any{}, Version: findString(document, []string{"revision_id", "version"}), Provenance: kernel.Provenance{ProviderID: "openapi.docs", Backend: "openapi", Operation: "fetch", RetrievedAt: time.Now().UTC()}}
		if content != "" {
			artifact.Chunks = append(artifact.Chunks, kernel.ContentChunk{ID: "document", Kind: "content", Text: content, Start: 0, End: len([]rune(content))})
			artifact.Projection |= projection
		}
		for _, key := range []string{"document_id", "revision_id", "reference_map", "tips"} {
			if value, ok := document[key]; ok {
				artifact.Metadata[key] = value
			}
		}
		if content == "" && !projection.Empty() {
			return nil, &kernel.ErrorDetail{Type: kernel.ErrParse, ProviderID: "openapi.docs", Source: kernel.SourceDocs, Message: fmt.Sprintf("document %s response contains no content", request.Ref.NativeID)}
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}
func (p *DocsProvider) Search(ctx context.Context, req kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	if len([]rune(req.Query)) > 30 {
		return kernel.CandidatePage{}, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, ProviderID: "openapi.docs", Source: kernel.SourceDocs, Message: "query exceeds Search v2 limit of 30 runes"}
	}
	body, err := p.buildRequest(req)
	if err != nil {
		return kernel.CandidatePage{}, err
	}
	data, err := p.client.searchDocsSDK(ctx, body)
	if err != nil {
		return kernel.CandidatePage{}, annotate(err, "openapi.docs", kernel.SourceDocs)
	}
	return p.parsePage(data, req.Identity)
}

func (p *DocsProvider) buildRequest(req kernel.ProviderSearchRequest) (map[string]any, error) {
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = 15
	}
	if pageSize > 20 {
		pageSize = 20
	}
	body := map[string]any{"query": req.Query, "page_size": pageSize}
	if req.Cursor != "" {
		body["page_token"] = req.Cursor
	}
	f := req.Filters
	if len(f.FolderTokens) > 0 && len(f.SpaceIDs) > 0 {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, ProviderID: "openapi.docs", Source: kernel.SourceDocs, Message: "folder_tokens and space_ids are mutually exclusive"}
	}
	common := map[string]any{}
	if f.Mine {
		if p.userOpenID == "" {
			return nil, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, ProviderID: "openapi.docs", Source: kernel.SourceDocs, Message: "mine filter requires open_api.user_open_id"}
		}
		common["creator_ids"] = []string{p.userOpenID}
	}
	if len(f.DocTypes) > 0 {
		types := make([]string, 0, len(f.DocTypes))
		for _, v := range f.DocTypes {
			if v = strings.TrimSpace(v); v != "" {
				types = append(types, strings.ToUpper(v))
			}
		}
		common["doc_types"] = types
	}
	if len(f.ChatIDs) > 0 {
		common["chat_ids"] = f.ChatIDs
	}
	if f.OnlyTitle {
		common["only_title"] = true
	}
	if f.After != nil || f.Before != nil {
		rng := map[string]any{}
		if f.After != nil {
			rng["start"] = f.After.Unix()
		}
		if f.Before != nil {
			rng["end"] = f.Before.Unix()
		}
		common["create_time"] = rng
	}
	copyFilter := func() map[string]any {
		out := map[string]any{}
		for k, v := range common {
			out[k] = v
		}
		return out
	}
	switch {
	case len(f.FolderTokens) > 0:
		doc := copyFilter()
		doc["folder_tokens"] = f.FolderTokens
		body["doc_filter"] = doc
	case len(f.SpaceIDs) > 0:
		wiki := copyFilter()
		wiki["space_ids"] = f.SpaceIDs
		body["wiki_filter"] = wiki
	default:
		body["doc_filter"] = copyFilter()
		body["wiki_filter"] = copyFilter()
	}
	return body, nil
}

func (p *DocsProvider) parsePage(data map[string]any, identity kernel.Identity) (kernel.CandidatePage, error) {
	items := findMapSlice(data, []string{"res_units", "results", "items"})
	page := kernel.CandidatePage{RawCount: len(items), NextCursor: findString(data, []string{"page_token", "next_page_token"}), HasMore: findBool(data, []string{"has_more"}), Metadata: map[string]any{"backend": "openapi"}}
	if page.NextCursor != "" {
		page.HasMore = true
	}
	for i, item := range items {
		id := findString(item, []string{"token", "doc_token", "obj_token", "document_id", "wiki_token", "id"})
		if id == "" {
			return kernel.CandidatePage{}, &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, ProviderID: "openapi.docs", Source: kernel.SourceDocs, Message: "document search result is missing its token"}
		}
		title := cleanText(findString(item, []string{"title_highlighted", "title", "name"}))
		snippet := cleanText(findString(item, []string{"content_highlighted", "content", "snippet", "summary"}))
		url := findString(item, []string{"url", "link"})
		kind, fetchable := openAPIDocumentSearchKind(item)
		scope := identity.Normalized().ScopeKey
		ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: scope, Kind: kind, NativeID: id, ProviderID: "openapi.docs", Source: kernel.SourceDocs, URL: url}
		ref.CanonicalID = kernel.BuildCanonicalID(scope, kind, id)
		rank := i + 1
		projection := kernel.ProjectionHead
		if snippet != "" {
			projection |= kernel.ProjectionSnippet
		}
		available := kernel.ProjectionSet(0)
		if fetchable {
			available = kernel.ProjectionStructure | kernel.ProjectionContent
		}
		page.Candidates = append(page.Candidates, kernel.Candidate{Ref: ref, Source: kernel.SourceDocs, Kind: kind, Title: title, Snippet: snippet, URL: url, Timestamp: findTime(item, []string{"edit_time", "update_time", "modified_time", "create_time"}), NativeRank: rank, Projection: projection, AvailableProjection: available, DiscoveredBy: []kernel.Discovery{{ProviderID: "openapi.docs", Source: kernel.SourceDocs, Rank: rank}}, Provenance: kernel.Provenance{ProviderID: "openapi.docs", Backend: "openapi", Operation: "search", RetrievedAt: time.Now().UTC(), SourceRank: rank, RawRef: item}})
	}
	return page, nil
}

func openAPIDocumentSearchKind(item map[string]any) (kernel.ObjectKind, bool) {
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

func fetchableOpenAPIDocumentRef(ref kernel.ObjectRef) bool {
	if ref.Kind != "" && ref.Kind != kernel.KindDocument {
		return false
	}
	url := strings.ToLower(ref.URL)
	return !strings.Contains(url, "/sheets/") && !strings.Contains(url, "/base/") && !strings.Contains(url, "/file/")
}

func annotate(err error, provider kernel.ProviderID, source kernel.SourceID) error {
	d := kernel.DetailFromError(err)
	d.ProviderID = provider
	d.Source = source
	return d
}
func findMapSlice(root map[string]any, keys []string) []map[string]any {
	for _, k := range keys {
		if a := toMaps(root[k]); len(a) > 0 {
			return a
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
				if a := toMaps(x[k]); len(a) > 0 {
					return a
				}
			}
			names := make([]string, 0, len(x))
			for k := range x {
				names = append(names, k)
			}
			sort.Strings(names)
			for _, k := range names {
				if a := walk(x[k], depth+1); len(a) > 0 {
					return a
				}
			}
		case []any:
			if a := toMaps(x); len(a) > 0 {
				return a
			}
			for _, v := range x {
				if a := walk(v, depth+1); len(a) > 0 {
					return a
				}
			}
		}
		return nil
	}
	return walk(root, 0)
}

func findMap(root map[string]any, keys []string) map[string]any {
	for _, key := range keys {
		if value, ok := root[key].(map[string]any); ok {
			return value
		}
	}
	for _, value := range root {
		if child, ok := value.(map[string]any); ok {
			if found := findMap(child, keys); found != nil {
				return found
			}
		}
	}
	return nil
}
func toMaps(v any) []map[string]any {
	a, ok := v.([]any)
	if !ok {
		return nil
	}
	out := []map[string]any{}
	for _, x := range a {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
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
				if s := findString(x, []string{"name", "text", "content", "value", "url", "token"}); s != "" {
					return s
				}
			}
		}
	}
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if child, ok := m[k].(map[string]any); ok {
			if s := findString(child, keys); s != "" {
				return s
			}
		}
	}
	return ""
}
func findBool(m map[string]any, keys []string) bool {
	for _, k := range keys {
		if b, ok := m[k].(bool); ok {
			return b
		}
	}
	return false
}

var highlightRE = regexp.MustCompile(`</?hb?>`)

func cleanText(s string) string {
	s = highlightRE.ReplaceAllString(strings.TrimSpace(s), "")
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
	if n, e := strconv.ParseInt(raw, 10, 64); e == nil {
		if n > 1e12 {
			n /= 1000
		}
		t := time.Unix(n, 0).UTC()
		return &t
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, e := time.Parse(layout, raw); e == nil {
			return &t
		}
	}
	return nil
}

var _ kernel.Provider = (*DocsProvider)(nil)
var _ kernel.Searcher = (*DocsProvider)(nil)
