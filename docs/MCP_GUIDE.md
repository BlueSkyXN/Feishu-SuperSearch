# MCP 指南

## 1. 启动方式

```bash
sfs --backend mock mcp
```

真实飞书：

```bash
sfs --backend larkcli --profile work --as user mcp
```

MCP 使用 stdio：

- stdin：MCP 请求；
- stdout：只输出协议消息；
- stderr：宿主可收集的诊断信息。

不要通过会向 stdout 打日志的 shell 包装器启动 MCP。

## 2. 工具列表

| 工具 | 作用 |
|---|---|
| `feishu_search` | 多来源搜索，返回 Candidate、来源状态、Cursor 和 Session |
| `feishu_continue` | 继续已有 Session 的来源分页 |
| `feishu_query` | 任务、Base、Sheets 等结构化或对象内查询 |
| `feishu_fetch` | 读取正文、上下文、原生摘要、结构、关系或附件 |
| `feishu_expand` | 沿受支持关系发现关联对象，最大深度 2 |
| `feishu_resolve` | 将名称、URL 或别名解析成 ObjectRef |
| `feishu_research` | 有界 Search→Fetch→Evidence 流程 |
| `feishu_ask` | 基于 Evidence Pack 的可选 AI 问答；返回 claims/citations |
| `feishu_get_session` | 读取完整 Session |
| `feishu_capabilities` | 查看 Provider、身份和 Projection 能力 |

所有工具都声明为只读语义；具体飞书 OpenAPI 的 risk 标记仍由上游身份和接口决定。

当前共 10 个工具。`feishu_research` 保持确定性语义；`feishu_ask` 不会在 AI 未启用时伪装成功，而会返回 `isError=true` 和 `unsupported` 结构化错误。

## 3. 推荐 Agent 调用顺序

### 找对象

```text
feishu_search
→ 展示候选或直接返回链接
```

### 回答事实问题

```text
feishu_search
→ 从 Candidate 中选择少量高相关对象
→ feishu_fetch
→ 仅依据 Artifact/Evidence 回答
```

### 多跳对象

```text
feishu_search / feishu_resolve
→ feishu_expand
→ feishu_fetch
→ 回答
```

### 第一轮不足

```text
检查 source status 与 continuation
→ feishu_continue 或更精确的 feishu_search
→ 再选择 Fetch
```

不要一开始对所有 Candidate 执行全文 Fetch。

## 4. Candidate 选择

Agent 应优先考虑：

1. `fused_score`；
2. 标题和 snippet 是否直接命中；
3. 时间与人员过滤；
4. 来源是否适合问题；
5. 当前 `projection` 是否已足够；
6. `available_projection` 是否能补齐所需内容；
7. 多来源发现与 provenance。

推荐每轮 Fetch 5–10 个对象，而不是几十个对象全文读取。

## 5. Session

`feishu_search` 返回的 `session_id` 应传给后续：

- `feishu_continue`；
- `feishu_fetch`；
- `feishu_expand`；
- `feishu_get_session`。
- 对需要最终回答且 AI 已启用的请求，可直接使用 `feishu_ask`；其 citation ID 必须能回溯到返回的 Evidence Pack。

Session 可以避免重复 Fetch，并保留 Cursor、Artifact 和来源状态。Session 与 identity scope 绑定，不能跨用户/profile 复用。

## 6. Projection

| Projection | 用途 |
|---|---|
| `head` | ID、标题、类型、作者、时间等元数据 |
| `snippet` | 搜索命中摘要 |
| `summary` | Provider 原生摘要，如妙记摘要 |
| `structure` | 文档目录、妙记章节、线程结构 |
| `content` | 正文或相关内容块 |
| `context` | 消息前后文、邮件线程等 |
| `relations` | 关联对象 |
| `attachments` | 附件信息 |

Agent 应只请求解决当前问题所需的 Projection。

## 7. 宿主配置示例

不同 MCP Host 的配置格式不同，核心命令形式如下：

```json
{
  "command": "/absolute/path/to/sfs",
  "args": [
    "--backend", "larkcli",
    "--profile", "work",
    "--as", "user",
    "mcp"
  ]
}
```

工作目录应能访问配置文件和外部 Provider/Planner 脚本。需要固定配置时：

```json
{
  "command": "/absolute/path/to/sfs",
  "args": ["--config", "/absolute/path/to/config.json", "mcp"]
}
```

## 8. 错误处理

Agent 不应把下面情况都解释为“没有搜索结果”：

- `missing_scope`；
- `identity_required`；
- `deadline_exceeded`；
- `rate_limited`；
- Provider `parse_error`；
- `partial=true`；
- 某来源 `status=failed`。

应向用户明确说明可用来源与失败来源，并在必要时缩小范围或请求正确身份。

## 9. 与 SKILL 的关系

`skills/super-feishu-search/SKILL.md` 只描述调用策略；MCP 提供结构化工具；Retrieval Kernel 负责实际执行。不要在 SKILL 中复制 lark-cli 的具体多步命令链。
