# lark-cli 兼容策略

## 1. 调用契约

- 使用 `exec.CommandContext` 参数数组；不使用 shell；
- 全局 `Identity.Profile` 传递为 `--profile`；
- 身份传递为 `--as user|bot`；
- 每次要求 `--format json`；
- stdout/stderr 分离并设大小上限；
- context 超时和取消终止子进程；
- 每个 lark-cli 子进程在隔离临时目录运行，命令结束后清理；
- 依据退出码与显式 CLI envelope 判定成功。

只在存在显式布尔字段 `ok` 时将输出识别为 CLI envelope：

```json
{"ok":true,"data":{}}
{"ok":false,"error":{}}
```

普通 OpenAPI JSON 即使也含 `data`，仍按 raw response 解析。不能用 `code == 0` 替代 CLI 进程成功语义。

## 2. 当前映射

| Provider | Search / Query | Fetch / Expand |
|---|---|---|
| `larkcli.docs` | `drive +search` | `docs +fetch` |
| `larkcli.messages` | `im +messages-search` | raw message get；关系提取 |
| `larkcli.chats` | `im +chat-search` | raw chat get |
| `larkcli.people` | `contact +search-user` | contact user get / Resolve |
| `larkcli.minutes` | `minutes +search` | `minutes +detail` |
| `larkcli.meetings` | `vc +search` | `vc +detail` / meeting→minute |
| `larkcli.calendar` | `calendar +search-event` | event get |
| `larkcli.tasks` | `task +search` | task get / structured Query |
| `larkcli.mail` | `mail +triage` | mailbox message get |
| `larkcli.base` | `base +record-search` | Query only |
| `larkcli.sheets` | `sheets +cells-search` | Query only |

Base/Sheets 不参加默认全局 Search。

### Docs Search 的结构化对象边界

`drive +search` 可能在 `docs` 来源中返回 Docx 以外的 Sheet、Base 或普通 File。Adapter 会根据 `doc_types`/`file_type`/URL 将它们分别标记为 `sheet`、`base`、`attachment`，并把 `AvailableProjection` 置空。

这些候选**不能**直接传给 `larkcli.docs` 的 `docs +fetch`：该命令是 Docx Markdown Fetch，不是通用 Drive 文件读取。Provider 会在启动子进程前返回 `unsupported`，避免把 Sheet/Base/File token 错当成 Docx token。

- Base 内容应使用 `query --source base`，并提供 `app_token/table_id` container；
- Sheets 内容应使用 `query --source sheets`，并提供 spreadsheet token 和 `sheet_id`；
- 普通 File 当前只有搜索候选元数据，没有通过 `docs +fetch` 读取文件正文的路径。

### 妙记 transcript 输出边界

`minutes +detail --transcript` 会把转写保存为文件，而不是直接放入 stdout。Adapter 会显式传入一次性 `--output-dir`，在大小限制内读取唯一的 transcript 文本，然后删除临时目录；不会把 `minutes/` 写到仓库工作目录。仓库忽略规则、Docker context 和源码打包也都排除意外残留的 `minutes/`。

## 3. 版本漂移

Parser 策略：

- 忽略未知字段；
- 在有限深度内寻找候选数组；
- 为 ID/title/snippet/time 配置字段别名；
- `Provenance.RawRef` 仅在进程内用于诊断，不参与 JSON 序列化；
- 缺失 canonical ID 时由 Kernel 根据 scope/kind/native ID 生成；
- 对新版 envelope 与 raw JSON 分支分别测试。

`doctor` 探测成功后，Provider Descriptor 会记录实际 `lark-cli --version` 输出；singleflight key 也包含 Provider version，防止升级后复用旧 in-flight 结果。

## 4. Scope 与身份

命令存在不代表当前身份能调用。`doctor` 会验证：

- 可执行文件；
- `lark-cli --version`；
- 每个 Provider operation 对应的命令是否存在；
- 各 operation 依赖的关键 flags 是否仍出现在命令帮助中。

这属于无租户数据的 capability handshake。`unsupported` 或 `version_incompatible` 只会让对应 operation 在执行前选择兼容备用实现；不会连带禁用同 Provider 的其他 operation。该检查不会代替 OAuth、scope 或对象可见性验证。

具体查询仍可能因以下原因失败：

```text
OAuth profile 不存在
user/bot 身份不符合接口
应用 scope 未开通
用户可见范围不足
对象本身无访问权限
CLI 版本的参数或输出发生变化
```

引擎将这类失败映射到每来源状态，其他来源继续执行。

## 5. 已知边界

- Drive Search v2 query 上限短，Planner 应使用 `source_queries.docs`；
- Calendar Fetch 需要 `calendar_id/event_id`；
- Mail Fetch 最好保留 `mailbox_id/message_id`；
- Base container 的 native ID 约定为 `app_token/table_id`；
- Sheets Query 需要 spreadsheet token，sheet ID 作为 filter；
- shortcut 可能已做详情增强，`ReturnedProjection` 用于避免重复 Fetch；
- 某些 shortcut 不支持真正批量 Fetch，Kernel 会按 `MaxFetchItems=1` 切批。

## 6. 当前真实验收边界

当前本机只读真实验证只覆盖到以下来源级结果：

| 状态 | 来源 |
|---|---|
| 成功 | docs、messages、chats、people、minutes、meetings、tasks |
| `missing_scope` | calendar、mail |
| 缺测试对象，未完成 | Base、Sheets |

因此，11 来源脱敏 fixture 通过只证明 Parser/参数/错误映射基线；不能据此表述为 11 来源真实租户验收通过。calendar/mail 需要补 scope 后重跑，Base/Sheets 需要提供已知 container/sheet 对象后重跑。

## 7. Live fixture 建议

目标环境执行：

```bash
sfs --backend larkcli --profile work --as user doctor
sfs --backend larkcli --profile work --as user \
  --record-dir .tmp/live-fixture \
  search "最小可见关键词" --sources docs,messages,minutes
```

录制目录可在 CI 中用 `--replay-dir` 离线回归。Fixture 应脱敏后进入仓库。
