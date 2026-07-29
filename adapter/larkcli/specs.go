package larkcli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type SourceSpec struct {
	Descriptor      kernel.ProviderDescriptor
	ProbeArgs       []string
	ProbeFlags      []string
	OperationProbes map[kernel.Operation]ProbeSpec
	SearchArgs      func(kernel.ProviderSearchRequest) ([]string, error)
	QueryArgs       func(kernel.ProviderQueryRequest) ([]string, error)
	FetchArgs       func(kernel.ProviderFetchRequest) ([]string, error)
	ExpandArgs      func(kernel.ProviderExpandRequest) ([]string, error)
	ItemKeys        []string
	IDKeys          []string
	TitleKeys       []string
	SnippetKeys     []string
	URLKeys         []string
	TimeKeys        []string
	Projection      kernel.ProjectionSet
	Available       kernel.ProjectionSet
}

type ProbeSpec struct {
	Args  []string
	Flags []string
}

func BuiltinSpecs() []SourceSpec {
	user := []kernel.IdentityMode{kernel.IdentityUser}
	userBot := []kernel.IdentityMode{kernel.IdentityUser, kernel.IdentityBot}
	specs := []SourceSpec{
		{
			Descriptor: desc("larkcli.docs", kernel.SourceDocs, []kernel.ObjectKind{kernel.KindDocument, kernel.KindSheet, kernel.KindBase, kernel.KindAttachment}, userBot, []string{"search:docs:read"}, 20, true, kernel.ProjectionHead|kernel.ProjectionSnippet, kernel.ProjectionStructure|kernel.ProjectionContent|kernel.ProjectionRelations),
			ProbeArgs:  []string{"drive", "+search", "--help"}, ProbeFlags: []string{"--query", "--page-size", "--page-token", "--only-title"},
			SearchArgs: docsSearchArgs, FetchArgs: docsFetchArgs, ItemKeys: []string{"results", "res_units", "items", "docs"}, IDKeys: []string{"token", "doc_token", "obj_token", "document_id", "wiki_token", "id"}, TitleKeys: []string{"title", "name"}, SnippetKeys: []string{"content", "snippet", "summary"}, URLKeys: []string{"url", "link"}, TimeKeys: []string{"edit_time", "update_time", "modified_time", "create_time"},
		},
		{
			Descriptor: desc("larkcli.messages", kernel.SourceMessages, []kernel.ObjectKind{kernel.KindMessage}, user, []string{"search:message", "im:message:readonly"}, 50, true, kernel.ProjectionHead|kernel.ProjectionSnippet|kernel.ProjectionContent|kernel.ProjectionContext, kernel.ProjectionContent|kernel.ProjectionContext|kernel.ProjectionRelations),
			ProbeArgs:  []string{"im", "+messages-search", "--help"}, ProbeFlags: []string{"--query", "--page-size", "--page-token", "--sender"},
			SearchArgs: messagesSearchArgs, FetchArgs: messageFetchArgs, ExpandArgs: messageFetchArgsAsExpand, ItemKeys: []string{"results", "messages", "items"}, IDKeys: []string{"message_id", "id"}, TitleKeys: []string{"chat_name", "title", "subject"}, SnippetKeys: []string{"text", "content", "snippet", "body"}, URLKeys: []string{"url", "link"}, TimeKeys: []string{"create_time", "timestamp", "time"},
		},
		{
			Descriptor: desc("larkcli.chats", kernel.SourceChats, []kernel.ObjectKind{kernel.KindChat}, userBot, nil, 50, true, kernel.ProjectionHead|kernel.ProjectionSnippet, kernel.ProjectionContent|kernel.ProjectionRelations),
			ProbeArgs:  []string{"im", "+chat-search", "--help"}, ProbeFlags: []string{"--query", "--page-size", "--page-token"},
			SearchArgs: chatsSearchArgs, FetchArgs: chatFetchArgs, ItemKeys: []string{"results", "chats", "items"}, IDKeys: []string{"chat_id", "id"}, TitleKeys: []string{"name", "title"}, SnippetKeys: []string{"description", "snippet"}, URLKeys: []string{"url", "link"}, TimeKeys: []string{"update_time", "create_time"},
		},
		{
			Descriptor: desc("larkcli.people", kernel.SourcePeople, []kernel.ObjectKind{kernel.KindPerson}, user, nil, 30, false, kernel.ProjectionHead|kernel.ProjectionContent, kernel.ProjectionRelations),
			ProbeArgs:  []string{"contact", "+search-user", "--help"}, ProbeFlags: []string{"--query", "--page-size"},
			SearchArgs: peopleSearchArgs, FetchArgs: personFetchArgs, ItemKeys: []string{"users", "results", "items"}, IDKeys: []string{"open_id", "user_id", "id"}, TitleKeys: []string{"name", "display_name", "en_name"}, SnippetKeys: []string{"email", "enterprise_email", "department_name"}, URLKeys: []string{"url"}, TimeKeys: nil,
		},
		{
			Descriptor: desc("larkcli.minutes", kernel.SourceMinutes, []kernel.ObjectKind{kernel.KindMinute}, user, []string{"minutes:minutes.search:read"}, 30, true, kernel.ProjectionHead|kernel.ProjectionSnippet, kernel.ProjectionSummary|kernel.ProjectionStructure|kernel.ProjectionContent|kernel.ProjectionRelations),
			ProbeArgs:  []string{"minutes", "+search", "--help"}, ProbeFlags: []string{"--query", "--page-size", "--page-token", "--participant-ids"},
			SearchArgs: minutesSearchArgs, FetchArgs: minutesFetchArgs, ItemKeys: []string{"minutes", "results", "items"}, IDKeys: []string{"minute_token", "token", "id"}, TitleKeys: []string{"title", "name"}, SnippetKeys: []string{"summary", "snippet"}, URLKeys: []string{"url", "minute_url"}, TimeKeys: []string{"start_time", "create_time", "time"},
		},
		{
			Descriptor: desc("larkcli.meetings", kernel.SourceMeetings, []kernel.ObjectKind{kernel.KindMeeting}, user, nil, 30, true, kernel.ProjectionHead|kernel.ProjectionSnippet, kernel.ProjectionContent|kernel.ProjectionRelations),
			ProbeArgs:  []string{"vc", "+search", "--help"}, ProbeFlags: []string{"--query", "--page-size", "--page-token", "--participant-ids"},
			SearchArgs: meetingsSearchArgs, FetchArgs: meetingFetchArgs, ExpandArgs: meetingExpandArgs, ItemKeys: []string{"meetings", "results", "items"}, IDKeys: []string{"meeting_id", "id"}, TitleKeys: []string{"topic", "title", "name"}, SnippetKeys: []string{"description", "summary"}, URLKeys: []string{"url", "meeting_url"}, TimeKeys: []string{"start_time", "create_time"},
		},
		{
			Descriptor: desc("larkcli.calendar", kernel.SourceCalendar, []kernel.ObjectKind{kernel.KindEvent}, user, nil, 50, true, kernel.ProjectionHead|kernel.ProjectionSnippet, kernel.ProjectionContent|kernel.ProjectionRelations),
			ProbeArgs:  []string{"calendar", "+search-event", "--help"}, ProbeFlags: []string{"--query", "--page-size", "--page-token", "--attendee-ids"},
			SearchArgs: calendarSearchArgs, FetchArgs: calendarFetchArgs, ItemKeys: []string{"events", "results", "items"}, IDKeys: []string{"event_id", "id"}, TitleKeys: []string{"summary", "title", "name"}, SnippetKeys: []string{"description", "snippet"}, URLKeys: []string{"app_link", "url", "link"}, TimeKeys: []string{"start_time", "start", "create_time"},
		},
		{
			Descriptor: desc("larkcli.tasks", kernel.SourceTasks, []kernel.ObjectKind{kernel.KindTask, kernel.KindTaskList}, user, nil, 50, true, kernel.ProjectionHead|kernel.ProjectionSnippet|kernel.ProjectionContent, kernel.ProjectionContent|kernel.ProjectionRelations),
			ProbeArgs:  []string{"task", "+search", "--help"}, ProbeFlags: []string{"--query", "--page-token", "--completed", "--assignee"},
			SearchArgs: tasksSearchArgs, QueryArgs: tasksQueryArgs, FetchArgs: taskFetchArgs, ItemKeys: []string{"tasks", "results", "items"}, IDKeys: []string{"guid", "task_guid", "task_id", "id"}, TitleKeys: []string{"summary", "title", "name"}, SnippetKeys: []string{"description", "snippet"}, URLKeys: []string{"url", "link"}, TimeKeys: []string{"updated_at", "update_time", "created_at", "create_time", "due"},
		},
		{
			Descriptor: desc("larkcli.mail", kernel.SourceMail, []kernel.ObjectKind{kernel.KindMail}, userBot, nil, 50, true, kernel.ProjectionHead|kernel.ProjectionSnippet, kernel.ProjectionContent|kernel.ProjectionContext|kernel.ProjectionRelations),
			ProbeArgs:  []string{"mail", "+triage", "--help"}, ProbeFlags: []string{"--query", "--max", "--page-token", "--mailbox"},
			SearchArgs: mailSearchArgs, FetchArgs: mailFetchArgs, ItemKeys: []string{"messages", "mails", "results", "items"}, IDKeys: []string{"message_id", "id"}, TitleKeys: []string{"subject", "title"}, SnippetKeys: []string{"snippet", "summary", "body"}, URLKeys: []string{"url", "link"}, TimeKeys: []string{"received_time", "create_time", "timestamp"},
		},
		{
			Descriptor: desc("larkcli.base", kernel.SourceBase, []kernel.ObjectKind{kernel.KindRecord}, userBot, nil, 200, true, kernel.ProjectionHead|kernel.ProjectionContent, 0),
			ProbeArgs:  []string{"base", "+record-search", "--help"}, ProbeFlags: []string{"--base-token", "--table-id", "--keyword", "--search-field", "--offset", "--limit"},
			QueryArgs: baseQueryArgs, ItemKeys: []string{"items", "records", "results"}, IDKeys: []string{"record_id", "id"}, TitleKeys: []string{"title", "name"}, SnippetKeys: []string{"text", "summary"}, URLKeys: []string{"url"}, TimeKeys: []string{"last_modified_time", "created_time"},
		},
		{
			Descriptor: desc("larkcli.sheets", kernel.SourceSheets, []kernel.ObjectKind{kernel.KindCell}, userBot, nil, 5000, false, kernel.ProjectionHead|kernel.ProjectionContent, 0),
			ProbeArgs:  []string{"sheets", "+cells-search", "--help"}, ProbeFlags: []string{"--spreadsheet-token", "--find", "--offset"},
			QueryArgs: sheetsQueryArgs, ItemKeys: []string{"matches", "cells", "results", "items"}, IDKeys: []string{"range", "cell", "id"}, TitleKeys: []string{"range", "sheet_name", "title"}, SnippetKeys: []string{"value", "text", "formula"}, URLKeys: []string{"url"}, TimeKeys: nil,
		},
	}
	for index := range specs {
		specs[index].OperationProbes = builtinOperationProbes(specs[index].Descriptor.Source)
	}
	return specs
}

func builtinOperationProbes(source kernel.SourceID) map[kernel.Operation]ProbeSpec {
	rawAPI := ProbeSpec{Args: []string{"api", "--help"}, Flags: []string{"--params", "--format"}}
	switch source {
	case kernel.SourceDocs:
		return map[kernel.Operation]ProbeSpec{kernel.OpFetch: {Args: []string{"docs", "+fetch", "--help"}, Flags: []string{"--doc", "--doc-format"}}}
	case kernel.SourceMessages:
		return map[kernel.Operation]ProbeSpec{kernel.OpFetch: rawAPI, kernel.OpExpand: rawAPI}
	case kernel.SourceChats, kernel.SourceTasks, kernel.SourceMail:
		return map[kernel.Operation]ProbeSpec{kernel.OpFetch: rawAPI}
	case kernel.SourcePeople:
		return map[kernel.Operation]ProbeSpec{kernel.OpFetch: rawAPI}
	case kernel.SourceMinutes:
		return map[kernel.Operation]ProbeSpec{kernel.OpFetch: {Args: []string{"minutes", "+detail", "--help"}, Flags: []string{"--minute-tokens", "--summary", "--chapter", "--transcript", "--output-dir", "--overwrite"}}}
	case kernel.SourceMeetings:
		detail := ProbeSpec{Args: []string{"vc", "+detail", "--help"}, Flags: []string{"--meeting-ids"}}
		return map[kernel.Operation]ProbeSpec{kernel.OpFetch: detail, kernel.OpExpand: detail}
	case kernel.SourceCalendar:
		return map[kernel.Operation]ProbeSpec{kernel.OpFetch: {Args: []string{"calendar", "events", "get", "--help"}, Flags: []string{"--calendar-id", "--event-id", "--params"}}}
	default:
		return nil
	}
}

func desc(id string, source kernel.SourceID, kinds []kernel.ObjectKind, identities []kernel.IdentityMode, scopes []string, maxPage int, paging bool, returned, fetchable kernel.ProjectionSet) kernel.ProviderDescriptor {
	ops := kernel.OperationSet{kernel.OpSearch: true, kernel.OpFetch: fetchable != 0, kernel.OpQuery: source == kernel.SourceTasks || source == kernel.SourceBase || source == kernel.SourceSheets, kernel.OpExpand: source == kernel.SourceMessages || source == kernel.SourceMeetings}
	if source == kernel.SourcePeople {
		ops[kernel.OpResolve] = true
	}
	if source == kernel.SourceBase || source == kernel.SourceSheets {
		delete(ops, kernel.OpSearch)
	}
	return kernel.ProviderDescriptor{ID: kernel.ProviderID(id), Source: source, ObjectKinds: kinds, Operations: ops, RequiredIdentity: identities, RequiredScopes: scopes, SearchLimits: kernel.SearchLimits{MaxQueryRunes: queryLimit(source), MaxPageSize: maxPage, MaxPages: 5}, BatchLimits: kernel.BatchLimits{MaxFetchItems: 1}, ReturnedProjection: returned, FetchableProjection: fetchable, SupportsPagination: paging, Backend: "lark-cli"}
}
func queryLimit(source kernel.SourceID) int {
	switch source {
	case kernel.SourceDocs:
		return 30
	case kernel.SourceMail:
		return 50
	default:
		return 200
	}
}

func commonPage(args []string, pageSize int, cursor string) []string {
	if pageSize > 0 {
		args = append(args, "--page-size", strconv.Itoa(pageSize))
	}
	if cursor != "" {
		args = append(args, "--page-token", cursor)
	}
	return args
}
func addTime(args []string, f kernel.SearchFilters) []string {
	if f.After != nil {
		args = append(args, "--start", f.After.Format(time.RFC3339))
	}
	if f.Before != nil {
		args = append(args, "--end", f.Before.Format(time.RFC3339))
	}
	return args
}
func docsSearchArgs(r kernel.ProviderSearchRequest) ([]string, error) {
	a := []string{"drive", "+search", "--query", r.Query}
	a = commonPage(a, min(r.PageSize, 20), r.Cursor)
	f := r.Filters
	if f.Mine {
		a = append(a, "--mine")
	}
	if len(f.DocTypes) > 0 {
		a = append(a, "--doc-types", strings.Join(f.DocTypes, ","))
	}
	if len(f.FolderTokens) > 0 {
		a = append(a, "--folder-tokens", strings.Join(f.FolderTokens, ","))
	}
	if len(f.SpaceIDs) > 0 {
		a = append(a, "--space-ids", strings.Join(f.SpaceIDs, ","))
	}
	if len(f.ChatIDs) > 0 {
		a = append(a, "--chat-ids", strings.Join(f.ChatIDs, ","))
	}
	if f.OnlyTitle {
		a = append(a, "--only-title")
	}
	if f.After != nil {
		a = append(a, "--created-since", f.After.Format(time.RFC3339))
	}
	if f.Before != nil {
		a = append(a, "--created-until", f.Before.Format(time.RFC3339))
	}
	return a, nil
}
func messagesSearchArgs(r kernel.ProviderSearchRequest) ([]string, error) {
	a := []string{"im", "+messages-search", "--query", r.Query}
	a = commonPage(a, r.PageSize, r.Cursor)
	a = addTime(a, r.Filters)
	if len(r.Filters.ChatIDs) > 0 {
		a = append(a, "--chat-id", strings.Join(r.Filters.ChatIDs, ","))
	}
	if len(r.Filters.SenderIDs) > 0 {
		a = append(a, "--sender", strings.Join(r.Filters.SenderIDs, ","))
	}
	return a, nil
}
func chatsSearchArgs(r kernel.ProviderSearchRequest) ([]string, error) {
	a := []string{"im", "+chat-search", "--query", r.Query}
	return commonPage(a, r.PageSize, r.Cursor), nil
}
func peopleSearchArgs(r kernel.ProviderSearchRequest) ([]string, error) {
	a := []string{"contact", "+search-user", "--query", r.Query}
	if r.PageSize > 0 {
		a = append(a, "--page-size", strconv.Itoa(min(r.PageSize, 30)))
	}
	return a, nil
}
func minutesSearchArgs(r kernel.ProviderSearchRequest) ([]string, error) {
	a := []string{"minutes", "+search", "--query", r.Query}
	a = commonPage(a, min(r.PageSize, 30), r.Cursor)
	a = addTime(a, r.Filters)
	if len(r.Filters.PersonIDs) > 0 {
		a = append(a, "--participant-ids", strings.Join(r.Filters.PersonIDs, ","))
	}
	return a, nil
}
func meetingsSearchArgs(r kernel.ProviderSearchRequest) ([]string, error) {
	a := []string{"vc", "+search", "--query", r.Query}
	a = commonPage(a, min(r.PageSize, 30), r.Cursor)
	return addTime(a, r.Filters), nil
}
func calendarSearchArgs(r kernel.ProviderSearchRequest) ([]string, error) {
	a := []string{"calendar", "+search-event", "--query", r.Query}
	a = commonPage(a, r.PageSize, r.Cursor)
	a = addTime(a, r.Filters)
	if len(r.Filters.PersonIDs) > 0 {
		a = append(a, "--attendee-ids", strings.Join(r.Filters.PersonIDs, ","))
	}
	return a, nil
}
func tasksSearchArgs(r kernel.ProviderSearchRequest) ([]string, error) {
	a := []string{"task", "+search", "--query", r.Query}
	if r.Cursor != "" {
		a = append(a, "--page-token", r.Cursor)
	}
	if r.Filters.Completed != nil {
		a = append(a, "--completed="+strconv.FormatBool(*r.Filters.Completed))
	}
	if len(r.Filters.PersonIDs) > 0 {
		a = append(a, "--assignee", strings.Join(r.Filters.PersonIDs, ","))
	}
	return a, nil
}
func mailSearchArgs(r kernel.ProviderSearchRequest) ([]string, error) {
	a := []string{"mail", "+triage", "--query", r.Query, "--max", strconv.Itoa(r.PageSize)}
	if r.Cursor != "" {
		a = append(a, "--page-token", r.Cursor)
	}
	return a, nil
}

func tasksQueryArgs(r kernel.ProviderQueryRequest) ([]string, error) {
	q := stringValue(r.Filter, "query")
	a := []string{"task", "+search"}
	if q != "" {
		a = append(a, "--query", q)
	}
	if v, ok := boolValue(r.Filter, "completed"); ok {
		a = append(a, "--completed="+strconv.FormatBool(v))
	}
	if assignee := stringValue(r.Filter, "assignee"); assignee != "" {
		a = append(a, "--assignee", assignee)
	}
	if r.Cursor != "" {
		a = append(a, "--page-token", r.Cursor)
	}
	return a, nil
}
func baseQueryArgs(r kernel.ProviderQueryRequest) ([]string, error) {
	if r.Container == nil {
		return nil, fmt.Errorf("base query requires container ref")
	}
	app, table := parseContainerIDs(*r.Container)
	if app == "" || table == "" {
		return nil, fmt.Errorf("base container native_id must be app_token/table_id")
	}
	keyword := stringValue(r.Filter, "keyword")
	field := stringValue(r.Filter, "search_field")
	if field == "" {
		field = stringValue(r.Filter, "field")
	}
	if keyword == "" || field == "" {
		return nil, fmt.Errorf("base query requires keyword and search_field")
	}
	a := []string{"base", "+record-search", "--base-token", app, "--table-id", table, "--keyword", keyword, "--search-field", field, "--limit", strconv.Itoa(min(r.Limit, 200))}
	if r.Cursor != "" {
		offset, err := strconv.Atoi(r.Cursor)
		if err != nil || offset < 0 {
			return nil, fmt.Errorf("base query cursor must be a non-negative offset")
		}
		a = append(a, "--offset", strconv.Itoa(offset))
	}
	return a, nil
}
func sheetsQueryArgs(r kernel.ProviderQueryRequest) ([]string, error) {
	if r.Container == nil {
		return nil, fmt.Errorf("sheets query requires container ref")
	}
	find := stringValue(r.Filter, "find")
	if find == "" {
		return nil, fmt.Errorf("sheets query requires find")
	}
	a := []string{"sheets", "+cells-search", "--spreadsheet-token", r.Container.NativeID, "--find", find}
	if sh := stringValue(r.Filter, "sheet_id"); sh != "" {
		a = append(a, "--sheet-id", sh)
	}
	if b, ok := boolValue(r.Filter, "regex"); ok && b {
		a = append(a, "--regex")
	}
	if r.Cursor != "" {
		offset, err := strconv.Atoi(r.Cursor)
		if err != nil || offset < 0 {
			return nil, fmt.Errorf("sheets query cursor must be a non-negative offset")
		}
		a = append(a, "--offset", strconv.Itoa(offset))
	}
	return a, nil
}

func docsFetchArgs(r kernel.ProviderFetchRequest) ([]string, error) {
	doc := r.Ref.NativeID
	if r.Ref.URL != "" {
		doc = r.Ref.URL
	}
	if doc == "" {
		return nil, fmt.Errorf("document ref has no token or URL")
	}
	return []string{"docs", "+fetch", "--doc", doc, "--doc-format", "markdown"}, nil
}
func messageFetchArgs(r kernel.ProviderFetchRequest) ([]string, error) {
	id, err := safePathSegment(r.Ref.NativeID, "message id")
	if err != nil {
		return nil, err
	}
	return []string{"api", "GET", "/open-apis/im/v1/messages/" + id}, nil
}
func chatFetchArgs(r kernel.ProviderFetchRequest) ([]string, error) {
	id, err := safePathSegment(r.Ref.NativeID, "chat id")
	if err != nil {
		return nil, err
	}
	return []string{"api", "GET", "/open-apis/im/v1/chats/" + id}, nil
}
func personFetchArgs(r kernel.ProviderFetchRequest) ([]string, error) {
	id, err := safePathSegment(r.Ref.NativeID, "person id")
	if err != nil {
		return nil, err
	}
	params := mustJSON(map[string]any{"user_id_type": "open_id"})
	return []string{"api", "GET", "/open-apis/contact/v3/users/" + id, "--params", params}, nil
}
func minutesFetchArgs(r kernel.ProviderFetchRequest) ([]string, error) {
	a := []string{"minutes", "+detail", "--minute-tokens", r.Ref.NativeID}
	p := r.Projection
	if p.Any(kernel.ProjectionSummary) {
		a = append(a, "--summary", "--todo")
	}
	if p.Any(kernel.ProjectionStructure) {
		a = append(a, "--chapter", "--keyword")
	}
	if p.Any(kernel.ProjectionContent) {
		a = append(a, "--transcript")
	}
	return a, nil
}
func meetingFetchArgs(r kernel.ProviderFetchRequest) ([]string, error) {
	return []string{"vc", "+detail", "--meeting-ids", r.Ref.NativeID}, nil
}
func calendarFetchArgs(r kernel.ProviderFetchRequest) ([]string, error) {
	calendarID := metadataString(r.Ref, "calendar_id")
	if calendarID == "" {
		return nil, fmt.Errorf("calendar fetch requires calendar_id in ref URL query or native_id 'calendar/event'")
	}
	eventID := r.Ref.NativeID
	if strings.Contains(eventID, "/") {
		parts := strings.SplitN(eventID, "/", 2)
		calendarID, eventID = parts[0], parts[1]
	}
	if _, err := safePathSegment(calendarID, "calendar id"); err != nil {
		return nil, err
	}
	if _, err := safePathSegment(eventID, "event id"); err != nil {
		return nil, err
	}
	params := mustJSON(map[string]any{"calendar_id": calendarID, "event_id": eventID})
	return []string{"calendar", "events", "get", "--params", params}, nil
}
func taskFetchArgs(r kernel.ProviderFetchRequest) ([]string, error) {
	id, err := safePathSegment(r.Ref.NativeID, "task id")
	if err != nil {
		return nil, err
	}
	return []string{"api", "GET", "/open-apis/task/v2/tasks/" + id}, nil
}
func mailFetchArgs(r kernel.ProviderFetchRequest) ([]string, error) {
	mailbox := metadataString(r.Ref, "mailbox")
	messageID := r.Ref.NativeID
	if strings.Contains(messageID, "/") {
		parts := strings.SplitN(messageID, "/", 2)
		mailbox, messageID = parts[0], parts[1]
	}
	if mailbox == "" {
		mailbox = "me"
	}
	if messageID == "" {
		return nil, fmt.Errorf("mail ref has no message id")
	}
	mailbox, err := safePathSegment(mailbox, "mailbox id")
	if err != nil {
		return nil, err
	}
	messageID, err = safePathSegment(messageID, "mail message id")
	if err != nil {
		return nil, err
	}
	return []string{"api", "GET", fmt.Sprintf("/open-apis/mail/v1/user_mailboxes/%s/messages/%s", mailbox, messageID)}, nil
}
func messageFetchArgsAsExpand(r kernel.ProviderExpandRequest) ([]string, error) {
	return messageFetchArgs(kernel.ProviderFetchRequest{Ref: r.Ref, Projection: kernel.ProjectionContent | kernel.ProjectionRelations, Identity: r.Identity})
}
func meetingExpandArgs(r kernel.ProviderExpandRequest) ([]string, error) {
	return []string{"vc", "+detail", "--meeting-ids", r.Ref.NativeID}, nil
}

func parseContainerIDs(r kernel.ObjectRef) (string, string) {
	parts := strings.Split(strings.Trim(r.NativeID, "/"), "/")
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "", ""
}
func stringValue(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[k]; ok {
		return fmt.Sprint(v)
	}
	return ""
}
func boolValue(m map[string]any, k string) (bool, bool) {
	if m == nil {
		return false, false
	}
	v, ok := m[k]
	if !ok {
		return false, false
	}
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		b, e := strconv.ParseBool(x)
		return b, e == nil
	}
	return false, false
}
func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func metadataString(r kernel.ObjectRef, key string) string {
	if r.URL == "" {
		return ""
	}
	parsed, err := url.Parse(r.URL)
	if err != nil {
		return ""
	}
	return parsed.Query().Get(key)
}

func safePathSegment(value, label string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value || value == "." || value == ".." || len(value) > 2048 {
		return "", fmt.Errorf("%s is not a valid path segment", label)
	}
	if strings.ContainsAny(value, "/\\") {
		return "", fmt.Errorf("%s must not contain a path separator", label)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%s must not contain control characters", label)
		}
	}
	return url.PathEscape(value), nil
}
func min(a, b int) int {
	if a <= 0 {
		return b
	}
	if a < b {
		return a
	}
	return b
}
