# Changelog

## Unreleased

### Changed

- Session 改为只增的检索工作区：后一次 Search 或 Continue 不再清掉之前写入的候选，同一计划中的多个 search 节点可以共用 Session，`map_fetch` 不再以 `fetch ref is not present in the selected session` 拒绝前一个节点的对象。请求、来源游标和来源状态仍属于最近一次 Search
- `sfs serve`、`sfs mcp` 启动时显式给出的 `--profile`、`--as` 会限定请求可用的身份；请求要求其他值时返回 `identity_required`（`subtype=identity_pinned`）。未显式给出的字段保持按请求选择
- Evidence Pack 的正文证据从"一个 chunk 一条、引用全文"改为按段落切分：每个 chunk 保留与查询词重合最多的 4 段（每段不超过 600 字），`quote` 与 `source_span` 指向段落；正文证据合计不超过 512 KiB。此前大文档会让 Ask 请求超过 4 MiB 而退回确定性摘要，引用也只能指向整篇文档
- `runtime.provider_timeout` 默认值从 4 秒改为 8 秒。实测 lark-cli 1.0.100 的 messages、tasks 搜索耗时 2.9–3.6 秒且时常超过 4 秒，旧默认值会丢掉这两个来源；搜索总时长仍受档位截止时间约束（balanced 为 8 秒）
- research 与 ask 的默认预算统一放在 research 服务中（15 秒、36 次调用、每来源 2 页、读取数等于 `fetch_top_k`）；此前只有 CLI 使用这组值，HTTP 与 MCP 落到 8 秒、24 次调用

- 启动时复用 SQLite 中 1 小时内、且只含兼容性结论的能力探测结果，lark-cli 可执行文件变化后重新探测；真实 lark-cli 1.0.100 下 `sfs sessions list` 从约 0.9 秒降到约 0.01 秒（首次启动仍需探测）。能力快照新增 `observed_at` 字段

### Fixed

- `sfs serve` 与 `sfs mcp` 现在把启动时的 `--profile`、`--as` 作为默认身份；此前请求未带 identity 时会落到 lark-cli 默认 profile 和 `auto` 身份。Web 工作台不再固定发送 `mode: "auto"`，改用服务端默认身份；`sfs plan` 同样使用全局身份参数补齐计划中缺省的字段
- 翻页结果的来源内名次改由 Kernel 统一加页偏移，第 2 页不再与第 1 页同名次、同分并穿插排序；Provider 契约改为返回页内名次，mock Provider 同步调整
- MCP `tools/call` 接受协议保留的 `_meta` 字段，不再以 `-32602` 拒绝带 progressToken 等元数据的请求
- SQLite Session 存储在同一进程内共享一个 Session 实例，并串行化同一 Session 的读写；计划中并行节点写同一 Session 时不再互相覆盖。其他进程保存过的 Session 会在下次读取时重新加载
- MCP `tools/call` 改为并发执行（最多 4 个），长时间的 research 不再阻塞后续请求；`notifications/cancelled` 与 `$/cancelRequest` 会取消对应调用，被取消的调用不再返回响应。此前取消通知不起作用，调用要跑到预算截止时间才结束
- 按 lark-cli 1.0.100 的实测输出结构补齐字段：docs 标题与摘要取 `title_highlighted`、`summary_highlighted`，ID、URL、时间取 `result_meta`；minutes、meetings 标题取 `display_info`，摘要与链接取 `meta_data`；people 标题取 `localized_name`；mail 时间取 `date`；calendar 时间取 `start.date_time`。此前这些来源的标题在真实搜索中全部为空。字段声明支持点号路径，并去掉 `<h>`、`<hb>`、`<b>`、`<em>` 高亮标签
- 读取卡片消息（`interactive`）时提取 `elements` 中的正文；只含图片的卡片按"正文已读取、内容为空"返回，不再以 `version_incompatible` 失败
- `map_fetch` 跳过没有可读取 Projection 的候选（如 docs 搜索命中的多维表格）并由后续候选补位，且只请求候选声明可读取的 Projection；与 research 的 rerank 补读规则一致。此前这类候选会让读取节点和整个 research 变成 `partial`
- Web 开发与测试依赖按 `npm audit fix` 升级：vite 8.2.0 → 8.3.8、undici 8.9.0 → 8.12.4，修复 2026-07 之后公布的 4 个 high、2 个 moderate 通告（`npm audit` 门禁自 2026-10 起在 `main` 的定时任务中失败）；`package.json` 未变，嵌入的前端产物随 vite 版本重新生成
- lark-cli calendar 搜索结果可以读取：日程条目缺少 `calendar_id` 时使用响应中页级的 `calendar_id`，读取命令先解析 `calendar_id/event_id` 复合 ID；此前真实搜索得到的日程都因缺少 `calendar_id` 读取失败
- 单聊消息（无 `chat_name`）以 `sender.name` 作标题，与 direct OpenAPI 适配器一致
- docs 读取不再把文档自身的 `document_id`（wiki 节点背后的 docx）当成 `message.links_to_document` 关系
- 来源第一页成功、后续翻页失败时，来源状态记为 `partial` 并保留错误，统计中不再算作失败来源；此前会显示为 `deadline_exceeded` 等失败状态，尽管已返回结果
- lark-cli 输出的无时区时间（messages `2006-01-02 15:04`、tasks `2006-01-02 15:04:05`）按本机时区解析；此前 messages 时间无法解析，tasks 时间按 UTC 解析
- lark-cli 字段抽取在顶层找不到字段时逐层查找，同一层按字段优先级、同级对象按 key 排序，结果不再随 map 遍历顺序变化

- lark-cli 输出解析现在只在 stdout/stderr 合计存在唯一、行边界完整且带显式 `ok` 的 envelope 时容忍诊断文本；多 JSON、失败流冲突、混合 raw JSON 与非零退出成功 envelope 继续 fail closed
- 将 GitHub Actions `run:` block 的 ShellCheck 改为带硬超时的独立门禁，再运行关闭内置 ShellCheck 的 actionlint v1.7.7，避免该版本在 Darwin 上向未启动 reader 的 stdin pipe 写入较大脚本时死锁

### Documentation

- README 的真实只读验证记录更新为 2026-11-13 在 lark-cli 1.0.100 上的结果，第 16 节改为引用该记录

### Verification

- 增加 lark-cli 1.0.100 搜索结构 fixture（真实 key 结构、虚构取值），逐字段断言 ID、标题、摘要、URL 与时间
- 增加跨页名次、启动身份默认值（MCP、HTTP）、MCP `_meta`、SQLite 交错读写与跨进程重新加载、并行计划节点持久化、lark-cli 嵌套字段确定性的回归测试
- 增加 lark-cli 混合输出、歧义输出和非零退出语义回归测试
- 增加 workflow shell 提取、表达式脱敏、ShellCheck 大 stdin、超时、缺失 binary、异常退出及 shell 优先级测试

## 1.0.3 - 2026-07-30

### Fixed

- 修复低高度桌面窗口中 sticky 侧栏超出视口后无法访问 Session 历史的问题；侧栏现在在桌面布局内独立滚动，移动布局保持自然文档流
- 将桌面端端到端回归视口收紧到 `1280x720`，并验证侧栏滚动后可恢复历史 Session

### Documentation

- 明确 Release workflow 只保证拒绝覆盖同名 Release；当前未启用 GitHub 平台级 immutable，不再使用强于实际能力的表述

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
- GitHub Release 已存在时，当前 workflow 拒绝覆盖同 Tag 资产

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
