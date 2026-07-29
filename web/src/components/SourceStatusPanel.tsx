import { SOURCE_LABELS, STATUS_LABELS } from "../labels";
import type { SourceRun } from "../types";

interface SourceStatusPanelProps {
  runs: SourceRun[];
}

export function SourceStatusPanel({ runs }: SourceStatusPanelProps) {
  if (runs.length === 0) return null;
  return (
    <section className="source-status-panel" aria-labelledby="source-status-title">
      <div className="section-heading compact-heading">
        <div>
          <p className="eyebrow">来源状态</p>
          <h2 id="source-status-title">来源执行状态</h2>
        </div>
        <span className="section-count">{runs.length} 个来源</span>
      </div>
      <ul className="source-run-list">
        {runs.map((run) => (
          <li className={`source-run status-${run.status}`} key={`${run.provider_id || "provider"}-${run.source}`}>
            <span className="status-dot" aria-hidden="true" />
            <span className="source-run-main">
              <strong>{SOURCE_LABELS[run.source] || run.source}</strong>
              <span>{STATUS_LABELS[run.status] || run.status}</span>
            </span>
            <span className="source-run-metrics">
              {run.hit_count} 条 · {run.elapsed_ms} ms
            </span>
            {run.error?.message ? <span className="source-run-error">{run.error.message}</span> : null}
          </li>
        ))}
      </ul>
    </section>
  );
}
