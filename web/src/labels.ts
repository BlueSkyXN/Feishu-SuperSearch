import type { RunMode, SourceID, SourceStatus } from "./types";

export const SOURCE_ORDER: SourceID[] = [
  "docs",
  "messages",
  "minutes",
  "meetings",
  "tasks",
  "calendar",
  "people",
  "chats",
  "mail",
  "base",
  "sheets",
];

export const SOURCE_LABELS: Record<SourceID, string> = {
  docs: "文档",
  messages: "消息",
  chats: "群组",
  people: "联系人",
  minutes: "妙记",
  meetings: "会议",
  calendar: "日程",
  tasks: "任务",
  mail: "邮件",
  base: "多维表格",
  sheets: "电子表格",
};

export const STATUS_LABELS: Record<SourceStatus, string> = {
  ok: "正常",
  partial: "部分成功",
  empty: "无结果",
  unavailable: "不可用",
  missing_scope: "缺少权限",
  failed: "失败",
  deadline_exceeded: "超时",
  budget_exhausted: "预算耗尽",
};

export const MODE_LABELS: Record<RunMode, string> = {
  search: "搜索",
  research: "深度检索",
  ask: "AI 问答",
};

export function safeExternalURL(value?: string): string | undefined {
  if (!value) return undefined;
  try {
    const url = new URL(value, window.location.origin);
    if (url.protocol === "http:" || url.protocol === "https:") return url.href;
  } catch {
    // Invalid upstream URLs remain non-clickable but are still visible in data.
  }
  return undefined;
}

export function formatDate(value?: string): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}
