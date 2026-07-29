package openapi

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type MessagesProvider struct {
	client *Client
}

func NewMessagesProvider(client *Client) *MessagesProvider {
	return &MessagesProvider{client: client}
}

func (p *MessagesProvider) Descriptor() kernel.ProviderDescriptor {
	return kernel.ProviderDescriptor{
		ID: "openapi.messages", Source: kernel.SourceMessages, ObjectKinds: []kernel.ObjectKind{kernel.KindMessage},
		Operations:       kernel.OperationSet{kernel.OpSearch: true, kernel.OpFetch: true, kernel.OpExpand: true},
		RequiredIdentity: []kernel.IdentityMode{kernel.IdentityUser}, RequiredScopes: []string{"search:message", "im:message:readonly"},
		SearchLimits: kernel.SearchLimits{MaxQueryRunes: 200, MaxPageSize: 50, MaxPages: 5}, BatchLimits: kernel.BatchLimits{MaxFetchItems: 50},
		ReturnedProjection:  kernel.ProjectionHead | kernel.ProjectionSnippet | kernel.ProjectionContent | kernel.ProjectionContext,
		FetchableProjection: kernel.ProjectionContent | kernel.ProjectionContext | kernel.ProjectionRelations,
		SupportsPagination:  true, Backend: "openapi", Version: "oapi-sdk-go/v3:search-v2/message+im-v1/message/get",
	}
}

func (p *MessagesProvider) Health(context.Context, kernel.Identity) (string, error) {
	if p == nil || p.client == nil || !p.client.HasSDKCredentials() {
		return "", &kernel.ErrorDetail{Type: kernel.ErrIdentityRequired, ProviderID: "openapi.messages", Source: kernel.SourceMessages, Message: "OpenAPI app_id or user token is not configured"}
	}
	return "oapi-sdk-go/v3:search-v2/message+im-v1/message/get", nil
}

func (p *MessagesProvider) Search(ctx context.Context, request kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	pageSize := request.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 50 {
		pageSize = 50
	}
	body := map[string]any{"query": request.Query}
	if len(request.Filters.ChatIDs) > 0 {
		body["chat_ids"] = request.Filters.ChatIDs
	}
	if len(request.Filters.SenderIDs) > 0 {
		body["from_ids"] = request.Filters.SenderIDs
	}
	if request.Filters.After != nil {
		body["start_time"] = strconv.FormatInt(request.Filters.After.Unix(), 10)
	}
	if request.Filters.Before != nil {
		body["end_time"] = strconv.FormatInt(request.Filters.Before.Unix(), 10)
	}
	data, err := p.client.searchMessagesSDK(ctx, body, pageSize, request.Cursor)
	if err != nil {
		return kernel.CandidatePage{}, annotate(err, "openapi.messages", kernel.SourceMessages)
	}
	ids := messageIDs(data)
	details := map[string]map[string]any{}
	if len(ids) > 0 {
		if messages, fetchErr := p.mget(ctx, ids); fetchErr == nil {
			for _, message := range messages {
				if id := findString(message, []string{"message_id", "id"}); id != "" {
					details[id] = message
				}
			}
		} else {
			data["enrichment_error"] = kernel.DetailFromError(fetchErr)
		}
	}
	page := kernel.CandidatePage{RawCount: len(ids), NextCursor: findString(data, []string{"page_token", "next_page_token"}), HasMore: findBool(data, []string{"has_more"}), Metadata: map[string]any{"backend": "openapi"}}
	if page.NextCursor != "" {
		page.HasMore = true
	}
	scope := request.Identity.Normalized().ScopeKey
	for index, id := range ids {
		detail := details[id]
		candidate := messageCandidate(scope, id, detail, index+1)
		page.Candidates = append(page.Candidates, candidate)
	}
	return page, nil
}

func (p *MessagesProvider) Fetch(ctx context.Context, requests []kernel.ProviderFetchRequest) ([]kernel.Artifact, error) {
	ids := make([]string, 0, len(requests))
	requestByID := map[string]kernel.ProviderFetchRequest{}
	for _, request := range requests {
		if err := validateFetchProjection("openapi.messages", kernel.SourceMessages, request.Projection, kernel.ProjectionContent|kernel.ProjectionContext|kernel.ProjectionRelations); err != nil {
			return nil, err
		}
		if request.Ref.NativeID == "" {
			return nil, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, ProviderID: "openapi.messages", Source: kernel.SourceMessages, Message: "message native_id is required"}
		}
		ids = append(ids, request.Ref.NativeID)
		requestByID[request.Ref.NativeID] = request
	}
	messages, err := p.mget(ctx, ids)
	if err != nil {
		return nil, err
	}
	artifacts := make([]kernel.Artifact, 0, len(messages))
	for _, message := range messages {
		id := findString(message, []string{"message_id", "id"})
		request, ok := requestByID[id]
		if !ok {
			continue
		}
		content := messageText(message)
		projection := request.Projection & (kernel.ProjectionContent | kernel.ProjectionContext | kernel.ProjectionRelations)
		artifact := kernel.Artifact{Ref: request.Ref, Metadata: map[string]any{"message": message}, Provenance: kernel.Provenance{ProviderID: "openapi.messages", Backend: "openapi", Operation: "fetch", RetrievedAt: time.Now().UTC()}}
		if content != "" {
			artifact.Chunks = append(artifact.Chunks, kernel.ContentChunk{ID: id, Kind: "message", Text: content, Start: 0, End: len([]rune(content)), Timestamp: findTime(message, []string{"create_time", "update_time"})})
			artifact.Projection |= projection & kernel.ProjectionContent
		}
		if chatID := findString(message, []string{"chat_id"}); chatID != "" && projection.Any(kernel.ProjectionContext|kernel.ProjectionRelations) {
			chatRef := kernel.ObjectRef{Platform: "feishu", ScopeKey: request.Ref.ScopeKey, Kind: kernel.KindChat, NativeID: chatID, CanonicalID: kernel.BuildCanonicalID(request.Ref.ScopeKey, kernel.KindChat, chatID), ProviderID: "openapi.messages", Source: kernel.SourceChats}
			artifact.Relations = append(artifact.Relations, kernel.Relation{From: request.Ref, Type: "message.in_chat", To: chatRef, Confidence: 1, Provenance: artifact.Provenance})
			if projection.Has(kernel.ProjectionContext) {
				artifact.Metadata["context"] = messageContext(message)
				artifact.Projection |= kernel.ProjectionContext
			}
		}
		if projection.Any(kernel.ProjectionRelations) {
			for _, target := range messageDocumentRefs(content, request.Ref.ScopeKey) {
				artifact.Relations = append(artifact.Relations, kernel.Relation{From: request.Ref, Type: "message.links_to_document", To: target, Confidence: .95, Provenance: artifact.Provenance})
			}
			artifact.Projection |= kernel.ProjectionRelations
		}
		if !artifact.Projection.Has(projection) {
			return nil, &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, ProviderID: "openapi.messages", Source: kernel.SourceMessages, Message: "message response did not materialize the requested projection", Details: map[string]any{"missing_projection": projection.Missing(artifact.Projection).Strings()}}
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

var feishuDocumentURL = regexp.MustCompile(`https?://(?:[A-Za-z0-9-]+\.)*(?:feishu\.cn|larksuite\.com)/(?:docx|docs|wiki|sheets)/([A-Za-z0-9_-]+)[^\s"'<>]*`)

func messageDocumentRefs(content, scope string) []kernel.ObjectRef {
	seen := map[string]bool{}
	refs := []kernel.ObjectRef{}
	for _, match := range feishuDocumentURL.FindAllStringSubmatch(content, -1) {
		if len(match) < 2 || seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		refs = append(refs, kernel.ObjectRef{Platform: "feishu", ScopeKey: scope, Kind: kernel.KindDocument, NativeID: match[1], CanonicalID: kernel.BuildCanonicalID(scope, kernel.KindDocument, match[1]), Source: kernel.SourceDocs, URL: match[0]})
	}
	return refs
}

func messageContext(message map[string]any) map[string]any {
	context := map[string]any{}
	for _, key := range []string{"chat_id", "chat_name", "chat_type", "thread_id", "root_id", "parent_id"} {
		if value, ok := message[key]; ok {
			context[key] = value
		}
	}
	return context
}

func (p *MessagesProvider) Expand(ctx context.Context, request kernel.ProviderExpandRequest) ([]kernel.Relation, error) {
	artifacts, err := p.Fetch(ctx, []kernel.ProviderFetchRequest{{Ref: request.Ref, Projection: kernel.ProjectionRelations, Identity: request.Identity, SessionID: request.SessionID}})
	if err != nil || len(artifacts) == 0 {
		return nil, err
	}
	return artifacts[0].Relations, nil
}

func (p *MessagesProvider) mget(ctx context.Context, ids []string) ([]map[string]any, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	results := make([]map[string]any, len(ids))
	jobs := make(chan int)
	workers := 6
	if workers > len(ids) {
		workers = len(ids)
	}
	var wait sync.WaitGroup
	var errorMu sync.Mutex
	var firstError error
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				data, err := p.client.getMessageSDK(ctx, ids[index])
				if err != nil {
					errorMu.Lock()
					if firstError == nil {
						firstError = annotate(err, "openapi.messages", kernel.SourceMessages)
					}
					errorMu.Unlock()
					continue
				}
				items := findMapSlice(data, []string{"items", "messages"})
				if len(items) > 0 {
					results[index] = items[0]
				}
			}
		}()
	}
	for index := range ids {
		select {
		case jobs <- index:
		case <-ctx.Done():
			close(jobs)
			wait.Wait()
			return nil, annotate(sdkCallError(ctx, ctx.Err()), "openapi.messages", kernel.SourceMessages)
		}
	}
	close(jobs)
	wait.Wait()
	if firstError != nil {
		return nil, firstError
	}
	out := make([]map[string]any, 0, len(results))
	for _, message := range results {
		if message != nil {
			out = append(out, message)
		}
	}
	return out, nil
}

func messageIDs(data map[string]any) []string {
	ids := []string{}
	seen := map[string]bool{}
	for _, item := range findMapSlice(data, []string{"items", "messages", "results"}) {
		if id := findString(item, []string{"message_id", "id"}); id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, key := range []string{"message_ids", "items"} {
		if values, ok := data[key].([]any); ok {
			for _, value := range values {
				if id, ok := value.(string); ok && id != "" && !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
	}
	return ids
}

func messageCandidate(scope, id string, message map[string]any, rank int) kernel.Candidate {
	ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: scope, Kind: kernel.KindMessage, NativeID: id, CanonicalID: kernel.BuildCanonicalID(scope, kernel.KindMessage, id), ProviderID: "openapi.messages", Source: kernel.SourceMessages}
	text := messageText(message)
	sender := findMap(message, []string{"sender"})
	senderName := findString(sender, []string{"name", "sender_name", "id"})
	chatID := findString(message, []string{"chat_id"})
	title := senderName
	if title == "" {
		title = "飞书消息"
	}
	projection := kernel.ProjectionHead
	if text != "" {
		projection |= kernel.ProjectionSnippet | kernel.ProjectionContent
	}
	if chatID != "" {
		projection |= kernel.ProjectionContext
	}
	candidate := kernel.Candidate{Ref: ref, Source: kernel.SourceMessages, Kind: kernel.KindMessage, Title: title, Snippet: cleanText(text), Timestamp: findTime(message, []string{"create_time", "update_time"}), NativeRank: rank, Projection: projection, AvailableProjection: kernel.ProjectionContent | kernel.ProjectionContext | kernel.ProjectionRelations, DiscoveredBy: []kernel.Discovery{{ProviderID: "openapi.messages", Source: kernel.SourceMessages, Rank: rank}}, Provenance: kernel.Provenance{ProviderID: "openapi.messages", Backend: "openapi", Operation: "search", RetrievedAt: time.Now().UTC(), SourceRank: rank, RawRef: message}}
	if chatID != "" {
		candidate.Container = &kernel.Container{Kind: kernel.KindChat, ID: chatID, Title: findString(message, []string{"chat_name"})}
	}
	if senderName != "" {
		candidate.Actors = []kernel.Actor{{ID: findString(sender, []string{"id", "sender_id", "open_id"}), Name: senderName}}
	}
	return candidate
}

func messageText(message map[string]any) string {
	value := message["content"]
	if value == nil {
		if body := findMap(message, []string{"body"}); body != nil {
			value = body["content"]
		}
	}
	if text, ok := value.(string); ok {
		var object any
		if json.Unmarshal([]byte(text), &object) == nil {
			return collectText(object)
		}
		return text
	}
	return collectText(value)
}

func collectText(value any) string {
	parts := []string{}
	var walk func(any)
	walk = func(current any) {
		switch item := current.(type) {
		case string:
			if strings.TrimSpace(item) != "" {
				parts = append(parts, strings.TrimSpace(item))
			}
		case []any:
			for _, child := range item {
				walk(child)
			}
		case map[string]any:
			for _, key := range []string{"text", "title", "content"} {
				if child, ok := item[key]; ok {
					walk(child)
				}
			}
		}
	}
	walk(value)
	return strings.Join(parts, "\n")
}
