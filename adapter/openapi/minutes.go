package openapi

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type MinutesProvider struct {
	client *Client
}

func NewMinutesProvider(client *Client) *MinutesProvider {
	return &MinutesProvider{client: client}
}

func (p *MinutesProvider) Descriptor() kernel.ProviderDescriptor {
	return kernel.ProviderDescriptor{
		ID: "openapi.minutes", Source: kernel.SourceMinutes, ObjectKinds: []kernel.ObjectKind{kernel.KindMinute},
		Operations: kernel.OperationSet{kernel.OpSearch: true, kernel.OpFetch: true}, RequiredIdentity: []kernel.IdentityMode{kernel.IdentityUser},
		RequiredScopes: []string{"minutes:minutes.search:read"}, SearchLimits: kernel.SearchLimits{MaxQueryRunes: 200, MaxPageSize: 30, MaxPages: 5}, BatchLimits: kernel.BatchLimits{MaxFetchItems: 1},
		ReturnedProjection:  kernel.ProjectionHead | kernel.ProjectionSnippet,
		FetchableProjection: kernel.ProjectionSummary | kernel.ProjectionStructure | kernel.ProjectionContent,
		SupportsPagination:  true, Backend: "openapi", Version: "oapi-sdk-go/v3:minutes/v1/search+get+artifacts",
	}
}

func (p *MinutesProvider) Health(context.Context, kernel.Identity) (string, error) {
	if p == nil || p.client == nil || !p.client.HasSDKCredentials() {
		return "", &kernel.ErrorDetail{Type: kernel.ErrIdentityRequired, ProviderID: "openapi.minutes", Source: kernel.SourceMinutes, Message: "OpenAPI app_id or user token is not configured"}
	}
	return "oapi-sdk-go/v3:minutes/v1/search+get+artifacts", nil
}

func (p *MinutesProvider) Search(ctx context.Context, request kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	pageSize := request.PageSize
	if pageSize <= 0 {
		pageSize = 15
	}
	if pageSize > 30 {
		pageSize = 30
	}
	body := map[string]any{"query": request.Query}
	filter := map[string]any{}
	if len(request.Filters.PersonIDs) > 0 {
		filter["participant_ids"] = request.Filters.PersonIDs
	}
	if owners := extraStringSlice(request.Filters.Extra, "owner_ids"); len(owners) > 0 {
		filter["owner_ids"] = owners
	}
	if request.Filters.After != nil || request.Filters.Before != nil {
		created := map[string]any{}
		if request.Filters.After != nil {
			created["start_time"] = request.Filters.After.Format(time.RFC3339)
		}
		if request.Filters.Before != nil {
			created["end_time"] = request.Filters.Before.Format(time.RFC3339)
		}
		filter["create_time"] = created
	}
	if len(filter) > 0 {
		body["filter"] = filter
	}
	data, err := p.client.searchMinutesSDK(ctx, body, pageSize, request.Cursor)
	if err != nil {
		return kernel.CandidatePage{}, annotate(err, "openapi.minutes", kernel.SourceMinutes)
	}
	items := findMapSlice(data, []string{"minutes", "items", "results"})
	page := kernel.CandidatePage{RawCount: len(items), NextCursor: findString(data, []string{"page_token", "next_page_token"}), HasMore: findBool(data, []string{"has_more"}), Metadata: map[string]any{"backend": "openapi"}}
	if page.NextCursor != "" {
		page.HasMore = true
	}
	scope := request.Identity.Normalized().ScopeKey
	for index, item := range items {
		token := findString(item, []string{"minute_token", "token", "id"})
		if token == "" {
			continue
		}
		metadata := findMap(item, []string{"meta_data", "metadata"})
		title := findString(item, []string{"title", "topic", "name", "display_info"})
		snippet := findString(item, []string{"summary", "abstract", "description", "display_info"})
		if description := findString(metadata, []string{"description"}); description != "" {
			snippet = description
		}
		shareURL := findString(item, []string{"url", "share_url"})
		if shareURL == "" {
			shareURL = findString(metadata, []string{"app_link", "url"})
		}
		ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: scope, Kind: kernel.KindMinute, NativeID: token, CanonicalID: kernel.BuildCanonicalID(scope, kernel.KindMinute, token), ProviderID: "openapi.minutes", Source: kernel.SourceMinutes, URL: shareURL}
		projection := kernel.ProjectionHead
		if snippet != "" {
			projection |= kernel.ProjectionSnippet
		}
		page.Candidates = append(page.Candidates, kernel.Candidate{Ref: ref, Source: kernel.SourceMinutes, Kind: kernel.KindMinute, Title: title, Snippet: cleanText(snippet), URL: shareURL, Timestamp: findTime(item, []string{"create_time", "start_time"}), NativeRank: index + 1, Projection: projection, AvailableProjection: kernel.ProjectionSummary | kernel.ProjectionStructure | kernel.ProjectionContent, DiscoveredBy: []kernel.Discovery{{ProviderID: "openapi.minutes", Source: kernel.SourceMinutes, Rank: index + 1}}, Provenance: kernel.Provenance{ProviderID: "openapi.minutes", Backend: "openapi", Operation: "search", RetrievedAt: time.Now().UTC(), SourceRank: index + 1, RawRef: item}})
	}
	if len(items) > 0 && len(page.Candidates) == 0 {
		return kernel.CandidatePage{}, &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, ProviderID: "openapi.minutes", Source: kernel.SourceMinutes, Message: "minute search results are missing tokens"}
	}
	return page, nil
}

func (p *MinutesProvider) Fetch(ctx context.Context, requests []kernel.ProviderFetchRequest) ([]kernel.Artifact, error) {
	artifacts := make([]kernel.Artifact, 0, len(requests))
	for _, request := range requests {
		if err := validateFetchProjection("openapi.minutes", kernel.SourceMinutes, request.Projection, kernel.ProjectionSummary|kernel.ProjectionStructure|kernel.ProjectionContent); err != nil {
			return artifacts, err
		}
		if request.Ref.NativeID == "" {
			return nil, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, ProviderID: "openapi.minutes", Source: kernel.SourceMinutes, Message: "minute native_id is required"}
		}
		token := request.Ref.NativeID
		detail, err := p.client.getMinuteSDK(ctx, token)
		if err != nil {
			return nil, annotate(err, "openapi.minutes", kernel.SourceMinutes)
		}
		artifactData, err := p.client.minuteArtifactsSDK(ctx, token)
		if err != nil {
			return nil, annotate(err, "openapi.minutes", kernel.SourceMinutes)
		}
		if len(artifactData) == 0 {
			return nil, &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, ProviderID: "openapi.minutes", Source: kernel.SourceMinutes, Message: "minute artifacts response contains no data"}
		}
		projection := request.Projection & (kernel.ProjectionSummary | kernel.ProjectionStructure | kernel.ProjectionContent)
		artifact := kernel.Artifact{Ref: request.Ref, Metadata: map[string]any{"detail": detail, "artifacts": artifactData}, Provenance: kernel.Provenance{ProviderID: "openapi.minutes", Backend: "openapi", Operation: "fetch", RetrievedAt: time.Now().UTC()}}
		summaryText := findString(artifactData, []string{"summary", "summary_text", "abstract"})
		if summaryText == "" {
			summaryText = findString(detail, []string{"summary", "summary_text", "abstract"})
		}
		todos := findMapSlice(artifactData, []string{"minute_todos", "todos", "todo_list", "action_items"})
		chapters := findMapSlice(artifactData, []string{"minute_chapters", "chapters", "chapter_list"})
		keywords := findStringValues(artifactData, []string{"keywords", "keyword_list"})
		if summaryText != "" || len(todos) > 0 || len(chapters) > 0 || len(keywords) > 0 {
			artifact.Summary = &kernel.NativeSummary{Text: cleanText(summaryText), Todos: todos, Chapters: chapters, Keywords: keywords}
		}
		if summaryText != "" || len(todos) > 0 {
			artifact.Projection |= kernel.ProjectionSummary
		}
		if len(chapters) > 0 || len(keywords) > 0 {
			artifact.Projection |= kernel.ProjectionStructure
		}
		transcript := findString(artifactData, []string{"transcript", "content", "text"})
		if transcript == "" {
			if value, ok := artifactData["transcript"]; ok {
				transcript = collectText(value)
			}
		}
		if transcript != "" {
			artifact.Chunks = append(artifact.Chunks, kernel.ContentChunk{ID: "transcript", Kind: "transcript", Text: transcript, Start: 0, End: len([]rune(transcript))})
			artifact.Projection |= kernel.ProjectionContent
		}
		for index, chapter := range chapters {
			text := collectText(chapter)
			if strings.TrimSpace(text) != "" {
				artifact.Chunks = append(artifact.Chunks, kernel.ContentChunk{ID: "chapter_" + strconv.Itoa(index+1), Kind: "chapter", Text: text})
			}
		}
		if !artifact.Projection.Has(projection) {
			return nil, &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, ProviderID: "openapi.minutes", Source: kernel.SourceMinutes, Message: "minute response did not materialize the requested projection", Details: map[string]any{"missing_projection": projection.Missing(artifact.Projection).Strings()}}
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func extraStringSlice(extra map[string]any, key string) []string {
	if extra == nil {
		return nil
	}
	switch value := extra[key].(type) {
	case []string:
		return value
	case []any:
		out := []string{}
		for _, item := range value {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	case string:
		return strings.FieldsFunc(value, func(r rune) bool { return r == ',' })
	default:
		return nil
	}
}
