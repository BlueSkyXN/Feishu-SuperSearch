# 配置参考

## 1. 配置优先级

从低到高：

```text
程序默认值
→ --config 指定的 JSON 文件
→ SFS_* 环境变量
→ CLI 全局参数
→ 子命令参数
```

例如 `SFS_BACKEND=openapi` 会覆盖配置文件，但显式 `--backend mock` 又会覆盖环境变量。

复制示例：

```bash
cp config.example.json ~/.config/sfs/config.json
sfs --config ~/.config/sfs/config.json providers
```

## 2. 完整结构

```json
{
  "version": 2,
  "backend": "auto",
  "runtime": {},
  "storage": {},
  "lark_cli": {},
  "open_api": {},
  "ai": {},
  "providers": {},
  "planner": {},
  "strategy": {},
  "replay": {},
  "external_providers": []
}
```

配置 v2 严格拒绝未知字段和多个 JSON 值。配置 v1 仍可读取并在内存中迁移：旧 `runtime.session_dir` 映射为 JSON File Store；没有旧目录时映射为 Memory Store。未知版本直接报错，不做猜测性迁移。

## 3. Backend

| 值 | 行为 |
|---|---|
| `auto` | lark-cli 与 OpenAPI 都可用时选择 `hybrid`；仅一个可用时选择对应真实后端；都不可用时报错 |
| `larkcli` | 注册内置 lark-cli Provider |
| `openapi` | 只注册当前已实现的 Direct OpenAPI Provider |
| `mock` | 完整离线演示数据 |
| `hybrid` | 高频来源 direct OpenAPI，其他来源 lark-cli；不注册 Mock |
| `replay` | 从 fixture 目录离线回放 Provider 调用 |

`auto` 不会静默回退 Mock。离线演示必须显式指定 `--backend mock`。生产环境仍建议显式指定 Backend，避免运行主机 PATH 变化导致 `auto` 选择不同实现。

## 4. Runtime

```json
{
  "runtime": {
    "global_concurrency": 6,
    "provider_timeout": "4s",
    "listen": "127.0.0.1:3765"
  }
}
```

- `global_concurrency`：Kernel 同时运行的上游操作数；
- `provider_timeout`：单次 Provider 操作的宿主级时限；
- `listen`：HTTP/Web 默认监听地址。

`~/` 会展开为当前用户主目录。

## 5. Storage

```json
{
  "storage": {
    "type": "sqlite",
    "path": "~/.sfs/sfs.db",
    "ttl": "45m"
  }
}
```

| type | 行为 |
|---|---|
| `sqlite` | 新安装默认；纯 Go SQLite、WAL、事务迁移、重启恢复 |
| `file` | 兼容旧 JSON Session 目录 |
| `memory` | 进程退出即丢失，只用于测试或临时运行 |

`--database PATH` 明确选择 SQLite；`--session-dir PATH` 明确选择旧 File Store。旧目录通过 `sfs migrate sessions --from DIR --to DB` 幂等导入，源文件不会被删除或改写。

持久化目录不能对 group/other 可写，也不能是 symlink；SQLite 数据库路径和现有 JSON Session 文件同样拒绝 symlink。默认 `~/.sfs` 符合本地单用户边界。需要共享部署时应为服务账号创建独占目录，而不是放宽目录权限。

## 6. lark-cli

```json
{
  "lark_cli": {
    "executable": "lark-cli",
    "profile_args": [],
    "timeout": "8s",
    "max_stdout_bytes": 16777216,
    "max_stderr_bytes": 4194304
  }
}
```

- `executable` 可以是绝对路径；
- `profile_args` 在每次调用时追加，适合特殊 CLI 包装器；
- `timeout` 是子进程时限；
- 输出上限用于防止异常 Provider 占用无限内存。

身份由请求中的 `Identity` 或 CLI 的 `--profile`、`--as` 提供。

## 7. Direct OpenAPI

```json
{
  "open_api": {
    "enabled": true,
    "app_id": "cli_xxx",
    "base_url": "https://open.feishu.cn",
    "token_env": "FEISHU_USER_ACCESS_TOKEN",
    "user_open_id": "",
    "timeout": "10s",
    "max_response_bytes": 16777216
  }
}
```

`app_id` 是官方 SDK 的必填应用标识，也可用 `SFS_OPENAPI_APP_ID` 注入。配置文件不接受明文 `open_api.token`；Token 只从 `token_env` 指向的环境变量读取。`base_url` 可用于受控代理或本地测试服务器。

direct Adapter 当前覆盖 docs、messages、people、minutes 的高频只读操作。Docs Search、Messages Search/Get、People Directory Search 和 Minutes Search/Get/Artifacts 使用固定的官方 `oapi-sdk-go/v3`；SDK 没有等价 binding 的 `docs_ai` XML/citation Fetch 才使用受限 HTTP Client。

## 8. Provider 开关、权重、配额和路由

```json
{
  "providers": {
    "enabled": ["docs", "messages", "minutes"],
    "disabled": ["mail"],
    "weights": {
      "docs": 1.2,
      "messages": 1.2,
      "minutes": 1.1
    },
    "quotas": {
      "docs": 10,
      "messages": 12,
      "minutes": 6
    },
    "routes": {
      "docs": {
        "search": "openapi.docs",
        "fetch": "larkcli.docs"
      }
    }
  }
}
```

路由粒度是：

```text
source + operation → provider_id
```

支持的 operation 包括：

```text
search query fetch expand resolve health
```

Fetch/Expand 默认优先使用 `ObjectRef.ProviderID`，显式路由可覆盖。

## 9. Search Strategy

```json
{
  "strategy": {
    "profile": "balanced",
    "fusion": "weighted_rrf",
    "pagination": "adaptive",
    "source_quota": true,
    "k0": 60
  }
}
```

### Profile 默认值

| Profile | 全局上限 | 每来源每页 | Deadline | 每来源页数 | Fetch 上限 |
|---|---:|---:|---:|---:|---:|
| `fast` | 25 | 5 | 4 秒 | 1 | 12 |
| `balanced` | 40 | 8 | 8 秒 | 2 | 12 |
| `deep` | 60 | 10 | 15 秒 | 2 | 20 |

未显式覆盖时，其他默认预算为：

```text
max_calls = 24
max_expanded_nodes = 20
max_bytes = 32 MiB
```

`source_quota` 默认开启。明确设置 `disable_source_quota=true` 才会关闭来源多样性限制。

## 10. Planner

```json
{
  "planner": {
    "type": "rules",
    "command": "",
    "args": [],
    "timeout": "30s",
    "max_stdout_bytes": 4194304,
    "max_stderr_bytes": 1048576
  }
}
```

| type | 含义 |
|---|---|
| `rules` | 基于问题线索选源、压缩 query、生成有界 Search→Fetch 计划 |
| `default` | 固定 Search→Fetch |
| `command` | 调用外部可执行 Planner，stdin/stdout 使用 JSON |
| `ai` | 使用下节配置的 OpenAI-compatible Planner；失败后回退 Rules Planner |

外部 Planner 示例见 `examples/external-planner.py` 和 `examples/config-external-planner.json`。

## 11. AI

```json
{
  "ai": {
    "enabled": false,
    "provider": "openai-compatible",
    "base_url": "https://api.openai.com/v1",
    "api_key_env": "SFS_AI_API_KEY",
    "model": "",
    "timeout": "45s",
    "max_input_bytes": 4194304,
    "max_response_bytes": 4194304,
    "json_schema": true,
    "planner": {"enabled": false, "max_output_tokens": 4096},
    "rerank": {"enabled": false, "top_n": 30, "max_output_tokens": 2048},
    "answer": {"enabled": false, "max_output_tokens": 4096}
  }
}
```

AI 默认关闭。`api_key_env` 保存环境变量名，不保存 Key。每个 feature 可覆盖 `model`、`timeout` 和输出预算。Rerank 最多处理确定性融合后的 Top 30；Planner 输出严格校验并最多修复一次；Answerer 只能读取 Evidence Pack。

`provider` 支持：

| 值 | 用途 |
|---|---|
| `openai-compatible` | 调用 OpenAI-compatible Chat Completions；需要 `api_key_env`、`base_url` 和 `model` |
| `demo` | 仅用于 `backend=mock` 的离线界面预览；不发网络请求，也不代表模型质量 |

完整离线预览配置已经提交为 [`config.demo.json`](../config.demo.json)：

```bash
sfs --config config.demo.json serve
```

`demo` 与任何真实 Backend 组合都会启动失败，避免把演示答案误认为真实飞书问答。

常用环境变量包括：

```text
SFS_AI_ENABLED
SFS_AI_PROVIDER
SFS_AI_BASE_URL
SFS_AI_API_KEY_ENV
SFS_AI_MODEL
```

启用远程 AI 前必须确认所选模型服务有权处理以下数据：

- Planner：用户查询、明确过滤条件、Provider capabilities 和预算；
- Rerank：确定性融合后的 Top 30 候选标题、snippet 和必要元数据；
- Answer：已 Fetch 并提取的 Evidence Pack，包括引用文本、对象时间和 URL。

飞书 Token、`lark-cli` profile 文件和原始 Provider JSON 不会发送给模型。AI/外部 Planner 生成的 Research Plan 还会在执行前固定调用者 identity、query、filters、limit、sources、fetch 数量和 Projection；只允许 Search、MapFetch 与纯变换节点。`Query`、`Resolve`、`Expand` 和显式 ObjectRef 操作必须由 CLI/HTTP/MCP 的对应确定性入口发起。

## 12. Record / Replay

```json
{
  "replay": {
    "mode": "off",
    "dir": "~/.sfs/replay"
  }
}
```

CLI 快捷方式：

```bash
sfs --backend mock --record-dir .tmp/fixtures search "A 项目" --sources docs
sfs --replay-dir .tmp/fixtures search "A 项目" --sources docs
```

`--record-dir` 与 `--replay-dir` 互斥。进入仓库的 fixture 必须先脱敏。

## 13. 外部 Provider

```json
{
  "external_providers": [
    {
      "descriptor": {},
      "command": "python3",
      "args": ["examples/external-provider.py"],
      "timeout": "15s",
      "max_stdout_bytes": 4194304,
      "max_stderr_bytes": 1048576,
      "preferred": true
    }
  ]
}
```

协议与 Descriptor 见 [Provider 开发](PROVIDER_GUIDE.md)。

## 14. 环境变量

| 变量 | 对应配置 |
|---|---|
| `SFS_BACKEND` | `backend` |
| `SFS_LARKCLI` | `lark_cli.executable` |
| `SFS_OPENAPI_ENABLED` | `open_api.enabled` |
| `SFS_OPENAPI_BASE_URL` | `open_api.base_url` |
| `SFS_OPENAPI_APP_ID` | `open_api.app_id` |
| `SFS_OPENAPI_TOKEN` | Direct Token，并自动启用 OpenAPI |
| `SFS_OPENAPI_TOKEN_ENV` | `open_api.token_env` |
| `SFS_OPENAPI_USER_OPEN_ID` | `open_api.user_open_id` |
| `SFS_OPENAPI_TIMEOUT` | `open_api.timeout` |
| `SFS_SESSION_DIR` | 兼容入口：选择 `storage.type=file` 并设置 `storage.path` |
| `SFS_STORAGE_TYPE` | `storage.type` |
| `SFS_STORAGE_PATH` | `storage.path` |
| `SFS_STORAGE_TTL` | `storage.ttl` |
| `SFS_LISTEN` | `runtime.listen` |
| `SFS_REPLAY_MODE` | `replay.mode` |
| `SFS_REPLAY_DIR` | `replay.dir` |
| `SFS_PLANNER` | `planner.type` |
| `SFS_PLANNER_COMMAND` | `planner.command` |
| `SFS_PLANNER_TIMEOUT` | `planner.timeout` |
| `SFS_CONCURRENCY` | `runtime.global_concurrency` |
| `SFS_AI_ENABLED` | `ai.enabled` |
| `SFS_AI_BASE_URL` | `ai.base_url` |
| `SFS_AI_API_KEY_ENV` | `ai.api_key_env` |
| `SFS_AI_MODEL` | `ai.model` |

## 15. 配置检查

```bash
sfs --config config.example.json providers
sfs --config config.example.json doctor
```

程序启动时会执行 capability handshake 并缓存结果；`providers` 读取当前能力，`doctor` 使用 `probe=true` 再次探测。只对 `unsupported`/`version_incompatible` 在执行前选择已验证备用 Provider；权限和身份错误不会被静默 fallback 掩盖。
