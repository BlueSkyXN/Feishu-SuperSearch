# 故障排查

## 1. `unknown command` 或全局参数不生效

全局参数必须位于命令之前。

错误：

```bash
sfs search "A 项目" --backend mock
```

正确：

```bash
sfs --backend mock search "A 项目"
```

查看帮助：

```bash
sfs help
sfs --backend mock search --help
```

## 2. `lark-cli` 找不到

```bash
which lark-cli
lark-cli version
```

指定路径：

```bash
export SFS_LARKCLI=/absolute/path/to/lark-cli
sfs --backend larkcli doctor
```

或在配置中设置 `lark_cli.executable`。

## 3. profile 不存在或未登录

```bash
lark-cli auth status
sfs --backend larkcli --profile work --as user doctor
```

SuperFeishuSearch 不创建 OAuth profile。先用 lark-cli 完成登录。

## 4. `missing_scope`

这表示命令存在，但当前应用/身份没有所需 scope。检查：

- user 与 bot 是否选错；
- profile 是否对应预期应用；
- 应用是否已开通接口 scope；
- 管理员是否已批准；
- 用户是否对目标对象可见。

不要把 `missing_scope` 当成“没有搜索结果”。

## 5. `identity_required`

某些来源只支持 user 或 bot。显式指定：

```bash
sfs --backend larkcli --profile work --as user search "..."
```

查看 Provider Descriptor 的 `required_identity`：

```bash
sfs --backend larkcli providers
```

## 6. 某些来源失败但命令退出成功

联邦搜索允许部分成功。检查：

```json
{
  "partial": true,
  "sources": [
    {
      "source": "mail",
      "status": "failed",
      "error": {}
    }
  ]
}
```

已有 Candidate 仍然可用。根据失败类型决定补 scope、缩小范围或重试。

## 7. 搜索有 ID 但没有正文

这是正常的 Late Materialization。检查 Candidate：

```json
{
  "projection": ["head", "snippet"],
  "available_projection": ["content", "context"]
}
```

随后调用：

```bash
sfs fetch --session rs_xxx --id OBJECT_ID --projection content,context
```

## 8. Fetch 返回 unsupported

可能原因：

- 当前 Provider 只实现 Search；
- 请求了不在 `fetchable_projection` 中的 Projection；
- 对象来自 OpenAPI Search，但 Fetch 路由未配置到 lark-cli；
- native ID 缺少 source/kind/container 信息。

检查：

```bash
sfs providers
```

混合路由示例见 `examples/config-openapi-route.json`。

## 9. Continue 没有新增结果

检查：

- Session 是否过期；
- `continuations[].has_more`；
- 指定来源是否有 Cursor；
- 已达到 `max_pages_per_source` 或调用预算；
- 新结果是否被去重；
- 来源是否已经稳定，不再进入全局 Top-K。

## 10. Session not found

可能原因：

- 使用内存 Session 且进程已重启；
- TTL 已过；
- `--session-dir` 指向了不同目录；
- Session ID 拼写错误；
- 文件权限不足。

```bash
sfs sessions list
```

## 11. Session identity mismatch

Session 与 profile/mode 归一化后的 scope key 绑定。不要用另一个用户、bot 或 profile 读取同一 Session。

重新搜索，或使用正确的：

```bash
--profile ... --as ...
```

## 12. `parse_error`

常见于 lark-cli 升级后输出结构变化。步骤：

1. 记录 `lark-cli version`；
2. 运行最小命令；
3. 使用 `--record-dir` 录制脱敏 fixture；
4. 对比 `adapter/larkcli` Parser；
5. 不要仅通过字符串搜索强行吞掉错误。

兼容策略见 [lark-cli 兼容策略](LARKCLI_COMPATIBILITY.md)。

## 13. `rate_limited`

降低：

```text
global_concurrency
limit_per_source
max_pages_per_source
max_fetches
```

Retry 应有限，并遵守上游建议等待时间。不要同时让 Planner 和 Provider 各自无限重试。

## 14. `deadline_exceeded`

区分：

- 整个 Search/Plan deadline；
- 单 Provider timeout；
- lark-cli 子进程 timeout；
- HTTP 客户端 timeout；
- 外部 Planner/Provider timeout。

适当增加最外层 deadline，同时避免内层 timeout 大于外层剩余时间。

## 15. `budget_exhausted`

响应可能仍含部分结果。检查 BudgetState：

```text
calls_used / max_calls
fetches_used / max_fetches
expanded_used / max_expanded
bytes_used / max_bytes
```

优先缩小来源、结果数和 Projection，而不是直接把所有预算翻倍。

## 16. Base / Sheets 搜不到

它们默认不是全局 Search 来源。

正确流程：

```text
先通过 docs 搜到 Base/Sheet 文件
→ 获得 app_token / spreadsheet_token
→ query --source base/sheets
```

Base 还需要 table ID；Sheets 通常还需要 sheet ID。

## 17. HTTP 499

499 表示客户端取消了请求或连接断开。SSE 客户端退出、反向代理超时或浏览器中断都可能触发。

## 18. MCP 无响应

检查：

- stdout 是否被日志污染；
- Host 是否使用 stdio；
- `sfs ... mcp` 参数顺序；
- 进程工作目录；
- 配置和外部脚本使用绝对路径；
- Host 是否发送了 initialize。

可先直接运行并用 MCP 调试器连接。

## 19. Docker 中无法使用 lark-cli

默认镜像不包含 lark-cli 或 OAuth profile。使用 Mock/OpenAPI，或构建自己的扩展镜像。不要简单挂载整个宿主 Home。

## 20. 仍无法定位

收集以下脱敏信息：

```text
sfs version
OS/architecture
backend
lark-cli version（如适用）
Provider Descriptor
命令和非敏感配置
ErrorDetail
SourceRun
是否可在 mock/replay 复现
```

Bug 报告见仓库 Issue Template；安全问题按 `SECURITY.md` 私密报告。
