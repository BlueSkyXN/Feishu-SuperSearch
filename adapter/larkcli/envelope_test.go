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
