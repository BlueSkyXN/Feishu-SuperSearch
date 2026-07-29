# SuperFeishuSearch Architecture

## 1. 模块边界

```text
CLI / HTTP / MCP / Web / Client SDK / SKILL
                       │
                       ▼
       Planner / Research / Rerank / Ask
      rules / fixed / command / optional AI
                       │
           SearchRequest / RetrievalPlan
                       ▼
┌────────────────────────────────────────────┐
│              Retrieval Kernel              │
│ Search / Query / Fetch / Expand / Continue │
│ Session / Projection / Budget / Plan Exec  │
│ Canonicalize / Dedup / Fusion / Pagination │
└───────────────────┬────────────────────────┘
                    │ Provider Ports
        ┌───────────┼────────────┬───────────┐
        ▼           ▼            ▼           ▼
    lark-cli    direct OAPI     mock      exec/replay
```

依赖方向：

```text
transport / planner / synthesis / client
                  ↓
               kernel
                  ↑
adapter / provider / internal engine
```

`kernel` 不依赖 Cobra、HTTP handler、MCP、LLM SDK、`os/exec`、SQLite 或具体飞书 SDK。外部策略只能通过结构化请求或 `retrieval-plan/v1` 控制内核。

## 2. 核心对象

| 对象 | 含义 |
|---|---|
| `ObjectRef` | 稳定引用；包含 scope、kind、native ID、canonical ID、provider/source |
| `Candidate` | 轻量搜索命中；带域内排名、融合分数、Projection 与 provenance |
| `Artifact` | Fetch 后的正文块、原生摘要、元数据、关系、附件 |
| `Relation` | `from → type → to` 的有向关系 |
| `RetrievalSession` | 候选、Artifact、cursor、来源运行状态、预算、事件 |
| `RetrievalPlan` | 版本化、有界、可校验的 DAG |

Candidate 与 Artifact 分离，支持 Late Materialization：先广召回 ID/摘要，只有被选择的对象才补正文。

## 3. Provider Registry 与路由

Registry 同时支持：

1. 每个来源的默认 Provider；
2. `source + operation` 的显式 Provider 路由；
3. `ObjectRef.ProviderID` 指定的来源保持性；
4. 同来源多个实现的 fallback。

选择优先级：

```text
显式 source/operation route
→ ObjectRef.ProviderID（Fetch/Expand）
→ 该来源默认 preferred Provider
→ 该来源其他兼容 Provider
```

示例：

```text
docs/search → openapi.docs
docs/fetch  → larkcli.docs
```

启动时并发执行 capability handshake。只有 `unsupported` 或 `version_incompatible` 会在操作开始前跳过首选实现并选择已验证备用实现；`missing_scope` 和 `identity_required` 不自动 fallback，避免掩盖真实权限错误。Hybrid 不注册 Mock。

## 4. Search 流水线

```text
Validate / Defaults
→ Resolve enabled sources and providers
→ Compile provider-specific request
→ Concurrent first-page fan-out
→ Normalize / Canonicalize
→ Exact dedup / provenance merge
→ Weighted RRF
→ Source quota / Top-K
→ Adaptive second-stage pagination
→ Persist session/frontiers
→ SearchSnapshot
```

`SearchRequest.Query` 是原始全局查询；`SourceQueries` 只覆盖特定来源的上游 query。融合排序仍可使用原始问题，避免为某个 API 截短后丢失用户意图。

每个来源拥有独立：

- timeout；
- cursor；
- page count；
- status/error；
- provider identity；
- call/byte budget。

单个来源失败不会取消其他来源。

## 5. Fusion

默认 Weighted RRF：

```text
rrf(candidate) = Σ weight(source) / (k0 + source_rank)
```

随后叠加有限、确定性的 lexical/recency/multi-source 信号。原生 score 只保留在 provenance，不作为跨来源可比较值。

去重优先：

```text
canonical ID
→ native ID + kind + scope
→ normalized URL/token
→ stable content fallback
```

同对象从多个来源被发现时合并 `DiscoveredBy`，并保留完整 provenance。

## 6. Pagination

- `none`：只取第一页；
- `fixed`：按 `max_pages_per_source` 固定继续；
- `adaptive`：首轮后仅继续有 cursor、仍可贡献 Top-K 的来源。

所有策略受共享 `SearchBudget` 约束。`Continue` 复用 session 中的 provider cursor，不重跑第一页。

## 7. Fetch 流水线

```text
Canonicalize refs
→ Merge duplicate object/projection requests
→ Resolve cached Projection from Session
→ Subtract already materialized Projection
→ Choose Provider per object/operation
→ Group by Provider and MaxFetchItems
→ Bounded concurrent batches
→ Context-aware singleflight
→ Normalize / Merge Artifact
→ Add bytes/fetches to shared budget
→ Persist session
```

singleflight key 包含：

```text
provider ID + provider version + identity scope + canonical object + projection
```

因此不会把不同 Provider 或不同身份范围的结果误合并。

## 8. Expand

Expand 使用有界 BFS：

```text
frontier[depth=0]
→ provider Expand
→ relation type filter
→ normalize relation refs
→ visited set
→ next frontier
```

程序上限 `max_depth=2`，并受 `max_expanded_nodes`、call/byte/deadline 约束。关系存入 Session，但执行图和业务关系图保持分离。

## 9. RetrievalPlan Executor

支持节点：

```text
resolve search query fetch expand merge dedup rank limit project map_fetch
```

执行器包含：

- DAG 与 cycle 校验；
- 200 节点上限；
- `PLANNED/BLOCKED/READY/RUNNING/...` 状态机；
- ready max-heap；
- retry min-heap；
- 有限指数退避；
- 整个 DAG 共享的原子预算账本；
- 部分依赖容忍与 optional node；
- NDJSON/SSE event stream。

Plan 不允许任意 shell、动态代码、无界 `while` 或内嵌模型节点。LLM 只能在内核外生成一份受 Schema 约束的 Plan。

## 10. Session

Session 保存：

```text
scope_key
original SearchRequest
Candidates / Artifacts / Relations
Provider frontiers/cursors
SourceRuns
BudgetState
RetrievalEvents
```

存储实现：

- Memory Store；
- SQLite Store（默认），纯 Go、WAL、事务 schema migration、TTL、并发与重启恢复；
- JSON File Store，作为 v1 配置兼容和迁移来源；
- `migrate sessions` 幂等导入，不删除源文件。

复用 Session 时必须匹配 identity `ScopeKey`。

## 11. Adapter

### lark-cli

- `exec.CommandContext`；
- 参数数组，不使用 shell；
- profile/as/format 明确传递；
- stdout/stderr 上限；
- envelope/raw JSON 兼容；
- 错误分类为 scope、identity、rate limit、transient、permanent、parse、deadline。

### direct OpenAPI

当前直接实现 docs Search/Fetch、messages Search/Fetch/Expand、people Search/Resolve、minutes Search/Fetch。高频正式 binding 使用固定的官方 `oapi-sdk-go/v3`，请求逐次注入 user access token；`docs_ai` XML/citation Fetch 作为 SDK 未覆盖的窄 HTTP fallback。SDK 与 fallback 都受同一超时、响应大小和错误分类边界约束。

### executable Provider

One-shot JSON-RPC 2.0 over stdio。宿主验证 descriptor、方法、envelope、响应上限、ObjectRef、Projection 与分页一致性；进程崩溃不污染 Kernel 地址空间。

### record/replay

在 Provider 边界录制 descriptor、请求、结果或错误。回放 key 去掉临时 session ID，保留 identity scope。

## 12. Planner 与 Synthesis

Planner 负责策略：

```text
选择来源
query rewrite/source_queries
是否深读
生成 RetrievalPlan
```

Kernel 负责机制：

```text
能力检查
执行
并发/重试/预算
分页/去重/融合
Fetch/Expand/Session
```

Synthesis 将候选和 Artifact 转成带稳定 ID 与 source span 的 Evidence Pack。`research` 到此为止并产生确定性 digest。

AI 位于 Kernel 外，通过三个端口接入：

```text
PlannerModel → 生成受限 Plan，严格校验，最多修复一次，失败回退 rules
Reranker    → 只重排确定性 Top 30，失败保留原排序
Answerer    → 只读取 Evidence Pack，所有 claim 必须引用真实 evidence_id
```

AI 默认关闭；模型故障不会使 Search 或 Research 不可用。
