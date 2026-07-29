# Implementation Status — v1 实现候选（Unreleased）

状态日期：2026-07-30
版本：1.0.1

## 当前开发状态

本表区分“代码存在”“本机验证通过”“真实租户验收”和“远程 CI/Release”。当前版本尚未满足全部发布门槛，不标记为完整交付。

| 模块 | 状态 | 说明 |
|---|---:|---|
| Retrieval Kernel | 完成 | Search/Query/Fetch/Expand/Continue/Resolve |
| 多源并发检索 | 完成 | 部分失败、来源状态、超时、预算 |
| Search 算法 | 完成 | Canonicalize、Dedup、Weighted RRF、来源配额、自适应分页 |
| Projection | 完成 | 8 类 Projection、延迟物化、缓存差集 |
| Fetch | 完成 | 分组 Batch、singleflight、byte/fetch budget |
| Session | 本机测试通过 | SQLite 默认、Memory/File 兼容、TTL、重启、并发、幂等 JSON 导入 |
| RetrievalPlan | 本机测试通过 | 运行时 Draft 2020-12 + 语义校验、DAG、状态机、共享预算、SSE/NDJSON |
| lark-cli Adapter | 部分真实验收 | 11 来源 fixture 基线通过；本机真实只读验证中 7 个来源成功，calendar/mail 为 `missing_scope`，Base/Sheets 缺测试对象 |
| direct OpenAPI | 本机 httptest 通过 | 官方 `oapi-sdk-go/v3 v3.9.9` 覆盖 docs/messages/people/minutes 高频只读操作；`docs_ai` Fetch 保留窄 HTTP fallback；当前缺同时可用的 App ID 与 user access token，尚未进行真实 scope/响应验收 |
| Provider Route | 完成 | source + operation 选择实现 |
| External Provider | 本机测试通过 | JSON-RPC 2.0、严格 descriptor/envelope/ObjectRef/Projection/分页/大小校验 |
| Record/Replay | 完成 | Provider 边界离线 fixture |
| Planner | 本机测试通过 | rules/default/external command + 可选 AI Planner 修复/回退 |
| Synthesis/Ask | 本机测试通过 | 稳定 Evidence ID/source span、Rerank 降级、Citation 校验、确定性 fallback |
| Offline Demo | 完成 | `config.demo.json` 无飞书账号、无 AI Key 预览 Search/Research/Ask/Evidence/Citation/Preview/Doctor/Session |
| CLI | 本机测试通过 | 独立 research/ask、migrate sessions 等命令 |
| HTTP/SSE | 本机测试通过 | Research/Ask 提供真实 `progress`/`retrieval` 流与唯一 `result`/`error` 终态；loopback Host、严格 same-origin、scoped Session |
| Web | 本机验证通过 | Vitest、typecheck、production build、静态漂移检查和 Playwright E2E 已在本机通过 |
| MCP | 本机测试通过 | 10 个工具，含 `feishu_ask` |
| Go Client | 完成 | HTTP Client |
| Examples/Docs | 完成 | Quickstart、配置、CLI、HTTP、MCP、Plan、运维、开发、测试、发布 |
| Cross-platform build | GitHub Actions 已配置/远程未验证 | 六目标 GOOS/GOARCH、CGO/trimpath、嵌入 version/commit 由 CI 与 Release verifier 回读；正式资产不在本机构建 |
| GitHub Actions | 本地配置检查通过/远程未验证 | Workflow 已接入 Verify、Web/E2E、coverage gate、三平台原生构建、Docker、NPM audit、Govulncheck、CodeQL 与 Tag Release |
| Repository metadata | 完成 | Dependabot、Issue/PR Template、贡献与安全策略 |

## 当前验证快照

当前工作区已获得的本机证据：

```text
Web：unit/typecheck/build/static drift/Playwright E2E 通过
HTTP/Web smoke：通过
coverage：整仓 71.9%（5409/7525），核心 76.5%（2153/2816），通过 65%/75% gate
lark-cli live：docs/messages/chats/people/minutes/meetings/tasks 成功
lark-cli exact-worktree：Doctor、Continue、Docs Fetch、Messages Context/Relations Fetch、Minutes Summary/Structure/Content Fetch、Tasks Fetch、People Resolve、Meeting Expand 均通过
lark-cli Search：7 ok / 2 missing_scope；真实对象与响应均未写入仓库
lark-cli blockers：calendar/mail = missing_scope；Base/Sheets = 缺测试对象
direct OpenAPI live：未执行，当前没有同时可用的 App ID 与 user access token
GitHub exact-head CI / CodeQL：未执行
正式 Release：尚未创建；必须由 Tag workflow 生成 9 个 GitHub Release 资产并远程回读
```

上述 Web、HTTP 和 coverage 是本机当前工作区证据，不等同于远程 exact-head CI。lark-cli 的 7 个成功来源也不能代表全部 11 个来源通过。

## 发布前仍需完成或在发布 head 重跑

统一门槛：

```text
gofmt
go vet ./...
go test ./...
go test -race ./...
coverage gate：核心 >=75%，整仓 >=65%（本机当前已通过，发布 head 仍需重跑）
JSON Schema / OpenAPI YAML parse
Web unit/build/E2E/responsive/a11y（本机当前已通过，发布 head 仍需重跑）
mock CLI smoke
RetrievalPlan smoke
external Planner smoke
external Provider smoke
record/replay smoke
HTTP/Web smoke（本机当前已通过，发布 head 仍需重跑）
MCP smoke
真实飞书只读矩阵
cross-platform build + source archive re-verify + SHA-256 readback（必须由 GitHub Release workflow 执行）
exact-head GitHub CI / CodeQL
```

## 本机测试无法替代的验证

以下必须在持有实际租户身份的环境完成：

- `lark-cli` OAuth/profile；
- 每个飞书域的 scope；
- 用户与机器人身份差异；
- 对象可见范围；
- direct OpenAPI App ID 与 user access token；
- 目标租户的真实响应字段与数据规模。

缺少上述证据时，只能说明对应本地代码或离线测试通过，不能声称“11 来源真实租户全部通过”。GitHub 仓库、CI/CD、Release 与离线完整预览可以独立完成；当前 calendar/mail 必须明确保留为 `missing_scope`，Base/Sheets 必须保留为缺测试对象；direct OpenAPI 必须保留为未做真实验收。

正式交付证据只能来自：PR exact-head checks、合并后的 main checks、指向同一 main SHA 的 `v1.0.1` Tag、成功的 Release workflow 和 GitHub Release 下载资产。本机 `dist/`、`bin/`、ZIP 或历史测试记录都不能替代这条链。

## 后续真实环境补验

```bash
sfs --backend larkcli --profile work --as user doctor
sfs --backend larkcli --profile work --as user \
  search "一个你确认存在的关键词" --sources docs,messages,minutes
```

如需直连 docs/search：

```bash
export FEISHU_USER_ACCESS_TOKEN='u-...'
export SFS_OPENAPI_APP_ID='cli_xxx'
sfs --backend openapi --as user search "周报" --sources docs
```
