package larkcli

import (
	"strings"
	"testing"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestParseEnvelopeSuccess(t *testing.T) {
	env, data, err := ParseEnvelope(CommandResult{ExitCode: 0, Stdout: []byte(`{"ok":true,"data":{"items":[{"id":"x"}]}}`)})
	if err != nil || !env.OK || data == nil {
		t.Fatalf("env=%+v data=%v err=%v", env, data, err)
	}
}
func TestParseEnvelopeMissingScope(t *testing.T) {
	_, _, err := ParseEnvelope(CommandResult{ExitCode: 1, Stderr: []byte(`{"ok":false,"error":{"type":"permission_error","code":99991679,"message":"missing scope"}}`)})
	d := kernel.DetailFromError(err)
	if d.Type != kernel.ErrMissingScope {
		t.Fatalf("type=%s err=%v", d.Type, err)
	}
}
func TestParseEnvelopeRawCompatibility(t *testing.T) {
	_, data, err := ParseEnvelope(CommandResult{ExitCode: 0, Stdout: []byte(`{"code":0,"data":{"page_token":"p"}}`)})
	if err != nil || data == nil {
		t.Fatalf("data=%v err=%v", data, err)
	}
}

func TestParseEnvelopeAcceptsSingleEnvelopeWithDiagnosticNoise(t *testing.T) {
	t.Run("success envelope in stdout", func(t *testing.T) {
		result := CommandResult{
			ExitCode: 0,
			Stdout: []byte("lark-cli: delayed local update check\n" +
				"{\n" +
				"  \"ok\": true,\n" +
				"  \"data\": {\"items\": [{\"id\": \"fixture\"}]}\n" +
				"}\n"),
		}
		env, data, err := ParseEnvelope(result)
		if err != nil || !env.OK || data == nil {
			t.Fatalf("env=%+v data=%v err=%v", env, data, err)
		}
	})

	t.Run("error envelope in stdout with stderr diagnostics", func(t *testing.T) {
		result := CommandResult{
			ExitCode: 3,
			Stdout:   []byte(`{"ok":false,"error":{"type":"authorization","subtype":"missing_scope","code":99991679,"message":"missing scope"}}` + "\n"),
			Stderr:   []byte("lark-cli: command exited after producing its JSON envelope\n"),
		}
		_, _, err := ParseEnvelope(result)
		if detail := kernel.DetailFromError(err); detail.Type != kernel.ErrMissingScope {
			t.Fatalf("error=%v detail=%+v", err, detail)
		}
	})
}

func TestParseEnvelopeRejectsAmbiguousOrUnstructuredOutput(t *testing.T) {
	tests := []struct {
		name     string
		result   CommandResult
		wantType kernel.ErrorType
	}{
		{
			name:     "diagnostics only",
			result:   CommandResult{ExitCode: 0, Stdout: []byte("lark-cli: no structured output\n")},
			wantType: kernel.ErrParse,
		},
		{
			name: "multiple envelopes",
			result: CommandResult{ExitCode: 0, Stdout: []byte(
				`{"ok":true,"data":{"items":[]}}` + "\n" +
					`{"ok":true,"data":{"items":[{"id":"second"}]}}` + "\n",
			)},
			wantType: kernel.ErrParse,
		},
		{
			name: "envelopes on both failure streams",
			result: CommandResult{
				ExitCode: 3,
				Stdout:   []byte(`{"ok":false,"error":{"type":"authorization","subtype":"missing_scope"}}`),
				Stderr:   []byte(`{"ok":false,"error":{"type":"upstream","subtype":"timeout"}}`),
			},
			wantType: kernel.ErrParse,
		},
		{
			name: "success envelope with nonzero exit",
			result: CommandResult{
				ExitCode: 3,
				Stdout:   []byte(`{"ok":true,"data":{"items":[]}}`),
				Stderr:   []byte("lark-cli: process failed\n"),
			},
			wantType: kernel.ErrUpstreamPermanent,
		},
		{
			name: "raw compatibility payload mixed with diagnostics",
			result: CommandResult{ExitCode: 0, Stdout: []byte(
				"lark-cli: compatibility output\n" +
					`{"code":0,"data":{"items":[]}}` + "\n",
			)},
			wantType: kernel.ErrParse,
		},
		{
			name: "envelope embedded in a diagnostic line",
			result: CommandResult{ExitCode: 0, Stdout: []byte(
				`lark-cli: payload={"ok":true,"data":{"items":[]}}` + "\n",
			)},
			wantType: kernel.ErrParse,
		},
		{
			name: "malformed structured output before envelope",
			result: CommandResult{ExitCode: 0, Stdout: []byte(
				`{"ok":false` + "\n" +
					`{"ok":true,"data":{"items":[]}}` + "\n",
			)},
			wantType: kernel.ErrParse,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ParseEnvelope(tt.result)
			if err == nil {
				t.Fatal("expected structured-output error")
			}
			if detail := kernel.DetailFromError(err); detail.Type != tt.wantType {
				t.Fatalf("error=%v detail=%+v want=%s", err, detail, tt.wantType)
			}
		})
	}
}

func TestParseEnvelopeDoesNotEchoRawFailurePayload(t *testing.T) {
	secret := "sensitive-upstream-value"
	_, _, err := ParseEnvelope(CommandResult{ExitCode: 1, Stderr: []byte(`{"unexpected":"` + secret + `"}`)})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked upstream payload: %v", err)
	}
	if detail := kernel.DetailFromError(err); strings.Contains(detail.Message, secret) {
		t.Fatalf("detail leaked upstream payload: %+v", detail)
	}
}

func TestCLIErrorDoesNotEchoObjectURL(t *testing.T) {
	secret := "https://tenant.feishu.cn/base/private-object-token"
	payload := `{"ok":false,"error":{"type":"validation_error","subtype":"invalid_argument","message":"unsupported --doc input \"` + secret + `\""}}`
	_, _, err := ParseEnvelope(CommandResult{ExitCode: 1, Stderr: []byte(payload)})
	detail := kernel.DetailFromError(err)
	if detail.Type != kernel.ErrInvalidRequest || strings.Contains(detail.Error(), secret) || strings.Contains(detail.Hint, secret) {
		t.Fatalf("detail leaked object URL: %+v", detail)
	}
}
