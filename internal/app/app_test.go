package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestAutoBackendFailsClosedWithoutRealBackend(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Storage = StorageConfig{Type: "memory", TTL: "45m"}
	cfg.Backend = "auto"
	cfg.LarkCLI.Executable = "sfs-test-lark-cli-does-not-exist"
	cfg.OpenAPI.Enabled = false
	cfg.OpenAPI.Token = ""
	cfg.OpenAPI.TokenEnv = "SFS_TEST_OPENAPI_TOKEN"
	t.Setenv("SFS_TEST_OPENAPI_TOKEN", "")

	_, err := Build(cfg)
	if err == nil || !strings.Contains(err.Error(), "select --backend mock explicitly") {
		t.Fatalf("expected fail-closed auto backend error, got %v", err)
	}
}

func TestAutoBackendSelectsHybridWhenBothBackendsExist(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX executable")
	}
	executable, err := exec.LookPath("true")
	if err != nil {
		t.Skip("true executable is unavailable")
	}
	cfg := DefaultConfig()
	cfg.Backend = "auto"
	cfg.LarkCLI.Executable = executable
	cfg.OpenAPI.Enabled = true
	cfg.OpenAPI.AppID = "cli_test"
	cfg.OpenAPI.Token = "test-token"
	backend, err := cfg.ResolvedBackend()
	if err != nil || backend != "hybrid" {
		t.Fatalf("backend=%q err=%v", backend, err)
	}
}

func TestBuildRoutesDocsSearchToOpenAPIAndKeepsFetchOnMock(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Backend = "mock"
	cfg.Storage = StorageConfig{Type: "memory", TTL: "45m"}
	cfg.OpenAPI.Enabled = true
	cfg.OpenAPI.AppID = "cli_test"
	cfg.OpenAPI.Token = "test-token"
	cfg.OpenAPI.BaseURL = "http://127.0.0.1:1"
	cfg.Providers.Routes = map[kernel.SourceID]map[kernel.Operation]kernel.ProviderID{
		kernel.SourceDocs: {kernel.OpSearch: "openapi.docs"},
	}
	a, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	search, ok := a.Registry.ForOperation(kernel.SourceDocs, kernel.OpSearch)
	if !ok || search.Descriptor().ID != "openapi.docs" {
		t.Fatalf("search provider=%v ok=%v", search, ok)
	}
	fetch, ok := a.Registry.ForOperation(kernel.SourceDocs, kernel.OpFetch)
	if !ok || fetch.Descriptor().ID != "mock.docs" {
		t.Fatalf("fetch provider=%v ok=%v", fetch, ok)
	}
}

func TestOpenAPIBackendRequiresToken(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Backend = "openapi"
	cfg.Storage = StorageConfig{Type: "memory", TTL: "45m"}
	cfg.OpenAPI.AppID = "cli_test"
	cfg.OpenAPI.Token = ""
	cfg.OpenAPI.TokenEnv = "SFS_TEST_MISSING_TOKEN"
	t.Setenv("SFS_TEST_MISSING_TOKEN", "")
	if _, err := Build(cfg); err == nil {
		t.Fatal("expected missing token error")
	}
	cfg.OpenAPI.Token = "test-token"
	cfg.OpenAPI.AppID = ""
	if _, err := Build(cfg); err == nil || !strings.Contains(err.Error(), "app_id") {
		t.Fatalf("expected missing app_id error, got %v", err)
	}
}

func TestBuildPlannerKinds(t *testing.T) {
	for _, typ := range []string{"rules", "default"} {
		p, err := BuildPlanner(PlannerConfig{Type: typ})
		if err != nil || p.Name() != typ {
			t.Fatalf("type=%s planner=%v err=%v", typ, p, err)
		}
	}
	if _, err := BuildPlanner(PlannerConfig{Type: "command"}); err == nil {
		t.Fatal("command planner without executable must fail")
	}
}

func TestLoadConfigOpenAPIEnvironment(t *testing.T) {
	t.Setenv("SFS_OPENAPI_APP_ID", "cli_test")
	t.Setenv("SFS_OPENAPI_TOKEN", "env-token")
	t.Setenv("SFS_OPENAPI_USER_OPEN_ID", "ou_me")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.OpenAPI.Enabled || cfg.OpenAPI.AppID != "cli_test" || cfg.OpenAPIToken() != "env-token" || cfg.OpenAPI.UserOpenID != "ou_me" {
		t.Fatalf("openapi config=%+v", cfg.OpenAPI)
	}
}

func TestLoadConfigMigratesV1FileStorage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	legacySessions := filepath.Join(dir, "sessions")
	payload := `{"version":1,"backend":"mock","runtime":{"session_ttl":"2h","session_dir":` + strconvQuote(legacySessions) + `}}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != 2 || cfg.Storage.Type != "file" || cfg.Storage.Path != legacySessions || cfg.Storage.TTL != "2h" {
		t.Fatalf("migrated config=%+v", cfg)
	}
}

func TestLoadConfigRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "unsupported config version 99") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfigRejectsUnknownFieldsAndInlineToken(t *testing.T) {
	for _, payload := range []string{
		`{"version":2,"unknown":true}`,
		`{"version":2,"open_api":{"token":"must-not-be-stored"}}`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatalf("expected strict config error for %s", payload)
		}
	}
}

func TestOpenAPITokenIsNeverSerialized(t *testing.T) {
	cfg := DefaultConfig()
	cfg.OpenAPI.Token = "secret-value"
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-value") || strings.Contains(string(raw), `"token"`) {
		t.Fatalf("token leaked into config JSON: %s", raw)
	}
}

func TestLoadConfigAIEnvironmentDoesNotStoreSecret(t *testing.T) {
	t.Setenv("SFS_AI_ENABLED", "true")
	t.Setenv("SFS_AI_MODEL", "test-model")
	t.Setenv("SFS_AI_API_KEY", "secret-value")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AI.Enabled || cfg.AI.Model != "test-model" || cfg.AIAPIKey() != "secret-value" {
		t.Fatalf("ai config=%+v", cfg.AI)
	}
	if strings.Contains(fmt.Sprintf("%+v", cfg), "secret-value") {
		t.Fatal("API key leaked into Config")
	}
}

func TestNormalizeAndConfigAccessors(t *testing.T) {
	cfg := Config{Version: 2, Storage: StorageConfig{Type: " FILE "}, Runtime: RuntimeConfig{SessionDir: "~/sessions"}, Replay: ReplayConfig{Dir: "~/replay"}}
	normalize(&cfg)
	if cfg.Backend != "auto" || cfg.Runtime.GlobalConcurrency != 6 || cfg.Runtime.ProviderTimeout != "4s" || cfg.Runtime.Listen == "" {
		t.Fatalf("runtime defaults=%+v", cfg.Runtime)
	}
	if cfg.Storage.Type != "file" || cfg.Storage.TTL != "45m" || !strings.HasSuffix(cfg.Storage.Path, "sessions") {
		t.Fatalf("storage defaults=%+v", cfg.Storage)
	}
	if cfg.Replay.Mode != "off" || !strings.HasSuffix(cfg.Replay.Dir, "replay") || cfg.LarkCLI.Executable != "lark-cli" {
		t.Fatalf("replay/lark defaults replay=%+v lark=%+v", cfg.Replay, cfg.LarkCLI)
	}
	if cfg.OpenAPI.BaseURL == "" || cfg.OpenAPI.TokenEnv == "" || cfg.Planner.Type != "rules" || cfg.AI.Provider != "openai-compatible" {
		t.Fatalf("service defaults openapi=%+v planner=%+v ai=%+v", cfg.OpenAPI, cfg.Planner, cfg.AI)
	}
	if cfg.AI.Planner.MaxOutputTokens != 4096 || cfg.AI.Rerank.MaxOutputTokens != 2048 || cfg.AI.Rerank.TopN != 30 || cfg.AI.Answer.MaxOutputTokens != 4096 {
		t.Fatalf("AI feature defaults=%+v", cfg.AI)
	}
	if cfg.Providers.Weights == nil || cfg.Providers.Quotas == nil || cfg.Providers.Routes == nil {
		t.Fatalf("provider maps not initialized: %+v", cfg.Providers)
	}

	cfg.Runtime.ProviderTimeout = "250ms"
	cfg.Storage.TTL = "2h"
	cfg.LarkCLI.Timeout = "3s"
	cfg.OpenAPI.Timeout = "4s"
	cfg.Planner.Timeout = "5s"
	cfg.AI.Timeout = "6s"
	if cfg.ProviderTimeout() != 250*time.Millisecond || cfg.SessionTTL() != 2*time.Hour || cfg.LarkTimeout() != 3*time.Second || cfg.OpenAPITimeout() != 4*time.Second || cfg.PlannerTimeout() != 5*time.Second || cfg.AITimeout() != 6*time.Second {
		t.Fatal("valid duration parsing mismatch")
	}
	cfg.Runtime.ProviderTimeout = "bad"
	cfg.Storage.TTL = "bad"
	cfg.LarkCLI.Timeout = "bad"
	cfg.OpenAPI.Timeout = "bad"
	cfg.Planner.Timeout = "bad"
	cfg.AI.Timeout = "0s"
	if cfg.ProviderTimeout() != 4*time.Second || cfg.SessionTTL() != 45*time.Minute || cfg.LarkTimeout() != 8*time.Second || cfg.OpenAPITimeout() != 10*time.Second || cfg.PlannerTimeout() != 30*time.Second || cfg.AITimeout() != 45*time.Second {
		t.Fatal("duration fallbacks mismatch")
	}
	if cfg.AIFeatureTimeout(AIFeatureConfig{Timeout: "7s"}) != 7*time.Second || cfg.AIFeatureTimeout(AIFeatureConfig{Timeout: "bad"}) != 45*time.Second {
		t.Fatal("feature timeout mismatch")
	}
}

func TestConfigSecretsModelsAndEnvironmentOverrides(t *testing.T) {
	cfg := DefaultConfig()
	t.Setenv("TEST_AI_KEY", " ai-secret ")
	t.Setenv("TEST_OPENAPI_TOKEN", " openapi-secret ")
	cfg.AI.APIKeyEnv = " TEST_AI_KEY "
	cfg.AI.Model = "base-model"
	cfg.OpenAPI.TokenEnv = " TEST_OPENAPI_TOKEN "
	if cfg.AIAPIKey() != "ai-secret" || cfg.AIModel(AIFeatureConfig{Model: " feature-model "}) != "feature-model" || cfg.AIModel(AIFeatureConfig{}) != "base-model" {
		t.Fatalf("AI accessors key=%q model=%q", cfg.AIAPIKey(), cfg.AIModel(AIFeatureConfig{}))
	}
	if cfg.OpenAPIToken() != "openapi-secret" {
		t.Fatalf("environment token=%q", cfg.OpenAPIToken())
	}
	cfg.OpenAPI.Token = " direct-token "
	if cfg.OpenAPIToken() != "direct-token" {
		t.Fatalf("direct token=%q", cfg.OpenAPIToken())
	}
	cfg.AI.APIKeyEnv = ""
	cfg.OpenAPI.Token = ""
	cfg.OpenAPI.TokenEnv = ""
	if cfg.AIAPIKey() != "" || cfg.OpenAPIToken() != "" {
		t.Fatal("blank secret env names returned values")
	}

	t.Setenv("SFS_BACKEND", "mock")
	t.Setenv("SFS_LARKCLI", "test-lark")
	t.Setenv("SFS_OPENAPI_ENABLED", "true")
	t.Setenv("SFS_OPENAPI_BASE_URL", "http://openapi.test")
	t.Setenv("SFS_OPENAPI_APP_ID", "cli_test")
	t.Setenv("SFS_OPENAPI_TOKEN", "test-token")
	t.Setenv("SFS_OPENAPI_TOKEN_ENV", "TEST_TOKEN")
	t.Setenv("SFS_OPENAPI_USER_OPEN_ID", "ou_test")
	t.Setenv("SFS_OPENAPI_TIMEOUT", "11s")
	t.Setenv("SFS_SESSION_DIR", "/tmp/sfs-test-sessions")
	t.Setenv("SFS_STORAGE_TYPE", "memory")
	t.Setenv("SFS_STORAGE_PATH", "/tmp/sfs-test.db")
	t.Setenv("SFS_STORAGE_TTL", "12m")
	t.Setenv("SFS_LISTEN", "127.0.0.1:9999")
	t.Setenv("SFS_REPLAY_MODE", "record")
	t.Setenv("SFS_REPLAY_DIR", "/tmp/sfs-test-replay")
	t.Setenv("SFS_PLANNER", "default")
	t.Setenv("SFS_PLANNER_COMMAND", "planner")
	t.Setenv("SFS_PLANNER_TIMEOUT", "13s")
	t.Setenv("SFS_AI_ENABLED", "true")
	t.Setenv("SFS_AI_BASE_URL", "http://ai.test/v1")
	t.Setenv("SFS_AI_API_KEY_ENV", "TEST_AI_KEY")
	t.Setenv("SFS_AI_MODEL", "env-model")
	t.Setenv("SFS_CONCURRENCY", "9")
	overridden := DefaultConfig()
	applyEnv(&overridden)
	if overridden.Backend != "mock" || overridden.LarkCLI.Executable != "test-lark" || !overridden.OpenAPI.Enabled || overridden.OpenAPI.AppID != "cli_test" || overridden.OpenAPI.Token != "test-token" || overridden.OpenAPI.UserOpenID != "ou_test" {
		t.Fatalf("backend overrides=%+v", overridden)
	}
	if overridden.Storage.Type != "memory" || overridden.Storage.Path != "/tmp/sfs-test.db" || overridden.Storage.TTL != "12m" || overridden.Runtime.GlobalConcurrency != 9 {
		t.Fatalf("storage/runtime overrides=%+v %+v", overridden.Storage, overridden.Runtime)
	}
	if overridden.Replay.Mode != "record" || overridden.Planner.Type != "default" || overridden.AI.Model != "env-model" || !overridden.AI.Enabled {
		t.Fatalf("feature overrides replay=%+v planner=%+v ai=%+v", overridden.Replay, overridden.Planner, overridden.AI)
	}
}

func TestApplyDefaultsPlannerTimeoutAndExternalProviderConfig(t *testing.T) {
	a := &App{Config: Config{
		Strategy:  kernel.SearchStrategy{Profile: "fast", Fusion: "weighted_rrf", Pagination: "adaptive", SourceQuota: true, K0: 25},
		Providers: ProviderConfig{Weights: map[kernel.SourceID]float64{kernel.SourceDocs: 2}, Quotas: map[kernel.SourceID]int{kernel.SourceDocs: 3}},
	}}
	request := a.ApplyDefaults(kernel.SearchRequest{Query: "q"})
	if request.Strategy.Profile != "fast" || request.Strategy.Weights[kernel.SourceDocs] != 2 || request.Strategy.Quotas[kernel.SourceDocs] != 3 {
		t.Fatalf("defaulted request=%+v", request)
	}
	request.Strategy.Weights[kernel.SourceDocs] = 99
	if a.Config.Providers.Weights[kernel.SourceDocs] != 2 {
		t.Fatal("request weights alias app config")
	}
	explicit := a.ApplyDefaults(kernel.SearchRequest{Query: "q", Strategy: kernel.SearchStrategy{Profile: "deep", Fusion: "weighted_rrf", Pagination: "fixed", Weights: map[kernel.SourceID]float64{kernel.SourceTasks: 4}, Quotas: map[kernel.SourceID]int{kernel.SourceTasks: 1}}})
	if explicit.Strategy.Profile != "deep" || explicit.Strategy.Weights[kernel.SourceTasks] != 4 {
		t.Fatalf("explicit strategy overwritten: %+v", explicit.Strategy)
	}

	if parsePlannerTimeout("") != 30*time.Second || parsePlannerTimeout("2s") != 2*time.Second || parsePlannerTimeout("bad") != 30*time.Second || parsePlannerTimeout("0s") != 30*time.Second {
		t.Fatal("planner timeout parsing mismatch")
	}
	external := ExternalProviderConfig{Descriptor: kernel.ProviderDescriptor{ID: "external"}, Command: "provider", Args: []string{"--stdio"}, Timeout: "2s", MaxStdoutBytes: 10, MaxStderrBytes: 20}.ToConfig()
	if external.Timeout != 2*time.Second || external.Command != "provider" || len(external.Args) != 1 || external.MaxStdoutBytes != 10 || external.MaxStderrBytes != 20 {
		t.Fatalf("external config=%+v", external)
	}
	if got := (ExternalProviderConfig{Timeout: "bad"}).ToConfig().Timeout; got != 15*time.Second {
		t.Fatalf("invalid external timeout=%s", got)
	}
	providerCfg := ProviderConfig{Enabled: []kernel.SourceID{kernel.SourceDocs, kernel.SourceTasks}, Disabled: []kernel.SourceID{kernel.SourceTasks}}
	if !sourceEnabled(kernel.SourceDocs, providerCfg) || sourceEnabled(kernel.SourceTasks, providerCfg) || sourceEnabled(kernel.SourceMessages, providerCfg) || !sourceEnabled(kernel.SourceMessages, ProviderConfig{}) {
		t.Fatal("source enablement mismatch")
	}
}

func TestBuildAIClientValidation(t *testing.T) {
	cfg := DefaultConfig()
	if _, _, err := buildAIClient(cfg, AIFeatureConfig{Enabled: true}); err == nil {
		t.Fatal("disabled AI accepted")
	}
	cfg.AI.Enabled = true
	cfg.AI.Provider = "unsupported"
	if _, _, err := buildAIClient(cfg, AIFeatureConfig{Enabled: true}); err == nil {
		t.Fatal("unsupported AI provider accepted")
	}
	cfg.AI.Provider = "openai-compatible"
	cfg.AI.Model = ""
	if _, _, err := buildAIClient(cfg, AIFeatureConfig{Enabled: true}); err == nil {
		t.Fatal("missing AI model accepted")
	}
	cfg.AI.Model = "test-model"
	t.Setenv("TEST_BUILD_AI_KEY", "test-key")
	cfg.AI.APIKeyEnv = "TEST_BUILD_AI_KEY"
	client, model, err := buildAIClient(cfg, AIFeatureConfig{Enabled: true, Timeout: "2s"})
	if err != nil || client == nil || model != "test-model" {
		t.Fatalf("AI client=%v model=%q err=%v", client, model, err)
	}
}

func TestBuildDemoAnswererOnlyWithMockBackend(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Backend = "mock"
	cfg.Storage.Type = "memory"
	cfg.AI.Enabled = true
	cfg.AI.Provider = "demo"
	cfg.AI.Answer.Enabled = true
	app, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if app.Answerer == nil || app.Answerer.Name() != "demo" {
		t.Fatalf("answerer=%v", app.Answerer)
	}

	cfg.Backend = "larkcli"
	if _, err := Build(cfg); err == nil || !strings.Contains(err.Error(), "only available with backend=mock") {
		t.Fatalf("expected demo backend guard, got %v", err)
	}
}

func TestLoadConfigRejectsReadParseAndMultipleValueErrors(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("missing config file accepted")
	}
	for _, payload := range []string{`{`, `{"version":2} {}`, `{"version":2} trailing`} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "parse config") {
			t.Fatalf("payload=%q error=%v", payload, err)
		}
	}
	legacy := Config{Version: 1}
	migrateV1Config(&legacy)
	if legacy.Version != 2 || legacy.Storage.Type != "memory" || legacy.Storage.TTL != "45m" {
		t.Fatalf("legacy memory migration=%+v", legacy)
	}
}

func strconvQuote(value string) string {
	b, _ := json.Marshal(value)
	return string(b)
}
