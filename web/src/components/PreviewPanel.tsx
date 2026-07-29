import { safeExternalURL, SOURCE_LABELS } from "../labels";
import type { Artifact, Candidate } from "../types";

interface PreviewPanelProps {
  candidate: Candidate;
  artifact?: Artifact;
  error?: string;
  onClose: () => void;
}

export function PreviewPanel({ candidate, artifact, error, onClose }: PreviewPanelProps) {
  const href = safeExternalURL(candidate.url || candidate.ref.url);
  return (
    <section className="preview-panel" aria-labelledby="preview-title" tabIndex={-1}>
      <div className="preview-header">
        <div>
          <p className="eyebrow">对象预览</p>
          <h2 id="preview-title">{candidate.title || candidate.ref.native_id}</h2>
        </div>
        <button className="icon-button" type="button" onClick={onClose} aria-label="关闭预览">×</button>
      </div>
      <div className="preview-meta">
        <span>{SOURCE_LABELS[candidate.source]}</span>
        <span>{candidate.kind}</span>
        {artifact?.projection?.map((item) => <span key={item}>{item}</span>)}
      </div>
      {error ? <div className="inline-error" role="alert">{error}</div> : null}
      {!artifact && !error ? <div className="preview-loading" role="status">正在读取可用字段…</div> : null}
      {artifact ? (
        <div className="preview-content">
          {artifact.summary?.text ? (
            <section>
              <h3>原生摘要</h3>
              <p>{artifact.summary.text}</p>
            </section>
          ) : null}
          {artifact.summary?.todos?.length ? (
            <section>
              <h3>待办</h3>
              <ul className="todo-list">
                {artifact.summary.todos.map((todo, index) => (
                  <li key={index}>{Object.entries(todo).map(([key, value]) => `${key}: ${String(value)}`).join(" · ")}</li>
                ))}
              </ul>
            </section>
          ) : null}
          {artifact.chunks?.length ? (
            <section>
              <h3>正文片段</h3>
              <ol className="chunk-list">
                {artifact.chunks.map((chunk, index) => (
                  <li key={chunk.id || index}>
                    {chunk.kind ? <span>{chunk.kind}</span> : null}
                    <p>{chunk.text}</p>
                  </li>
                ))}
              </ol>
            </section>
          ) : null}
          {!artifact.summary?.text && !artifact.chunks?.length ? <p>该对象没有返回可预览的正文。</p> : null}
        </div>
      ) : null}
      {href ? <a className="preview-source-link" href={href} target="_blank" rel="noreferrer">在飞书中打开来源</a> : null}
    </section>
  );
}
