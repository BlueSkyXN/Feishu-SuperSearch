# 开发指南

## 1. 目录结构

```text
cmd/sfs/                 CLI 进程入口
kernel/                  公共模型、接口、Plan 协议
internal/engine/         Search/Query/Fetch/Expand 实现
internal/planexec/       DAG 执行器、状态机与调度
internal/fusion/         Canonicalize、Dedup、Weighted RRF
internal/session/        内存与文件 Session
internal/budget/         共享预算账本
internal/app/            配置与对象装配
adapter/larkcli/         lark-cli 子进程 Provider
adapter/openapi/         Direct OpenAPI Provider
adapter/mock/            离线数据
adapter/execprovider/    外部 JSON-RPC Provider
adapter/replay/          Record/Replay
provider/                Registry 与路由
planner/                 rules/default/command Planner
synthesis/               Candidate/Artifact/Evidence Pack
transport/cli/           CLI 输出与解析辅助
transport/httpapi/       REST/SSE/Web
transport/mcp/           MCP stdio Server
client/                  HTTP Client
api/                     OpenAPI 与 JSON Schema
examples/                计划与扩展示例
skills/                  Agent SKILL
docs/                    文档
scripts/                 本地检查与发布脚本
.github/                 Actions、Dependabot、模板
```

## 2. 依赖方向

```text
transport / planner / synthesis / client
                    ↓
                  kernel
                    ↑
adapter / provider / internal engine
```

`kernel` 不得导入具体 Adapter、HTTP、MCP、CLI 或 LLM。

## 3. 开发环境

```bash
go version
python3 --version
ruby --version
make verify
```

Go 项目当前只使用标准库。不要仅为了少量工具函数引入大型运行时依赖。

## 4. 常见开发路径

### 新增全局搜索源

1. 在 `kernel` 中确认 SourceID/ObjectKind 已存在；
2. 实现 Provider Descriptor；
3. 实现 Search；
4. 归一化 Candidate 与 Projection；
5. 注册到 `internal/app`；
6. 更新默认来源与配置；
7. 增加 Mock/fixture 测试；
8. 更新 README、Provider Guide、OpenAPI/MCP enum。

### 新增 Fetch Projection

1. Provider Descriptor 声明 `fetchable_projection`；
2. Fetcher 只获取缺失 Projection；
3. Artifact 声明实际返回的 Projection；
4. 更新 Session merge 测试；
5. 验证同对象并发 singleflight；
6. 更新 CLI/API/MCP 文档。

### 新增 RetrievalPlan 节点

1. 更新 `kernel.PlanOp`；
2. 更新 JSON Schema；
3. 在 plan executor 中实现确定性语义；
4. 设置输入、输出、动态展开和预算上限；
5. 增加 Cycle、取消、失败和事件测试；
6. 更新 RetrievalPlan 文档。

### 新增 HTTP/MCP 参数

先修改 Kernel 请求模型，再同步：

```text
HTTP handler
OpenAPI Schema
MCP InputSchema
CLI（如需要）
Client SDK
文档和测试
```

不要只在某个 Transport 中增加私有搜索逻辑。

## 5. 调试 Provider

静态能力：

```bash
sfs --backend larkcli --profile work --as user providers
```

Health：

```bash
sfs --backend larkcli --profile work --as user doctor
```

录制：

```bash
sfs --backend larkcli --profile work --as user \
  --record-dir .tmp/fixture \
  search "最小关键词" --sources docs,messages
```

回放：

```bash
sfs --replay-dir .tmp/fixture \
  search "最小关键词" --sources docs,messages
```

调试时优先检查：

```text
ProviderDescriptor
实际 argv
退出码
stdout/stderr envelope
ErrorDetail 类型
Candidate Projection
Cursor
identity.scope_key
```

## 6. 调试计划

```bash
sfs --backend mock --output ndjson \
  plan examples/plan-search-fetch.json --events
```

关注事件中的：

- 节点状态转换；
- attempt；
- retry 时间；
- budget；
- dependency；
- partial/empty/failed 区别。

## 7. HTTP 调试

```bash
make http-smoke
```

手动：

```bash
sfs --backend mock serve --listen 127.0.0.1:3765
curl -sS http://127.0.0.1:3765/v1/capabilities | python3 -m json.tool
```

SSE：

```bash
curl -N http://127.0.0.1:3765/v1/plans:execute \
  -H 'Content-Type: application/json' \
  -H 'Accept: text/event-stream' \
  --data-binary @examples/plan-search-fetch.json
```

## 8. 代码风格

- Go 使用 `gofmt`；
- 错误应映射为 `kernel.ErrorDetail`；
- 时间统一传输 RFC3339，内部使用 `time.Time`；
- Provider 时间戳归一化为 UTC；
- 结构化输出不要混入诊断文本；
- MCP stdout 只能是协议；
- 子进程必须使用参数数组，不得 `sh -c` 拼接用户输入；
- 所有循环必须有预算、页数或深度上限。

## 9. 文档与契约同步

运行：

```bash
make docs-check
make workflow-check
```

公共模型变化还要人工核对：

- `api/openapi.yaml`；
- `api/retrieval-plan.schema.json`；
- `api/provider-protocol.schema.json`；
- MCP InputSchema；
- 示例文件。

## 10. 提交前

```bash
make verify
make http-smoke
```

发布相关变更再执行：

```bash
make package VERSION=1.0.2
```
