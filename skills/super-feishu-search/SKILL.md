---
name: super-feishu-search
description: 使用 SuperFeishuSearch 在飞书文档、消息、群、联系人、妙记、会议、日程、任务和邮件中检索，并按需读取证据。
---

# SuperFeishuSearch

## 何时使用

用户要找飞书中的资料、消息、会议纪要、任务、日程、人员或邮件时，先调用 `feishu_search`。复杂事实问题应在候选中选择少量对象，再调用 `feishu_fetch`。

## 标准流程

1. 调用 `feishu_search`，通常每来源 5–10 条，总候选不超过 40–60 条。
2. 查看每个 Candidate 的 `source`、`kind`、`snippet`、`projection` 和 `available_projection`。
3. 只选最相关且来源多样的 3–8 个对象。
4. 调用 `feishu_fetch`：
   - 文档：`structure,content`
   - 消息：`content,context,relations`
   - 妙记：先 `summary,relations`，不足再 `content`
   - 任务：`content`
   - 会议：`content,relations`
5. 基于 Artifact 内容回答，并引用 `ref.url` 或 `source_ref`。

## 结构化查询

以下场景优先 `feishu_query`，不要强行全文搜索：

- 未完成任务
- 已知 Base/Table 后查记录
- 已知 Sheet 后查单元格

## 关系展开

仅在存在明确关系需求时调用 `feishu_expand`：

- meeting → minute
- message → document

默认深度 1，最大深度 2。

## 继续分页

第一轮结果不足，并且 response 中某来源 `has_more=true` 时，调用 `feishu_continue`。不要把所有来源翻到底。

## 有界深度检索

`feishu_research` 会运行规则化 Search→Fetch→Evidence 流程，适合快速生成候选包和证据包。它返回的 `summary` 是确定性整理，不等同于经过语义判断的最终答案；回答仍应以 Evidence Pack 为依据。

## 基于证据问答

当 `feishu_ask` 可用且用户需要综合结论时，可以直接调用它。它会执行检索、深读、证据提取，再让 Answerer 只依据 Evidence Pack 回答。

- 每个 claim 的 `evidence_ids` 必须存在于返回的 Evidence Pack；
- 引用展示 `citation.quote` 与 `citation.url`，不要只显示模型文本；
- `partial=true` 或 warnings 非空时，向用户说明覆盖边界；
- 返回 `unsupported` 表示本机未启用 AI，改用 `feishu_research`，不要自行编造答案。

## 错误处理

- 某个来源 `missing_scope`：保留其他来源结果，并说明该来源不可用。
- `partial=true`：不要声称检索覆盖完整。
- 没有证据：明确说未找到，不根据标题或 snippet 编造结论。
- 不要直接生成或拼接任意 `lark-cli` shell 命令；使用结构化工具。
