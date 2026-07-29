package larkcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type Envelope struct {
	OK       bool            `json:"ok"`
	Identity string          `json:"identity,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
	Meta     map[string]any  `json:"meta,omitempty"`
	Error    *CLIError       `json:"error,omitempty"`
}

type CLIError struct {
	Type    string `json:"type,omitempty"`
	Subtype string `json:"subtype,omitempty"`
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

func ParseEnvelope(result CommandResult) (Envelope, any, error) {
	var env Envelope
	payload := bytes.TrimSpace(result.Stdout)
	if result.ExitCode != 0 && len(bytes.TrimSpace(result.Stderr)) > 0 {
		payload = bytes.TrimSpace(result.Stderr)
	}
	if len(payload) == 0 {
		return env, nil, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: fmt.Sprintf("lark-cli produced no JSON (exit=%d)", result.ExitCode)}
	}
	// The official CLI envelope always has an explicit `ok` field. Raw
	// OpenAPI responses can also contain `data`, so data presence alone must
	// not be used to classify a payload as a CLI envelope.
	var marker struct {
		OK *bool `json:"ok"`
	}
	_ = json.Unmarshal(payload, &marker)
	if marker.OK != nil {
		if err := json.Unmarshal(payload, &env); err != nil {
			return env, nil, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: "cannot parse lark-cli envelope: " + err.Error()}
		}
		if !env.OK {
			return env, nil, mapCLIError(env.Error, result.ExitCode)
		}
		var data any
		if len(env.Data) > 0 {
			if err := json.Unmarshal(env.Data, &data); err != nil {
				return env, nil, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: "cannot parse lark-cli data: " + err.Error()}
			}
		}
		return env, data, nil
	}
	// Compatibility path for raw OpenAPI output or older CLI builds.
	var raw any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return env, nil, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: "invalid JSON from lark-cli: " + err.Error()}
	}
	if result.ExitCode != 0 {
		return env, nil, &kernel.ErrorDetail{Type: kernel.ErrUpstreamPermanent, Message: "lark-cli command failed", Details: map[string]any{"exit_code": result.ExitCode}}
	}
	return Envelope{OK: true}, raw, nil
}

func mapCLIError(e *CLIError, exit int) *kernel.ErrorDetail {
	if e == nil {
		return &kernel.ErrorDetail{Type: kernel.ErrUpstreamPermanent, Message: fmt.Sprintf("lark-cli failed with exit code %d", exit)}
	}
	t := kernel.ErrUpstreamPermanent
	lower := strings.ToLower(e.Type + " " + e.Subtype + " " + e.Message)
	switch {
	case strings.Contains(lower, "scope") || strings.Contains(lower, "permission") || e.Code == 99991679:
		t = kernel.ErrMissingScope
	case strings.Contains(lower, "rate") || e.Code == 99991400:
		t = kernel.ErrRateLimited
	case strings.Contains(lower, "validation") || strings.Contains(lower, "argument"):
		t = kernel.ErrInvalidRequest
	case strings.Contains(lower, "identity") || strings.Contains(lower, "authorization"):
		t = kernel.ErrIdentityRequired
	case strings.Contains(lower, "not found"):
		t = kernel.ErrNotFound
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "temporar") || strings.Contains(lower, "5xx"):
		t = kernel.ErrUpstreamTransient
	case strings.Contains(lower, "version") || strings.Contains(lower, "schema"):
		t = kernel.ErrVersionIncompatible
	}
	message, hint := publicCLIError(t)
	return &kernel.ErrorDetail{Type: t, Subtype: e.Subtype, Code: e.Code, Message: message, Hint: hint, Retryable: t == kernel.ErrRateLimited || t == kernel.ErrUpstreamTransient}
}

func publicCLIError(t kernel.ErrorType) (string, string) {
	switch t {
	case kernel.ErrMissingScope:
		return "lark-cli reported a missing permission scope", "verify the read-only scopes for the selected profile"
	case kernel.ErrRateLimited:
		return "lark-cli reported rate limiting", "retry after backoff"
	case kernel.ErrInvalidRequest:
		return "lark-cli rejected the request parameters", "check the object type, filters, and provider capabilities"
	case kernel.ErrIdentityRequired:
		return "lark-cli requires a compatible authenticated identity", "verify the selected profile and identity mode"
	case kernel.ErrNotFound:
		return "lark-cli could not find the requested object", "verify that the current identity can access the object"
	case kernel.ErrUpstreamTransient:
		return "lark-cli reported a transient upstream failure", "retry the read-only operation"
	case kernel.ErrVersionIncompatible:
		return "lark-cli response is incompatible with this adapter", "run sfs doctor and verify the supported lark-cli version"
	default:
		return "lark-cli command failed", "inspect the local lark-cli configuration without exposing credentials"
	}
}
