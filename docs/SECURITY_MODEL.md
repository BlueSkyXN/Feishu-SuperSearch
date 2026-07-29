# 安全模型

## 1. 信任边界

```text
用户 / Agent
  ↓
CLI / HTTP / MCP
  ↓
Retrieval Kernel
  ↓
Provider Adapter
  ↓
lark-cli / OpenAPI / 外部进程 / Replay
  ↓
飞书与本地文件系统
```

SuperFeishuSearch 不提升飞书权限。它只能使用所选 profile、user token 或 bot token 已有的可见范围。

## 2. Identity

Identity 包含：

```text
profile
mode: auto | user | bot
scope_key
```

`scope_key` 用于：

- Canonical ID 范围；
- Session 隔离；
- Fetch cache；
- singleflight key；
- 防止不同身份复用对象内容。

`scope_key` 是由 `profile + mode` 在服务端派生的只读值。HTTP/MCP 请求中的 `Identity.scope_key` 会被清空后重新计算，Provider 返回的 ScopeKey 也不会成为 Canonical ID 的可信来源。调用者不能把一个用户的 Session ID 交给另一个用户使用。

## 3. Credential

### lark-cli

宿主不读取 OAuth 文件内容，只传递 profile 和 mode。风险主要在：

- 启动参数；
- 子进程环境；
- stderr；
- 录制 fixture；
- 本机 profile 目录权限。

### Direct OpenAPI

官方 SDK 需要 `open_api.app_id`，该标识不是访问凭据；配置文件仍拒绝明文 Token，运行时只从 `open_api.token_env` 指定的环境变量读取。不要：

- 写入 Git；
- 写入命令行参数；
- 打印到日志；
- 返回给 HTTP/MCP 客户端；
- 写入 Session/fixture。

AI API Key 同样只从 `ai.api_key_env` 读取。OpenAPI/lark-cli 业务错误会映射为公开错误类型，只保留经过字符集校验的 `log_id`，不把上游 `error`、`data`、响应正文或对象 URL写入公开 `ErrorDetail`。

### AI 数据出境

远程 AI 默认关闭。启用后会向配置的模型服务发送：

- Planner：查询、过滤条件、Provider capabilities 和预算；
- Rerank：Top 30 候选的标题、snippet 和必要元数据；
- Answerer：Evidence Pack 中已物化的引用文本、时间和 URL。

Planner 不接收飞书 Token 或原始 Provider JSON，Answerer 不能绕过 Evidence Pack 直接调用 Provider。启用远程 AI 即表示操作者已确认该模型服务和数据处理地域可以接收这些业务内容。`provider=demo` 只允许配合 Mock backend，在本机生成固定演示答案，不发出网络请求。

## 4. 子进程

lark-cli、外部 Planner 和外部 Provider 都是代码执行边界。

实现约束：

- 不使用 `sh -c`；
- executable 与 args 分离；
- Context timeout/cancel；
- stdout/stderr 上限；
- stdout 必须符合协议；
- lark-cli 使用自动清理的隔离临时工作目录；需要文件输出的妙记 transcript 只从受控目录限量读取；
- 外部 Planner/Provider 的可执行路径由可信配置提供；
- 外部程序与主进程共享当前操作系统用户权限。

配置外部命令等同于允许执行该程序，不应接受普通 HTTP 用户提交任意命令路径。

## 5. HTTP

服务没有内置用户认证，设计目标首先是本地服务。`serve` 只接受回环监听地址；HTTP `Host` 必须是 `localhost` 或回环 IP；带 `Origin` 的浏览器请求必须与当前 Host 严格同源，其他本机端口也不能跨域调用。

如果通过反向代理暴露，代理应与 SFS 位于同机并连接回环监听地址，同时：

- 加 TLS 与认证；
- 限制 Body、并发和速率；
- 禁止任意用户选择高权限 profile；
- 将 profile/identity 绑定到服务端用户，而不是信任请求体；
- 隔离 SessionStore；
- 对 SSE 设置合理连接上限。

当前 API 允许请求中携带 Identity，因而不应直接用作不受信任的多租户公共 API。panic 与 Provider 错误只返回固定公开信息，不向 HTTP/MCP 回显 panic 值或原始上游 payload。

## 6. MCP

MCP Host 可以代表用户调用本机飞书身份。Host 应：

- 明确展示工具调用；
- 不向模型暴露 Token；
- 限制可用命令和配置；
- 保留用户对 Fetch/Expand 的可见控制；
- 防止 Prompt 注入直接改写 executable/path。

搜索结果和文档正文本身是不可信内容，不能把其中的指令当作系统指令执行。

## 7. Session 与磁盘

Session 可能包含业务内容。新安装默认 SQLite `~/.sfs/sfs.db`，正文只保存用户已经 Fetch 的工作集，不做全量飞书镜像。Session 的 get/list/delete/continue 全部按派生 identity scope 隔离；带 Session 的 Fetch/Expand 只接受已由该 Session 的 Candidate、Artifact 或 Relation 发现的 ObjectRef。

SQLite 主文件、WAL、SHM、旧 JSON Session 和原子临时文件都强制为 `0600`。Store 拒绝 group/other 可写目录、数据库路径 symlink 和 Session 文件 symlink；File Store 使用随机 `O_EXCL` 临时文件后再 rename，避免固定 `.tmp` 路径被预置。数据库或旧 JSON 目录：

- 只允许服务账号访问；
- 不进入 Git；
- 不进入通用备份或同步盘，除非有明确数据策略；
- 按 TTL 清理；
- 删除用户时同步删除相关 Session；
- 多用户部署必须分区存储。

旧 JSON → SQLite 迁移不会删除源文件；确认目标数据库与备份策略前，不应手工清理旧目录。

## 8. Record / Replay

Fixture 设计用于 Parser 回归，不是数据归档。提交前必须删除：

- Authorization；
- cookie；
- Token；
- open_id/chat_id/doc token；
- 真实姓名、邮箱和正文；
- 可反推出组织的信息。

## 9. 资源限制

内核使用：

```text
provider timeout
global concurrency
max calls
max pages
max fetches
max expanded nodes
max bytes
stdout/stderr limit
HTTP body limit
Plan node limit
Expand depth limit
```

所有外部响应都应在解析前受大小限制。Planner 不能绕过这些硬限制。

RetrievalPlan 与 external Provider 输出都在执行前严格校验。Research Planner 的计划还会固定调用者 identity、query、filters、sources、limit、fetch 数量和安全 Projection，只允许 Search、MapFetch 与纯变换节点；通用 Query/Resolve/Expand 仍通过显式 API 使用。只有实现不可用或版本不兼容可触发预执行 fallback；权限和身份错误必须按结构化类型显式暴露，不能静默回退 Mock。

## 10. 数据完整性

最终回答层应只依据 Artifact/Evidence，并保留来源引用。Search snippet 可能截断、过期或缺少上下文，不应被当成确定事实。

Citation 校验保证模型返回的 ID 确实存在于 Evidence Pack，并用真实 quote/ObjectRef/URL 回填；它不自动证明 Claim 文本被该引用在语义上蕴含。UI 的“可核验结论”表示用户可以沿引用复核，不等于系统已经完成自动事实核验。高风险结论仍需人工阅读原文。

Provider 原始 score 不可跨来源直接比较；错误归一化或去重也可能影响召回。需要保留 provenance 以便复核。

## 11. 已知不覆盖范围

当前项目本身不提供：

- 多用户认证系统；
- KMS/Secret Manager；
- 数据库行级权限；
- 分布式 Session 加密；
- 企业审计平台；
- DLP；
- 对外部 Planner/Provider 的 OS 沙箱。

这些能力应由部署环境提供，或通过独立 Adapter 扩展。
