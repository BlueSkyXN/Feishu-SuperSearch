# RetrievalPlan v1

## 1. 定位

`retrieval-plan/v1` 是 Planner 与 Retrieval Kernel 之间的声明式执行契约。它用于表达有界的 Search、Query、Fetch、Expand 和确定性结果处理，不是通用脚本引擎。

Schema：[`api/retrieval-plan.schema.json`](../api/retrieval-plan.schema.json)。

## 2. 顶层结构

```json
{
  "version": "retrieval-plan/v1",
  "session": {
    "id": "",
    "reuse": false
  },
  "identity": {
    "profile": "work",
    "mode": "user"
  },
  "budget": {
    "deadline_ms": 15000,
    "max_calls": 30,
    "max_pages_per_source": 2,
    "max_fetches": 8,
    "max_expanded_nodes": 20,
    "max_bytes": 33554432
  },
  "nodes": [],
  "output": []
}
```

限制：

- 最多 200 个节点；
- 依赖图必须无环；
- 不允许任意 shell、动态代码或无界循环；
- 动态 `map_fetch` 最多 100 项，并受总 Fetch 预算限制；
- Expand 最大深度 2；
- 每个节点可配置有限重试。

## 3. 节点通用字段

```json
{
  "id": "search_docs",
  "op": "search",
  "depends_on": [],
  "request": {},
  "priority": 10,
  "optional": false,
  "max_items": 8,
  "retry": {
    "max_attempts": 2,
    "backoff_ms": 200,
    "max_backoff_ms": 1000
  }
}
```

| 字段 | 含义 |
|---|---|
| `id` | 计划内唯一 ID |
| `op` | 节点操作 |
| `depends_on` | 所有前置节点完成后才能 Ready |
| `request` | 与操作对应的 JSON 请求 |
| `priority` | Ready Heap 中的调度优先级 |
| `optional` | 失败时不强制使整个计划失败 |
| `max_items` | Map/Limit 等操作的上限 |
| `retry` | 只对可重试错误生效 |

## 4. 支持的操作

### `resolve`

`request` 对应 `ResolveRequest`：

```json
{
  "id": "resolve_owner",
  "op": "resolve",
  "request": {
    "source": "people",
    "text": "张三",
    "limit": 5
  }
}
```

### `search`

```json
{
  "id": "search_all",
  "op": "search",
  "request": {
    "query": "A 项目 延期",
    "sources": ["docs", "messages", "minutes"],
    "limit": 40,
    "limit_per_source": 8,
    "strategy": {
      "profile": "balanced"
    }
  }
}
```

### `query`

```json
{
  "id": "query_tasks",
  "op": "query",
  "request": {
    "source": "tasks",
    "filter": {
      "query": "A 项目",
      "completed": false
    },
    "limit": 30
  }
}
```

### `fetch`

```json
{
  "id": "fetch_one",
  "op": "fetch",
  "request": {
    "items": [
      {
        "ref": {
          "canonical_id": "feishu:scope:minute:min_xxx"
        },
        "projection": ["summary", "relations"]
      }
    ]
  }
}
```

### `expand`

```json
{
  "id": "expand_meeting",
  "op": "expand",
  "request": {
    "ref": {
      "canonical_id": "feishu:scope:meeting:m_xxx"
    },
    "relations": ["meeting.has_minute"],
    "max_depth": 2
  }
}
```

### `merge`

合并上游节点中的 Candidate/Artifact/Relation 集合，不改变语义分数。

### `dedup`

使用 Canonical ID、稳定 native ID 和 URL/token 规则去重，并合并 provenance。

### `rank`

对 Candidate 执行配置的融合排序。目前公共策略为 Weighted RRF。

### `limit`

截取前 N 项：

```json
{
  "id": "top_20",
  "op": "limit",
  "depends_on": ["ranked"],
  "max_items": 20
}
```

### `project`

将上游结果转换为指定输出形态或字段集合。当前用于确定性计划内部数据转换。

### `map_fetch`

从上游 Candidate 中取前 N 项，按其 ObjectRef 生成 Fetch：

```json
{
  "id": "fetch_top",
  "op": "map_fetch",
  "depends_on": ["search_all"],
  "max_items": 6,
  "request": {
    "projection": ["summary", "content", "context"]
  }
}
```

该节点仍受 Provider BatchLimit、全局并发、`max_fetches` 和 `max_bytes` 限制。

## 5. Search → Fetch 示例

仓库示例：[`examples/plan-search-fetch.json`](../examples/plan-search-fetch.json)。

逻辑：

```text
search
→ map_fetch(top 6)
→ 输出 search 与 fetch
```

运行：

```bash
sfs --backend mock plan examples/plan-search-fetch.json
```

事件模式：

```bash
sfs --backend mock --output ndjson \
  plan examples/plan-search-fetch.json --events
```

## 6. 状态机

节点状态：

```text
planned
→ blocked
→ ready
→ running
   ├─ succeeded
   ├─ partial
   ├─ empty
   ├─ retry_wait → ready
   ├─ failed
   ├─ skipped
   └─ cancelled
```

`optional=true` 的节点失败时，依赖节点仍可根据已有输入运行；非 optional 失败可能使依赖节点跳过。

## 7. 调度

执行器使用：

- Ready Max-Heap：优先级高的节点先运行；
- Retry Min-Heap：到达下一次重试时间后重新进入 Ready；
- 全局 semaphore：限制并发；
- 共享 Budget Ledger：原子扣减调用、Fetch、Expand 与字节预算；
- Context：传播取消与 deadline。

计划图表达“做什么、依赖什么”；Planner 负责“为什么这么做”。

## 8. Retry

只有 ErrorDetail 明确可重试，或错误类型属于临时上游错误/限流时才重试。以下情况不会盲目重试：

- `missing_scope`；
- `identity_required`；
- `invalid_request`；
- `unsupported`；
- `parse_error`；
- `not_found`。

建议最多 2–3 次，并设置总 deadline。

## 9. 输出

`PlanResult` 包含：

```text
session_id
每节点状态、尝试次数、结果与错误
output 映射
partial
budget
```

计划可能在 `partial=true` 下仍有可用 Candidate、Artifact 或 Evidence。调用者应检查指定输出节点，而不是只判断是否存在失败节点。

## 10. Planner 生成计划的约束

外部 LLM Planner 的输出必须：

1. 符合 JSON Schema；
2. 只使用已注册 Provider 能力；
3. 不伪造 ObjectRef；
4. 显式设置预算；
5. 限制 `map_fetch` 数量；
6. 不生成循环依赖；
7. 不把总结文本作为检索事实写回 Kernel。
