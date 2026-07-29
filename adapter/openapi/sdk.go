package openapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkdirectory "github.com/larksuite/oapi-sdk-go/v3/service/directory/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkminutes "github.com/larksuite/oapi-sdk-go/v3/service/minutes/v1"
	larksearch "github.com/larksuite/oapi-sdk-go/v3/service/search/v2"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

var errSDKResponseTooLarge = errors.New("OpenAPI SDK response exceeded configured size limit")

type officialSDK struct {
	client *lark.Client
}

func newOfficialSDK(appID, baseURL string, timeout time.Duration, maxResponseBytes int64, client *http.Client) *officialSDK {
	limited := limitedSDKHTTPClient{next: client, maxResponseBytes: maxResponseBytes}
	return &officialSDK{client: lark.NewClient(
		appID,
		"",
		lark.WithOpenBaseUrl(baseURL),
		lark.WithHttpClient(limited),
		lark.WithReqTimeout(timeout),
		lark.WithLogLevel(larkcore.LogLevelError),
		lark.WithSource("superfeishusearch"),
	)}
}

type limitedSDKHTTPClient struct {
	next             *http.Client
	maxResponseBytes int64
}

func (c limitedSDKHTTPClient) Do(request *http.Request) (*http.Response, error) {
	response, err := c.next.Do(request)
	if err != nil || response == nil || response.Body == nil {
		return response, err
	}
	response.Body = &limitedSDKBody{ReadCloser: response.Body, remaining: c.maxResponseBytes}
	return response, nil
}

type limitedSDKBody struct {
	io.ReadCloser
	remaining int64
}

func (r *limitedSDKBody) Read(buffer []byte) (int, error) {
	if r.remaining <= 0 {
		var probe [1]byte
		n, err := r.ReadCloser.Read(probe[:])
		if n > 0 {
			return 0, errSDKResponseTooLarge
		}
		return 0, err
	}
	if int64(len(buffer)) > r.remaining {
		buffer = buffer[:r.remaining]
	}
	n, err := r.ReadCloser.Read(buffer)
	r.remaining -= int64(n)
	return n, err
}

func (c *Client) sdkOption() larkcore.RequestOptionFunc {
	return larkcore.WithUserAccessToken(c.token)
}

func (c *Client) searchDocsSDK(ctx context.Context, body map[string]any) (map[string]any, error) {
	var typed larksearch.SearchDocWikiReqBody
	if err := sdkRequestBody(body, &typed); err != nil {
		return nil, err
	}
	request := larksearch.NewSearchDocWikiReqBuilder().Body(&typed).Build()
	response, err := c.sdk.client.Search.DocWiki.Search(ctx, request, c.sdkOption())
	if err != nil {
		return nil, sdkCallError(ctx, err)
	}
	return sdkResponseData(response.ApiResp, response.Code, response.Msg, response.Data)
}

func (c *Client) searchMessagesSDK(ctx context.Context, body map[string]any, pageSize int, cursor string) (map[string]any, error) {
	var typed larksearch.CreateMessageReqBody
	if err := sdkRequestBody(body, &typed); err != nil {
		return nil, err
	}
	builder := larksearch.NewCreateMessageReqBuilder().PageSize(pageSize).Body(&typed)
	if cursor != "" {
		builder.PageToken(cursor)
	}
	response, err := c.sdk.client.Search.Message.Create(ctx, builder.Build(), c.sdkOption())
	if err != nil {
		return nil, sdkCallError(ctx, err)
	}
	return sdkResponseData(response.ApiResp, response.Code, response.Msg, response.Data)
}

func (c *Client) getMessageSDK(ctx context.Context, messageID string) (map[string]any, error) {
	request := larkim.NewGetMessageReqBuilder().MessageId(messageID).UserIdType("open_id").CardMsgContentType("raw_card_content").Build()
	response, err := c.sdk.client.Im.Message.Get(ctx, request, c.sdkOption())
	if err != nil {
		return nil, sdkCallError(ctx, err)
	}
	return sdkResponseData(response.ApiResp, response.Code, response.Msg, response.Data)
}

func (c *Client) searchPeopleSDK(ctx context.Context, query string, pageSize int, cursor string) (map[string]any, error) {
	page := larkdirectory.NewPageConditionBuilder().PageSize(pageSize)
	if cursor != "" {
		page.PageToken(cursor)
	}
	body := larkdirectory.NewSearchEmployeeReqBodyBuilder().
		Query(query).
		PageRequest(page.Build()).
		RequiredFields([]string{
			"base_info.employee_id",
			"base_info.user_id",
			"base_info.name.name",
			"base_info.name.display_name",
			"base_info.enterprise_email",
			"base_info.email",
		}).
		Build()
	request := larkdirectory.NewSearchEmployeeReqBuilder().EmployeeIdType("open_id").DepartmentIdType("open_department_id").Body(body).Build()
	response, err := c.sdk.client.Directory.V1.Employee.Search(ctx, request, c.sdkOption())
	if err != nil {
		return nil, sdkCallError(ctx, err)
	}
	return sdkResponseData(response.ApiResp, response.Code, response.Msg, response.Data)
}

func (c *Client) searchMinutesSDK(ctx context.Context, body map[string]any, pageSize int, cursor string) (map[string]any, error) {
	var typed larkminutes.SearchMinuteReqBody
	if err := sdkRequestBody(body, &typed); err != nil {
		return nil, err
	}
	builder := larkminutes.NewSearchMinuteReqBuilder().PageSize(pageSize).UserIdType("open_id").Body(&typed)
	if cursor != "" {
		builder.PageToken(cursor)
	}
	response, err := c.sdk.client.Minutes.V1.Minute.Search(ctx, builder.Build(), c.sdkOption())
	if err != nil {
		return nil, sdkCallError(ctx, err)
	}
	return sdkResponseData(response.ApiResp, response.Code, response.Msg, response.Data)
}

func (c *Client) getMinuteSDK(ctx context.Context, token string) (map[string]any, error) {
	request := larkminutes.NewGetMinuteReqBuilder().MinuteToken(token).UserIdType("open_id").Build()
	response, err := c.sdk.client.Minutes.V1.Minute.Get(ctx, request, c.sdkOption())
	if err != nil {
		return nil, sdkCallError(ctx, err)
	}
	return sdkResponseData(response.ApiResp, response.Code, response.Msg, response.Data)
}

func (c *Client) minuteArtifactsSDK(ctx context.Context, token string) (map[string]any, error) {
	request := larkminutes.NewArtifactsMinuteReqBuilder().MinuteToken(token).Build()
	response, err := c.sdk.client.Minutes.V1.Minute.Artifacts(ctx, request, c.sdkOption())
	if err != nil {
		return nil, sdkCallError(ctx, err)
	}
	return sdkResponseData(response.ApiResp, response.Code, response.Msg, response.Data)
}

func sdkRequestBody(input map[string]any, output any) error {
	encoded, err := json.Marshal(input)
	if err != nil {
		return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "encode OpenAPI SDK request: " + err.Error()}
	}
	if err := json.Unmarshal(encoded, output); err != nil {
		return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "build OpenAPI SDK request: " + err.Error()}
	}
	return nil
}

func sdkResponseData(response *larkcore.ApiResp, code int, message string, data any) (map[string]any, error) {
	if response == nil {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: "OpenAPI SDK returned no response"}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		typ := kernel.ErrUpstreamPermanent
		switch {
		case response.StatusCode == http.StatusUnauthorized:
			typ = kernel.ErrIdentityRequired
		case response.StatusCode == http.StatusForbidden:
			typ = kernel.ErrMissingScope
		case response.StatusCode == http.StatusTooManyRequests:
			typ = kernel.ErrRateLimited
		case response.StatusCode >= http.StatusInternalServerError:
			typ = kernel.ErrUpstreamTransient
		}
		return nil, newPublicOpenAPIError(typ, response.StatusCode, openAPILogIDFromRaw(response.RawBody))
	}
	if code != 0 {
		return nil, newPublicOpenAPIError(classifyAPIError(int64(code), message), code, openAPILogIDFromRaw(response.RawBody))
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: "encode OpenAPI SDK response: " + err.Error()}
	}
	result := map[string]any{}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: "normalize OpenAPI SDK response: " + err.Error()}
	}
	return result, nil
}

func sdkCallError(ctx context.Context, err error) error {
	if errors.Is(err, errSDKResponseTooLarge) {
		return &kernel.ErrorDetail{Type: kernel.ErrParse, Message: errSDKResponseTooLarge.Error()}
	}
	switch ctx.Err() {
	case context.Canceled:
		return &kernel.ErrorDetail{Type: kernel.ErrCancelled, Message: "OpenAPI SDK request cancelled"}
	case context.DeadlineExceeded:
		return &kernel.ErrorDetail{Type: kernel.ErrDeadlineExceeded, Message: "OpenAPI SDK request deadline exceeded"}
	}
	var clientTimeout *larkcore.ClientTimeoutError
	var serverTimeout *larkcore.ServerTimeoutError
	var dialFailed *larkcore.DialFailedError
	var illegalParam *larkcore.IllegalParamError
	var urlError *url.Error
	switch {
	case errors.As(err, &clientTimeout), errors.As(err, &serverTimeout):
		return newPublicOpenAPIError(kernel.ErrDeadlineExceeded, 0, "")
	case errors.As(err, &dialFailed), errors.As(err, &urlError):
		return newPublicOpenAPIError(kernel.ErrUpstreamTransient, 0, "")
	case errors.As(err, &illegalParam):
		return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "OpenAPI SDK rejected the request parameters"}
	default:
		return &kernel.ErrorDetail{Type: kernel.ErrParse, Message: "OpenAPI SDK returned an invalid response"}
	}
}
