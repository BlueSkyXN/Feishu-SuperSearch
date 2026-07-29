# 测试指南

## 1. 测试层次

```text
纯模型与算法单元测试
→ Provider Adapter 测试
→ Kernel 集成测试
→ Plan Executor 测试
→ CLI/HTTP/MCP Transport 测试
→ Mock 端到端
→ Record/Replay 回归
→ 真实租户最小联调
```

## 2. 标准命令

```bash
make test
make race
make vet
make verify
make coverage
make web-install
make web-test
gitleaks git --staged --redact --no-banner --no-color .
```

`make verify` 还包含文档、OpenAPI YAML 结构与重复键、工作流、运行时依赖许可证闭合集合、脚本语法和 Mock smoke。Coverage 与 Web 分别保留独立 target，便于本地按需运行；CI 会把它们全部作为必需检查执行。

## 3. 覆盖率

```bash
make coverage
```

输出：

```text
.tmp/coverage.out
```

覆盖率用于识别缺口，不应为了数字而写没有断言价值的测试。

`make coverage` 不只生成 profile，还会执行固定门槛：

```text
整仓 statements >= 65%
核心包 statements >= 75%
```

核心范围由 `scripts/check-coverage.py` 定义，包括 Engine、Plan Executor、Research、Budget、Session、Plan Validation、Synthesis 和 Planner。不能通过 Make 参数降低门槛。

当前工作区最近一次 atomic profile 为：

```text
整仓 71.9%（5409/7525）
核心 76.5%（2153/2816）
```

该结果说明本机当前代码通过 gate；代码变化后必须重新生成 profile，且不能把它当作 exact-head GitHub CI 结果。

## 4. Unit Test

适合覆盖：

- Projection 集合运算；
- Canonical ID；
- Candidate merge；
- Weighted RRF；
- 来源配额；
- 时间解析；
- ErrorDetail 映射；
- Config normalize；
- Session TTL 与 scope。

## 5. Provider Test

每个 Provider 至少测试：

```text
Descriptor 与实际能力一致
query/filter/page/cursor 参数映射
empty page 与 has_more
Candidate ID/title/snippet/time 归一化
Fetch Projection
部分失败
scope / identity / 429 / 5xx / parse error
Context timeout/cancel
stdout/stderr 或 HTTP 响应大小限制
未知字段兼容
```

lark-cli Adapter 应使用 fake executable 或 fixture，不依赖开发机真实 profile。

Docs Search 还必须验证对象类型边界：Sheet、Base、File 候选不应声明可 Fetch Projection，调用 `larkcli.docs.Fetch` 时应在执行 `docs +fetch` 前返回 `unsupported`。Base/Sheets 内容分别通过其 Query Provider 获取；普通 File 当前没有 Docx Fetch 路径。

## 6. Kernel Integration Test

需要验证：

- 多来源并发；
- 单源失败而整体 partial；
- Cursor 写入与 Continue；
- 去重和 provenance merge；
- SourceQuota；
- Fetch 分组、差集、批次和 singleflight；
- Expand visited set 与深度；
- 共享预算原子扣减；
- Session identity scope。

## 7. RetrievalPlan Test

至少包含：

- 合法 DAG；
- 缺失依赖；
- Cycle；
- Ready priority；
- Retryable/Non-retryable；
- optional node；
- partial output；
- 动态 `map_fetch` 上限；
- Context cancellation；
- Deadline 与 Budget exhaustion；
- Event 顺序与最终 Result。

## 8. Mock Smoke

```bash
make smoke
```

脚本实际执行：

```text
build
version
multi-source search
tasks query
research
retrieval plan
JSON assertions
```

## 9. Web 与 HTTP Smoke

首次安装锁定的 Web 依赖：

```bash
make web-install
```

常用目标：

```bash
make web-unit          # Vitest 组件测试
make web-static-check  # typecheck + build:web + Go embed 静态漂移检查
make web-verify        # web-unit + web-static-check
make web-e2e           # Playwright 浏览器端到端测试
make web-test          # web-verify + web-e2e 完整 Web 验收
make web-build         # 完整 build 并同步 transport/httpapi/static
```

`web-static-check` 有意不调用 `sync:static`：它先重新生成 `web/dist`，再与已提交的 `transport/httpapi/static` 比较，因此能发现漏提交或过期的嵌入产物。修复漂移时显式运行 `make web-build`，复查后再提交生成物。

Playwright E2E 使用 `config.demo.json` 启动 Mock backend 与离线 Demo Answerer，不需要真实飞书身份或 AI Key。它覆盖 Search、Continue、Research、Ask、Evidence/Citation、对象预览、Doctor、Session 恢复，以及 390×844/桌面尺寸、键盘、无障碍和横向溢出。首次运行先安装浏览器：

```bash
cd web
npx playwright install chromium
cd ..
make web-e2e
```

CI 使用 `npx playwright install --with-deps chromium` 安装 runner 所需系统依赖。

HTTP/Web smoke：

```bash
make http-smoke
```

脚本会选择空闲本地端口，验证：

- `/v1/health`；
- `/v1/search`；
- 内嵌 Web 首页；
- 服务优雅终止。

本机通过只证明 Mock/browser/loopback 路径，不证明真实飞书或 GitHub runner。正式交付必须读取 PR exact-head 和 main exact-head 的 `CI / Web` 终态；Release workflow 还会在 Tag SHA 上重新执行同一套 E2E。

## 10. Docker

```bash
make docker
```

CI 也会构建镜像，并在容器内运行 `version` 和 Mock search。

## 11. Record / Replay

录制真实 Provider 边界：

```bash
sfs --backend larkcli --profile work --as user \
  --record-dir .tmp/live \
  search "最小关键词" --sources docs,messages,minutes
```

回放：

```bash
sfs --replay-dir .tmp/live \
  search "最小关键词" --sources docs,messages,minutes
```

提交 fixture 前：

- 替换真实姓名；
- 删除正文；
- 替换 open_id、chat_id、doc token；
- 删除 Token/header；
- 确认 `RawRef` 无敏感字段；
- 保留足以验证 Parser 的结构。

## 12. 真实租户测试

真实联调不能作为普通 PR 的硬依赖，因为它需要私有身份和 scope。建议在受控环境执行以下最小集合：

```bash
sfs --backend larkcli --profile work --as user doctor
sfs --backend larkcli --profile work --as user \
  search "已知关键词" --sources docs,messages,minutes
```

然后选择一个已知对象验证 Fetch 与 Session：

```bash
sfs fetch --session rs_xxx --id OBJECT_ID --projection content
```

记录：

```text
lark-cli 版本
Provider ID/version
身份模式
已开通 scope
来源状态
字段漂移
P50/P95 延迟
结果规模
```

不要把真实响应直接粘贴到公开 CI 日志。

仓库提供聚合的只读验收入口：

```bash
SFS_LIVE_PROFILE=work \
SFS_LIVE_QUERY="已知关键词" \
SFS_LIVE_PERSON_QUERY="已知人员" \
SFS_LIVE_BASE_CONTAINER="KNOWN_BASE_TABLE" \
SFS_LIVE_SHEET_CONTAINER="KNOWN_SHEET_CONTAINER" \
SFS_LIVE_SHEET_ID="KNOWN_SHEET_ID" \
make live-smoke
```

`scripts/live-smoke.py` 使用临时 SQLite 数据库，验证 Doctor、全来源 Search/Continue、按 kind Fetch、Resolve、Expand、Base Query 和 Sheets Query。已知对象 ID 可通过相应 `SFS_LIVE_*_ID` 提供；否则部分 kind 会尝试从 Search 候选中选择。

真实租户验收不是普通 PR gate。GitHub Actions 只提供绑定 `live-readonly` Environment 的手动工作流，且要求带 `sfs-live` 标签、已预装并授权 `lark-cli` 的受控 self-hosted runner。工作流未实际运行时，不能声称 live acceptance 已通过。

当前已知的本机真实只读结果：

```text
成功：docs、messages、chats、people、minutes、meetings、tasks
missing_scope：calendar、mail
缺测试对象：Base、Sheets
direct OpenAPI：没有同时可用的 App ID 与 user access token，未执行真实验收
```

因此当前只能表述为 lark-cli 7 个来源成功，而不是 11 来源全通过。

## 13. GitHub Actions

CI workflow 已配置为在 Linux 上运行完整 verify/race 和固定 coverage gate；独立 Web Job 配置了 `npm ci`、Vitest、typecheck、生产构建、静态漂移检查与 Playwright Chromium E2E；平台矩阵和 Docker、安全工作流也已配置。当前 exact-head GitHub CI 与 CodeQL 尚未运行，不能把这些配置项写成远程通过。详见 [GitHub Actions](GITHUB_ACTIONS.md)。
