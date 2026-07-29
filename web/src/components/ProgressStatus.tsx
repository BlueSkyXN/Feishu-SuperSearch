import { MODE_LABELS } from "../labels";
import type { RunMode, WorkflowProgressEvent } from "../types";

interface ProgressStatusProps {
  mode: RunMode;
  elapsedSeconds: number;
  event?: WorkflowProgressEvent;
  onCancel: () => void;
}

export function ProgressStatus({ mode, elapsedSeconds, event, onCancel }: ProgressStatusProps) {
  const fallback = mode === "search"
    ? "内核正在并发召回并融合候选。"
    : mode === "research"
      ? "等待服务端检索阶段事件。"
      : "等待服务端检索与回答阶段事件。";
  const detail = event?.message || (event?.retrieval
    ? `${event.retrieval.source || "检索内核"} · ${event.retrieval.type}${event.retrieval.node_id ? ` · ${event.retrieval.node_id}` : ""}`
    : fallback);
  return (
    <div className="progress-status" role="status" aria-live="polite">
      <span className="progress-spinner" aria-hidden="true" />
      <span>
        <strong>{MODE_LABELS[mode]}执行中 · {elapsedSeconds}s</strong>
        <small>{detail}</small>
      </span>
      <button className="text-button" type="button" onClick={onCancel}>取消</button>
    </div>
  );
}
