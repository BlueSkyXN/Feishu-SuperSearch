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
	payload, err := selectJSONPayload(result)
	if err != nil {
		return env, nil, err
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
		if result.ExitCode != 0 {
			return env, nil, &kernel.ErrorDetail{Type: kernel.ErrUpstreamPermanent, Message: "lark-cli returned a success envelope with a non-zero exit code", Details: map[string]any{"exit_code": result.ExitCode}}
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

type jsonPayloadCandidate struct {
	payload  []byte
	stream   int
	envelope bool
	clean    bool
}

func selectJSONPayload(result CommandResult) ([]byte, error) {
	streams := [][]byte{result.Stdout, result.Stderr}
	candidates := make([]jsonPayloadCandidate, 0, 2)
	malformedStructuredOutput := false
	for stream, output := range streams {
		found, malformed := scanJSONPayloads(output, stream)
		candidates = append(candidates, found...)
		malformedStructuredOutput = malformedStructuredOutput || malformed
	}

	primaryStream := 0
	if result.ExitCode != 0 && len(bytes.TrimSpace(result.Stderr)) > 0 {
		primaryStream = 1
	}
	primary := bytes.TrimSpace(streams[primaryStream])
	if len(candidates) == 0 {
		if len(primary) == 0 {
			return nil, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: fmt.Sprintf("lark-cli produced no JSON (exit=%d)", result.ExitCode)}
		}
		var raw any
		if err := json.Unmarshal(primary, &raw); err != nil {
			return nil, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: "invalid JSON from lark-cli: " + err.Error()}
		}
		return nil, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: "lark-cli produced no supported JSON payload"}
	}
	if malformedStructuredOutput || len(candidates) != 1 {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: "lark-cli produced ambiguous JSON output"}
	}

	candidate := candidates[0]
	// Mixed diagnostic output is only safe to ignore when a typed CLI
	// envelope anchors the result. Raw OpenAPI compatibility stays whole-stream.
	if !candidate.envelope && (!candidate.clean || candidate.stream != primaryStream) {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: "lark-cli mixed diagnostics with an untyped JSON payload"}
	}
	return candidate.payload, nil
}

func scanJSONPayloads(output []byte, stream int) ([]jsonPayloadCandidate, bool) {
	candidates := make([]jsonPayloadCandidate, 0, 1)
	malformed := false
	for cursor := 0; cursor < len(output); {
		lineEnd := len(output)
		if offset := bytes.IndexByte(output[cursor:], '\n'); offset >= 0 {
			lineEnd = cursor + offset
		}
		line := output[cursor:lineEnd]
		trimmedLine := bytes.TrimSpace(line)
		if len(trimmedLine) == 0 || (!json.Valid(trimmedLine) && !looksLikeJSONContainer(trimmedLine)) {
			cursor = nextLineOffset(lineEnd, len(output))
			continue
		}

		start := cursor + bytes.Index(line, trimmedLine)
		decoder := json.NewDecoder(bytes.NewReader(output[start:]))
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			malformed = true
			cursor = nextLineOffset(lineEnd, len(output))
			continue
		}
		end := start + int(decoder.InputOffset())
		valueLineEnd := len(output)
		if offset := bytes.IndexByte(output[end:], '\n'); offset >= 0 {
			valueLineEnd = end + offset
		}
		if len(bytes.TrimSpace(output[end:valueLineEnd])) != 0 {
			malformed = true
			cursor = nextLineOffset(valueLineEnd, len(output))
			continue
		}

		payload := append([]byte(nil), bytes.TrimSpace(output[start:end])...)
		var marker struct {
			OK *bool `json:"ok"`
		}
		_ = json.Unmarshal(payload, &marker)
		candidates = append(candidates, jsonPayloadCandidate{
			payload:  payload,
			stream:   stream,
			envelope: marker.OK != nil,
			clean:    bytes.Equal(bytes.TrimSpace(output), payload),
		})
		cursor = nextLineOffset(valueLineEnd, len(output))
	}
	return candidates, malformed
}

func looksLikeJSONContainer(line []byte) bool {
	if len(line) == 0 || (line[0] != '{' && line[0] != '[') {
		return false
	}
	remaining := bytes.TrimSpace(line[1:])
	if len(remaining) == 0 {
		return true
	}
	if line[0] == '{' {
		return remaining[0] == '"' || remaining[0] == '}'
	}
	return bytes.ContainsAny(remaining[:1], `[{"-0123456789tfn]`)
}

func nextLineOffset(lineEnd, outputLen int) int {
	if lineEnd < outputLen {
		return lineEnd + 1
	}
	return outputLen
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
