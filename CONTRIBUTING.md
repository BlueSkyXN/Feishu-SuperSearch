# 贡献指南

SuperFeishuSearch 是一个确定性的飞书联邦检索内核。提交变更时，应尽量保持“内核机制、Planner 策略、Provider 适配、Transport 入口”之间的边界。

## 开始开发

```bash
git clone https://github.com/BlueSkyXN/Feishu-SuperSearch.git
cd Feishu-SuperSearch
make verify
```

要求：

- Go 1.23 或更高版本；
- Python 3，用于文档与 JSON 检查；
- Ruby，用于 GitHub Actions YAML 语法检查；
- Docker 仅在验证容器镜像时需要；
- 真实飞书联调需要独立安装并登录 `lark-cli`，或者提供可用的用户 Access Token。

## 分支与提交

建议从 `main` 创建短生命周期分支：

```text
feat/provider-calendar-fetch
fix/session-scope-check
docs/http-api-examples
```

提交信息建议采用简洁的 Conventional Commits 形式：

```text
feat: add message thread hydration
fix: preserve provider cursor on partial failure
docs: document release workflow
test: add replay regression fixture
```

## 本地检查

提交 Pull Request 前至少执行：

```bash
make verify
```

其中包含：

```text
gofmt 检查
go vet ./...
go test ./...
go test -race ./...
文档本地链接与 JSON 检查
GitHub Actions YAML 语法检查
Shell/Python/Ruby 自动化脚本语法检查
Mock 端到端 smoke test
```

需要生成覆盖率时：

```bash
make coverage
```

需要验证跨平台发布包时：

```bash
make package VERSION=1.0.1
```

## 代码边界

### Kernel

`kernel/` 只定义稳定模型、接口和协议。不要直接依赖：

- `os/exec`；
- HTTP handler；
- MCP；
- LLM SDK；
- 某个具体飞书 API 客户端；
- CLI flag 解析。

### Adapter

`adapter/` 负责把外部系统转换成 Kernel 模型。Provider 应准确声明：

- 支持的操作；
- 身份要求；
- 分页能力；
- 已返回与可补齐的 Projection；
- 批量限制；
- 错误类型。

### Planner

Planner 可以使用规则、算法或外部模型，但只能生成结构化请求或受限 `retrieval-plan/v1`。不要让 Planner 绕过预算、身份和 Provider 能力检查。

### Transport

CLI、HTTP、MCP 和 Web 只负责输入输出转换，不应重复实现搜索、Fetch、排序或 Session 逻辑。

## 新增 Provider

请同时提交：

1. `ProviderDescriptor`；
2. Search/Query/Fetch/Expand 中实际支持的接口；
3. Candidate/Artifact 归一化；
4. scope、identity、429、5xx、解析失败和超时测试；
5. 至少一份脱敏 fixture 或 Mock 测试；
6. `docs/PROVIDER_GUIDE.md` 和能力表更新。

不要在 fixture 中提交 Token、邮箱正文、消息正文、真实 open_id 或租户信息。

## 文档变更

公共参数、响应模型或工作流发生变化时，应同步更新：

- `README.md`；
- `docs/CLI_REFERENCE.md`；
- `docs/HTTP_API.md`；
- `api/openapi.yaml`；
- 相关 JSON Schema；
- `CHANGELOG.md`。

## Pull Request 要求

PR 描述应说明：

- 问题和目标；
- 设计选择；
- 可见行为变化；
- 测试方法；
- 是否涉及真实飞书权限、数据或兼容性；
- 是否需要迁移配置。

任何涉及 credential、权限边界或缓存范围的变更，都应明确说明威胁模型和降级行为。
