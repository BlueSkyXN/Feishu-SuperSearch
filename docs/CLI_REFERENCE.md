# CLI 参考

## 1. 基本语法

```text
sfs [全局参数] <命令> [命令参数]
```

全局参数必须位于命令之前：

```bash
sfs --backend mock --output json search "A 项目"
```

查看帮助：

```bash
sfs help
sfs --help
sfs --backend mock search --help
```

## 2. 全局参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `--config PATH` | 空 | JSON 配置文件 |
| `--backend NAME` | 配置或 `auto` | `auto/larkcli/openapi/mock/hybrid/replay` |
| `--session-dir PATH` | 空 | 明确选择旧 JSON File Store |
| `--database PATH` | 配置值 | 明确选择 SQLite；默认 `~/.sfs/sfs.db` |
| `--output FORMAT` | `pretty` | `pretty/json/ndjson` |
| `--profile NAME` | 空 | lark-cli profile |
| `--as MODE` | `auto` | `auto/user/bot` |
| `--listen ADDRESS` | 配置值 | HTTP 监听地址 |
| `--record-dir PATH` | 空 | 录制 Provider fixture |
| `--replay-dir PATH` | 空 | 从 fixture 离线回放，并将 Backend 切为 replay |

`--record-dir` 与 `--replay-dir` 互斥。

`auto` 在 lark-cli 与 OpenAPI 都可用时选择 `hybrid`；只存在一个真实后端时选择该后端。两者都不可用时报错，Mock 必须显式指定。

## 3. `version`

```bash
sfs version
```

输出版本、Git commit 和构建时间。

## 4. `providers` 与 `doctor`

```bash
sfs --backend mock providers
sfs --backend larkcli --profile work --as user doctor
```

- `providers`：返回启动 capability handshake 的缓存能力；
- `doctor`：对每个 Provider 执行 Health 探测。

## 5. `search`

```bash
sfs search QUERY [参数]
```

| 参数 | 说明 |
|---|---|
| `--sources docs,messages,...` | 指定来源；空值使用启用的全局来源 |
| `--source-queries-json JSON` | 按来源覆盖上游 query |
| `--strategy fast\|balanced\|deep` | 搜索档位 |
| `--pagination none\|adaptive\|fixed` | 分页策略 |
| `--limit N` | 全局结果上限 |
| `--per-source N` | 每来源每页条数 |
| `--after TIME` | RFC3339、`YYYY-MM-DD`、`14d`、`2w` |
| `--before TIME` | RFC3339 或日期 |
| `--doc-types LIST` | `docx,wiki,sheet` 等 |
| `--mine` | 仅我的对象 |
| `--only-title` | 仅标题匹配 |
| `--deadline DURATION` | 总时限，例如 `8s` |
| `--max-calls N` | 最大上游调用 |
| `--pages N` | 每来源最大页数 |
| `--max-fetches N` | 复用 Session/Plan 时可用的 Fetch 预算 |
| `--max-bytes N` | 最大响应字节预算 |
| `--session ID` | 复用已有 Session |
| `--no-source-quota` | 关闭来源配额 |

示例：

```bash
sfs --backend larkcli --profile work --as user search \
  "A 项目 延期" \
  --sources docs,messages,minutes \
  --after 14d \
  --strategy balanced \
  --limit 40
```

按来源 query：

```bash
sfs --backend mock search "A 项目为什么延期，谁做了决定" \
  --sources docs,messages,minutes \
  --source-queries-json '{
    "docs":"A 项目 延期",
    "messages":"A 项目 延期 决定",
    "minutes":"A 项目"
  }'
```

`ndjson` 模式逐行输出 Candidate，不输出完整 Snapshot 包装：

```bash
sfs --backend mock --output ndjson search "A 项目"
```

## 6. `continue`

```bash
sfs continue --session SESSION_ID [--sources LIST] [--pages N]
```

也可把 Session ID 作为位置参数：

```bash
sfs continue rs_xxx --sources messages,docs --pages 1
```

只会继续拥有有效 Cursor 的来源。

`continue` 使用全局 `--profile` / `--as` 派生 identity scope；不能继续另一个 scope 创建的 Session。

## 7. `query`

```bash
sfs query --source SOURCE --filter JSON [参数]
```

| 参数 | 说明 |
|---|---|
| `--source SOURCE` | 必填；例如 `tasks/base/sheets` |
| `--filter JSON` | Provider-specific filter |
| `--limit N` | 默认 50 |
| `--cursor TOKEN` | 继续对象内查询 |
| `--session ID` | 将结果写入/复用 Session |
| `--container-id ID` | Base/Sheet 等容器 ID |
| `--container-kind KIND` | `base_table`、`sheet` 等 |

任务：

```bash
sfs query --source tasks \
  --filter '{"query":"A 项目","completed":false}'
```

Base：

```bash
sfs query --source base \
  --container-id 'app_token/table_id' \
  --container-kind base_table \
  --filter '{"keyword":"延期","search_field":"项目名称"}'
```

Sheets：

```bash
sfs query --source sheets \
  --container-id spreadsheet_token \
  --container-kind sheet \
  --filter '{"find":"延期","sheet_id":"sheet_xxx"}'
```

## 8. `fetch`

```bash
sfs fetch --id ID [--id ID...] [参数]
```

| 参数 | 默认值 | 说明 |
|---|---|---|
| `--session ID` | 空 | 从 Session 查找 Candidate/Artifact |
| `--source SOURCE` | 空 | 无 Session 且使用 native ID 时需要 |
| `--kind KIND` | 空 | 无 Session 且 ID 无类型时建议提供 |
| `--projection LIST` | `content` | `head,summary,structure,content,context,relations,attachments` |
| `--id ID` | 无 | 可重复，也可作为位置参数 |

```bash
sfs fetch --session rs_xxx \
  --id feishu:scope:minute:min_xxx \
  --projection summary,structure,relations
```

Fetch 会合并重复对象和 Projection，并扣除 Session 中已经物化的部分。

## 9. `expand`

```bash
sfs expand --id ID [参数]
```

| 参数 | 默认值 | 说明 |
|---|---|---|
| `--session ID` | 空 | Session |
| `--source SOURCE` | 空 | native ID 对应来源 |
| `--kind KIND` | 空 | 对象类型 |
| `--relations LIST` | 空 | 逗号分隔关系；空值由 Provider 决定 |
| `--depth N` | 1 | 最大 2 |

```bash
sfs expand --session rs_xxx \
  --id feishu:scope:meeting:m_xxx \
  --relations meeting.has_minute \
  --depth 2
```

## 10. `resolve`

```bash
sfs resolve TEXT [--source people] [--kind KIND] [--limit N]
```

```bash
sfs --as user resolve "张三" --source people --limit 10
```

## 11. `plan`

```bash
sfs plan [FILE|-] [--events]
```

- 文件省略或 `-`：从 stdin 读取；
- `--events` 或全局 `--output ndjson`：流式输出 RetrievalEvent，最后输出 Result；
- 其他输出模式：等待计划完成后返回 Result + Event 列表。

```bash
sfs --backend mock plan examples/plan-search-fetch.json
sfs --backend mock --output ndjson \
  plan examples/plan-search-fetch.json --events
```

## 12. `research` / `ask`

两个命令共享参数，但语义不同：

- `research`：确定性 Search→Fetch→Evidence，AI 关闭时仍可用；
- `ask`：在 Research 后调用可选 Answerer，只依据 Evidence Pack 输出 claims/citations；未启用时返回 `unsupported`。

```bash
sfs research QUERY [参数]
```

| 参数 | 默认值 | 说明 |
|---|---:|---|
| `--sources LIST` | Planner 决定 | 限定来源 |
| `--fetch-top N` | 8 | Fetch 前 N 个候选 |
| `--limit N` | 40 | Candidate 上限 |
| `--deadline DURATION` | 15 秒 | 总时限 |
| `--planner TYPE` | 配置值 | `rules/default/command/ai` |
| `--planner-command PATH` | 空 | 外部 Planner 命令 |
| `--planner-args-json JSON` | 空 | 外部 Planner 参数数组 |
| `--planner-timeout DURATION` | 配置值 | Planner 时限 |

```bash
sfs --backend mock research \
  "A 项目为什么延期，还有哪些任务未完成" \
  --sources docs,messages,minutes,tasks \
  --fetch-top 6
```

启用 AI 后问答：

```bash
sfs --config ~/.sfs/config.json ask \
  "A 项目为什么延期" --sources docs,messages,minutes --fetch-top 8
```

## 13. `sessions`

```bash
sfs sessions list
sfs sessions get SESSION_ID
sfs sessions rm SESSION_ID
```

SQLite 和文件 Session 都按配置 TTL 清理。不同 identity scope 不能复用同一 Session 对象缓存。

这三个命令同样使用全局 `--profile` / `--as`。例如：

```bash
sfs --profile work --as user sessions list
sfs --profile work --as user sessions get rs_xxx
```

## 14. `migrate sessions`

```bash
sfs migrate sessions --from ~/.sfs/sessions --to ~/.sfs/sfs.db
```

导入是幂等的；已存在的 Session 不会被旧 JSON 覆盖，源文件不会删除或修改。输出包含 `scanned/imported/already_present/invalid/expired/skipped`。

## 15. `serve`

```bash
sfs serve [--listen ADDRESS]
```

```bash
sfs --backend mock serve --listen 127.0.0.1:3765
```

同时提供 HTTP API 与内嵌 Web 页面。

## 16. `mcp`

```bash
sfs --backend mock mcp
```

使用 stdin/stdout 运行 MCP Server。stdout 只用于协议消息。

## 17. 退出码和错误

- `0`：成功或正常帮助；
- `2`：CLI 参数或命令错误；
- `1`：其他错误。

错误输出为：

```json
{
  "ok": false,
  "error": {
    "type": "invalid_request",
    "message": "..."
  }
}
```

复杂搜索可能返回 HTTP/CLI 成功但 `partial=true`，此时应检查每个 `SourceRun`，不能只看进程退出码。
