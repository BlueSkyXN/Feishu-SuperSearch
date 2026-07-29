# Provider 开发指南

## 1. Provider 契约

最小接口：

```go
type Provider interface {
    Descriptor() ProviderDescriptor
}
```

按能力实现：

```go
Searcher
Querier
Fetcher
Expander
Resolver
HealthChecker
```

Descriptor 声明能力，Registry 以 Descriptor 决定是否可调。不要通过运行时报错来“猜”操作支持。

## 2. Descriptor

必须或建议声明：

- 唯一 `id`；
- 业务 `source`；
- `object_kinds`；
- 支持的 `operations`；
- 身份模式；
- 已知 scope；
- query/page/batch 上限；
- 搜索直接返回的 Projection；
- 可 Fetch 的 Projection；
- 分页/流式能力；
- backend/version。

示例：

```json
{
  "id": "example.docs",
  "source": "docs",
  "object_kinds": ["document"],
  "operations": {"search": true, "fetch": true},
  "required_identity": ["user"],
  "search_limits": {"max_query_runes": 200, "max_page_size": 20, "max_pages": 2},
  "batch_limits": {"max_fetch_items": 20},
  "returned_projection": ["head", "snippet"],
  "fetchable_projection": ["structure", "content"],
  "supports_pagination": true,
  "supports_streaming": false,
  "backend": "custom",
  "version": "1"
}
```

Descriptor 是 Planner、MCP、UI 与 Kernel 路由的能力事实源，不应虚报。

## 3. Candidate Normalize

每个 Candidate 至少应有：

```text
Ref.NativeID 或稳定 URL
Source / Kind
Title
NativeRank
Projection
AvailableProjection
Provenance
```

推荐在 Adapter 内补充：

```text
Timestamp
Actors
Container
RelationHints
URL
NativeScore
```

规则：

1. 不把 Provider 原始 score 当成全局可比较分数；
2. `Ref.ProviderID` 必须是当前 Provider；
3. `Provenance.RetrievedAt` 使用 UTC；
4. `Projection` 只声明实际已经返回的内容；
5. `AvailableProjection` 只声明 Fetch 确实能补齐的内容；
6. 原始关键字段可放 `Provenance.RawRef`，但不要放 Token。

## 4. Fetch

Fetcher 接收批量请求。实现应：

- 遵守 Descriptor 的 `MaxFetchItems`；
- 返回与请求对象一一可识别的 Artifact；
- 只声明实际物化的 Projection；
- 保留稳定 ObjectRef；
- 对不存在对象返回 `ErrNotFound`；
- 对部分失败可返回已完成 Artifact + error。

宿主会负责跨请求 cache、Projection 差集、批次切分与 singleflight。

## 5. 错误分类

优先返回 `*kernel.ErrorDetail`：

```text
invalid_request
not_found
missing_scope
identity_required
rate_limited
upstream_transient
upstream_permanent
unsupported
parse_error
deadline_exceeded
cancelled
budget_exhausted
```

`Retryable=true` 或 transient/rate-limited 会触发 Plan 的有限重试。Scope、身份、参数与 parse 问题不应盲目重试。

## 6. 外部进程 Provider

协议是一条 one-shot JSON-RPC 2.0：

```json
{"jsonrpc":"2.0","id":1,"method":"search","params":{"query":"A 项目"}}
```

响应：

```json
{"jsonrpc":"2.0","id":1,"result":{"candidates":[],"has_more":false,"raw_count":0}}
```

支持方法：

```text
health search query fetch expand resolve
```

要求：

- stdin 读取一条请求；
- stdout 只写一条 JSON-RPC 响应；
- stderr 写诊断；
- 调用完成后退出；
- 不输出 credential；
- 正确响应进程取消。

Schema：`api/provider-protocol.schema.json`。

示例：

```bash
sfs --config examples/config-external-provider.json \
  search "External Provider" --sources docs
```

实现：`examples/external-provider.py`。

## 7. 注册与路由

Go Provider：

```go
reg.Register(provider, preferred)
```

配置路由：

```json
{
  "providers": {
    "routes": {
      "docs": {
        "search": "example.docs",
        "fetch": "larkcli.docs"
      }
    }
  }
}
```

路由只对指定操作生效。由 Provider A 搜出的 ObjectRef 默认优先由 A Fetch；显式 route 可覆盖。

## 8. 测试清单

Provider 至少应覆盖：

- Descriptor 与接口一致；
- query/page/cursor 参数；
- empty page；
- pagination；
- Candidate ID/title/time normalize；
- Fetch Projection；
- scope/identity/429/5xx/parse error；
- context timeout/cancel；
- 最大输出与畸形 JSON；
- Provider 版本漂移 fixture。

可使用 record/replay 将 live 响应固化为离线回归样例。
