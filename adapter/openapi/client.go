package openapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type ClientConfig struct {
	AppID            string
	BaseURL          string
	Token            string
	Timeout          time.Duration
	MaxResponseBytes int64
	HTTPClient       *http.Client
}

type Client struct {
	appID            string
	baseURL          string
	token            string
	maxResponseBytes int64
	http             *http.Client
	sdk              *officialSDK
}

func NewClient(cfg ClientConfig) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = "https://open.feishu.cn"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = 16 << 20
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: cfg.Timeout}
	}
	client := &Client{appID: strings.TrimSpace(cfg.AppID), baseURL: strings.TrimRight(cfg.BaseURL, "/"), token: strings.TrimSpace(cfg.Token), maxResponseBytes: cfg.MaxResponseBytes, http: hc}
	client.sdk = newOfficialSDK(client.appID, client.baseURL, cfg.Timeout, cfg.MaxResponseBytes, hc)
	return client, nil
}

func (c *Client) HasToken() bool { return c != nil && c.token != "" }

func (c *Client) HasSDKCredentials() bool { return c != nil && c.appID != "" && c.token != "" }

func (c *Client) Call(ctx context.Context, method, path string, body any) (map[string]any, error) {
	if c == nil {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "openapi client is nil"}
	}
	if c.token == "" {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrIdentityRequired, Message: "direct OpenAPI access token is not configured"}
	}
	parsedPath, err := url.ParseRequestURI(path)
	if err != nil || parsedPath.IsAbs() || parsedPath.Host != "" || parsedPath.RawQuery != "" || parsedPath.Fragment != "" || !strings.HasPrefix(parsedPath.Path, "/open-apis/") {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "OpenAPI path must be an absolute /open-apis/ path without query or fragment"}
	}
	for _, candidate := range []string{parsedPath.Path, parsedPath.EscapedPath()} {
		for _, segment := range strings.Split(candidate, "/") {
			if segment != "." && segment != ".." {
				continue
			}
			return nil, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "OpenAPI path contains a traversal segment"}
		}
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "encode OpenAPI request: " + err.Error()}
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		typ := kernel.ErrUpstreamTransient
		switch ctx.Err() {
		case context.DeadlineExceeded:
			typ = kernel.ErrDeadlineExceeded
		case context.Canceled:
			typ = kernel.ErrCancelled
		}
		return nil, newPublicOpenAPIError(typ, 0, "")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.maxResponseBytes+1))
	if err != nil {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrUpstreamTransient, Message: "read OpenAPI response: " + err.Error()}
	}
	if int64(len(raw)) > c.maxResponseBytes {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: "OpenAPI response exceeded configured size limit"}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, newPublicOpenAPIError(kernel.ErrRateLimited, resp.StatusCode, openAPILogIDFromRaw(raw))
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, newPublicOpenAPIError(kernel.ErrIdentityRequired, resp.StatusCode, openAPILogIDFromRaw(raw))
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, newPublicOpenAPIError(kernel.ErrMissingScope, resp.StatusCode, openAPILogIDFromRaw(raw))
	}
	if resp.StatusCode >= 500 {
		return nil, newPublicOpenAPIError(kernel.ErrUpstreamTransient, resp.StatusCode, openAPILogIDFromRaw(raw))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newPublicOpenAPIError(kernel.ErrUpstreamPermanent, resp.StatusCode, openAPILogIDFromRaw(raw))
	}
	var envelope map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&envelope); err != nil {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: "decode OpenAPI response: " + err.Error()}
	}
	code := numericCode(envelope["code"])
	if code != 0 {
		message := stringValue(envelope["msg"])
		typ := classifyAPIError(code, message)
		return nil, newPublicOpenAPIError(typ, int(code), safeOpenAPILogID(envelope["log_id"]))
	}
	if data, ok := envelope["data"].(map[string]any); ok {
		return data, nil
	}
	return envelope, nil
}

func safeOpenAPIPathSegment(value, label string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value || value == "." || value == ".." || len(value) > 2048 || strings.ContainsAny(value, "/\\") {
		return "", &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: label + " is not a valid path segment"}
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: label + " contains control characters"}
		}
	}
	return url.PathEscape(value), nil
}

func validateFetchProjection(provider kernel.ProviderID, source kernel.SourceID, requested, allowed kernel.ProjectionSet) error {
	if requested.Empty() {
		return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, ProviderID: provider, Source: source, Message: "fetch projection is required"}
	}
	if unsupported := requested &^ allowed; unsupported != 0 {
		return &kernel.ErrorDetail{Type: kernel.ErrUnsupported, ProviderID: provider, Source: source, Message: "requested projection is not supported", Details: map[string]any{"projection": unsupported.Strings()}}
	}
	return nil
}

func numericCode(v any) int64 {
	switch x := v.(type) {
	case nil:
		return 0
	case json.Number:
		n, _ := x.Int64()
		return n
	case float64:
		return int64(x)
	case int:
		return int64(x)
	case int64:
		return x
	case string:
		var n int64
		_, _ = fmt.Sscan(x, &n)
		return n
	default:
		return -1
	}
}
func classifyAPIError(code int64, msg string) kernel.ErrorType {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "scope") || strings.Contains(lower, "permission") || strings.Contains(lower, "forbidden") || strings.Contains(msg, "权限"):
		return kernel.ErrMissingScope
	case strings.Contains(lower, "token") || strings.Contains(lower, "unauthorized") || strings.Contains(msg, "登录"):
		return kernel.ErrIdentityRequired
	case strings.Contains(lower, "rate") || strings.Contains(lower, "too many") || code == 99991400:
		return kernel.ErrRateLimited
	case code >= 50000000 && code < 60000000:
		return kernel.ErrUpstreamTransient
	default:
		return kernel.ErrUpstreamPermanent
	}
}
func newPublicOpenAPIError(t kernel.ErrorType, code int, logID string) *kernel.ErrorDetail {
	message, hint := publicOpenAPIError(t)
	detail := &kernel.ErrorDetail{
		Type:      t,
		Code:      code,
		Message:   message,
		Hint:      hint,
		Retryable: t == kernel.ErrRateLimited || t == kernel.ErrUpstreamTransient,
	}
	if logID != "" {
		detail.Details = map[string]any{"log_id": logID}
	}
	return detail
}

func publicOpenAPIError(t kernel.ErrorType) (string, string) {
	switch t {
	case kernel.ErrMissingScope:
		return "Feishu OpenAPI reported a missing permission scope", "verify the read-only scopes for the selected identity"
	case kernel.ErrIdentityRequired:
		return "Feishu OpenAPI requires a compatible authenticated identity", "verify the configured user access token and identity mode"
	case kernel.ErrRateLimited:
		return "Feishu OpenAPI reported rate limiting", "retry after backoff"
	case kernel.ErrDeadlineExceeded:
		return "Feishu OpenAPI request deadline exceeded", "retry the read-only operation with an appropriate timeout"
	case kernel.ErrCancelled:
		return "Feishu OpenAPI request was cancelled", "retry the read-only operation if it is still needed"
	case kernel.ErrUpstreamTransient:
		return "Feishu OpenAPI reported a transient upstream failure", "retry the read-only operation"
	default:
		return "Feishu OpenAPI request failed", "verify the request, object access, and provider capabilities"
	}
}

func openAPILogIDFromRaw(raw []byte) string {
	var envelope map[string]any
	if json.Unmarshal(raw, &envelope) != nil {
		return ""
	}
	return safeOpenAPILogID(envelope["log_id"])
}

func safeOpenAPILogID(value any) string {
	logID, ok := value.(string)
	if !ok {
		return ""
	}
	logID = strings.TrimSpace(logID)
	if logID == "" || len(logID) > 128 {
		return ""
	}
	for _, r := range logID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("_-:.", r) {
			continue
		}
		return ""
	}
	return logID
}
func stringValue(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
