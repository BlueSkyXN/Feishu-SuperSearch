package openapi

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type PeopleProvider struct {
	client *Client
}

func NewPeopleProvider(client *Client) *PeopleProvider {
	return &PeopleProvider{client: client}
}

func (p *PeopleProvider) Descriptor() kernel.ProviderDescriptor {
	return kernel.ProviderDescriptor{
		ID: "openapi.people", Source: kernel.SourcePeople, ObjectKinds: []kernel.ObjectKind{kernel.KindPerson},
		Operations: kernel.OperationSet{kernel.OpSearch: true, kernel.OpResolve: true}, RequiredIdentity: []kernel.IdentityMode{kernel.IdentityUser},
		SearchLimits:       kernel.SearchLimits{MaxQueryRunes: 50, MaxPageSize: 30, MaxPages: 5},
		ReturnedProjection: kernel.ProjectionHead | kernel.ProjectionContent, SupportsPagination: true,
		Backend: "openapi", Version: "oapi-sdk-go/v3:directory-v1/employee/search",
	}
}

func (p *PeopleProvider) Health(context.Context, kernel.Identity) (string, error) {
	if p == nil || p.client == nil || !p.client.HasSDKCredentials() {
		return "", &kernel.ErrorDetail{Type: kernel.ErrIdentityRequired, ProviderID: "openapi.people", Source: kernel.SourcePeople, Message: "OpenAPI app_id or user token is not configured"}
	}
	return "oapi-sdk-go/v3:directory-v1/employee/search", nil
}

func (p *PeopleProvider) Search(ctx context.Context, request kernel.ProviderSearchRequest) (kernel.CandidatePage, error) {
	if len([]rune(request.Query)) > 50 {
		return kernel.CandidatePage{}, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, ProviderID: "openapi.people", Source: kernel.SourcePeople, Message: "people query exceeds 50 runes"}
	}
	pageSize := request.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 30 {
		pageSize = 30
	}
	data, err := p.client.searchPeopleSDK(ctx, request.Query, pageSize, request.Cursor)
	if err != nil {
		return kernel.CandidatePage{}, annotate(err, "openapi.people", kernel.SourcePeople)
	}
	users := findMapSlice(data, []string{"employees", "users", "items", "results"})
	abnormals := findMapSlice(data, []string{"abnormals"})
	pageResponse := findMap(data, []string{"page_response"})
	page := kernel.CandidatePage{RawCount: len(users), NextCursor: findString(pageResponse, []string{"page_token"}), HasMore: findBool(pageResponse, []string{"has_more"}), Metadata: map[string]any{"backend": "openapi"}}
	if len(abnormals) > 0 {
		page.Metadata["abnormal_count"] = len(abnormals)
	}
	scope := request.Identity.Normalized().ScopeKey
	for index, user := range users {
		base := findMap(user, []string{"base_info"})
		if base == nil {
			base = user
		}
		id := findString(base, []string{"employee_id", "open_id", "user_id", "id"})
		if id == "" {
			continue
		}
		name := employeeName(base)
		email := findString(base, []string{"enterprise_email", "email"})
		department := strings.Join(findStringValues(base, []string{"department_name", "department_names"}), ", ")
		snippet := strings.Trim(strings.Join([]string{email, department}, " · "), " ·")
		ref := kernel.ObjectRef{Platform: "feishu", ScopeKey: scope, Kind: kernel.KindPerson, NativeID: id, CanonicalID: kernel.BuildCanonicalID(scope, kernel.KindPerson, id), ProviderID: "openapi.people", Source: kernel.SourcePeople}
		projection := kernel.ProjectionHead
		if snippet != "" {
			projection |= kernel.ProjectionContent
		}
		page.Candidates = append(page.Candidates, kernel.Candidate{Ref: ref, Source: kernel.SourcePeople, Kind: kernel.KindPerson, Title: name, Snippet: snippet, NativeRank: index + 1, Projection: projection, DiscoveredBy: []kernel.Discovery{{ProviderID: "openapi.people", Source: kernel.SourcePeople, Rank: index + 1}}, Provenance: kernel.Provenance{ProviderID: "openapi.people", Backend: "openapi", Operation: "search", RetrievedAt: time.Now().UTC(), SourceRank: index + 1, RawRef: user}})
	}
	if len(page.Candidates) == 0 && len(abnormals) > 0 {
		return kernel.CandidatePage{}, &kernel.ErrorDetail{Type: kernel.ErrMissingScope, ProviderID: "openapi.people", Source: kernel.SourcePeople, Message: "Directory Search did not return readable employee identity fields", Details: map[string]any{"abnormal_count": len(abnormals)}}
	}
	if len(users) > 0 && len(page.Candidates) == 0 {
		return kernel.CandidatePage{}, &kernel.ErrorDetail{Type: kernel.ErrVersionIncompatible, ProviderID: "openapi.people", Source: kernel.SourcePeople, Message: "Directory Search employees are missing IDs"}
	}
	return page, nil
}

func employeeName(base map[string]any) string {
	if name := findString(base, []string{"localized_name", "display_name", "name"}); name != "" {
		return name
	}
	name := findMap(base, []string{"name"})
	for _, key := range []string{"display_name", "name"} {
		if text := localizedText(name[key]); text != "" {
			return text
		}
	}
	return "飞书用户"
}

func localizedText(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	if text := findString(object, []string{"default_value"}); text != "" {
		return text
	}
	translations, ok := object["i18n_value"].(map[string]any)
	if !ok {
		return ""
	}
	for _, locale := range []string{"zh_cn", "en_us", "ja_jp"} {
		if text := findString(translations, []string{locale}); text != "" {
			return text
		}
	}
	locales := make([]string, 0, len(translations))
	for locale := range translations {
		locales = append(locales, locale)
	}
	sort.Strings(locales)
	for _, locale := range locales {
		if text := findString(translations, []string{locale}); text != "" {
			return text
		}
	}
	return ""
}

func (p *PeopleProvider) Resolve(ctx context.Context, request kernel.ProviderResolveRequest) ([]kernel.ObjectRef, error) {
	page, err := p.Search(ctx, kernel.ProviderSearchRequest{Query: request.Text, Identity: request.Identity, PageSize: request.Limit})
	if err != nil {
		return nil, err
	}
	refs := make([]kernel.ObjectRef, 0, len(page.Candidates))
	for _, candidate := range page.Candidates {
		refs = append(refs, candidate.Ref)
	}
	return refs, nil
}

func findStringValues(root map[string]any, keys []string) []string {
	for _, key := range keys {
		switch value := root[key].(type) {
		case string:
			if strings.TrimSpace(value) != "" {
				return []string{strings.TrimSpace(value)}
			}
		case []any:
			out := []string{}
			for _, item := range value {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					out = append(out, strings.TrimSpace(text))
				}
			}
			return out
		}
	}
	return nil
}
