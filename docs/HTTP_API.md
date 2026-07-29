# HTTP API

完整机器可读契约：[`api/openapi.yaml`](../api/openapi.yaml)。

## 1. 启动

```bash
sfs --backend mock serve --listen 127.0.0.1:3765
```

默认最大请求体为 4 MiB。服务端不会自动添加用户认证，因此只允许监听回环地址。请求 `Host` 必须是 `localhost` 或回环 IP；带 `Origin` 的浏览器请求必须与当前 Host 严格同源，`127.0.0.1:5173` 不能跨端口调用 `127.0.0.1:3765`。远程访问应由同机、受信任且带认证的反向代理转发到回环监听地址。

## 2. 端点

| 方法 | 路径 | 作用 |
|---|---|---|
| `GET` | `/v1/health` | 服务状态 |
| `GET` | `/v1/capabilities?probe=true` | Provider 能力与可选 Health 探测 |
| `POST` | `/v1/search` | 联邦搜索 |
| `POST` | `/v1/search/continue` | 继续 Session 分页 |
| `POST` | `/v1/query` | 结构化/对象内查询 |
| `POST` | `/v1/fetch` | 物化指定 Projection |
| `POST` | `/v1/expand` | 展开关系 |
| `POST` | `/v1/resolve` | 解析名称或链接 |
| `POST` | `/v1/plans:execute` | 执行 RetrievalPlan；支持 SSE |
| `POST` | `/v1/research` | Search→Fetch→Evidence；支持 SSE |
| `POST` | `/v1/ask` | Research→Evidence-grounded Answer；AI 可选；支持 SSE |
| `GET` | `/v1/sessions` | Session 列表 |
| `GET` | `/v1/sessions/{id}` | Session 详情 |
| `DELETE` | `/v1/sessions/{id}` | 删除 Session |

## 3. Search

```bash
curl -sS http://127.0.0.1:3765/v1/search \
  -H 'Content-Type: application/json' \
  -d '{
    "query":"A 项目 延期",
    "source_queries":{
      "docs":"A 项目 延期",
      "messages":"A 项目 延期 决定"
    },
    "sources":["docs","messages","minutes"],
    "filters":{
      "after":"2026-07-01T00:00:00+08:00",
      "doc_types":["docx","wiki"]
    },
    "identity":{
      "profile":"work",
      "mode":"user"
    },
    "limit":40,
    "limit_per_source":8,
    "budget":{
      "deadline_ms":8000,
      "max_calls":24,
      "max_pages_per_source":2
    },
    "strategy":{
      "profile":"balanced",
      "fusion":"weighted_rrf",
      "pagination":"adaptive"
    }
  }'
```

响应核心字段：

```json
{
  "session_id":"rs_...",
  "query":"A 项目 延期",
  "candidates":[],
  "sources":[],
  "continuations":[],
  "partial":false,
  "budget":{},
  "stats":{}
}
```

`partial=true` 表示至少一个来源失败、超时或预算不足，但已有结果仍然有效。

## 4. Continue

```bash
curl -sS http://127.0.0.1:3765/v1/search/continue \
  -H 'Content-Type: application/json' \
  -d '{
    "session_id":"rs_...",
    "sources":["messages"],
    "max_additional_pages":1,
    "identity":{"profile":"work","mode":"user"}
  }'
```

Session ID 不能跨 identity scope 继续；`scope_key` 是服务端派生字段，客户端传入值不会被信任。

## 5. Query

```bash
curl -sS http://127.0.0.1:3765/v1/query \
  -H 'Content-Type: application/json' \
  -d '{
    "source":"tasks",
    "filter":{"query":"A 项目","completed":false},
    "identity":{"profile":"work","mode":"user"},
    "limit":50
  }'
```

对象内 Base Query：

```json
{
  "source":"base",
  "container":{
    "source":"base",
    "kind":"base_table",
    "native_id":"app_token/table_id"
  },
  "filter":{
    "keyword":"延期",
    "search_field":"项目名称"
  },
  "identity":{"profile":"work","mode":"user"}
}
```

## 6. Fetch

```bash
curl -sS http://127.0.0.1:3765/v1/fetch \
  -H 'Content-Type: application/json' \
  -d '{
    "session_id":"rs_...",
    "identity":{"profile":"work","mode":"user"},
    "items":[
      {
        "ref":{"canonical_id":"feishu:scope:minute:min_xxx"},
        "projection":["summary","structure","relations"]
      }
    ]
  }'
```

Artifact 只声明实际已经物化的 Projection。单项失败可能与成功项同时存在。

带 `session_id` 时，每个 ObjectRef 必须已经由该 Session 的 Candidate、Artifact 或 Relation 发现；服务端不会把客户端提供的任意 ID 当作已授权的 Session 对象。

## 7. Expand

```bash
curl -sS http://127.0.0.1:3765/v1/expand \
  -H 'Content-Type: application/json' \
  -d '{
    "session_id":"rs_...",
    "identity":{"profile":"work","mode":"user"},
    "ref":{"canonical_id":"feishu:scope:meeting:m_xxx"},
    "relations":["meeting.has_minute"],
    "max_depth":2
  }'
```

与 Fetch 相同，带 `session_id` 的 Expand 只能从该 Session 已发现的 ObjectRef 开始。

## 8. Resolve

```bash
curl -sS http://127.0.0.1:3765/v1/resolve \
  -H 'Content-Type: application/json' \
  -d '{
    "source":"people",
    "text":"张三",
    "identity":{"profile":"work","mode":"user"},
    "limit":10
  }'
```

## 9. RetrievalPlan JSON

```bash
curl -sS http://127.0.0.1:3765/v1/plans:execute \
  -H 'Content-Type: application/json' \
  --data-binary @examples/plan-search-fetch.json
```

非流式响应：

```json
{
  "result":{},
  "events":[]
}
```

## 10. RetrievalPlan SSE

```bash
curl -N http://127.0.0.1:3765/v1/plans:execute \
  -H 'Content-Type: application/json' \
  -H 'Accept: text/event-stream' \
  --data-binary @examples/plan-search-fetch.json
```

事件类型：

```text
event: retrieval
data: {...}

event: result
data: {...}
```

客户端断开连接后，请求 Context 会取消，正在执行的 Provider 应响应取消。

## 11. Research

```bash
curl -sS http://127.0.0.1:3765/v1/research \
  -H 'Content-Type: application/json' \
  -d '{
    "query":"A 项目为什么延期，还有哪些任务未完成",
    "sources":["docs","messages","minutes","tasks"],
    "identity":{"profile":"work","mode":"user"},
    "limit":40,
    "deep":true,
    "fetch_top_k":6
  }'
```

返回 PlanResult、Candidate Pack、Artifact Pack、Evidence Pack 和确定性 Summary。Evidence 包含稳定 `id`、`claim_type`、`source_ref`、`projection`、`source_span`、原文引用、时间和 URL。

Web 使用 POST `fetch` 和 `ReadableStream` 消费 SSE，不使用无法携带请求体的 `EventSource`：

```bash
curl -N http://127.0.0.1:3765/v1/research \
  -H 'Content-Type: application/json' \
  -H 'Accept: text/event-stream' \
  -d '{"query":"A 项目为什么延期","sources":["docs","messages"]}'
```

## 12. Ask

```bash
curl -sS http://127.0.0.1:3765/v1/ask \
  -H 'Content-Type: application/json' \
  -d '{
    "query":"A 项目为什么延期",
    "sources":["docs","messages","minutes"],
    "identity":{"profile":"work","mode":"user"},
    "fetch_top_k":8
  }'
```

`answer.claims[].evidence_ids` 必须全部存在于 `research.evidence_pack.evidence[].id`；`citations` 回填对应 ObjectRef、quote 和 URL。AI 未启用时返回 `501 unsupported`。模型运行中失败时返回确定性证据摘要、`partial=true` 和 warning，Search/Research 不受影响。

该校验防止引用不存在的 ID，但不做 Claim 与 quote 的自动语义蕴含证明；调用方应把 Citation 当作可复核来源，而不是自动事实核验结论。

Research 与 Ask 的 SSE 事件契约：

```text
event: progress
data: {"type":"progress","phase":"planning","message":"...","time":"..."}

event: retrieval
data: {"type":"retrieval","phase":"retrieving","retrieval":{...},"time":"..."}

event: result
data: <ResearchResult 或 AskResult>
```

运行时硬错误使用唯一终态 `event: error`，payload 与 JSON Error Envelope 相同。成功流必须以唯一 `result` 收口；`result` 后不得再出现业务事件。客户端遇到没有 `result`/`error` 的 EOF 应视为 `stream_incomplete`，且不得自动重试 POST，以免重复检索、Fetch 或模型调用。

真实阶段为 `capabilities`、`planning`、`retrieving`、可选 `reranking`、`evidence`、`research_complete`；Ask 随后增加 `answering` 和 `complete`。这些事件不伪造百分比。

Research/Ask 的不可信 AI 或外部 Planner 计划会再次被服务端约束：固定调用者 query、filters、sources、limit、identity、fetch 数量和 Projection，并只允许 Search、MapFetch 与纯变换节点。结构化 Query、Resolve、Expand 和显式对象操作应调用对应端点。

## 13. Session

Session API 使用 query 参数选择 identity scope：

```bash
curl -sS 'http://127.0.0.1:3765/v1/sessions?profile=work&mode=user'
curl -sS 'http://127.0.0.1:3765/v1/sessions/rs_xxx?profile=work&mode=user'
curl -X DELETE 'http://127.0.0.1:3765/v1/sessions/rs_xxx?profile=work&mode=user'
```

`mode` 只能是 `auto`、`user` 或 `bot`。列表只返回当前 scope 的 Session；读取、删除或 Continue 其他 scope 的 Session 会失败。

## 14. 错误与 HTTP 状态

错误 Envelope：

```json
{
  "ok":false,
  "error":{
    "type":"missing_scope",
    "message":"...",
    "retryable":false
  }
}
```

| Error type | HTTP |
|---|---:|
| `invalid_request` | 400 |
| `not_found` | 404 |
| `missing_scope` / `identity_required` | 403 |
| `rate_limited` | 429 |
| `unsupported` | 501 |
| `deadline_exceeded` | 504 |
| `budget_exhausted` | 422 |
| `cancelled` | 499 |
| 其他 | 500 |

来源级错误通常放在成功的 SearchSnapshot 内，不一定转换为整个请求失败。

## 15. 客户端注意事项

- 为每个请求设置客户端超时；
- 处理 `partial=true`；
- 保留 `session_id`，避免重复搜索和 Fetch；
- 不要把不同用户的 Session ID 混用；
- 对 SSE 做断线和唯一 `result`/`error` 终态检查；
- 反向代理不要缓冲 SSE；
- 公网部署前必须增加认证与 TLS。

所有 JSON 请求严格拒绝未知字段和多余 JSON 值。`/v1/plans:execute` 会在启动任何 Provider 之前执行 `retrieval-plan/v1` Schema 与语义校验。
