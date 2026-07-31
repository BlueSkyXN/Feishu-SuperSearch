# SuperFeishuSearch

[![CI](https://github.com/BlueSkyXN/Feishu-SuperSearch/actions/workflows/ci.yml/badge.svg)](https://github.com/BlueSkyXN/Feishu-SuperSearch/actions/workflows/ci.yml)
[![Security](https://github.com/BlueSkyXN/Feishu-SuperSearch/actions/workflows/security.yml/badge.svg)](https://github.com/BlueSkyXN/Feishu-SuperSearch/actions/workflows/security.yml)
[![Release](https://github.com/BlueSkyXN/Feishu-SuperSearch/actions/workflows/release.yml/badge.svg)](https://github.com/BlueSkyXN/Feishu-SuperSearch/actions/workflows/release.yml)
[![GitHub Release](https://img.shields.io/github/v/release/BlueSkyXN/Feishu-SuperSearch)](https://github.com/BlueSkyXN/Feishu-SuperSearch/releases/latest)

SuperFeishuSearch 是一个面向飞书对象的、确定性的联邦检索程序。它将文档、消息、群、联系人、妙记、会议、日程、任务、邮件、Base 与 Sheets 的分散能力统一为一个可编程的 Retrieval Kernel：

- `Search`：并发搜索多个全局来源，统一候选、去重并融合排序
- `Query`：任务、Base、Sheets 等结构化或对象内查询
- `Fetch`：按稳定 `ObjectRef` 补齐正文、上下文、原生摘要、结构或附件
- `Expand`：有界展开会议→妙记、消息→文档、妙记→任务等关系
- `Continue`：复用 session/cursor 继续特定来源分页
- `Resolve`：将人员名称等解析为稳定对象引用
- `RetrievalPlan`：执行有预算、有状态、可重试的有界 DAG

Planner、LLM、SKILL、MCP 与 Web 位于内核外层。内核本身不依赖 LLM，所以可单独运行、测试、录制和回放。

## 文档导航

- [快速开始](docs/GETTING_STARTED.md)
- [配置参考](docs/CONFIGURATION.md)
- [CLI 参考](docs/CLI_REFERENCE.md)
- [HTTP API](docs/HTTP_API.md)
- [MCP 指南](docs/MCP_GUIDE.md)
- [RetrievalPlan](docs/RETRIEVAL_PLAN.md)
- [架构说明](docs/ARCHITECTURE.md)
- [Provider 开发](docs/PROVIDER_GUIDE.md)
- [开发与测试](docs/DEVELOPMENT.md)
- [运行与故障排查](docs/OPERATIONS.md)
- [GitHub Actions 与发布](docs/GITHUB_ACTIONS.md)
- [完整文档索引](docs/README.md)

仓库协作见 [CONTRIBUTING.md](CONTRIBUTING.md)，安全报告与部署边界见 [SECURITY.md](SECURITY.md)。

## 1. 已实现范围

```text
Retrieval Kernel
├── Provider Registry + source/operation 路由
├── Search / Query / Fetch / Expand / Continue / Resolve
├── 并发 fan-out、总预算、超时、取消、有限重试、部分失败
├── Provider-specific source_queries
├── Canonical ID、精确去重、Weighted RRF、来源配额
├── 两阶段自适应分页
├── Projection / Late Materialization
├── 分组 Batch Fetch / cache / context-aware singleflight
├── RetrievalSession + SQLite 默认持久化 + JSON 兼容迁移
└── 有界 RetrievalPlan DAG、节点状态机、ready/retry heap

Adapters
├── lark-cli：11 个来源
├── direct OpenAPI：docs / messages / people / minutes
├── mock：完整离线演示数据
├── executable JSON-RPC Provider
└── Provider 交互 record/replay

Planners
├── rules：规则路由、query 压缩、按来源 query
├── default：固定 Search→Fetch
├── external command：可接独立 Planner
└── 可选 OpenAI-compatible AI Planner / Rerank / Answerer

Entrypoints
├── CLI
├── HTTP API + SSE
├── Web UI
├── MCP stdio server
└── Go HTTP Client SDK
```

## 2. 免编译安装

普通用户不需要 Go、Node.js、SQLite 或本地编译。打开 [GitHub Releases](https://github.com/BlueSkyXN/Feishu-SuperSearch/releases/latest)，下载与系统对应的资产：

| 系统 | 资产 |
|---|---|
| macOS Apple Silicon | `SuperFeishuSearch-1.0.3-darwin-arm64.tar.gz` |
| macOS Intel | `SuperFeishuSearch-1.0.3-darwin-amd64.tar.gz` |
| Linux x86-64 | `SuperFeishuSearch-1.0.3-linux-amd64.tar.gz` |
| Linux ARM64 | `SuperFeishuSearch-1.0.3-linux-arm64.tar.gz` |
| Windows x86-64 | `SuperFeishuSearch-1.0.3-windows-amd64.zip` |
| Windows ARM64 | `SuperFeishuSearch-1.0.3-windows-arm64.zip` |

macOS/Linux：

```bash
tar -xzf SuperFeishuSearch-1.0.3-<os>-<arch>.tar.gz
cd SuperFeishuSearch-1.0.3-<os>-<arch>
./sfs version
```

Windows PowerShell：

```powershell
Expand-Archive .\SuperFeishuSearch-1.0.3-windows-amd64.zip
cd .\SuperFeishuSearch-1.0.3-windows-amd64
.\sfs.exe version
```

`SHA256SUMS` 覆盖 8 个压缩资产。Release workflow 会验证精确资产集合、校验和、六平台 `GOOS/GOARCH`、`CGO_ENABLED=0`、`trimpath`、嵌入版本/commit、源码包与 Git tracked tree 一致性、源码包在无 `.git` 环境下完整执行 `make verify`，以及 Linux amd64 离线 smoke。

## 3. 完整离线预览

发布包自带 `config.demo.json`，不需要飞书账号、Token 或 AI Key：

```bash
./sfs --config config.demo.json serve
```

Windows 使用 `.\sfs.exe --config config.demo.json serve`。浏览器打开 [http://127.0.0.1:3765](http://127.0.0.1:3765)，可以实际预览：

```text
Search / Continue
Research / Evidence
Ask / Claim / Citation
对象正文预览
来源状态与 Doctor
Session 历史
桌面、移动端、浅色和深色界面
```

Demo 使用 Mock Provider 和确定性 Demo Answerer。它用于预览完整交互与数据契约，不代表模型回答，也不代表真实租户权限已通过。

命令行 Mock 搜索：

```bash
sfs --backend mock search "A 项目 延期" \
  --sources docs,messages,minutes,meetings,tasks
```

按来源传入不同的实际检索词，同时保留原始问题用于全局融合：

```bash
sfs --backend mock search "A 项目为什么延期，谁做了决定" \
  --sources docs,messages,minutes \
  --source-queries-json '{"docs":"A 项目 延期","messages":"A 项目 延期 决定","minutes":"A 项目"}'
```

执行 Search→Fetch→Evidence：

```bash
sfs --backend mock research "A 项目延期原因和未完成任务" \
  --sources docs,messages,minutes,meetings,tasks \
  --fetch-top 6
```

执行声明式计划：

```bash
sfs --backend mock plan examples/plan-search-fetch.json
sfs --backend mock --output ndjson plan examples/plan-search-fetch.json --events
```

启动 Web 与 HTTP API：

```bash
sfs --config config.demo.json serve
```

浏览器打开 `http://127.0.0.1:3765`。

## 4. 真实飞书后端

### 4.1 lark-cli 模式

确保本机可执行：

```bash
lark-cli --version
lark-cli doctor
```

SuperFeishuSearch 不复制或接管 `lark-cli` OAuth；它将 `--profile`、`--as user|bot` 传给子进程，并继承当前 profile 的 scope 与对象可见范围。

```bash
./sfs --backend larkcli --profile work --as user \
  search "上线" --sources docs,messages,minutes
```

探测已注册 Provider：

```bash
./sfs --backend larkcli --profile work --as user doctor
```

某个来源权限不足、超时或不可用时，其他来源仍可返回，结果会标记 `partial=true` 并保留每来源状态。

当前本机真实只读验证并非 11 来源全通过：docs、messages、chats、people、minutes、meetings、tasks 共 7 个来源成功；calendar、mail 返回 `missing_scope`；Base、Sheets 因缺少已知测试对象尚未完成验收。11 来源 fixture 基线不能替代这一真实矩阵。

最终工作树还重新验证了妙记 `summary/structure/content` Fetch：`minutes[].artifacts` 的嵌套结构和 transcript 文件均成功物化，子进程临时目录清理后仓库原有 `minutes/` 状态未发生变化。

### 4.2 direct OpenAPI 高频来源

内置 direct OpenAPI Adapter 覆盖：

```text
docs      Search / Fetch
messages  Search / Fetch Context / Expand Relations
people    Search / Resolve
minutes   Search / Fetch Summary/Structure/Content
```

SDK 调用需要应用 `app_id` 和 user access token。Token 只从 `open_api.token_env` 指定的环境变量读取，默认是 `FEISHU_USER_ACCESS_TOKEN`：

```bash
export SFS_OPENAPI_APP_ID='cli_xxx'
export FEISHU_USER_ACCESS_TOKEN='u-...'
./sfs --backend openapi --as user search "周报" --sources docs
```

也可通过 operation 级路由指定某个操作的后端：

```bash
export SFS_OPENAPI_APP_ID='cli_xxx'
export FEISHU_USER_ACCESS_TOKEN='u-...'
./sfs --config examples/config-openapi-route.json --profile work --as user \
  research "A 项目延期原因" --sources docs,messages,minutes
```

对应路由：

```json
{
  "providers": {
    "routes": {
      "docs": {
        "search": "openapi.docs",
        "fetch": "larkcli.docs"
      }
    }
  }
}
```

这种路由粒度是 `source + operation`，不要求整个来源只能绑定一个后端。

direct OpenAPI 使用固定版本的官方 `oapi-sdk-go/v3` 实现文档搜索、消息搜索/读取、人员搜索和妙记 Search/Get/Artifacts；`docs_ai` Fetch 因 SDK 没有等价 XML/citation binding，保留受限 HTTP fallback。当前只完成了本机 `httptest` 请求、分页、大小限制和错误映射验证；由于没有同时可用的 App ID 与 Token，尚未执行真实租户 OpenAPI 验收。

### 4.3 后端选择

```text
auto      两者都可用时选择 hybrid；仅一个可用时选择对应真实后端；都不可用则直接报错
larkcli   全部使用内置 lark-cli Provider
openapi   仅注册当前已实现的直接 OpenAPI Provider
mock      离线演示 Provider
hybrid    高频来源 direct OpenAPI，其他来源 lark-cli；绝不注册或回退 Mock
replay    从录制 fixture 离线重放
```

## 5. 内置来源

| 来源 | Search / Query 主路径 | Fetch / Expand |
|---|---|---|
| docs | `drive +search`；或 direct Search v2 | `docs +fetch`；或 direct `docs_ai` Fetch |
| messages | `im +messages-search`；或 direct Search | 消息详情/上下文；消息→文档/群关系 |
| chats | `im +chat-search` | 群详情 |
| people | `contact +search-user`；或 direct Search | 用户详情；Resolve |
| minutes | `minutes +search`；或 direct Search | summary/todo/chapter/transcript |
| meetings | `vc +search` | 会议详情；meeting→minute |
| calendar | `calendar +search-event` | 已知 calendar/event 后读取 |
| tasks | `task +search`；结构化 Query | 任务详情 |
| mail | `mail +triage` | 邮件正文/上下文 |
| base | 不参加默认全局 Search | `base +record-search` 对象内 Query |
| sheets | 不参加默认全局 Search | `sheets +cells-search` 对象内 Query |

`lark-cli` Adapter 使用 `exec.CommandContext` 参数数组，不经过 shell；限制 stdout/stderr，支持取消和超时，兼容显式 `{ok,data,error}` envelope 与 raw OpenAPI JSON。

`docs` 的 Drive Search 可能返回 Sheet、Base 或普通 File。它们会被标记为 `sheet`、`base`、`attachment`，不声明可 Fetch Projection，也不能直接走面向 Docx Markdown 的 `docs +fetch`：Base 应改用 `query --source base`，Sheets 应改用 `query --source sheets`；普通 File 当前仅保留搜索候选元数据。详见 [lark-cli 兼容策略](docs/LARKCLI_COMPATIBILITY.md)。

## 6. CLI

全局参数必须位于命令之前：

```text
--config PATH
--backend auto|larkcli|openapi|mock|hybrid|replay
--session-dir PATH
--database PATH
--output pretty|json|ndjson
--profile NAME
--as auto|user|bot
--listen ADDRESS
--record-dir PATH
--replay-dir PATH
```

### 6.1 Search

```bash
sfs search "A 项目 延期" \
  --sources docs,messages,minutes \
  --after 14d \
  --strategy balanced \
  --limit 40 \
  --per-source 8 \
  --pages 2
```

输出包含：

- `session_id`
- 统一 `Candidate[]`
- 每来源状态、页数、耗时和错误
- continuation cursor
- budget 与统计
- `projection` / `available_projection`

### 6.2 Continue

```bash
sfs continue --session rs_xxx --sources messages,docs --pages 1
```

### 6.3 Query

任务：

```bash
sfs query --source tasks --filter '{"query":"A 项目","completed":false}'
```

Base：

```bash
sfs query --source base \
  --container-id app_token/table_id \
  --container-kind base_table \
  --filter '{"keyword":"延期","search_field":"项目名称"}'
```

Sheets：

```bash
sfs query --source sheets \
  --container-id spreadsheet_token \
  --container-kind sheet \
  --filter '{"find":"延期","sheet_id":"sheet_xxx"}'
```

### 6.4 Fetch

从 Search session 读取对象：

```bash
sfs fetch --session rs_xxx \
  --id feishu:scope:minute:obc_xxx \
  --projection summary,structure,relations
```

多个 ID：

```bash
sfs fetch --session rs_xxx --id ID1 --id ID2 --projection content,context
```

Fetch 会合并重复对象和 Projection、扣除 session 已缓存 Projection、按 Provider 能力分批，并对并发相同请求做 singleflight。

### 6.5 Expand

```bash
sfs expand --session rs_xxx \
  --id feishu:scope:meeting:m_xxx \
  --relations meeting.has_minute \
  --depth 2
```

默认最大深度 1，程序上限为 2；使用 visited set 防止关系环。

### 6.6 Resolve

```bash
sfs --as user resolve "张三" --source people --limit 10
```

### 6.7 RetrievalPlan

```bash
sfs plan examples/plan-search-fetch.json
sfs --output ndjson plan examples/plan-search-fetch.json --events
```

支持节点：

```text
resolve search query fetch expand merge dedup rank limit project map_fetch
```

执行器包含 DAG 校验、200 节点上限、节点状态机、ready max-heap、retry min-heap、共享预算和有限指数退避。它不执行任意 shell，也不允许无界循环。

### 6.8 Research / Ask

`research` 是确定性的 Search→Fetch→Evidence 流程，不调用 Answerer：

```bash
sfs --backend mock research "A 项目为什么延期" --fetch-top 6
```

`ask` 会先执行同一检索与证据流程，再由启用的 Answerer 仅依据 Evidence Pack 生成带 citation 的答案：

```bash
SFS_AI_API_KEY='...' sfs --config ~/.sfs/config.json \
  ask "A 项目为什么延期" --sources docs,messages,minutes --fetch-top 8
```

AI 默认关闭；Answerer 不可用时 `ask` 返回 `unsupported`，Search/Research 不受影响。模型运行中失败时返回确定性证据摘要并标记 `partial=true`。

启用 OpenAI-compatible AI 表示允许程序把 Planner 所需的查询/能力、Rerank 的候选标题与摘要，以及 Answerer 的 Evidence Pack 发送到配置的 `base_url`。不要对无权处理业务正文的模型服务启用这些功能。离线 `provider=demo` 不发送网络请求，并且只能与 `backend=mock` 一起使用。

### 6.9 Session

```bash
sfs sessions list
sfs sessions get rs_xxx
sfs sessions rm rs_xxx
sfs migrate sessions --from ~/.sfs/sessions --to ~/.sfs/sfs.db
```

新安装默认使用 `~/.sfs/sfs.db`。旧 JSON Session 可幂等导入 SQLite；迁移成功前不会删除或改写源文件。Session ID 会校验格式；复用 session 时还会校验 identity scope。

## 7. Planner

### 7.1 内置规则 Planner

默认 `planner.type=rules`：

- 根据问题中的任务、会议、邮件、人员等线索选择来源
- 对长问题进行有限压缩
- 为具有短 query 限制的来源生成 `source_queries`
- 在 deep 模式生成有界 `map_fetch`

固定 Planner：

```json
{"planner":{"type":"default"}}
```

### 7.2 外部命令 / LLM Planner

外部 Planner 从 stdin 读取：

```json
{
  "request": {},
  "capabilities": {},
  "output_schema": "retrieval-plan/v1"
}
```

并向 stdout 只写一个 `RetrievalPlan` JSON。接入示例：

```bash
sfs --config examples/config-external-planner.json \
  research "A 项目延期原因和待办" --fetch-top 6
```

或命令行覆盖：

```bash
sfs --backend mock research "A 项目延期原因" \
  --planner command \
  --planner-command python3 \
  --planner-args-json '["examples/external-planner.py"]' \
  --planner-timeout 10s
```

Planner 只生成计划；预算、节点上限、重试、Provider 能力和数据访问仍由 Kernel 控制。

内建 AI Planner 对首次输出和最多一次修复输出都执行同一份 `retrieval-plan/v1` 严格校验；仍不合法时回退 Rules Planner，并在 Plan `metadata` 中记录原因。

## 8. 外部 Provider

独立进程通过 one-shot JSON-RPC 2.0 over stdio 实现：

```text
health search query fetch expand resolve
```

运行示例：

```bash
sfs --config examples/config-external-provider.json \
  search "External Provider" --sources docs
```

协议：`api/provider-protocol.schema.json`。示例实现：`examples/external-provider.py`。

每次调用启动一个隔离进程；stdout 只能输出 JSON-RPC 响应。宿主严格校验 envelope、方法、descriptor、ObjectRef、Projection、分页一致性和输出大小。

## 9. Record / Replay

录制 Provider 层输入输出：

```bash
rm -rf .tmp/replay
sfs --backend mock --record-dir .tmp/replay \
  search "A 项目 延期" --sources docs,messages,minutes
```

离线回放相同检索：

```bash
sfs --replay-dir .tmp/replay \
  search "A 项目 延期" --sources docs,messages,minutes
```

Replay key 会去掉临时 `session_id`，但保留 identity scope 与业务请求，因此可跨进程复现，同时不会把不同身份范围混在一起。

## 10. HTTP API 与 Web

```bash
sfs serve --listen 127.0.0.1:3765
```

主要接口：

```text
GET    /v1/health
GET    /v1/capabilities?probe=true
POST   /v1/search
POST   /v1/search/continue
POST   /v1/query
POST   /v1/fetch
POST   /v1/expand
POST   /v1/resolve
POST   /v1/plans:execute
POST   /v1/research
POST   /v1/ask
GET    /v1/sessions
GET    /v1/sessions/{id}
DELETE /v1/sessions/{id}
```

```bash
curl -s http://127.0.0.1:3765/v1/search \
  -H 'Content-Type: application/json' \
  -d '{
    "query":"A 项目 延期",
    "source_queries":{"minutes":"A 项目"},
    "sources":["docs","messages","minutes"],
    "identity":{"mode":"user"},
    "limit":30
  }'
```

Plan SSE：

```bash
curl -N http://127.0.0.1:3765/v1/plans:execute \
  -H 'Content-Type: application/json' \
  -H 'Accept: text/event-stream' \
  --data-binary @examples/plan-search-fetch.json
```

OpenAPI 3.1 契约：`api/openapi.yaml`。

## 11. MCP

```bash
sfs --backend larkcli --profile work --as user mcp
```

工具：

```text
feishu_search
feishu_continue
feishu_query
feishu_fetch
feishu_expand
feishu_resolve
feishu_research
feishu_ask
feishu_get_session
feishu_capabilities
```

Agent 推荐流程：

```text
feishu_search
→ 查看 Candidate 的 projection / available_projection
→ 对 3–8 个高价值对象调用 feishu_fetch
→ 必要时 feishu_expand / feishu_continue
→ 基于 Artifact/Evidence 输出带来源答案
```

SKILL：`skills/super-feishu-search/SKILL.md`。

## 12. Go API

内核：

```go
snap, err := app.Engine.Search(ctx, kernel.SearchRequest{
    Query:         "A 项目 延期",
    SourceQueries: map[kernel.SourceID]string{kernel.SourceMinutes: "A 项目"},
    Sources:       []kernel.SourceID{kernel.SourceDocs, kernel.SourceMessages},
    Identity:      kernel.Identity{Mode: kernel.IdentityUser},
})
```

HTTP Client：

```go
c := client.New("http://127.0.0.1:3765")
snap, err := c.Search(ctx, kernel.SearchRequest{Query: "A 项目 延期"})
```

## 13. Projection 与对象模型

Candidate 是轻量命中，Artifact 是 Fetch 后的物化对象。Projection：

```text
head snippet summary structure content context relations attachments
```

例如妙记搜索只得到 `head/snippet`，随后可请求 `summary/relations`；消息 shortcut 若已经返回正文和会话上下文，Candidate 会声明已有 `content/context`，避免重复读取。

核心对象：

```text
ObjectRef    稳定引用：scope/kind/native/canonical/provider/source
Candidate    搜索候选、原生排名、融合分数、Projection
Artifact     正文块、原生摘要、元数据、附件、关系
Relation     from/type/to/confidence/provenance
Session      候选、Artifact、cursor、来源运行、预算、事件
```

## 14. 搜索算法

默认融合为 Weighted Reciprocal Rank Fusion：

```text
score = Σ source_weight / (k0 + native_rank)
```

再叠加有限的确定性信号：标题/短语命中、多来源发现与时效。各飞书来源的原生 score 不直接互相比大小。

分页采用有界 adaptive 策略：首轮并发；只对仍有 cursor 且仍可能影响结果的来源继续，始终受 `max_pages_per_source`、`max_calls`、deadline 与 byte budget 约束。

## 15. 配置与环境变量

复制配置：

```bash
cp config.example.json ~/.sfs/config.json
sfs --config ~/.sfs/config.json doctor
```

主要环境变量：

```text
SFS_BACKEND
SFS_LARKCLI
SFS_SESSION_DIR
SFS_STORAGE_TYPE
SFS_STORAGE_PATH
SFS_STORAGE_TTL
SFS_LISTEN
SFS_CONCURRENCY
SFS_OPENAPI_ENABLED
SFS_OPENAPI_BASE_URL
SFS_OPENAPI_APP_ID
SFS_OPENAPI_TOKEN
SFS_OPENAPI_TOKEN_ENV
SFS_OPENAPI_USER_OPEN_ID
SFS_OPENAPI_TIMEOUT
SFS_REPLAY_MODE
SFS_REPLAY_DIR
SFS_PLANNER
SFS_PLANNER_COMMAND
SFS_PLANNER_TIMEOUT
SFS_AI_ENABLED
SFS_AI_BASE_URL
SFS_AI_API_KEY_ENV
SFS_AI_MODEL
```

飞书 Token 和 AI API Key 不允许写入配置文件，只通过配置指定的环境变量名读取；不得写入日志、Session、fixture 或发布包。

## 16. 验证边界

自动化验证覆盖：

- Projection、ObjectRef 与 JSON 契约
- Canonical 去重、Weighted RRF、来源配额
- 多源 Search、`source_queries`、分页、Continue
- Batch Fetch、cache、并发 singleflight、共享预算
- 两跳 Expand 与关系过滤
- RetrievalPlan DAG、状态机、重试 heap、跨节点共享预算
- SQLite/JSON Session 持久化、TTL、重启恢复、并发与幂等迁移
- lark-cli 版本、命令面、关键 flag、参数/profile 传播、envelope 与 raw JSON 兼容
- direct OpenAPI 官方 SDK 请求、分页、响应大小限制和错误映射
- Research/Ask 的服务端 SSE 阶段、RetrievalEvent、唯一 result/error 终态和 Web 流解析
- AI JSON Schema、Planner 修复/回退、Rerank 降级、Citation 完整性
- Provider record/replay 与 operation route
- rules/default/external Planner
- HTTP/Web、MCP、Go Client

真实飞书 live 调用无法由 Mock/单元测试替代。当前 lark-cli 真实验证只有 7 个来源成功，calendar/mail 被 `missing_scope` 阻塞，Base/Sheets 缺测试对象；direct OpenAPI 因没有同时可用的 App ID 与 user access token 未做真实验收。是否能读到具体对象，最终取决于用户 Token、应用 scope、租户配置、对象权限及目标 `lark-cli` 版本。

## 17. GitHub Actions CI/CD 与开发者验证

仓库内置：

- `CI`：PR 与 `main` exact-head 的格式、Vet、单元/竞态测试、coverage、文档、Mock、Web、Playwright、平台原生构建和 Docker；
- `Security`：每个 PR、`main`、merge queue 和每周定时的 NPM audit、Govulncheck 与 CodeQL；
- `Release`：只接受属于 `main` 的稳定 `vX.Y.Z` Tag，自动构建六平台二进制、源码包和 SHA-256；workflow 拒绝覆盖已存在的同名 Release，但未启用 GitHub 平台级 immutable；
- `Live Smoke`：仅允许 `main`，在受保护 Environment 与受控 self-hosted runner 上执行真实租户只读验收；
- Dependabot：Go Module、NPM、GitHub Actions 与 Docker 更新；
- Issue/PR Template、安全策略、贡献指南和发布脚本。

正式交付事实以 [Actions](https://github.com/BlueSkyXN/Feishu-SuperSearch/actions) 中具体 commit 的终态和 [Releases](https://github.com/BlueSkyXN/Feishu-SuperSearch/releases) 中实际资产为准。`pending`、`cancelled`、不同 SHA 或本地测试都不能替代 exact-head 成功记录。

源码贡献者需要 Go 1.25+、Node.js 22.22.2+（或 24.15.0+/26+）、Python 3、Ruby、ShellCheck 和 Chromium。维护者本地入口：

```bash
make verify
make coverage
make web-install
make web-test
make http-smoke
```

普通使用者不需要执行这些命令，也不需要运行 `make package`；正式二进制只从 GitHub Release 下载。

详细说明见 [docs/GITHUB_ACTIONS.md](docs/GITHUB_ACTIONS.md) 和 [docs/RELEASING.md](docs/RELEASING.md)。

## 18. 目录

```text
adapter/       lark-cli、OpenAPI、mock、exec Provider、replay
api/           OpenAPI 3.1 与 JSON Schema
client/        Go HTTP Client
cmd/sfs/       主程序
examples/      Plan、配置、外部 Planner/Provider
internal/      Engine、Budget、Fusion、Session、Plan Executor、Research
kernel/        公共领域模型和接口
planner/       rules、default、external-command
provider/      Registry 与 source/operation 路由
skills/        Agent SKILL
synthesis/     Candidate/Artifact/Evidence Pack
transport/     CLI、HTTP、MCP
web/           React/TypeScript/Vite Web 唯一源码
```

完整设计：`docs/SuperFeishuSearch_完整技术设计_v1.md`。实现状态：`docs/IMPLEMENTATION_STATUS.md`。

仓库整体采用 GPL-3.0；来源材料及正式运行时依赖的许可证、版本与归属保存在 `LICENSES/` 与 `THIRD_PARTY_NOTICES.md`。`make license-check` 会拒绝未登记的 Go 运行时模块、Web 生产依赖、版本漂移或许可证文件变更，GitHub Release 运行包携带完整 `LICENSES/` 目录。
