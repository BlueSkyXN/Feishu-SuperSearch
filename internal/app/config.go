package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/adapter/execprovider"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type Config struct {
	Version           int                      `json:"version"`
	Backend           string                   `json:"backend"`
	Runtime           RuntimeConfig            `json:"runtime"`
	Storage           StorageConfig            `json:"storage,omitempty"`
	LarkCLI           LarkCLIConfig            `json:"lark_cli"`
	OpenAPI           OpenAPIConfig            `json:"open_api,omitempty"`
	AI                AIConfig                 `json:"ai,omitempty"`
	Providers         ProviderConfig           `json:"providers"`
	Planner           PlannerConfig            `json:"planner,omitempty"`
	ExternalProviders []ExternalProviderConfig `json:"external_providers,omitempty"`
	Replay            ReplayConfig             `json:"replay,omitempty"`
	Strategy          kernel.SearchStrategy    `json:"strategy"`
}

type RuntimeConfig struct {
	GlobalConcurrency int    `json:"global_concurrency"`
	ProviderTimeout   string `json:"provider_timeout"`
	SessionTTL        string `json:"session_ttl,omitempty"`
	SessionDir        string `json:"session_dir,omitempty"`
	Listen            string `json:"listen"`
}

type StorageConfig struct {
	Type string `json:"type"`
	Path string `json:"path,omitempty"`
	TTL  string `json:"ttl"`
}

type LarkCLIConfig struct {
	Executable     string   `json:"executable"`
	ProfileArgs    []string `json:"profile_args,omitempty"`
	Timeout        string   `json:"timeout"`
	MaxStdoutBytes int64    `json:"max_stdout_bytes"`
	MaxStderrBytes int64    `json:"max_stderr_bytes"`
}

type OpenAPIConfig struct {
	Enabled          bool   `json:"enabled"`
	AppID            string `json:"app_id,omitempty"`
	BaseURL          string `json:"base_url"`
	Token            string `json:"-"`
	TokenEnv         string `json:"token_env"`
	UserOpenID       string `json:"user_open_id,omitempty"`
	Timeout          string `json:"timeout"`
	MaxResponseBytes int64  `json:"max_response_bytes"`
}

type AIConfig struct {
	Enabled          bool            `json:"enabled"`
	Provider         string          `json:"provider"`
	BaseURL          string          `json:"base_url"`
	APIKeyEnv        string          `json:"api_key_env"`
	Model            string          `json:"model"`
	Timeout          string          `json:"timeout"`
	MaxInputBytes    int64           `json:"max_input_bytes"`
	MaxResponseBytes int64           `json:"max_response_bytes"`
	JSONSchema       bool            `json:"json_schema"`
	Planner          AIFeatureConfig `json:"planner,omitempty"`
	Rerank           AIFeatureConfig `json:"rerank,omitempty"`
	Answer           AIFeatureConfig `json:"answer,omitempty"`
}

type AIFeatureConfig struct {
	Enabled         bool   `json:"enabled"`
	Model           string `json:"model,omitempty"`
	Timeout         string `json:"timeout,omitempty"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
	TopN            int    `json:"top_n,omitempty"`
}

type ProviderConfig struct {
	Enabled  []kernel.SourceID                                          `json:"enabled,omitempty"`
	Disabled []kernel.SourceID                                          `json:"disabled,omitempty"`
	Weights  map[kernel.SourceID]float64                                `json:"weights,omitempty"`
	Quotas   map[kernel.SourceID]int                                    `json:"quotas,omitempty"`
	Routes   map[kernel.SourceID]map[kernel.Operation]kernel.ProviderID `json:"routes,omitempty"`
}

type PlannerConfig struct {
	Type           string   `json:"type"`
	Command        string   `json:"command,omitempty"`
	Args           []string `json:"args,omitempty"`
	Timeout        string   `json:"timeout,omitempty"`
	MaxStdoutBytes int64    `json:"max_stdout_bytes,omitempty"`
	MaxStderrBytes int64    `json:"max_stderr_bytes,omitempty"`
}

type ReplayConfig struct {
	Mode string `json:"mode,omitempty"` // off|record
	Dir  string `json:"dir,omitempty"`
}

type ExternalProviderConfig struct {
	Descriptor     kernel.ProviderDescriptor `json:"descriptor"`
	Command        string                    `json:"command"`
	Args           []string                  `json:"args,omitempty"`
	Timeout        string                    `json:"timeout,omitempty"`
	MaxStdoutBytes int64                     `json:"max_stdout_bytes,omitempty"`
	MaxStderrBytes int64                     `json:"max_stderr_bytes,omitempty"`
	Preferred      bool                      `json:"preferred,omitempty"`
}

func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Version:   2,
		Backend:   "auto",
		Runtime:   RuntimeConfig{GlobalConcurrency: 6, ProviderTimeout: "4s", Listen: "127.0.0.1:3765"},
		Storage:   StorageConfig{Type: "sqlite", Path: filepath.Join(home, ".sfs", "sfs.db"), TTL: "45m"},
		LarkCLI:   LarkCLIConfig{Executable: "lark-cli", Timeout: "8s", MaxStdoutBytes: 16 << 20, MaxStderrBytes: 4 << 20},
		OpenAPI:   OpenAPIConfig{BaseURL: "https://open.feishu.cn", TokenEnv: "FEISHU_USER_ACCESS_TOKEN", Timeout: "10s", MaxResponseBytes: 16 << 20},
		AI:        AIConfig{Provider: "openai-compatible", BaseURL: "https://api.openai.com/v1", APIKeyEnv: "SFS_AI_API_KEY", Timeout: "45s", MaxInputBytes: 4 << 20, MaxResponseBytes: 4 << 20, JSONSchema: true, Planner: AIFeatureConfig{MaxOutputTokens: 4096}, Rerank: AIFeatureConfig{MaxOutputTokens: 2048, TopN: 30}, Answer: AIFeatureConfig{MaxOutputTokens: 4096}},
		Providers: ProviderConfig{Weights: map[kernel.SourceID]float64{}, Quotas: map[kernel.SourceID]int{}, Routes: map[kernel.SourceID]map[kernel.Operation]kernel.ProviderID{}},
		Planner:   PlannerConfig{Type: "rules", Timeout: "30s", MaxStdoutBytes: 4 << 20, MaxStderrBytes: 1 << 20},
		Replay:    ReplayConfig{Mode: "off", Dir: filepath.Join(home, ".sfs", "replay")},
		Strategy:  kernel.SearchStrategy{Fusion: "weighted_rrf", Pagination: "adaptive", SourceQuota: true, K0: 60},
	}
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return cfg, err
		}
		var header struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(b, &header); err != nil {
			return cfg, fmt.Errorf("parse config: %w", err)
		}
		if header.Version == 0 {
			header.Version = 1
		}
		if header.Version != 1 && header.Version != 2 {
			return cfg, fmt.Errorf("unsupported config version %d", header.Version)
		}
		if header.Version == 1 {
			cfg.Storage = StorageConfig{}
			cfg.Version = 1
		}
		decoder := json.NewDecoder(bytes.NewReader(b))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&cfg); err != nil {
			return cfg, fmt.Errorf("parse config: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			if err == nil {
				return cfg, fmt.Errorf("parse config: multiple JSON values")
			}
			return cfg, fmt.Errorf("parse config: %w", err)
		}
		if header.Version == 1 {
			migrateV1Config(&cfg)
		}
	}
	applyEnv(&cfg)
	normalize(&cfg)
	return cfg, nil
}
func applyEnv(c *Config) {
	if v := os.Getenv("SFS_BACKEND"); v != "" {
		c.Backend = v
	}
	if v := os.Getenv("SFS_LARKCLI"); v != "" {
		c.LarkCLI.Executable = v
	}
	if v := os.Getenv("SFS_OPENAPI_ENABLED"); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			c.OpenAPI.Enabled = enabled
		}
	}
	if v := os.Getenv("SFS_OPENAPI_BASE_URL"); v != "" {
		c.OpenAPI.BaseURL = v
	}
	if v := os.Getenv("SFS_OPENAPI_APP_ID"); v != "" {
		c.OpenAPI.AppID = v
	}
	if v := os.Getenv("SFS_OPENAPI_TOKEN"); v != "" {
		c.OpenAPI.Token = v
		c.OpenAPI.Enabled = true
	}
	if v := os.Getenv("SFS_OPENAPI_TOKEN_ENV"); v != "" {
		c.OpenAPI.TokenEnv = v
	}
	if v := os.Getenv("SFS_OPENAPI_USER_OPEN_ID"); v != "" {
		c.OpenAPI.UserOpenID = v
	}
	if v := os.Getenv("SFS_OPENAPI_TIMEOUT"); v != "" {
		c.OpenAPI.Timeout = v
	}
	if v := os.Getenv("SFS_SESSION_DIR"); v != "" {
		c.Runtime.SessionDir = v
		c.Storage.Type = "file"
		c.Storage.Path = v
	}
	if v := os.Getenv("SFS_STORAGE_TYPE"); v != "" {
		c.Storage.Type = v
	}
	if v := os.Getenv("SFS_STORAGE_PATH"); v != "" {
		c.Storage.Path = v
	}
	if v := os.Getenv("SFS_STORAGE_TTL"); v != "" {
		c.Storage.TTL = v
	}
	if v := os.Getenv("SFS_LISTEN"); v != "" {
		c.Runtime.Listen = v
	}
	if v := os.Getenv("SFS_REPLAY_MODE"); v != "" {
		c.Replay.Mode = v
	}
	if v := os.Getenv("SFS_REPLAY_DIR"); v != "" {
		c.Replay.Dir = v
	}
	if v := os.Getenv("SFS_PLANNER"); v != "" {
		c.Planner.Type = v
	}
	if v := os.Getenv("SFS_PLANNER_COMMAND"); v != "" {
		c.Planner.Command = v
	}
	if v := os.Getenv("SFS_PLANNER_TIMEOUT"); v != "" {
		c.Planner.Timeout = v
	}
	if v := os.Getenv("SFS_AI_ENABLED"); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			c.AI.Enabled = enabled
		}
	}
	if v := os.Getenv("SFS_AI_PROVIDER"); v != "" {
		c.AI.Provider = v
	}
	if v := os.Getenv("SFS_AI_BASE_URL"); v != "" {
		c.AI.BaseURL = v
	}
	if v := os.Getenv("SFS_AI_API_KEY_ENV"); v != "" {
		c.AI.APIKeyEnv = v
	}
	if v := os.Getenv("SFS_AI_MODEL"); v != "" {
		c.AI.Model = v
	}
	if v := os.Getenv("SFS_CONCURRENCY"); v != "" {
		if n, e := strconv.Atoi(v); e == nil {
			c.Runtime.GlobalConcurrency = n
		}
	}
}
func normalize(c *Config) {
	if c.Version == 0 {
		c.Version = 2
	}
	if c.Version != 2 {
		return
	}
	if c.Backend == "" {
		c.Backend = "auto"
	}
	if c.Runtime.GlobalConcurrency <= 0 {
		c.Runtime.GlobalConcurrency = 6
	}
	if c.Runtime.ProviderTimeout == "" {
		c.Runtime.ProviderTimeout = "4s"
	}
	if c.Runtime.Listen == "" {
		c.Runtime.Listen = "127.0.0.1:3765"
	}
	if c.Storage.Type == "" {
		c.Storage.Type = "sqlite"
	}
	c.Storage.Type = strings.ToLower(strings.TrimSpace(c.Storage.Type))
	if c.Storage.TTL == "" {
		c.Storage.TTL = "45m"
	}
	c.Storage.Path = expandHome(c.Storage.Path)
	if c.Storage.Type == "sqlite" && c.Storage.Path == "" {
		if home, err := os.UserHomeDir(); err == nil {
			c.Storage.Path = filepath.Join(home, ".sfs", "sfs.db")
		}
	}
	if c.Storage.Type == "file" && c.Storage.Path == "" {
		c.Storage.Path = expandHome(c.Runtime.SessionDir)
	}
	c.Replay.Mode = strings.ToLower(strings.TrimSpace(c.Replay.Mode))
	if c.Replay.Mode == "" {
		c.Replay.Mode = "off"
	}
	if strings.HasPrefix(c.Replay.Dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			c.Replay.Dir = filepath.Join(home, strings.TrimPrefix(c.Replay.Dir, "~/"))
		}
	}
	if c.LarkCLI.Executable == "" {
		c.LarkCLI.Executable = "lark-cli"
	}
	if c.LarkCLI.Timeout == "" {
		c.LarkCLI.Timeout = "8s"
	}
	if c.LarkCLI.MaxStdoutBytes <= 0 {
		c.LarkCLI.MaxStdoutBytes = 16 << 20
	}
	if c.LarkCLI.MaxStderrBytes <= 0 {
		c.LarkCLI.MaxStderrBytes = 4 << 20
	}
	if c.OpenAPI.BaseURL == "" {
		c.OpenAPI.BaseURL = "https://open.feishu.cn"
	}
	if c.OpenAPI.TokenEnv == "" {
		c.OpenAPI.TokenEnv = "FEISHU_USER_ACCESS_TOKEN"
	}
	if c.OpenAPI.Timeout == "" {
		c.OpenAPI.Timeout = "10s"
	}
	if c.OpenAPI.MaxResponseBytes <= 0 {
		c.OpenAPI.MaxResponseBytes = 16 << 20
	}
	if c.OpenAPIToken() != "" {
		c.OpenAPI.Enabled = true
	}
	c.Strategy = c.Strategy.WithDefaults()
	if c.Providers.Weights == nil {
		c.Providers.Weights = map[kernel.SourceID]float64{}
	}
	if c.Providers.Quotas == nil {
		c.Providers.Quotas = map[kernel.SourceID]int{}
	}
	if c.Providers.Routes == nil {
		c.Providers.Routes = map[kernel.SourceID]map[kernel.Operation]kernel.ProviderID{}
	}
	c.Planner.Type = strings.ToLower(strings.TrimSpace(c.Planner.Type))
	if c.Planner.Type == "" {
		c.Planner.Type = "rules"
	}
	if c.Planner.Timeout == "" {
		c.Planner.Timeout = "30s"
	}
	if c.Planner.MaxStdoutBytes <= 0 {
		c.Planner.MaxStdoutBytes = 4 << 20
	}
	if c.Planner.MaxStderrBytes <= 0 {
		c.Planner.MaxStderrBytes = 1 << 20
	}
	c.AI.Provider = strings.ToLower(strings.TrimSpace(c.AI.Provider))
	if c.AI.Provider == "" {
		c.AI.Provider = "openai-compatible"
	}
	if c.AI.BaseURL == "" {
		c.AI.BaseURL = "https://api.openai.com/v1"
	}
	c.AI.BaseURL = strings.TrimRight(strings.TrimSpace(c.AI.BaseURL), "/")
	if c.AI.APIKeyEnv == "" {
		c.AI.APIKeyEnv = "SFS_AI_API_KEY"
	}
	if c.AI.Timeout == "" {
		c.AI.Timeout = "45s"
	}
	if c.AI.MaxInputBytes <= 0 {
		c.AI.MaxInputBytes = 4 << 20
	}
	if c.AI.MaxResponseBytes <= 0 {
		c.AI.MaxResponseBytes = 4 << 20
	}
	normalizeAIFeature(&c.AI.Planner, 4096, 0)
	normalizeAIFeature(&c.AI.Rerank, 2048, 30)
	normalizeAIFeature(&c.AI.Answer, 4096, 0)
}

func migrateV1Config(c *Config) {
	c.Version = 2
	c.Storage.TTL = c.Runtime.SessionTTL
	if c.Storage.TTL == "" {
		c.Storage.TTL = "45m"
	}
	if strings.TrimSpace(c.Runtime.SessionDir) == "" {
		c.Storage.Type = "memory"
		return
	}
	c.Storage.Type = "file"
	c.Storage.Path = c.Runtime.SessionDir
}

func normalizeAIFeature(feature *AIFeatureConfig, maxOutputTokens, topN int) {
	if feature.MaxOutputTokens <= 0 {
		feature.MaxOutputTokens = maxOutputTokens
	}
	if feature.TopN <= 0 && topN > 0 {
		feature.TopN = topN
	}
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
func (c Config) ProviderTimeout() time.Duration {
	d, e := time.ParseDuration(c.Runtime.ProviderTimeout)
	if e != nil {
		return 4 * time.Second
	}
	return d
}
func (c Config) SessionTTL() time.Duration {
	d, e := time.ParseDuration(c.Storage.TTL)
	if e != nil {
		return 45 * time.Minute
	}
	return d
}
func (c Config) LarkTimeout() time.Duration {
	d, e := time.ParseDuration(c.LarkCLI.Timeout)
	if e != nil {
		return 8 * time.Second
	}
	return d
}
func (c Config) OpenAPITimeout() time.Duration {
	d, e := time.ParseDuration(c.OpenAPI.Timeout)
	if e != nil {
		return 10 * time.Second
	}
	return d
}
func (c Config) PlannerTimeout() time.Duration {
	d, e := time.ParseDuration(c.Planner.Timeout)
	if e != nil {
		return 30 * time.Second
	}
	return d
}

func (c Config) AITimeout() time.Duration {
	d, err := time.ParseDuration(c.AI.Timeout)
	if err != nil || d <= 0 {
		return 45 * time.Second
	}
	return d
}

func (c Config) AIFeatureTimeout(feature AIFeatureConfig) time.Duration {
	if feature.Timeout != "" {
		if d, err := time.ParseDuration(feature.Timeout); err == nil && d > 0 {
			return d
		}
	}
	return c.AITimeout()
}

func (c Config) AIAPIKey() string {
	if name := strings.TrimSpace(c.AI.APIKeyEnv); name != "" {
		return strings.TrimSpace(os.Getenv(name))
	}
	return ""
}

func (c Config) AIModel(feature AIFeatureConfig) string {
	if model := strings.TrimSpace(feature.Model); model != "" {
		return model
	}
	return strings.TrimSpace(c.AI.Model)
}
func (c Config) OpenAPIToken() string {
	if token := strings.TrimSpace(c.OpenAPI.Token); token != "" {
		return token
	}
	if name := strings.TrimSpace(c.OpenAPI.TokenEnv); name != "" {
		return strings.TrimSpace(os.Getenv(name))
	}
	return ""
}
func (c Config) ResolvedBackend() (string, error) {
	b := strings.ToLower(strings.TrimSpace(c.Backend))
	if b != "auto" {
		return b, nil
	}
	larkAvailable := false
	if path, err := exec.LookPath(c.LarkCLI.Executable); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := exec.CommandContext(ctx, path, "--version").Run(); err == nil {
			larkAvailable = true
		}
		cancel()
	}
	openAPIAvailable := c.OpenAPI.Enabled && c.OpenAPIToken() != "" && strings.TrimSpace(c.OpenAPI.AppID) != ""
	switch {
	case larkAvailable && openAPIAvailable:
		return "hybrid", nil
	case larkAvailable:
		return "larkcli", nil
	case openAPIAvailable:
		return "openapi", nil
	}
	return "", fmt.Errorf("backend=auto found no usable real backend; install a compatible lark-cli, configure direct OpenAPI, or select --backend mock explicitly for offline demos")
}
func (x ExternalProviderConfig) ToConfig() execprovider.Config {
	d := 15 * time.Second
	if x.Timeout != "" {
		if parsed, e := time.ParseDuration(x.Timeout); e == nil {
			d = parsed
		}
	}
	return execprovider.Config{Descriptor: x.Descriptor, Command: x.Command, Args: x.Args, Timeout: d, MaxStdoutBytes: x.MaxStdoutBytes, MaxStderrBytes: x.MaxStderrBytes}
}
func sourceEnabled(source kernel.SourceID, c ProviderConfig) bool {
	for _, s := range c.Disabled {
		if s == source {
			return false
		}
	}
	if len(c.Enabled) == 0 {
		return true
	}
	for _, s := range c.Enabled {
		if s == source {
			return true
		}
	}
	return false
}
