# SuperFeishuSearch 完整技术设计 v1.0

> 定位：以确定性检索内核为核心，向外扩展规则 Planner、算法 Planner、LLM Planner、SKILL、MCP、HTTP API 与 Web。
>
> 核心原则：**内核负责检索机制，Planner 负责检索策略，Synthesis 负责总结与回答。**

---

## 0. 执行摘要

SuperFeishuSearch 不应只是“一次并发调用多个飞书搜索接口”，也不应一开始成为一个无限自治的 Agent。

推荐将系统拆成三层：

1. **SFS Retrieval Kernel**
   - 稳定、确定性、无 LLM 依赖；
   - 提供 Search、Query、Fetch、Expand、Continue；
   - 负责 Provider 能力、并发、分页、重试、归一化、Canonical ID、去重、融合排序、Session 与显式 RetrievalPlan 执行。

2. **SFS Planner**
   - 可替换；
   - 可以是固定规则、普通算法、LLM、SKILL 驱动的外部 Agent，或固定业务 Workflow；
   - 负责自然语言理解、选源、查询改写、决定 Fetch 哪些对象、是否进行第二轮检索；
   - 输出结构化 SearchRequest 或 RetrievalPlan。

3. **SFS Synthesis**
   - 可选；
   - 将 Fetch 后的 Artifact 整理成 Evidence Pack；
   - 执行抽取、跨来源总结、问答和引用组织；
   - 不进入 Retrieval Kernel。

推荐语言与形态：

- 核心：Go；
- 本地飞书接入：`lark-cli` 受控子进程 Adapter；
- 后续高频路径：官方 OpenAPI Go SDK Adapter；
- 扩展 Provider：JSON-RPC over stdio 的独立进程；
- 公共入口：Go API、CLI、HTTP/SSE、MCP；
- Web 和 SKILL：只调用公共契约，不直接拼接底层命令。

---

# 1. 产品目标与边界

## 1.1 产品目标

SuperFeishuSearch 要解决四类问题：

1. **统一找对象**
   - 一次请求搜索文档、消息、群、联系人、妙记、会议、日程、任务、邮件等来源。

2. **统一读对象**
   - 搜索通常只拿到 ID、标题或 snippet；
   - 系统必须能够按对象类型继续读取正文、上下文、原生摘要、任务状态、会议关联等内容。

3. **支持多步检索**
   - 第一轮广召回；
   - 结果融合；
   - 定向分页或补搜；
   - 选择性 Fetch；
   - 一跳或两跳 Expand。

4. **为算法和 LLM 提供稳定底座**
   - 上层可以替换选源、重排、Fetch 选择和停止策略；
   - 内核的 Provider、对象、结果和执行接口不随 Planner 改动。

## 1.2 明确不做

第一阶段不做：

- 复刻飞书内部全量索引；
- 全公司离线镜像；
- 默认建立向量数据库；
- 在内核中内置 LLM；
- 把所有 OpenAPI endpoint 都当成独立搜索源；
- 无限循环、自主反思式 Agent；
- 通用 BPM/工作流平台；
- 使用 Go plugin 或 Rust dylib 作为公共扩展 ABI。

---

# 2. 总体架构

```text
┌────────────────────────────────────────────────────────────┐
│                    Product / Transport                     │
│       CLI · HTTP/SSE · MCP · Web · SDK · SKILL            │
└────────────────────────────┬───────────────────────────────┘
                             │
                             ▼
┌────────────────────────────────────────────────────────────┐
│                    Planner / Orchestrator                  │
│ Rule Planner · Algorithm Planner · LLM Planner · Workflow │
└────────────────────────────┬───────────────────────────────┘
                             │ SearchRequest / RetrievalPlan
                             ▼
┌────────────────────────────────────────────────────────────┐
│                    Retrieval Kernel                        │
│                                                            │
│ Capability Registry · Query Compile · Plan Executor        │
│ Fan-out · Pagination · Retry · Budget · Session            │
│ Normalize · Canonicalize · Dedup · Rank Fusion             │
│ Fetch/Hydration · Expand · Cursor · Provenance             │
└────────────────────────────┬───────────────────────────────┘
                             │ Provider Ports
             ┌───────────────┼────────────────┐
             ▼               ▼                ▼
┌──────────────────┐ ┌────────────────┐ ┌───────────────────┐
│ lark-cli Adapter │ │ OpenAPI Adapter│ │ External Provider │
│ subprocess       │ │ official SDK   │ │ JSON-RPC / stdio  │
└──────────────────┘ └────────────────┘ └───────────────────┘
```

依赖方向：

```text
transport  ─┐
planner    ─┼──> kernel
synthesis  ─┘

adapter ───────> kernel/provider ports
kernel ────────X planner / LLM / Cobra / HTTP handler
```

---

# 3. 内核边界

## 3.1 属于 Retrieval Kernel

- Provider 注册与能力发现；
- Search / Query / Fetch / Expand / Continue；
- 统一请求校验；
- Provider-specific 参数编译；
- 并发、超时、取消、重试；
- 分页 Cursor；
- 结果归一化；
- Canonical ID；
- 去重与来源合并；
- Rank Fusion；
- 来源配额与 Top-K；
- Projection/完整度；
- 批量 Fetch；
- singleflight；
- Session/Object Store；
- 显式 RetrievalPlan 的 DAG 执行；
- 事件流；
- 部分来源失败的结构化返回。

## 3.2 不属于 Retrieval Kernel

- 自然语言任务拆解；
- “为什么延期”需要什么证据的判断；
- 同义词和语义 Query Rewrite；
- LLM 语义重排；
- LLM 总结；
- 最终问答；
- SKILL 提示词；
- Web 展示；
- 无限补搜决策。

## 3.3 边界原则

```text
内核负责：如何可靠执行。
Planner 负责：为什么执行这些操作。
Synthesis 负责：如何从检索事实形成答案。
```

---

# 4. 核心检索原语

内核定义六类一等操作。

| 操作 | 语义 | 示例 |
|---|---|---|
| `Resolve` | 名称、URL、别名解析成对象引用 | “张三” → open_id；文档 URL → token |
| `Search` | 全局或域内关键词相关性召回 | 搜文档、消息、妙记 |
| `Query` | 结构化过滤或对象内查询 | 未完成任务、Base 记录、Sheets 单元格 |
| `Fetch` | 已知对象 ID 后补充内容 | 文档正文、消息上下文、妙记摘要 |
| `Expand` | 沿关系发现关联对象 | Meeting → Minute；Message → Document |
| `Continue` | 使用 Session/Cursor 继续分页或指定来源 | 继续消息第二页 |

`Summarize` 不作为内核通用操作。

例外：Provider 原生返回的摘要属于 Projection：

```text
Fetch(minute_ref, projection=summary)
```

跨对象归纳总结属于 Synthesis。

---

# 5. 领域数据模型

## 5.1 ObjectRef

```go
type ObjectRef struct {
    Platform   string     `json:"platform"`
    ScopeKey   string     `json:"scope_key"`
    Kind       ObjectKind `json:"kind"`
    NativeID   string     `json:"native_id"`
    CanonicalID string    `json:"canonical_id"`

    ProviderID string     `json:"provider_id"`
    URL        string     `json:"url,omitempty"`
}
```

说明：

- `ScopeKey` 隔离租户与身份空间；
- `CanonicalID` 用于跨来源合并；
- `NativeID` 保留 Provider 原始 ID；
- 同一对象可有多个 Provider alias。

建议 Canonical ID 形式：

```text
feishu:<scope-hash>:<kind>:<native-id>
```

不应只用裸 token，以免不同身份/租户缓存混用。

## 5.2 ProjectionSet

Projection 描述对象已经物化了哪些部分，不使用单一“详情等级”。

```go
type Projection uint64

const (
    ProjectionHead Projection = 1 << iota
    ProjectionSnippet
    ProjectionSummary
    ProjectionStructure
    ProjectionContent
    ProjectionContext
    ProjectionRelations
    ProjectionAttachments
)
```

外部 JSON 使用字符串数组：

```json
["head", "snippet", "context"]
```

典型对象：

```text
文档搜索命中：
    current = head + snippet
    available = structure + content + relations + attachments

妙记搜索命中：
    current = head
    available = summary + structure + content + relations

消息增强搜索命中：
    current = head + snippet + content + context
    available = relations + attachments
```

## 5.3 Candidate

```go
type Candidate struct {
    Ref ObjectRef `json:"ref"`

    Source SourceID   `json:"source"`
    Kind   ObjectKind `json:"kind"`

    Title   string `json:"title"`
    Snippet string `json:"snippet,omitempty"`
    URL     string `json:"url,omitempty"`

    Timestamp *time.Time `json:"timestamp,omitempty"`
    Actors    []Actor    `json:"actors,omitempty"`
    Container *Container `json:"container,omitempty"`

    NativeRank  int      `json:"native_rank"`
    NativeScore *float64 `json:"native_score,omitempty"`
    FusedScore  float64  `json:"fused_score"`

    Projection          ProjectionSet `json:"projection"`
    AvailableProjection ProjectionSet `json:"available_projection"`

    RelationHints []RelationHint `json:"relation_hints,omitempty"`
    DiscoveredBy  []Discovery    `json:"discovered_by"`
    Provenance    Provenance     `json:"provenance"`
}
```

Candidate 是 Search 的核心产物，不是最终答案。

## 5.4 Artifact

```go
type Artifact struct {
    Ref        ObjectRef    `json:"ref"`
    Projection ProjectionSet `json:"projection"`

    Metadata map[string]any `json:"metadata,omitempty"`
    Chunks   []ContentChunk `json:"chunks,omitempty"`
    Summary  *NativeSummary `json:"summary,omitempty"`
    Relations []Relation    `json:"relations,omitempty"`
    Attachments []Attachment `json:"attachments,omitempty"`

    Version    string     `json:"version,omitempty"`
    Provenance Provenance `json:"provenance"`
}
```

## 5.5 Relation

```go
type Relation struct {
    From ObjectRef `json:"from"`
    Type string    `json:"type"`
    To   ObjectRef `json:"to"`

    Confidence float64    `json:"confidence"`
    Provenance Provenance `json:"provenance"`
}
```

首批关系：

```text
meeting.has_minute
minute.belongs_to_meeting
message.links_to_document
message.in_chat
document.in_wiki_space
task.assigned_to
event.has_attendee
artifact.authored_by
```

## 5.6 Provenance

所有候选和内容必须带来源：

```go
type Provenance struct {
    ProviderID  string    `json:"provider_id"`
    Backend     string    `json:"backend"`
    Operation   string    `json:"operation"`
    RetrievedAt time.Time `json:"retrieved_at"`

    SourceRank  int       `json:"source_rank,omitempty"`
    RawRef      any       `json:"raw_ref,omitempty"`
}
```

---

# 6. Provider 能力模型

不要求所有 Provider 实现所有操作，使用小接口组合。

```go
type Provider interface {
    Descriptor() ProviderDescriptor
}

type Searcher interface {
    Search(ctx context.Context, req ProviderSearchRequest) (CandidatePage, error)
}

type Querier interface {
    Query(ctx context.Context, req ProviderQueryRequest) (CandidatePage, error)
}

type Fetcher interface {
    Fetch(ctx context.Context, req []ProviderFetchRequest) ([]Artifact, error)
}

type Expander interface {
    Expand(ctx context.Context, req ProviderExpandRequest) (RelationPage, error)
}

type Resolver interface {
    Resolve(ctx context.Context, req ProviderResolveRequest) ([]ObjectRef, error)
}
```

Descriptor：

```go
type ProviderDescriptor struct {
    ID           ProviderID
    Source       SourceID
    ObjectKinds  []ObjectKind
    Operations   OperationSet

    RequiredIdentity []IdentityMode
    RequiredScopes   []string

    SearchLimits SearchLimits
    BatchLimits  BatchLimits

    ReturnedProjection  ProjectionSet
    FetchableProjection ProjectionSet

    SupportsPagination bool
    SupportsStreaming  bool
}
```

## 6.1 首批 Provider 分类

### 全局型来源

第一轮可以直接接收关键词：

- docs / drive；
- messages；
- chats；
- people；
- minutes；
- meetings；
- calendar events；
- tasks；
- mail。

### 对象内或二段式来源

必须先定位容器对象：

- Base record search；
- Sheets cells search；
- Wiki 节点/文档内部；
- 消息线程；
- 指定 mailbox；
- 指定表、指定 range。

## 6.2 Provider 建议映射

| Source | Search/Query | Fetch | Expand |
|---|---|---|---|
| docs | 文档/Wiki/文件全局搜索 | metadata、structure、content、attachments | links、wiki/folder relation |
| messages | 跨会话消息搜索 | message、context/thread、attachments | chat、document links |
| chats | 群搜索 | chat metadata、members | related messages |
| people | 用户搜索/resolve | profile | chats/messages hints |
| minutes | 妙记搜索 | summary、todos、chapters、transcript | meeting、participants |
| meetings | 历史会议搜索 | meeting detail | associated minute/event |
| calendar | 日程搜索 | event detail | attendees、meeting |
| tasks | task/tasklist search/query | task detail、subtasks、comments | assignee、related item |
| mail | mail search/triage | body、thread、attachments | correspondents |
| base | record query | record detail | linked records |
| sheets | cell search/query | ranges | referenced objects |

Provider 对现有 shortcut 的返回内容必须准确声明 Projection，避免重复 Fetch。

---

# 7. Retrieval Kernel 公共接口

```go
type Kernel interface {
    Capabilities(
        ctx context.Context,
        req CapabilityRequest,
    ) (CapabilitySnapshot, error)

    Search(
        ctx context.Context,
        req SearchRequest,
    ) (SearchSnapshot, error)

    Continue(
        ctx context.Context,
        req ContinueRequest,
    ) (SearchSnapshot, error)

    Query(
        ctx context.Context,
        req QueryRequest,
    ) (QuerySnapshot, error)

    Fetch(
        ctx context.Context,
        req FetchBatchRequest,
    ) (ArtifactBatch, error)

    Expand(
        ctx context.Context,
        req ExpandRequest,
    ) (RelationBatch, error)

    Execute(
        ctx context.Context,
        plan RetrievalPlan,
    ) (<-chan RetrievalEvent, error)
}
```

`Search` 是默认检索计划的便捷入口：

```text
Search(req)
= Execute(DefaultSearchPlan(req))
```

---

# 8. SearchRequest 与 SearchSnapshot

## 8.1 SearchRequest

```go
type SearchRequest struct {
    Query   string     `json:"query"`
    Sources []SourceID `json:"sources,omitempty"`

    Filters SearchFilters `json:"filters,omitempty"`
    Identity Identity     `json:"identity"`

    Limit          int `json:"limit"`
    LimitPerSource int `json:"limit_per_source"`

    Budget   SearchBudget   `json:"budget"`
    Strategy SearchStrategy `json:"strategy"`

    SessionID string `json:"session_id,omitempty"`
}
```

示例：

```json
{
  "query": "A 项目 延期",
  "sources": [
    "docs",
    "messages",
    "minutes",
    "meetings",
    "tasks"
  ],
  "filters": {
    "after": "2026-07-14T00:00:00+08:00",
    "before": "2026-07-29T00:00:00+08:00"
  },
  "identity": {
    "profile": "work",
    "mode": "user"
  },
  "limit": 40,
  "limit_per_source": 8,
  "budget": {
    "deadline_ms": 8000,
    "max_calls": 15,
    "max_pages_per_source": 2
  },
  "strategy": {
    "fusion": "weighted_rrf",
    "source_quota": true,
    "pagination": "adaptive"
  }
}
```

## 8.2 SearchSnapshot

```go
type SearchSnapshot struct {
    SessionID string `json:"session_id"`
    Query     string `json:"query"`

    Candidates []Candidate   `json:"candidates"`
    Sources    []SourceRun   `json:"sources"`
    Continuations []Continuation `json:"continuations,omitempty"`

    Partial bool `json:"partial"`

    Budget BudgetState `json:"budget"`
    Stats  SearchStats `json:"stats"`
}
```

部分失败是正常结果，不把整个请求判为失败：

```json
{
  "source": "mail",
  "status": "missing_scope",
  "hit_count": 0,
  "error": {
    "type": "missing_scope",
    "message": "..."
  }
}
```

---

# 9. Search 内核算法

一次 Search 使用确定性流水线：

```text
SearchRequest
  ↓
Validate
  ↓
Select Enabled Providers
  ↓
Compile Provider Requests
  ↓
Concurrent Fan-out
  ↓
Parse / Normalize
  ↓
Canonicalize
  ↓
Deduplicate / Merge
  ↓
Rank Fusion
  ↓
Source Quota / Top-K
  ↓
Optional Adaptive Pagination
  ↓
Persist Session
  ↓
SearchSnapshot
```

## 9.1 请求校验

校验：

- Query 与 Filter 是否符合统一模型；
- source 是否存在；
- identity 是否支持；
- limit、deadline、call budget；
- Provider 的 query 长度、时间窗和 ID 数量限制；
- 已知互斥条件。

Provider 限制由 Descriptor 与 Query Compiler 处理，不散落在 Planner。

## 9.2 Provider 选择

默认策略：

```text
sources 显式指定：
    只使用指定来源中当前可用的 Provider

sources 为空或 all：
    使用配置启用的全局 Provider
```

Provider 选择顺序：

```text
operation-specific configured backend
→ lark-cli shortcut
→ typed/raw adapter
→ direct OpenAPI adapter
→ unsupported
```

同一业务域只选一个主召回实现，不把同义 endpoint 当成多个来源。

## 9.3 Provider-specific Query Compile

内核允许确定性参数映射，不做开放式语义改写。

允许：

```text
统一 after → Provider start_time
统一 doc type → Provider enum
RFC3339 → Unix timestamp
统一 identity → --as user
```

不允许：

```text
“上线延期” → “发布推迟 OR delay”
根据问题猜测应该搜索任务
```

后者属于 Planner。

## 9.4 并发 Fan-out

初始建议：

```text
global concurrency     = 6
per-provider concurrency = 2
per-source timeout     = 3s
total search deadline  = 8s
```

实现：

- `context.Context`；
- `errgroup`；
- 全局 semaphore；
- Provider semaphore；
- 每个 SourceRun 独立状态；
- deadline 到达后返回已完成来源。

## 9.5 归一化

每个 Provider 有自己的 Normalizer：

```go
type Normalizer interface {
    NormalizeSearch(
        req ProviderSearchRequest,
        raw ProviderPage,
    ) ([]Candidate, error)
}
```

Normalizer 负责：

- 字段解析；
- 时间统一；
- ObjectKind；
- URL；
- 当前 Projection；
- 可 Fetch Projection；
- 原生 rank；
- provenance。

不在全局层直接读取 Provider 原始 JSON 字段。

---

# 10. Canonicalization 与去重

去重优先级：

```text
1. 官方对象唯一 ID
2. 标准化 URL / token
3. Provider alias 映射
4. 确定性内容指纹
5. 不确定时不合并
```

Canonicalizer：

```go
type Canonicalizer interface {
    Key(candidate Candidate) CanonicalKey
    Merge(a Candidate, b Candidate) Candidate
}
```

合并规则：

- 保留更完整的 Projection；
- 合并 `discovered_by`；
- 保留所有原始 Provider refs；
- title/snippet 选择更完整非空值；
- 时间冲突保留 provenance，不静默覆盖；
- 多来源发现可用于排名加分。

不建议第一版用 LLM 判重。

---

# 11. Rank Fusion

## 11.1 默认算法：Weighted RRF

不同 Provider 的原生 score 不可直接比较。

```text
RRF(candidate)
= Σ source_weight[source] / (K0 + native_rank)
```

建议：

```text
K0 = 60
```

最终分数：

```text
final =
  weighted_rrf
+ exact_title_bonus
+ exact_phrase_bonus
+ filter_match_bonus
+ recency_bonus
+ multi_source_bonus
+ kind_prior
```

所有 bonus 都是可配置、确定性的。

## 11.2 来源配额

避免消息等高召回来源淹没结果：

```yaml
quota:
  docs: 10
  messages: 12
  minutes: 6
  meetings: 4
  tasks: 6
  people: 4
  chats: 4
  calendar: 5
  mail: 6
```

配额应用顺序：

1. 每来源保留最少保障数；
2. 按全局 fused score 填充剩余名额；
3. 总量截断到 `limit`。

## 11.3 Top-K 数据结构

全局候选较多时使用大小为 K 的 Min-Heap：

- 堆顶是当前 Top-K 最低分；
- 新候选高于堆顶则替换；
- 最后排序输出。

第一版数据量小时普通排序也可，但接口保持一致。

---

# 12. 分页与 Continue

## 12.1 不默认翻到底

第一轮：

```text
每来源第一页
每来源 Top 5–10
总候选 30–60
```

继续分页必须受 Budget 和策略控制。

## 12.2 基础自适应分页

对每个来源维护：

```go
type SourceFrontier struct {
    Cursor           string
    HasMore          bool
    PagesFetched     int
    NewUniqueCount   int
    NewTopKCount     int
    LastPageLatency  time.Duration
}
```

继续条件：

```text
has_more
AND pages_fetched < max_pages
AND call budget remains
AND (
      last_page_new_topk > 0
      OR source is explicitly requested
      OR source has minimum quota deficit
    )
```

停止条件：

```text
上一页无新增唯一对象
或无对象进入全局 Top-K
或达到页数/时间/调用预算
```

## 12.3 高级 Top-K 稳定策略

后续可以使用排名上界：

```text
NextBound(source) = weight / (K0 + next_rank)

UnknownCandidateUpperBound
= Σ NextBound(source) + max_bonus
```

当第 K 名的已知下界高于所有未知候选上界时停止。

v1 可先实现基础策略，保持 `PaginationPolicy` 接口。

## 12.4 Continue API

```json
{
  "session_id": "rs_01...",
  "sources": ["messages", "docs"],
  "max_additional_pages": 1
}
```

Continue 使用 Session 中已保存的 Cursor，不重新执行第一页。

---

# 13. Fetch / Hydration

## 13.1 Fetch 是内核一等能力

Planner 只表达：

```json
{
  "ref": "feishu:...:minute:min_xxx",
  "projection": ["summary", "relations"]
}
```

内核负责找到 Provider、批量、去重、调用与合并。

## 13.2 FetchBatch 算法

```text
FetchBatchRequest
  ↓
Canonicalize refs
  ↓
Merge duplicate requests by ref
  ↓
Union requested projections
  ↓
Subtract already materialized projections
  ↓
Group by provider + operation + identity
  ↓
Batch-capable calls
  ↓
Controlled concurrent calls
  ↓
Normalize Artifact
  ↓
Merge into Session Object Store
  ↓
ArtifactBatch
```

伪代码：

```text
for each request:
    key = canonical(ref)
    desired[key] |= request.projection

for each key:
    missing = desired[key] - session.materialized[key]
    if missing is empty:
        return cached artifact
    else:
        enqueue provider fetch(key, missing)
```

## 13.3 singleflight

相同身份下，同一对象和 Projection 的并发 Fetch 共享一个 in-flight 调用：

```text
singleflight key
= scope_key + canonical_id + projection + provider_version
```

## 13.4 渐进物化

文档：

```text
head/snippet
→ structure
→ relevant content
→ full content
→ attachments
```

消息：

```text
hit
→ message content
→ local context
→ full thread
→ attachments / linked objects
```

妙记：

```text
head
→ native summary + todos
→ chapters
→ relevant transcript
→ full transcript
```

任务：

```text
head
→ status/assignee/due
→ description/subtasks
→ comments
```

内核不自动决定“哪个对象最值得 Fetch”；这是 Planner 的职责。

但内核可提供确定性便捷算子：

```text
HydrateTopK(kind-specific projection policy)
```

---

# 14. Query 与对象内检索

Search 与 Query 必须分开。

## Search

关键词相关性召回：

```text
docs/messages/minutes/meetings
```

## Query

结构化条件或对象内操作：

```text
tasks completed=false
Base table record filter
Sheets cell find
指定 mailbox 搜索
指定 chat history
```

QueryRequest：

```go
type QueryRequest struct {
    Source      SourceID     `json:"source"`
    Container   *ObjectRef   `json:"container,omitempty"`
    Filter      QueryFilter  `json:"filter"`
    Sort        []SortSpec   `json:"sort,omitempty"`
    Limit       int          `json:"limit"`
    Identity    Identity     `json:"identity"`
    SessionID   string       `json:"session_id,omitempty"`
}
```

示例：

```json
{
  "source": "base",
  "container": {
    "kind": "base_table",
    "native_id": "tbl_xxx"
  },
  "filter": {
    "keyword": "延期",
    "fields": ["标题", "原因"]
  },
  "limit": 50
}
```

---

# 15. Expand 与关系发现

Expand 输入已知对象，输出关联引用：

```json
{
  "ref": "feishu:...:meeting:xxx",
  "relations": ["has_minute"],
  "max_depth": 1
}
```

内核维护：

- `visited[(canonical_id, relation_type)]`；
- `max_depth`；
- `max_expanded_nodes`；
- `max_calls`；
- relation provenance。

首版只做一跳，最多两跳。

不做任意无界图遍历。

---

# 16. RetrievalSession

复杂检索必须有 Session，而不是每一步都无状态。

```go
type RetrievalSession struct {
    ID string `json:"id"`

    ScopeKey string `json:"scope_key"`
    CreatedAt time.Time
    ExpiresAt time.Time

    Candidates map[string]Candidate
    Artifacts  map[string]Artifact
    Relations  []Relation

    Frontiers map[SourceID]SourceFrontier
    SourceRuns []SourceRun

    Budget BudgetState
    Events []RetrievalEvent
}
```

Session 用途：

- 保留第一页结果；
- 保存各来源 Cursor；
- 避免重复 Fetch；
- 合并 Planner 的多轮调用；
- 记录部分失败；
- 支持 Continue；
- 支持回放测试；
- 为 Web/SSE 提供渐进结果。

建议：

- 内存 Session：短请求；
- SQLite Session：本地持久化；
- 默认 TTL 30–60 分钟；
- 正文 Artifact 可使用更短 TTL；
- Cache key 必须包含 ScopeKey。

---

# 17. RetrievalPlan

Planner 与 Kernel 的高级契约是一种受限、版本化的 DAG。

## 17.1 允许的节点

```text
resolve
search
query
fetch
expand
merge
dedup
rank
limit
project
map_fetch
```

不允许：

- 任意脚本；
- 无界 while；
- LLM 节点；
- Shell 命令；
- 动态加载代码。

## 17.2 示例

```json
{
  "version": "retrieval-plan/v1",
  "session": {
    "reuse": true
  },
  "nodes": [
    {
      "id": "search_docs",
      "op": "search",
      "request": {
        "source": "docs",
        "query": "A 项目 延期",
        "limit": 10
      }
    },
    {
      "id": "search_messages",
      "op": "search",
      "request": {
        "source": "messages",
        "query": "A 项目 延期",
        "limit": 10
      }
    },
    {
      "id": "search_minutes",
      "op": "search",
      "request": {
        "source": "minutes",
        "query": "A 项目",
        "limit": 6
      }
    },
    {
      "id": "merge",
      "op": "merge",
      "depends_on": [
        "search_docs",
        "search_messages",
        "search_minutes"
      ]
    },
    {
      "id": "rank",
      "op": "rank",
      "depends_on": ["merge"],
      "request": {
        "policy": "weighted_rrf"
      }
    },
    {
      "id": "fetch_top",
      "op": "map_fetch",
      "depends_on": ["rank"],
      "request": {
        "top_k": 8,
        "projection_by_kind": {
          "document": ["structure", "content"],
          "message": ["context"],
          "minute": ["summary", "relations"]
        },
        "max_items": 8
      }
    }
  ],
  "output": ["rank", "fetch_top"]
}
```

## 17.3 动态节点

`map_fetch` 可以根据上游结果物化有限子节点：

```text
fetch_top[0]
fetch_top[1]
...
fetch_top[7]
```

必须有 `max_items`。

分页和 Expand 循环也通过有限展开实现：

```text
max_pages
max_depth
max_rounds
```

运行图始终保持有界 DAG。

---

# 18. DAG Executor

## 18.1 节点状态机

```text
PLANNED
   ↓
BLOCKED
   ↓ dependencies satisfied
READY
   ↓
RUNNING
   ├─ SUCCEEDED
   ├─ PARTIAL
   ├─ EMPTY
   ├─ RETRY_WAIT ──→ READY
   ├─ FAILED
   ├─ SKIPPED
   └─ CANCELLED
```

## 18.2 队列与堆

| 数据结构 | 作用 |
|---|---|
| Ready Max-Heap | 选择下一批可运行节点 |
| Retry Min-Heap | 按 `next_retry_at` 调度重试 |
| Top-K Min-Heap | 维护全局候选 |
| Source Frontier | 管理每来源分页状态 |
| In-flight Map | singleflight 与取消 |
| Visited Set | Expand 防循环 |

## 18.3 Ready 节点优先级

建议初始公式：

```text
priority =
  critical_path_bonus
+ dependency_unlock_bonus
+ user_visible_bonus
+ configured_source_priority
+ aging_bonus
- estimated_latency_penalty
- call_cost_penalty
```

不需要第一版预测复杂“信息增益”。

对于默认 Search：

- 第一页 Search 节点同优先级并发；
- Merge/Rank 在来源达到最小完成条件后执行；
- Fetch 节点由显式 Plan 或外部 Planner 生成。

## 18.4 Budget Ledger

```go
type Budget struct {
    Deadline time.Time

    MaxCalls int
    MaxPagesPerSource int
    MaxFetches int
    MaxExpandedNodes int
    MaxBytes int64
}
```

节点启动前预留预算，结束后结算。

预算耗尽：

- 不启动低优先级节点；
- 取消可取消的非关键节点；
- 返回 Partial Snapshot。

## 18.5 执行伪代码

```text
validate(plan)
session = load_or_create_session()

ready = max_heap()
retry = min_heap()
running = map()

enqueue roots

while not terminal:
    move_due_retry_nodes(retry, ready)

    while capacity_available and ready not empty:
        node = ready.pop()
        if budget.can_reserve(node.cost):
            budget.reserve(node.cost)
            dispatch(node)
        else:
            mark_skipped(node, budget_exhausted)

    event = await_next_completion_or_deadline()

    update_state(event)
    budget.settle(event)

    if event.has_candidates:
        normalize
        canonicalize
        merge session candidates
        update Top-K

    if event.has_artifacts:
        merge session object store

    materialize_bounded_dynamic_nodes(event)
    unlock_dependents(event.node)

    if deadline reached:
        cancel noncritical running nodes
        break

return snapshot(session)
```

---

# 19. 错误模型与部分成功

统一错误类型：

```text
invalid_request
unsupported
missing_scope
identity_required
not_found
rate_limited
upstream_transient
upstream_permanent
parse_error
version_incompatible
deadline_exceeded
budget_exhausted
cancelled
```

重试策略：

| 错误 | 策略 |
|---|---|
| 429 | 指数退避 + jitter，限次重试 |
| 502/503/504 | 限次重试 |
| missing_scope | 不重试 |
| identity_required | 尝试配置的兼容实现，否则失败 |
| invalid_request | 不重试 |
| parse/version | 禁用该 Adapter 实例 |
| deadline | 返回部分结果 |

Search 顶层只有在以下情况整体失败：

- 请求本身无效；
- 所有指定来源均不可用且无候选；
- Session 无效且无法恢复；
- 内核内部不可恢复错误。

单个来源失败不应抹掉其他来源结果。

---

# 20. lark-cli Adapter

## 20.1 为什么首版使用子进程

当前 `lark-cli` 已经提供：

- shortcut；
- typed command；
- raw API；
- user/bot 身份；
- scope/auth；
- JSON 输出；
- 分页；
- 部分 shortcut 内部 enrichment。

首版复用这些行为，避免重写所有业务规则。

## 20.2 Runner

```go
type CommandRunner interface {
    Run(
        ctx context.Context,
        spec CommandSpec,
    ) (CommandResult, error)
}

type CommandSpec struct {
    Executable string
    Args       []string
    Env        []string
    Stdin      []byte

    Timeout     time.Duration
    StdoutLimit int64
    StderrLimit int64
}

type CommandResult struct {
    ExitCode int
    Stdout   []byte
    Stderr   []byte
    Duration time.Duration
}
```

要求：

- 不使用 `sh -c`；
- 参数使用数组；
- 固定命令模板；
- `--format json`；
- context cancellation；
- stdout/stderr 大小限制；
- 退出码与 envelope 同时解析；
- Provider Parser 单独版本化。

## 20.3 Capability Handshake

启动或配置变更时：

```text
lark-cli version
lark-cli auth status
必要命令 help/schema 探测
```

记录：

```json
{
  "backend": "lark-cli",
  "version": "1.0.79",
  "providers": {
    "docs.search": "available",
    "messages.search": "missing_scope"
  }
}
```

不能按某个固定版本的输出结构永久硬编码。

## 20.4 Provider 与 Backend 解耦

例如：

```text
DocsProvider.Search
  backend = lark-cli drive +search

DocsProvider.Fetch
  backend = lark-cli docs +fetch

后续：
DocsProvider.Search
  backend = OpenAPI SDK
```

上层接口不变。

---

# 21. OpenAPI Adapter

当满足以下条件时，将单个操作迁移为直接 SDK：

- 子进程启动成本成为瓶颈；
- 高频操作需要连接复用；
- 需要批量、流式或更细 Projection；
- 多用户服务端；
- shortcut 返回字段不足；
- CLI 版本兼容成本较高。

迁移按操作，而不是一次重写所有 Provider：

```yaml
providers:
  docs:
    search_backend: openapi
    fetch_backend: larkcli

  messages:
    search_backend: larkcli
    fetch_backend: openapi
```

---

# 22. 外部 Provider 协议

公共扩展机制：

```text
JSON-RPC 2.0 over stdio
```

方法：

```text
initialize
capabilities
search
query
fetch
expand
cancel
health
shutdown
```

优点：

- Go、Rust、Python、TypeScript 均可实现；
- Provider 崩溃不带崩核心；
- 易于超时、取消、录制和回放；
- 不依赖 Go/Rust 不稳定 ABI。

第一版不需要 gRPC。

---

# 23. Planner 层

Planner 是可替换控制面。

## 23.1 Default Planner

无 LLM：

```text
显式 query
→ 所有启用的全局来源
→ 每来源一页
→ Weighted RRF
→ 返回 Candidate
```

适合 `sfs search`。

## 23.2 Rule Planner

依据简单规则选源：

```text
有人名过滤 → people resolve
有 completed=false → tasks query
有会议/评审词 → meetings + minutes
有邮箱限制 → mail
```

## 23.3 Algorithm Planner

依据：

- 历史命中率；
- 延迟；
- 当前 scope；
- 查询类型；
- 来源覆盖；
- 调用预算；

决定来源和页数。

## 23.4 LLM Planner

负责：

- 自然语言转结构化请求；
- 查询拆解；
- source-specific query rewrite；
- 从 Candidate Pack 选择 Fetch；
- 判断是否需要第二轮搜索；
- 输出受 Schema 限制的 RetrievalPlan。

LLM 不直接执行 shell，不直接管理 retry，不直接解析原始 OpenAPI JSON。

## 23.5 SKILL Planner

SKILL 只描述：

```text
何时调用 search
如何查看 Candidate
何时调用 fetch
何时调用 continue/query/expand
```

SKILL 不编排十几个底层 `lark-cli` 命令。

---

# 24. Synthesis 层

Synthesis 输入 Artifact，不输入原始 Provider JSON。

## 24.1 Candidate Pack

给 LLM 做语义选择：

```json
{
  "query": "A 项目为什么延期？",
  "candidates": [
    {
      "id": "feishu:...:message:om_xxx",
      "kind": "message",
      "source": "messages",
      "title": "A 项目讨论群",
      "snippet": "测试环境交付延迟……",
      "timestamp": "...",
      "available_projection": ["context", "attachments"]
    }
  ]
}
```

## 24.2 Artifact Pack

Fetch 后：

```json
{
  "artifacts": [
    {
      "id": "feishu:...:minute:min_xxx",
      "kind": "minute",
      "projection": ["summary", "relations"],
      "summary": {
        "text": "...",
        "todos": []
      },
      "provenance": {}
    }
  ]
}
```

## 24.3 Evidence Pack

由 Synthesis 将 Artifact 转成可引用证据：

```json
{
  "evidence": [
    {
      "id": "ev_1",
      "claim_type": "delay_reason",
      "text": "测试环境交付比计划晚三天",
      "source_ref": "feishu:...:minute:min_xxx",
      "source_span": "...",
      "timestamp": "...",
      "confidence": 0.91
    }
  ]
}
```

最终回答只基于 Evidence Pack。

---

# 25. HTTP API

建议：

```text
GET  /v1/capabilities
POST /v1/search
POST /v1/search/continue
POST /v1/query
POST /v1/fetch
POST /v1/expand
POST /v1/plans:execute
GET  /v1/sessions/{session_id}
GET  /v1/sessions/{session_id}/events
```

事件流：

- HTTP SSE；
- CLI NDJSON。

事件类型：

```text
session.started
node.started
source.completed
candidate.upsert
artifact.upsert
node.retrying
node.completed
session.partial
session.completed
```

---

# 26. CLI

```bash
sfs providers
sfs doctor

sfs search "A 项目 延期" \
  --sources docs,messages,minutes,tasks \
  --after 14d \
  --limit 40 \
  --format json

sfs continue rs_01... \
  --sources messages,docs \
  --pages 1

sfs fetch feishu:...:message:om_xxx \
  --projection context

sfs query tasks \
  --filter '{"completed":false,"query":"A 项目"}'

sfs expand feishu:...:meeting:xxx \
  --relation has_minute

sfs plan run plan.json \
  --format ndjson

sfs serve --listen 127.0.0.1:3765
sfs mcp
```

应用层可另加：

```bash
sfs ask "A 项目为什么延期？"
```

但 `ask` 不属于 Kernel package。

---

# 27. MCP 与 SKILL

MCP Tools：

```text
feishu_search
feishu_continue
feishu_query
feishu_fetch
feishu_expand
```

优先保持工具原子化，让外部 Agent 控制多轮。

也可以提供一个应用层工具：

```text
feishu_research
```

它调用 Planner + Kernel + Synthesis，但不能替代原子工具。

---

# 28. 配置示例

```yaml
version: 1

runtime:
  global_concurrency: 6
  default_deadline: 8s
  session_ttl: 45m

backends:
  larkcli:
    executable: lark-cli
    profile: work
    format: json
    max_stdout_bytes: 16777216
    max_stderr_bytes: 4194304

providers:
  docs:
    enabled: true
    search_backend: larkcli
    fetch_backend: larkcli
    weight: 1.2
    quota: 10
    timeout: 3s

  messages:
    enabled: true
    search_backend: larkcli
    fetch_backend: larkcli
    weight: 1.2
    quota: 12
    timeout: 4s

  minutes:
    enabled: true
    weight: 1.1
    quota: 6

  meetings:
    enabled: true
    weight: 0.9
    quota: 4

  tasks:
    enabled: true
    weight: 1.0
    quota: 6

  mail:
    enabled: false

policies:
  fusion:
    type: weighted_rrf
    k0: 60

  pagination:
    type: adaptive
    max_pages_per_source: 2

  fetch:
    max_batch_items: 20
    max_concurrency: 6
```

---

# 29. Go 工程结构

```text
super-feishu-search/
├── cmd/
│   └── sfs/
│       └── main.go
│
├── kernel/
│   ├── kernel.go
│   ├── request.go
│   ├── candidate.go
│   ├── artifact.go
│   ├── projection.go
│   ├── relation.go
│   ├── capability.go
│   ├── session.go
│   └── plan.go
│
├── internal/
│   ├── engine/
│   │   ├── search.go
│   │   ├── query.go
│   │   ├── fetch.go
│   │   ├── expand.go
│   │   └── continue.go
│   │
│   ├── executor/
│   │   ├── dag.go
│   │   ├── scheduler.go
│   │   ├── state.go
│   │   ├── budget.go
│   │   └── retry.go
│   │
│   ├── canonical/
│   ├── normalize/
│   ├── fusion/
│   │   ├── rrf.go
│   │   ├── quota.go
│   │   └── topk.go
│   │
│   ├── hydration/
│   │   ├── batch.go
│   │   └── singleflight.go
│   │
│   ├── session/
│   │   ├── memory.go
│   │   └── sqlite.go
│   │
│   └── events/
│
├── provider/
│   ├── provider.go
│   ├── registry.go
│   ├── docs/
│   ├── messages/
│   ├── chats/
│   ├── people/
│   ├── minutes/
│   ├── meetings/
│   ├── calendar/
│   ├── tasks/
│   ├── mail/
│   ├── base/
│   └── sheets/
│
├── adapter/
│   ├── larkcli/
│   │   ├── runner.go
│   │   ├── envelope.go
│   │   ├── capability.go
│   │   └── parsers/
│   ├── openapi/
│   └── execprovider/
│
├── planner/
│   ├── default/
│   ├── rules/
│   ├── algorithm/
│   └── llm/
│
├── synthesis/
│   ├── candidatepack/
│   ├── evidence/
│   └── answer/
│
├── transport/
│   ├── cli/
│   ├── http/
│   └── mcp/
│
├── api/
│   ├── openapi.yaml
│   ├── retrieval-plan.schema.json
│   └── provider-protocol.schema.json
│
├── skills/
│   └── super-feishu-search/
│       └── SKILL.md
│
├── web/
├── testdata/
│   └── lark-cli/
│       ├── 1.0.78/
│       └── 1.0.79/
│
└── go.mod
```

---

# 30. 完整运行示例

用户问题：

```text
总结最近两周 A 项目延期原因、最终决策人，以及尚未完成的任务。
```

## 30.1 LLM/Rule Planner

生成：

```text
Search docs:     "A 项目 延期"
Search messages: "A 项目 延期"
Search minutes:  "A 项目"
Search meetings: "A 项目"
Query tasks:     query="A 项目", completed=false
```

## 30.2 Kernel 第一波

并发执行：

```text
docs search
messages search
minutes search
meetings search
tasks query
```

内核：

- 统一 Candidate；
- Canonical ID；
- 去重；
- Weighted RRF；
- 来源配额；
- 返回 35 个候选和 Session ID。

## 30.3 Planner 选择 Fetch

Planner 只看 Candidate Pack，返回：

```text
3 条消息 → context
2 份妙记 → summary + relations
2 份文档 → structure + content
5 个任务 → head + content
1 个会议 → relations
```

## 30.4 Kernel 第二波

并发 Fetch；会议 Expand 出关联妙记后，受限生成一个新 Fetch 节点。

Kernel 返回 Artifact Pack。

## 30.5 Synthesis

从 Artifact 提取：

```text
延期原因
最终决定
决策人
未完成任务及负责人/截止时间
```

生成带 ObjectRef/URL 的 Evidence Pack 和答案。

## 30.6 停止

若任务状态齐全、决策有正式妙记、原因有至少两类来源支持，则结束。

若“决策人”仍不清楚，Planner 最多再发一轮定向消息/妙记搜索。

Kernel 本身不自主无限循环。

---

# 31. 默认策略档位

## `fast`

```text
全局来源第一页
每源 5 条
总量 25
不自动分页
不 Fetch
deadline 4s
```

## `balanced`

```text
每源 8 条
总量 40
Adaptive 第二页
可按显式 Projection Fetch
deadline 8s
```

## `deep`

```text
每源 10 条
总量 60
最多两页
允许显式 map_fetch / expand
deadline 15s
```

档位只是 Budget 与默认 Plan，不改变内核语义。

---

# 32. 测试与可回放性

内核无 LLM，因此可做确定性测试。

## 32.1 Golden fixtures

保存各版本 `lark-cli --format json` 输出：

```text
success
empty
pagination
permission denied
rate limit
schema drift
partial enrichment
```

## 32.2 Fake Provider

```go
type FakeProvider struct {
    Pages map[string][]CandidatePage
    Artifacts map[string]Artifact
}
```

用于验证：

- fan-out；
- partial；
- RRF；
- dedup；
- Continue；
- singleflight；
- DAG；
- Budget。

## 32.3 Replay

记录 Provider 原始响应，离线重放整次 Session，区分：

```text
Provider 变化
Normalizer 变化
Fusion 算法变化
Planner 变化
```

---

# 33. 实施路线

## Phase 1：Kernel v0.1

来源：

```text
docs
messages
people
minutes
meetings
tasks
```

能力：

```text
Capabilities
Search
Fetch
Session
```

机制：

```text
fan-out
timeout
partial results
Candidate
Projection
Canonical ID
exact dedup
Weighted RRF
source quota
lark-cli runner
```

输出：

```text
Go API
CLI JSON
```

## Phase 2：Kernel v0.2

增加：

```text
Continue
adaptive pagination
Query
Base/Sheets
singleflight
SQLite session
HTTP/SSE
```

## Phase 3：Plan Executor v0.3

增加：

```text
RetrievalPlan v1
DAG
state machine
ready/retry heap
map_fetch
Expand one hop
```

## Phase 4：Planner / Synthesis v0.4

增加：

```text
Rule Planner
LLM Planner
Candidate Pack
Evidence Pack
MCP
SKILL
ask/research
```

## Phase 5：OpenAPI / Performance v0.5

将高频操作逐步迁移为直接 SDK：

```text
docs search
messages search/fetch
people resolve
minutes fetch
```

---

# 34. 首版验收标准

1. 同一 SearchRequest 在固定 fixtures 下输出确定；
2. 任一来源失败时其余来源仍返回；
3. 候选均有 Canonical ID、Projection、Provenance；
4. 同一对象跨来源出现时可合并；
5. 不比较不同来源的原生 score；
6. Continue 不重跑第一页；
7. Fetch 不重复读取已物化 Projection；
8. 同一 Fetch 并发请求被 singleflight 合并；
9. Planner 可以完全不使用 LLM；
10. LLM Planner 可以只通过公共 Search/Fetch/Plan 契约接入；
11. SKILL 不直接拼底层命令；
12. Provider Backend 可从 lark-cli 单独切换成 OpenAPI，而不改 Kernel API。

---

# 35. 最终架构决策

```text
核心语言：
    Go

产品本体：
    Retrieval Kernel

核心公共原语：
    Resolve / Search / Query / Fetch / Expand / Continue

核心数据模型：
    ObjectRef / Candidate / Projection / Artifact / Relation / Session

核心算法：
    Provider fan-out
    Canonicalization
    Dedup
    Weighted RRF
    Source quota
    Adaptive pagination
    Batch hydration
    singleflight
    Bounded DAG execution

执行模型：
    DAG + Node State Machine + Ready/Retry/Top-K Heaps

首版后端：
    lark-cli subprocess Adapter

长期后端：
    per-operation OpenAPI Go SDK Adapter

控制层：
    Default / Rule / Algorithm / LLM / SKILL Planner

答案层：
    Candidate Pack → Artifact Pack → Evidence Pack → Synthesis
```

最终可以用一句话定义 SuperFeishuSearch：

> **一个身份和权限范围内、面向飞书多业务域的确定性联邦检索内核；它统一搜索、结构化查询、内容物化、分页和关系展开，并为算法 Planner、LLM、SKILL、MCP 与 Web 提供稳定的数据和执行平面。**
