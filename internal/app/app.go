package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/adapter/execprovider"
	"github.com/BlueSkyXN/Feishu-SuperSearch/adapter/larkcli"
	"github.com/BlueSkyXN/Feishu-SuperSearch/adapter/mock"
	openapiadapter "github.com/BlueSkyXN/Feishu-SuperSearch/adapter/openapi"
	"github.com/BlueSkyXN/Feishu-SuperSearch/adapter/replay"
	"github.com/BlueSkyXN/Feishu-SuperSearch/ai"
	demoadapter "github.com/BlueSkyXN/Feishu-SuperSearch/ai/demo"
	openaiadapter "github.com/BlueSkyXN/Feishu-SuperSearch/ai/openaicompat"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/engine"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/session"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
	rulesplanner "github.com/BlueSkyXN/Feishu-SuperSearch/planner/rules"
	"github.com/BlueSkyXN/Feishu-SuperSearch/provider"
)

type App struct {
	Config   Config
	Registry *provider.Registry
	Engine   *engine.Engine
	Planner  planner.Planner
	Reranker ai.Reranker
	Answerer ai.Answerer
	Backend  string
}

type providerRegistration struct {
	provider  kernel.Provider
	preferred bool
}

func Build(cfg Config) (*App, error) {
	reg := provider.NewRegistry()
	backend, err := cfg.ResolvedBackend()
	if err != nil {
		return nil, err
	}
	registrations := []providerRegistration{}
	add := func(p kernel.Provider, preferred bool) {
		if sourceEnabled(p.Descriptor().Source, cfg.Providers) {
			registrations = append(registrations, providerRegistration{provider: p, preferred: preferred})
		}
	}
	addOpenAPI := func(preferred bool) error {
		token := cfg.OpenAPIToken()
		if token == "" {
			return fmt.Errorf("direct OpenAPI backend requires a token; set %s or SFS_OPENAPI_TOKEN", cfg.OpenAPI.TokenEnv)
		}
		if strings.TrimSpace(cfg.OpenAPI.AppID) == "" {
			return fmt.Errorf("direct OpenAPI SDK operations require open_api.app_id or SFS_OPENAPI_APP_ID")
		}
		client, err := openapiadapter.NewClient(openapiadapter.ClientConfig{AppID: cfg.OpenAPI.AppID, BaseURL: cfg.OpenAPI.BaseURL, Token: token, Timeout: cfg.OpenAPITimeout(), MaxResponseBytes: cfg.OpenAPI.MaxResponseBytes})
		if err != nil {
			return err
		}
		add(openapiadapter.NewDocsProvider(client, cfg.OpenAPI.UserOpenID), preferred)
		add(openapiadapter.NewMessagesProvider(client), preferred)
		add(openapiadapter.NewPeopleProvider(client), preferred)
		add(openapiadapter.NewMinutesProvider(client), preferred)
		return nil
	}

	switch backend {
	case "mock":
		for _, p := range mock.NewProviders(mock.DemoDataset()) {
			add(p, true)
		}
	case "larkcli":
		for _, p := range larkcli.NewProviders(larkcli.ExecRunner{}, larkcli.Config{Executable: cfg.LarkCLI.Executable, ProfileArgs: cfg.LarkCLI.ProfileArgs, Timeout: cfg.LarkTimeout(), StdoutLimit: cfg.LarkCLI.MaxStdoutBytes, StderrLimit: cfg.LarkCLI.MaxStderrBytes}) {
			add(p, true)
		}
	case "hybrid":
		for _, p := range larkcli.NewProviders(larkcli.ExecRunner{}, larkcli.Config{Executable: cfg.LarkCLI.Executable, ProfileArgs: cfg.LarkCLI.ProfileArgs, Timeout: cfg.LarkTimeout(), StdoutLimit: cfg.LarkCLI.MaxStdoutBytes, StderrLimit: cfg.LarkCLI.MaxStderrBytes}) {
			add(p, true)
		}
		if err := addOpenAPI(true); err != nil {
			return nil, err
		}
	case "openapi":
		if err := addOpenAPI(true); err != nil {
			return nil, err
		}
	case "replay":
		store, err := replay.NewStore(cfg.Replay.Dir)
		if err != nil {
			return nil, err
		}
		descriptors := store.Descriptors()
		if len(descriptors) == 0 {
			return nil, fmt.Errorf("replay store %q has no provider manifest", cfg.Replay.Dir)
		}
		for _, descriptor := range descriptors {
			add(replay.NewProvider(descriptor, store), true)
		}
	default:
		return nil, fmt.Errorf("unknown backend %q", backend)
	}

	// In lark-cli/mock/hybrid modes, direct OpenAPI providers are additional
	// implementations. A per-operation route can opt into them without moving
	// Fetch or Expand away from the source's normal provider.
	if cfg.OpenAPI.Enabled && backend != "openapi" && backend != "hybrid" && backend != "replay" {
		if err := addOpenAPI(false); err != nil {
			return nil, err
		}
	}

	for _, x := range cfg.ExternalProviders {
		p, err := execprovider.New(x.ToConfig())
		if err != nil {
			return nil, err
		}
		add(p, x.Preferred)
	}

	if cfg.Replay.Mode == "record" && backend != "replay" {
		store, err := replay.NewStore(cfg.Replay.Dir)
		if err != nil {
			return nil, err
		}
		for i := range registrations {
			wrapped, err := replay.Wrap(registrations[i].provider, store)
			if err != nil {
				return nil, err
			}
			registrations[i].provider = wrapped
		}
	} else if cfg.Replay.Mode != "off" && cfg.Replay.Mode != "" && backend != "replay" {
		return nil, fmt.Errorf("unsupported replay mode %q", cfg.Replay.Mode)
	}

	for _, registration := range registrations {
		if err := reg.Register(registration.provider, registration.preferred); err != nil {
			return nil, err
		}
	}
	for source, operations := range cfg.Providers.Routes {
		for operation, providerID := range operations {
			if err := reg.SetPreferredForOperation(source, operation, providerID); err != nil {
				return nil, fmt.Errorf("provider route %s/%s -> %s: %w", source, operation, providerID, err)
			}
		}
	}
	if len(reg.All()) == 0 {
		return nil, fmt.Errorf("no providers are enabled")
	}

	var store session.Store
	switch cfg.Storage.Type {
	case "", "sqlite":
		sqliteStore, err := session.NewSQLiteStore(cfg.Storage.Path, cfg.SessionTTL())
		if err != nil {
			return nil, err
		}
		store = sqliteStore
	case "file":
		fileStore, err := session.NewFileStore(cfg.Storage.Path, cfg.SessionTTL())
		if err != nil {
			return nil, err
		}
		store = fileStore
	case "memory":
		store = session.NewMemoryStore(cfg.SessionTTL())
	default:
		return nil, fmt.Errorf("unsupported storage type %q", cfg.Storage.Type)
	}
	eng := engine.New(reg, store, engine.Config{GlobalConcurrency: cfg.Runtime.GlobalConcurrency, ProviderTimeout: cfg.ProviderTimeout(), SessionTTL: cfg.SessionTTL()})
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 8*time.Second)
	_, _ = eng.Capabilities(probeCtx, kernel.CapabilityRequest{Probe: true})
	probeCancel()
	var (
		pl       planner.Planner
		reranker ai.Reranker
		answerer ai.Answerer
	)
	plannerType := strings.ToLower(strings.TrimSpace(cfg.Planner.Type))
	if plannerType == "ai" || cfg.AI.Planner.Enabled {
		client, model, aiErr := buildAIClient(cfg, cfg.AI.Planner)
		if aiErr != nil {
			_ = eng.Close()
			return nil, aiErr
		}
		pl = openaiadapter.Planner{Client: client, Model: model, MaxOutputTokens: cfg.AI.Planner.MaxOutputTokens, Fallback: rulesplanner.Planner{}}
	} else {
		pl, err = BuildPlanner(cfg.Planner)
		if err != nil {
			_ = eng.Close()
			return nil, err
		}
	}
	if cfg.AI.Enabled && cfg.AI.Rerank.Enabled {
		client, model, aiErr := buildAIClient(cfg, cfg.AI.Rerank)
		if aiErr != nil {
			_ = eng.Close()
			return nil, aiErr
		}
		reranker = openaiadapter.Reranker{Client: client, Model: model, TopN: cfg.AI.Rerank.TopN, MaxOutputTokens: cfg.AI.Rerank.MaxOutputTokens}
	}
	if cfg.AI.Enabled && cfg.AI.Answer.Enabled {
		if cfg.AI.Provider == "demo" {
			if backend != "mock" {
				_ = eng.Close()
				return nil, fmt.Errorf("AI provider demo is only available with backend=mock")
			}
			answerer = demoadapter.Answerer{MaxEvidence: 4}
		} else {
			client, model, aiErr := buildAIClient(cfg, cfg.AI.Answer)
			if aiErr != nil {
				_ = eng.Close()
				return nil, aiErr
			}
			answerer = openaiadapter.Answerer{Client: client, Model: model, MaxOutputTokens: cfg.AI.Answer.MaxOutputTokens}
		}
	}
	return &App{Config: cfg, Registry: reg, Engine: eng, Planner: pl, Reranker: reranker, Answerer: answerer, Backend: backend}, nil
}

func buildAIClient(cfg Config, feature AIFeatureConfig) (*openaiadapter.Client, string, error) {
	if !cfg.AI.Enabled {
		return nil, "", &kernel.ErrorDetail{Type: kernel.ErrUnsupported, Message: "AI feature is enabled but ai.enabled is false"}
	}
	if cfg.AI.Provider != "openai-compatible" {
		return nil, "", fmt.Errorf("unsupported AI provider %q", cfg.AI.Provider)
	}
	model := cfg.AIModel(feature)
	if model == "" {
		return nil, "", &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "AI model is required for an enabled feature"}
	}
	client, err := openaiadapter.NewClient(openaiadapter.ClientConfig{BaseURL: cfg.AI.BaseURL, APIKey: cfg.AIAPIKey(), Timeout: cfg.AIFeatureTimeout(feature), MaxInputBytes: cfg.AI.MaxInputBytes, MaxResponseBytes: cfg.AI.MaxResponseBytes, JSONSchema: cfg.AI.JSONSchema})
	return client, model, err
}

func (a *App) ApplyDefaults(req kernel.SearchRequest) kernel.SearchRequest {
	strategyUnset := req.Strategy.Profile == "" && req.Strategy.Fusion == "" && req.Strategy.Pagination == "" && !req.Strategy.SourceQuota && !req.Strategy.DisableSourceQuota && req.Strategy.K0 == 0 && len(req.Strategy.Weights) == 0 && len(req.Strategy.Quotas) == 0
	if strategyUnset {
		req.Strategy = a.Config.Strategy
	}
	req = req.WithDefaults()
	if len(req.Strategy.Weights) == 0 {
		req.Strategy.Weights = map[kernel.SourceID]float64{}
		for k, v := range a.Config.Providers.Weights {
			req.Strategy.Weights[k] = v
		}
	}
	if len(req.Strategy.Quotas) == 0 {
		req.Strategy.Quotas = map[kernel.SourceID]int{}
		for k, v := range a.Config.Providers.Quotas {
			req.Strategy.Quotas[k] = v
		}
	}
	return req
}
func (a *App) Close() error { return a.Engine.Close() }
