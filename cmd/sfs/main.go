package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	appcore "github.com/BlueSkyXN/Feishu-SuperSearch/internal/app"
	askcore "github.com/BlueSkyXN/Feishu-SuperSearch/internal/ask"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/planvalidate"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/research"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/session"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
	clicore "github.com/BlueSkyXN/Feishu-SuperSearch/transport/cli"
	"github.com/BlueSkyXN/Feishu-SuperSearch/transport/httpapi"
	"github.com/BlueSkyXN/Feishu-SuperSearch/transport/mcp"
)

var (
	version = "1.0.3"
	commit  = "dev"
	builtAt = "unknown"
)

type globals struct {
	configPath string
	backend    string
	sessionDir string
	database   string
	output     string
	profile    string
	identity   string
	listen     string
	recordDir  string
	replayDir  string
}

type commandError struct {
	err  error
	code int
}

func (e commandError) Error() string { return e.err.Error() }
func (e commandError) Unwrap() error { return e.err }

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		detail := kernel.DetailFromError(err)
		_ = clicore.PrintJSON(os.Stderr, map[string]any{"ok": false, "error": detail}, true)
		var ce commandError
		if errors.As(err, &ce) {
			os.Exit(ce.code)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	gfs := flag.NewFlagSet("sfs", flag.ContinueOnError)
	gfs.SetOutput(stderr)
	g := globals{}
	gfs.StringVar(&g.configPath, "config", "", "配置文件路径")
	gfs.StringVar(&g.backend, "backend", "", "auto|larkcli|openapi|mock|hybrid|replay")
	gfs.StringVar(&g.sessionDir, "session-dir", "", "会话持久化目录；空字符串使用内存")
	gfs.StringVar(&g.database, "database", "", "SQLite 数据库路径")
	gfs.StringVar(&g.output, "output", "pretty", "pretty|json|ndjson")
	gfs.StringVar(&g.profile, "profile", "", "lark-cli profile")
	gfs.StringVar(&g.identity, "as", "auto", "auto|user|bot")
	gfs.StringVar(&g.listen, "listen", "", "HTTP 监听地址")
	gfs.StringVar(&g.recordDir, "record-dir", "", "记录 Provider 交互到目录")
	gfs.StringVar(&g.replayDir, "replay-dir", "", "从目录离线回放 Provider 交互")
	gfs.Usage = func() { rootUsage(stderr) }
	if err := gfs.Parse(args); err != nil {
		return commandError{err: err, code: 2}
	}
	rest := gfs.Args()
	if len(rest) == 0 {
		rootUsage(stderr)
		return commandError{err: fmt.Errorf("缺少命令"), code: 2}
	}
	cmd := rest[0]
	cmdArgs := rest[1:]
	if cmd == "version" {
		fmt.Fprintf(stdout, "sfs %s commit=%s built=%s\n", version, commit, builtAt)
		return nil
	}
	if cmd == "help" {
		rootUsage(stdout)
		return nil
	}

	cfg, err := appcore.LoadConfig(g.configPath)
	if err != nil {
		return err
	}
	if g.backend != "" {
		cfg.Backend = g.backend
	}
	if g.sessionDir != "" {
		cfg.Runtime.SessionDir = g.sessionDir
		cfg.Storage = appcore.StorageConfig{Type: "file", Path: g.sessionDir, TTL: cfg.Storage.TTL}
	}
	if g.database != "" {
		cfg.Storage = appcore.StorageConfig{Type: "sqlite", Path: g.database, TTL: cfg.Storage.TTL}
	}
	if g.listen != "" {
		cfg.Runtime.Listen = g.listen
	}
	if g.recordDir != "" && g.replayDir != "" {
		return commandError{err: fmt.Errorf("--record-dir and --replay-dir are mutually exclusive"), code: 2}
	}
	if g.recordDir != "" {
		cfg.Replay.Mode = "record"
		cfg.Replay.Dir = g.recordDir
	}
	if g.replayDir != "" {
		cfg.Backend = "replay"
		cfg.Replay.Mode = "off"
		cfg.Replay.Dir = g.replayDir
	}
	if cmd == "migrate" {
		return cmdMigrate(ctx, cfg, g.output, cmdArgs, stdout, stderr)
	}
	app, err := appcore.Build(cfg)
	if err != nil {
		return err
	}
	defer app.Close()
	identity := clicore.ParseIdentity(g.profile, g.identity)

	switch cmd {
	case "providers":
		return cmdProviders(ctx, app, identity, g.output, stdout, false)
	case "doctor":
		return cmdProviders(ctx, app, identity, g.output, stdout, true)
	case "search":
		return cmdSearch(ctx, app, identity, g.output, cmdArgs, stdout, stderr)
	case "continue":
		return cmdContinue(ctx, app, identity, g.output, cmdArgs, stdout, stderr)
	case "query":
		return cmdQuery(ctx, app, identity, g.output, cmdArgs, stdout, stderr)
	case "fetch":
		return cmdFetch(ctx, app, identity, g.output, cmdArgs, stdout, stderr)
	case "expand":
		return cmdExpand(ctx, app, identity, g.output, cmdArgs, stdout, stderr)
	case "resolve":
		return cmdResolve(ctx, app, identity, g.output, cmdArgs, stdout, stderr)
	case "plan":
		return cmdPlan(ctx, app, g.output, cmdArgs, stdout, stderr)
	case "research":
		return cmdResearch(ctx, app, identity, g.output, cmdArgs, stdout, stderr)
	case "ask":
		return cmdAsk(ctx, app, identity, g.output, cmdArgs, stdout, stderr)
	case "sessions":
		return cmdSessions(ctx, app, identity, g.output, cmdArgs, stdout, stderr)
	case "serve":
		return cmdServe(ctx, app, cmdArgs, stdout, stderr)
	case "mcp":
		return mcp.New(app, mcp.WithVersion(version)).Serve(ctx, os.Stdin, stdout)
	default:
		rootUsage(stderr)
		return commandError{err: fmt.Errorf("未知命令 %q", cmd), code: 2}
	}
}

func rootUsage(w io.Writer) {
	fmt.Fprintf(w, `SuperFeishuSearch %s

用法：
  sfs [全局参数] <命令> [参数]

全局参数必须放在命令之前：
  --config PATH          JSON 配置文件
  --backend NAME         auto|larkcli|openapi|mock|hybrid|replay
  --session-dir PATH     旧版 JSON Session 目录
  --database PATH        SQLite Session 数据库
  --output FORMAT        pretty|json|ndjson
  --profile NAME         lark-cli profile
  --as MODE              auto|user|bot
  --listen ADDRESS       HTTP 监听地址
  --record-dir PATH      记录 Provider 请求/响应 fixture
  --replay-dir PATH      从 fixture 离线回放

命令：
  search QUERY           多来源联邦搜索
  continue               继续已有 session 的分页
  query                  结构化/对象内查询
  fetch                  按 ObjectRef 读取正文、上下文或摘要
  expand                 展开关联对象
  resolve TEXT           解析人名、链接或别名
  research QUERY         有界 Search→Fetch→Evidence 流程
  ask QUERY              基于 Evidence 的可选 AI 问答
  plan FILE              执行 retrieval-plan/v1 JSON
  providers              显示 Provider 能力
  doctor                 实际探测 Provider 后端
  sessions list|get|rm   管理检索会话
  migrate sessions       幂等导入旧 JSON Session
  serve                  启动 HTTP API + Web UI
  mcp                    启动 stdio MCP Server
  version                显示版本

示例：
  sfs --backend mock search "A 项目 延期" --sources docs,messages,minutes
  sfs --backend mock research "A 项目为什么延期" --fetch-top 6
  sfs serve --listen 127.0.0.1:3765
`, version)
}

func cmdProviders(ctx context.Context, a *appcore.App, id kernel.Identity, format string, w io.Writer, probe bool) error {
	caps, err := a.Engine.Capabilities(ctx, kernel.CapabilityRequest{Identity: id, Probe: probe})
	if err != nil {
		return err
	}
	if format == "pretty" {
		fmt.Fprintf(w, "Backend: %s\nProviders: %d\n\n", a.Backend, len(caps.Providers))
		for _, p := range caps.Providers {
			fmt.Fprintf(w, "%-22s %-10s %-12s backend=%s", p.Descriptor.ID, p.Descriptor.Source, p.Status, p.Descriptor.Backend)
			if p.Version != "" {
				fmt.Fprintf(w, " version=%s", p.Version)
			}
			if p.Error != nil {
				fmt.Fprintf(w, " error=%s", p.Error.Message)
			}
			fmt.Fprintln(w)
		}
		return nil
	}
	return clicore.PrintJSON(w, caps, format != "ndjson")
}

func cmdSearch(ctx context.Context, a *appcore.App, id kernel.Identity, format string, args []string, w, ew io.Writer) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(ew)
	sources := fs.String("sources", "", "逗号分隔来源")
	sourceQueriesJSON := fs.String("source-queries-json", "", "按来源覆盖 query 的 JSON 对象")
	strategyProfile := fs.String("strategy", "", "检索档位: fast|balanced|deep")
	pagination := fs.String("pagination", "", "分页策略: none|adaptive|fixed")
	limit := fs.Int("limit", 0, "全局结果上限（0 使用档位默认）")
	per := fs.Int("per-source", 0, "每来源每页条数（0 使用档位默认）")
	afterS := fs.String("after", "", "RFC3339/日期/14d/2w")
	beforeS := fs.String("before", "", "RFC3339/日期")
	docs := fs.String("doc-types", "", "docx,wiki,sheet 等")
	mine := fs.Bool("mine", false, "仅我的对象")
	titleOnly := fs.Bool("only-title", false, "仅标题匹配")
	deadline := fs.Duration("deadline", 0, "总时限（0 使用档位默认）")
	calls := fs.Int("max-calls", 0, "最大上游调用（0 使用默认）")
	pages := fs.Int("pages", 0, "每来源最大页数（0 使用档位默认）")
	maxFetches := fs.Int("max-fetches", 0, "后续共享计划可用的最大 Fetch 数")
	maxBytes := fs.Int64("max-bytes", 0, "本次预算最大响应字节数")
	sessionID := fs.String("session", "", "复用 session")
	noQuota := fs.Bool("no-source-quota", false, "关闭来源配额")
	if err := fs.Parse(interspersed(args, map[string]bool{"mine": true, "only-title": true, "no-source-quota": true})); err != nil {
		return commandError{err: err, code: 2}
	}
	q := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if q == "" {
		return commandError{err: fmt.Errorf("search 需要查询文本"), code: 2}
	}
	after, err := clicore.ParseTime(*afterS, time.Now())
	if err != nil {
		return err
	}
	before, err := clicore.ParseTime(*beforeS, time.Now())
	if err != nil {
		return err
	}
	strategy := a.Config.Strategy
	if *strategyProfile != "" {
		strategy.Profile = *strategyProfile
		if *pagination == "" {
			// Let the selected profile choose its own default instead of inheriting
			// the configured profile's pagination mode.
			strategy.Pagination = ""
		}
	}
	if *pagination != "" {
		strategy.Pagination = *pagination
	}
	sourceQueries := map[kernel.SourceID]string{}
	if *sourceQueriesJSON != "" {
		if err := json.Unmarshal([]byte(*sourceQueriesJSON), &sourceQueries); err != nil {
			return commandError{err: fmt.Errorf("invalid --source-queries-json: %w", err), code: 2}
		}
	}
	req := kernel.SearchRequest{Query: q, SourceQueries: sourceQueries, Sources: clicore.ParseSources(*sources), Identity: id, Limit: *limit, LimitPerSource: *per, SessionID: *sessionID, Filters: kernel.SearchFilters{After: after, Before: before, Mine: *mine, OnlyTitle: *titleOnly}, Budget: kernel.SearchBudget{DeadlineMS: int(deadline.Milliseconds()), MaxCalls: *calls, MaxPagesPerSource: *pages, MaxFetches: *maxFetches, MaxBytes: *maxBytes}, Strategy: strategy}
	if *docs != "" {
		req.Filters.DocTypes = strings.Split(*docs, ",")
	}
	if *noQuota {
		req.Strategy.SourceQuota = false
		req.Strategy.DisableSourceQuota = true
	}
	req = a.ApplyDefaults(req)
	snap, err := a.Engine.Search(ctx, req)
	if err != nil && len(snap.Candidates) == 0 {
		return err
	}
	return printSearchResult(w, format, snap)
}
func printSearchResult(w io.Writer, format string, s kernel.SearchSnapshot) error {
	switch format {
	case "pretty":
		clicore.PrintSearch(w, s)
		return nil
	case "ndjson":
		enc := json.NewEncoder(w)
		for _, c := range s.Candidates {
			if err := enc.Encode(c); err != nil {
				return err
			}
		}
		return nil
	default:
		return clicore.PrintJSON(w, s, true)
	}
}

func cmdContinue(ctx context.Context, a *appcore.App, identity kernel.Identity, format string, args []string, w, ew io.Writer) error {
	fs := flag.NewFlagSet("continue", flag.ContinueOnError)
	fs.SetOutput(ew)
	sid := fs.String("session", "", "session id")
	sources := fs.String("sources", "", "逗号分隔来源")
	pages := fs.Int("pages", 1, "追加页数")
	if err := fs.Parse(interspersed(args, nil)); err != nil {
		return err
	}
	if *sid == "" && len(fs.Args()) > 0 {
		*sid = fs.Args()[0]
	}
	if *sid == "" {
		return fmt.Errorf("continue requires --session")
	}
	snap, err := a.Engine.Continue(ctx, kernel.ContinueRequest{SessionID: *sid, Sources: clicore.ParseSources(*sources), MaxAdditionalPages: *pages, Identity: identity})
	if err != nil && len(snap.Candidates) == 0 {
		return err
	}
	return printSearchResult(w, format, snap)
}

func cmdQuery(ctx context.Context, a *appcore.App, id kernel.Identity, format string, args []string, w, ew io.Writer) error {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	fs.SetOutput(ew)
	source := fs.String("source", "", "来源")
	filterJSON := fs.String("filter", "{}", "JSON filter")
	limit := fs.Int("limit", 50, "结果上限")
	cursor := fs.String("cursor", "", "继续对象内查询的 cursor")
	sid := fs.String("session", "", "session id")
	containerID := fs.String("container-id", "", "容器 native/canonical id，例如 app_token/table_id")
	containerKind := fs.String("container-kind", "", "容器类型，例如 base_table 或 sheet")
	if err := fs.Parse(interspersed(args, nil)); err != nil {
		return err
	}
	if *source == "" {
		return fmt.Errorf("query requires --source")
	}
	filter := map[string]any{}
	if err := json.Unmarshal([]byte(*filterJSON), &filter); err != nil {
		return fmt.Errorf("invalid --filter: %w", err)
	}
	var container *kernel.ObjectRef
	if *containerID != "" {
		r := parseRef(*containerID, *source, *containerKind)
		container = &r
	}
	snap, err := a.Engine.Query(ctx, kernel.QueryRequest{Source: kernel.SourceID(*source), Container: container, Filter: filter, Limit: *limit, Cursor: *cursor, Identity: id, SessionID: *sid})
	if err != nil && len(snap.Candidates) == 0 {
		return err
	}
	if format == "pretty" {
		return printSearchResult(w, "pretty", kernel.SearchSnapshot{SessionID: snap.SessionID, Query: fmt.Sprint(filter["query"]), Candidates: snap.Candidates, Sources: []kernel.SourceRun{snap.SourceRun}, Budget: snap.Budget, Stats: kernel.SearchStats{Returned: len(snap.Candidates)}})
	}
	return clicore.PrintJSON(w, snap, format != "ndjson")
}

func parseRef(id, source, kind string) kernel.ObjectRef {
	r := kernel.ObjectRef{CanonicalID: id, Source: kernel.SourceID(source), Kind: kernel.ObjectKind(kind)}
	parts := strings.SplitN(id, ":", 4)
	if len(parts) == 4 && parts[0] == "feishu" {
		r.Platform = "feishu"
		r.ScopeKey = parts[1]
		r.Kind = kernel.ObjectKind(parts[2])
		r.NativeID = parts[3]
	} else if id != "" && !strings.Contains(id, ":") {
		r.CanonicalID = ""
		r.NativeID = id
	}
	return r
}
func cmdFetch(ctx context.Context, a *appcore.App, id kernel.Identity, format string, args []string, w, ew io.Writer) error {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	fs.SetOutput(ew)
	sid := fs.String("session", "", "session id")
	source := fs.String("source", "", "来源（无 session 时需要）")
	kind := fs.String("kind", "", "对象类型")
	projS := fs.String("projection", "content", "head,summary,structure,content,context,relations,attachments")
	var ids clicore.StringList
	fs.Var(&ids, "id", "canonical/native id，可重复")
	if err := fs.Parse(interspersed(args, nil)); err != nil {
		return err
	}
	ids = append(ids, fs.Args()...)
	if len(ids) == 0 {
		return fmt.Errorf("fetch requires --id or positional id")
	}
	proj, err := clicore.ParseProjection(*projS)
	if err != nil {
		return err
	}
	items := make([]kernel.FetchRequest, 0, len(ids))
	for _, x := range ids {
		items = append(items, kernel.FetchRequest{Ref: parseRef(x, *source, *kind), Projection: proj})
	}
	batch, err := a.Engine.Fetch(ctx, kernel.FetchBatchRequest{SessionID: *sid, Identity: id, Items: items})
	if err != nil && len(batch.Items) == 0 {
		return err
	}
	return clicore.PrintJSON(w, batch, format != "ndjson")
}
func cmdExpand(ctx context.Context, a *appcore.App, id kernel.Identity, format string, args []string, w, ew io.Writer) error {
	fs := flag.NewFlagSet("expand", flag.ContinueOnError)
	fs.SetOutput(ew)
	sid := fs.String("session", "", "session id")
	obj := fs.String("id", "", "对象 id")
	source := fs.String("source", "", "来源")
	kind := fs.String("kind", "", "类型")
	rels := fs.String("relations", "", "逗号分隔关系")
	depth := fs.Int("depth", 1, "最大深度 1-2")
	if err := fs.Parse(interspersed(args, nil)); err != nil {
		return err
	}
	if *obj == "" && len(fs.Args()) > 0 {
		*obj = fs.Args()[0]
	}
	if *obj == "" {
		return fmt.Errorf("expand requires --id")
	}
	batch, err := a.Engine.Expand(ctx, kernel.ExpandRequest{SessionID: *sid, Identity: id, Ref: parseRef(*obj, *source, *kind), Relations: strings.FieldsFunc(*rels, func(r rune) bool { return r == ',' }), MaxDepth: *depth})
	if err != nil && len(batch.Relations) == 0 {
		return err
	}
	return clicore.PrintJSON(w, batch, format != "ndjson")
}
func cmdResolve(ctx context.Context, a *appcore.App, id kernel.Identity, format string, args []string, w, ew io.Writer) error {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	fs.SetOutput(ew)
	source := fs.String("source", "people", "来源")
	kind := fs.String("kind", "", "类型")
	limit := fs.Int("limit", 10, "上限")
	if err := fs.Parse(interspersed(args, nil)); err != nil {
		return err
	}
	text := strings.Join(fs.Args(), " ")
	if text == "" {
		return fmt.Errorf("resolve requires text")
	}
	refs, err := a.Engine.Resolve(ctx, kernel.ResolveRequest{Source: kernel.SourceID(*source), Text: text, Kind: kernel.ObjectKind(*kind), Identity: id, Limit: *limit})
	if err != nil {
		return err
	}
	return clicore.PrintJSON(w, map[string]any{"refs": refs}, format != "ndjson")
}

func cmdPlan(ctx context.Context, a *appcore.App, format string, args []string, w, ew io.Writer) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(ew)
	stream := fs.Bool("events", false, "输出事件 NDJSON")
	if err := fs.Parse(interspersed(args, map[string]bool{"events": true})); err != nil {
		return err
	}
	path := "-"
	if len(fs.Args()) > 0 {
		path = fs.Args()[0]
	}
	var r io.Reader = os.Stdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	}
	raw, err := io.ReadAll(io.LimitReader(r, (8<<20)+1))
	if err != nil {
		return err
	}
	if len(raw) > 8<<20 {
		return &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, Message: "plan exceeds 8 MiB"}
	}
	if err := planvalidate.ValidateJSON(raw); err != nil {
		return err
	}
	var plan kernel.RetrievalPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return err
	}
	events, results, err := a.Engine.Execute(ctx, plan)
	if err != nil {
		return err
	}
	collected := []kernel.RetrievalEvent{}
	var result kernel.PlanResult
	eventCh, resultCh := events, results
	enc := json.NewEncoder(w)
	for eventCh != nil || resultCh != nil {
		select {
		case ev, ok := <-eventCh:
			if !ok {
				eventCh = nil
				continue
			}
			if *stream || format == "ndjson" {
				_ = enc.Encode(ev)
			} else {
				collected = append(collected, ev)
			}
		case res, ok := <-resultCh:
			if !ok {
				resultCh = nil
				continue
			}
			result = res
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if *stream || format == "ndjson" {
		return enc.Encode(map[string]any{"type": "result", "result": result})
	}
	return clicore.PrintJSON(w, map[string]any{"result": result, "events": collected}, format == "pretty" || format == "json")
}

func cmdResearch(ctx context.Context, a *appcore.App, id kernel.Identity, format string, args []string, w, ew io.Writer) error {
	req, selectedPlanner, err := parseResearchCommand(a, id, "research", args, ew)
	if err != nil {
		return err
	}
	result, err := (research.Service{Kernel: a.Engine, Planner: selectedPlanner, Reranker: a.Reranker}).Run(ctx, research.Request{UserRequest: req})
	if err != nil {
		return err
	}
	if format == "pretty" {
		fmt.Fprintf(w, "Planner: %s  Session: %s\nCandidates: %d  Artifacts: %d  Evidence: %d  Partial: %v\n\n%s\n", result.Planner, result.PlanResult.SessionID, len(result.CandidatePack.Candidates), len(result.ArtifactPack.Artifacts), len(result.EvidencePack.Evidence), result.PlanResult.Partial, result.Summary)
		for _, warning := range result.Warnings {
			fmt.Fprintf(w, "Warning: %s\n", warning)
		}
		return nil
	}
	return clicore.PrintJSON(w, result, format != "ndjson")
}

func cmdAsk(ctx context.Context, a *appcore.App, id kernel.Identity, format string, args []string, w, ew io.Writer) error {
	req, selectedPlanner, err := parseResearchCommand(a, id, "ask", args, ew)
	if err != nil {
		return err
	}
	result, err := (askcore.Service{Research: research.Service{Kernel: a.Engine, Planner: selectedPlanner, Reranker: a.Reranker}, Answerer: a.Answerer}).Run(ctx, askcore.Request{UserRequest: req})
	if err != nil {
		return err
	}
	if format == "pretty" {
		fmt.Fprintf(w, "%s\n\n", result.Answer.Text)
		for _, citation := range result.Answer.Citations {
			fmt.Fprintf(w, "[%s] %s", citation.ID, citation.Quote)
			if citation.URL != "" {
				fmt.Fprintf(w, " (%s)", citation.URL)
			}
			fmt.Fprintln(w)
		}
		for _, warning := range result.Warnings {
			fmt.Fprintf(w, "Warning: %s\n", warning)
		}
		return nil
	}
	return clicore.PrintJSON(w, result, format != "ndjson")
}

func parseResearchCommand(a *appcore.App, id kernel.Identity, name string, args []string, ew io.Writer) (planner.UserRequest, planner.Planner, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(ew)
	sources := fs.String("sources", "", "来源")
	fetchTop := fs.Int("fetch-top", 8, "读取前 N 个候选")
	limit := fs.Int("limit", 40, "候选上限")
	deadline := fs.Duration("deadline", 15*time.Second, "总时限")
	plannerType := fs.String("planner", "", "rules|default|command；空值使用配置")
	plannerCommand := fs.String("planner-command", "", "外部 Planner 可执行文件")
	plannerArgsJSON := fs.String("planner-args-json", "", "外部 Planner 参数 JSON 数组")
	plannerTimeout := fs.Duration("planner-timeout", 0, "外部 Planner 超时")
	if err := fs.Parse(interspersed(args, nil)); err != nil {
		return planner.UserRequest{}, nil, err
	}
	q := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if q == "" {
		return planner.UserRequest{}, nil, fmt.Errorf("%s requires query", name)
	}
	selectedPlanner := a.Planner
	if *plannerType != "" || *plannerCommand != "" || *plannerArgsJSON != "" || *plannerTimeout > 0 {
		pcfg := a.Config.Planner
		if *plannerType != "" {
			pcfg.Type = *plannerType
		}
		if *plannerCommand != "" {
			pcfg.Command = *plannerCommand
		}
		if *plannerArgsJSON != "" {
			if err := json.Unmarshal([]byte(*plannerArgsJSON), &pcfg.Args); err != nil {
				return planner.UserRequest{}, nil, commandError{err: fmt.Errorf("invalid --planner-args-json: %w", err), code: 2}
			}
		}
		if *plannerTimeout > 0 {
			pcfg.Timeout = plannerTimeout.String()
		}
		var err error
		selectedPlanner, err = appcore.BuildPlanner(pcfg)
		if err != nil {
			return planner.UserRequest{}, nil, err
		}
	}
	req := planner.UserRequest{Query: q, Sources: clicore.ParseSources(*sources), Identity: id, Limit: *limit, Deep: true, FetchTopK: *fetchTop, Budget: kernel.SearchBudget{DeadlineMS: int(deadline.Milliseconds()), MaxCalls: 36, MaxFetches: *fetchTop, MaxPagesPerSource: 2}}
	return req, selectedPlanner, nil
}

func cmdSessions(ctx context.Context, a *appcore.App, identity kernel.Identity, format string, args []string, w, ew io.Writer) error {
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "list":
		ss, err := a.Engine.Sessions(ctx, identity)
		if err != nil {
			return err
		}
		if format == "pretty" {
			for _, s := range ss {
				q := ""
				if s.Request != nil {
					q = s.Request.Query
				}
				fmt.Fprintf(w, "%-24s %s  candidates=%d artifacts=%d  %s\n", s.ID, s.CreatedAt.Format(time.RFC3339), len(s.Candidates), len(s.Artifacts), q)
			}
			return nil
		}
		return clicore.PrintJSON(w, map[string]any{"sessions": ss}, true)
	case "get":
		if len(args) == 0 {
			return fmt.Errorf("sessions get requires id")
		}
		s, err := a.Engine.Session(ctx, args[0], identity)
		if err != nil {
			return err
		}
		return clicore.PrintJSON(w, s, true)
	case "rm", "delete":
		if len(args) == 0 {
			return fmt.Errorf("sessions rm requires id")
		}
		return a.Engine.DeleteSession(ctx, args[0], identity)
	default:
		return fmt.Errorf("unknown sessions subcommand %q", sub)
	}
}

func cmdMigrate(ctx context.Context, cfg appcore.Config, format string, args []string, w, ew io.Writer) error {
	if len(args) == 0 || args[0] != "sessions" {
		return commandError{err: fmt.Errorf("migrate requires the sessions subcommand"), code: 2}
	}
	fs := flag.NewFlagSet("migrate sessions", flag.ContinueOnError)
	fs.SetOutput(ew)
	from := fs.String("from", "", "旧 JSON Session 目录")
	to := fs.String("to", "", "目标 SQLite 数据库")
	if err := fs.Parse(interspersed(args[1:], nil)); err != nil {
		return commandError{err: err, code: 2}
	}
	if *from == "" {
		*from = cfg.Runtime.SessionDir
		if *from == "" {
			home, _ := os.UserHomeDir()
			*from = filepath.Join(home, ".sfs", "sessions")
		}
	}
	if *to == "" {
		if cfg.Storage.Type == "sqlite" {
			*to = cfg.Storage.Path
		}
		if *to == "" {
			home, _ := os.UserHomeDir()
			*to = filepath.Join(home, ".sfs", "sfs.db")
		}
	}
	store, err := session.NewSQLiteStore(*to, cfg.SessionTTL())
	if err != nil {
		return err
	}
	defer store.Close()
	report, err := session.ImportJSONDir(ctx, *from, store)
	if err != nil {
		return err
	}
	if format == "pretty" {
		fmt.Fprintf(w, "Scanned: %d  Imported: %d  Existing: %d  Expired: %d  Invalid: %d  Skipped: %d\nDatabase: %s\n", report.Scanned, report.Imported, report.AlreadyPresent, report.Expired, report.Invalid, report.Skipped, *to)
		for _, issue := range report.Issues {
			fmt.Fprintf(w, "- %s: %s\n", issue.Kind, issue.Error)
		}
		return nil
	}
	return clicore.PrintJSON(w, report, format != "ndjson")
}

func cmdServe(ctx context.Context, a *appcore.App, args []string, w, ew io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(ew)
	listen := fs.String("listen", a.Config.Runtime.Listen, "监听地址")
	if err := fs.Parse(interspersed(args, nil)); err != nil {
		return err
	}
	if !isLoopbackListen(*listen) {
		return fmt.Errorf("refusing non-loopback listen address %q: the built-in HTTP server has no multi-user authentication; place it behind a trusted authenticated proxy or use a loopback address", *listen)
	}
	srv := &http.Server{Addr: *listen, Handler: httpapi.New(a).Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	fmt.Fprintf(w, "SuperFeishuSearch HTTP/Web: http://%s\n", *listen)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func isLoopbackListen(address string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil || host == "" {
		return false
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// interspersed allows command flags before or after positional arguments while
// still using the standard library flag package. Values following non-boolean
// flags remain attached to their flag during reordering.
func interspersed(args []string, boolFlags map[string]bool) []string {
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		token := args[i]
		if token == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(token, "-") && token != "-" {
			flags = append(flags, token)
			name := strings.TrimLeft(token, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				continue
			}
			if boolFlags != nil && boolFlags[name] {
				continue
			}
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positionals = append(positionals, token)
	}
	return append(flags, positionals...)
}
