import { formatDate, safeExternalURL, SOURCE_LABELS } from "../labels";
import type { Candidate } from "../types";

interface ResultListProps {
  candidates: Candidate[];
  pendingPreviewID?: string;
  onPreview: (candidate: Candidate) => void;
}

export function ResultList({ candidates, pendingPreviewID, onPreview }: ResultListProps) {
  if (candidates.length === 0) {
    return (
      <div className="empty-state" role="status">
        <span className="empty-mark" aria-hidden="true">⌁</span>
        <strong>还没有结果</strong>
        <p>输入问题并选择执行模式。搜索只召回候选，深度检索和 AI 问答会按需读取证据。</p>
      </div>
    );
  }

  return (
    <ol className="result-list" aria-label="检索结果">
      {candidates.map((candidate, index) => {
        const identity = candidate.ref.canonical_id || candidate.ref.native_id || `${candidate.source}-${index}`;
        const href = safeExternalURL(candidate.url || candidate.ref.url);
        const pending = pendingPreviewID === identity;
        return (
          <li key={identity}>
            <article className="result-card">
              <div className="result-rank" aria-label={`排序 ${index + 1}`}>{String(index + 1).padStart(2, "0")}</div>
              <div className="result-body">
                <div className="result-meta">
                  <span className={`source-pill source-${candidate.source}`}>{SOURCE_LABELS[candidate.source] || candidate.source}</span>
                  <span>{candidate.kind}</span>
                  {candidate.timestamp ? <time dateTime={candidate.timestamp}>{formatDate(candidate.timestamp)}</time> : null}
                  <span className="score">RRF {candidate.fused_score.toFixed(4)}</span>
                </div>
                <h3>{candidate.title || candidate.ref.native_id || "未命名结果"}</h3>
                <p className="result-snippet">{candidate.snippet || "该候选没有返回摘要，读取后查看正文。"}</p>
                <div className="result-footer">
                  <div className="projection-list" aria-label="当前字段投影">
                    {(candidate.projection?.length ? candidate.projection : ["head"]).map((item) => (
                      <span key={item}>{item}</span>
                    ))}
                  </div>
                  <div className="result-actions">
                    <button
                      className="text-button"
                      type="button"
                      disabled={pending}
                      onClick={() => onPreview(candidate)}
                    >
                      {pending ? "读取中…" : "读取预览"}
                    </button>
                    {href ? (
                      <a className="text-link" href={href} target="_blank" rel="noreferrer">
                        打开来源<span className="sr-only">（新窗口）</span>
                      </a>
                    ) : null}
                  </div>
                </div>
              </div>
            </article>
          </li>
        );
      })}
    </ol>
  );
}
