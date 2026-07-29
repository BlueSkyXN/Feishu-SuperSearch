# Changelog

## 1.0.2 - 2026-07-30

### Fixed

- 修复 GitHub Release 源码包在无 `.git` 环境或嵌套于其他 Git 仓库时，许可证清单验证错误依赖外层 Git index 的问题
- 源码归档模式改为从 `LICENSES/` 文件系统闭合集合校验，并拒绝 symlink、非普通文件、缺失项和额外项

### Release validation

- Release verifier 现在会安全物化源码 ZIP、恢复文件模式，并在无 `.git` 的临时目录中重新执行完整 `make verify`
- 只读 `package` 阶段执行二进制 smoke 与源码包无 `.git` 环境 `make verify`；拥有 `contents: write` 的 `publish` 阶段只做被动结构、校验和与内容复核，不执行归档代码
- Release verifier 的运行命令均有局部超时和统一错误映射；非 Linux 维护机可用 `ARCHIVE_EXECUTION=source` 单独执行源码包验证
- macOS 候选打包会移除 Release 输出根目录的 `.DS_Store`，避免 OS 元数据污染 9 资产集合和 `SHA256SUMS`
- 增加独立源码树、外层 Git 仓库、额外许可证、symlink、exact Git root 和归档物化的回归测试

## 1.0.1 - 2026-07-30

### Added

- 配置 v2、默认纯 Go SQLite Session、TTL/重启/并发恢复与幂等 JSON Session 迁移
- 可选 OpenAI-compatible AI Planner、Top-30 Rerank 和 Evidence-grounded Ask；AI 默认关闭
- `POST /v1/ask`、MCP `feishu_ask`、Go Client `Ask` 与 CLI `migrate sessions`
- direct OpenAPI docs/messages/people/minutes 高频只读 Adapter
- `lark-cli 1.0.79` 的 11 来源脱敏兼容 fixtures 与 Schema drift/error matrix；该基线不代表 11 来源真实租户验收
- 固定整仓 65%/核心 75% coverage gate、Web unit/build/static drift/Playwright CI，以及受保护的手动只读 Live Smoke 入口
- 来源材料 MIT 原文和第三方归属通知
- Research/Ask 的真实 SSE 阶段与 RetrievalEvent 流，以及浏览器 `fetch`/`ReadableStream` 消费
- 官方 `oapi-sdk-go/v3` 依赖与 MIT notice

### Changed

- 将仓库 module path 对齐到 `github.com/BlueSkyXN/Feishu-SuperSearch`
- `backend=auto` 改为 fail-closed；Mock 仅在显式选择时启用
- HTTP/Web 内置服务限制为回环监听，并只允许回环浏览器 Origin
- Web 根据 Provider capabilities 动态启用来源，不再把缺失来源请求成“部分成功”
- `backend=auto` 在 lark-cli 与 OpenAPI 均可用时选择 Hybrid；Hybrid 不注册 Mock
- `research` 保持确定性 Evidence 语义，`ask` 成为独立 AI 问答流程
- 外部 Planner、CLI/HTTP Plan 和 Executor 使用统一 `retrieval-plan/v1` 运行时严格校验
- External Provider 严格验证 descriptor、JSON-RPC envelope、ObjectRef、Projection、分页与响应大小
- direct OpenAPI 的 Docs Search、Messages Search/Get、People Search、Minutes Search/Get/Artifacts 改用官方 SDK；仅 `docs_ai` Fetch 保留窄 HTTP fallback
- Web 工作台改用中性画布、技术蓝单一强调色与扁平内容层级，并支持系统浅色/深色模式、移动触控尺寸和 Windows forced-colors
- 正式交付改为 GitHub PR exact-head checks、main checks、Tag workflow 与 GitHub Release；本机压缩包不再作为发布证据

### Fixed

- Session get/list/delete/continue 全部按服务端派生 identity scope 隔离；带 Session 的 Fetch/Expand 只允许已发现 ObjectRef
- Canonical ID 始终使用调用者 scope 重建，不信任 Provider 或外部请求提供的 ScopeKey
- HTTP 强制 loopback Host 与严格 same-origin，并将 panic 固定映射为公开错误，不回显 panic 值
- lark-cli/OpenAPI 路径参数拒绝 traversal 和非法 segment；OpenAPI 错误不再回显原始 `error`、`data`、响应正文或对象 URL
- Research Planner 固定调用者 query、filters、sources、limit、identity、fetch 数量和安全 Projection；禁止未经显式入口授权的 Query/Resolve/Expand/直接 Fetch
- SQLite 主文件、WAL、SHM、JSON Session 与原子临时文件强制 `0600`；File Store TTL 真正删除过期文件并显式报告损坏数据
- Record/Replay 不再吞 fixture 持久化失败或损坏 manifest；MCP initialize 不再回显不支持的客户端协议版本

- lark-cli 探测改用实际支持的 `--version`，并检查每个来源的主命令和关键 flags
- capability handshake 按 Provider operation 记录兼容状态，单个操作不兼容时只回退该操作
- lark-cli 子进程使用隔离临时工作目录；妙记 transcript 只从受控输出目录读取并在读取后清理
- 修复 `lark-cli 1.0.79` 妙记 `minutes[].artifacts` 嵌套数组解析，并使用工作目录内相对 `--output-dir` 满足 CLI 路径约束
- direct OpenAPI 妙记 Fetch 只声明实际物化的 Summary/Structure/Content，不再把请求 Projection 当作成功结果
- 修复 Base Query 的 `--base-token`、`--limit`、offset 分页和记录矩阵解析
- 修复 Task Search 不存在的 `--page-size`、布尔 flag 传参以及多人员过滤
- 修复 Message Search 丢弃多个群和发送者过滤的问题
- Docs Search 对 Sheet/Base/File 候选分类为不可直接 Docx Fetch，避免把结构化对象或普通文件 token 传给 `docs +fetch`
- MCP `serverInfo.version` 改为使用实际二进制版本，不再固定返回 `1.0.0`
- 让 GitHub Actions YAML 检查脚本兼容 macOS 系统 Ruby 2.6
- GitHub Release 已存在时拒绝覆盖同 Tag 资产，保持发布不可变

### Verification

- 当前本机 Web unit/typecheck/build/static drift/Playwright E2E、HTTP/Web smoke 已通过
- 当前 atomic coverage 为整仓 71.9%（5409/7525）、核心 76.5%（2153/2816），已通过 65%/75% gate
- 补齐六个发布目标静态链接的 Go 模块、内嵌组件和 Web 生产依赖许可证；新增依赖、版本、notice 与 SHA-256 闭合集合检查，并让所有运行包携带完整 `LICENSES/`
- Security workflow 增加 Gitleaks exact-head 扫描；许可证 manifest 只精确豁免已校验的 SHA-256 行
- lark-cli 真实只读验证中 docs/messages/chats/people/minutes/meetings/tasks 成功；calendar/mail 为 `missing_scope`；Base/Sheets 缺测试对象
- direct OpenAPI 仅完成 httptest；因没有同时可用的 App ID 与 user access token，未做真实租户验收
- GitHub CI、CodeQL、Tag 与 Release 属于动态交付证据，不在 Changelog 中固化状态；必须按 exact SHA 从 Actions 和 9 个 Release 资产回读
- 固定构建参数下两组 9 个候选文件逐字节一致；source archive 与安全工作树一致

### Documentation

- 增加快速开始、配置、CLI、HTTP、MCP、RetrievalPlan、开发、测试、部署、排障、发布与安全模型文档
- 增加完整文档索引、贡献指南、安全策略、行为准则和 GitHub Issue/PR 模板
- 补充 README 的文档导航、仓库自动化与本地验证说明

### Automation

- 将最小 CI 扩展为 Verify、Linux/macOS/Windows 原生构建和 Docker smoke
- 增加 Govulncheck 与 CodeQL 安全工作流
- 增加 Tag 驱动的六平台二进制、源码包、SHA-256 与 GitHub Release 工作流
- 增加 Dependabot、文档检查、Workflow YAML 检查、HTTP smoke 与发布打包脚本
- 增加 actionlint 的 `sfs-live` self-hosted runner label 配置

- `--help` 和子命令 `--help` 正常退出，不再额外打印结构化错误

## 1.0.0 - 2026-07-29

### Retrieval Kernel

- 完成 Search / Query / Fetch / Expand / Continue / Resolve
- 完成 Provider Registry 与 `source + operation` 路由
- 完成 provider-specific `source_queries`
- 完成多源并发、部分失败、超时、取消与共享预算
- 完成 Canonical ID、精确去重、Weighted RRF、来源配额与自适应分页
- 完成 Projection、Artifact、Session cache、Batch Fetch 与 context-aware singleflight
- 完成两跳有界关系展开与 relation filter

### Plan / Planner

- 完成 `retrieval-plan/v1` 有界 DAG Executor
- 完成节点状态机、ready max-heap、retry min-heap 与有限指数退避
- 完成跨 DAG 的原子共享预算
- 完成 rules/default/external-command Planner

### Adapters

- 完成 11 个 lark-cli 来源
- 完成 profile/as 参数传播、envelope/raw JSON 兼容与错误映射
- 完成 direct OpenAPI Search v2 文档搜索
- 完成 executable JSON-RPC Provider
- 完成 Provider record/replay

### Entrypoints

- 完成 CLI、HTTP/SSE、Web、MCP 与 Go HTTP Client
- 完成 OpenAPI 3.1、JSON Schema、SKILL、示例与文档
- 完成跨平台二进制与自动化验证
