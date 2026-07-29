import { safeExternalURL, SOURCE_LABELS } from "../labels";
import type { Evidence, WorkspaceResult } from "../types";

interface EvidencePanelProps {
  result: WorkspaceResult;
}

function EvidenceLink({ evidence }: { evidence: Evidence }) {
  const href = safeExternalURL(evidence.url || evidence.source_ref.url);
  const source = evidence.source_ref.source ? SOURCE_LABELS[evidence.source_ref.source] : evidence.source_ref.kind;
  return href ? (
    <a href={href} target="_blank" rel="noreferrer">
      {source || "来源"}<span className="sr-only">（新窗口）</span>
    </a>
  ) : (
    <span>{source || "来源"}</span>
  );
}

export function EvidencePanel({ result }: EvidencePanelProps) {
  const hasAnswer = Boolean(result.answer?.text);
  const hasResearch = Boolean(result.summary || result.evidence.length);
  if (!hasAnswer && !hasResearch && result.warnings.length === 0) return null;

  return (
    <section className="evidence-panel" aria-labelledby="evidence-title">
      <div className="section-heading">
        <div>
          <p className="eyebrow">可追溯输出</p>
          <h2 id="evidence-title">{hasAnswer ? "回答与引用" : "证据整理"}</h2>
        </div>
        <span className="section-count">{result.evidence.length} 条证据</span>
      </div>

      {result.warnings.length > 0 ? (
        <div className="warning-stack" role="status">
          {result.warnings.map((warning, index) => <p key={`${warning}-${index}`}>{warning}</p>)}
        </div>
      ) : null}

      {hasAnswer ? (
        <article className="answer-card">
          <div className="answer-badge">{result.answerer || "AI"}</div>
          <p className="answer-text">{result.answer?.text}</p>
          {result.answer?.partial ? <p className="partial-note">回答标记为部分结果，请结合引用复核。</p> : null}
          {result.answer?.claims?.length ? (
            <div className="claim-list">
              <h3>可核验结论</h3>
              <ul>
                {result.answer.claims.map((claim, index) => (
                  <li key={`${claim.text}-${index}`}>
                    <span>{claim.text}</span>
                    <span className="evidence-ids">{claim.evidence_ids.join(" · ")}</span>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {result.answer?.citations?.length ? (
            <div className="citation-list">
              <h3>引用</h3>
              <ol>
                {result.answer.citations.map((citation) => {
                  const href = safeExternalURL(citation.url || citation.source_ref.url);
                  return (
                    <li key={citation.id}>
                      <span className="citation-id">{citation.id}</span>
                      <q>{citation.quote}</q>
                      {href ? <a href={href} target="_blank" rel="noreferrer">查看来源</a> : null}
                    </li>
                  );
                })}
              </ol>
            </div>
          ) : null}
        </article>
      ) : result.summary ? <p className="research-summary">{result.summary}</p> : null}

      {result.evidence.length > 0 ? (
        <div className="evidence-grid">
          {result.evidence.map((evidence) => (
            <article className="evidence-card" id={`evidence-${evidence.id}`} key={evidence.id}>
              <div className="evidence-meta">
                <span className="evidence-id">{evidence.id}</span>
                <span>{evidence.claim_type || evidence.kind}</span>
                <span>{Math.round(evidence.confidence * 100)}%</span>
              </div>
              <blockquote>{evidence.quote || evidence.text}</blockquote>
              <footer>
                <EvidenceLink evidence={evidence} />
                <span>{evidence.source_span?.chunk_id || evidence.projection?.join(", ")}</span>
              </footer>
            </article>
          ))}
        </div>
      ) : null}
    </section>
  );
}
