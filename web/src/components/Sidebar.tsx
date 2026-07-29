import { formatDate, SOURCE_LABELS, STATUS_LABELS } from "../labels";
import type { CapabilitySnapshot, HealthSnapshot, SessionSnapshot } from "../types";

interface RuntimePanelProps {
  health?: HealthSnapshot;
  capabilities?: CapabilitySnapshot;
  loading: boolean;
  probing: boolean;
  error?: string;
  onProbe: () => void;
}

export function RuntimePanel({ health, capabilities, loading, probing, error, onProbe }: RuntimePanelProps) {
  return (
    <section className="side-card runtime-card" aria-labelledby="runtime-title">
      <div className="side-card-heading">
        <div>
          <p className="eyebrow">本机运行时</p>
          <h2 id="runtime-title">运行时与 Doctor</h2>
        </div>
        <span className={`connection-dot ${health?.ok ? "online" : "offline"}`} aria-hidden="true" />
      </div>
      {loading ? <p className="muted" role="status">正在读取本机能力…</p> : null}
      {error ? <p className="side-error" role="alert">{error}</p> : null}
      {health ? (
        <dl className="runtime-facts">
          <div><dt>Backend</dt><dd>{health.backend}</dd></div>
          <div><dt>Storage</dt><dd>{health.storage || "未报告"}</dd></div>
          <div><dt>Providers</dt><dd>{health.providers}</dd></div>
          <div><dt>AI Answer</dt><dd>{health.ai?.answer ? "已启用" : "未启用"}</dd></div>
        </dl>
      ) : null}
      <button className="secondary-button full-button" type="button" onClick={onProbe} disabled={probing || !health?.ok}>
        {probing ? "Doctor 检查中…" : "运行 Doctor"}
      </button>
      <p className="doctor-note">检查本机 Provider 版本和可用性；不会在浏览器中读取或展示凭据。</p>
      {capabilities?.providers?.length ? (
        <details className="provider-details">
          <summary>查看 {capabilities.providers.length} 个 Provider</summary>
          <ul>
            {capabilities.providers.map((provider) => (
              <li key={provider.descriptor.id}>
                <span className={`status-dot status-${provider.status}`} aria-hidden="true" />
                <span>
                  <strong>{SOURCE_LABELS[provider.descriptor.source] || provider.descriptor.source}</strong>
                  <small>{provider.descriptor.id} · {provider.descriptor.backend}</small>
                </span>
                <span className="provider-status">{STATUS_LABELS[provider.status] || provider.status}</span>
                {provider.error?.message ? <p>{provider.error.message}</p> : null}
                {Object.entries(provider.operations || {}).some(([, operation]) => operation.status !== "ok") ? (
                  <p>
                    操作异常：{Object.entries(provider.operations || {})
                      .filter(([, operation]) => operation.status !== "ok")
                      .map(([name, operation]) => `${name}=${STATUS_LABELS[operation.status] || operation.status}`)
                      .join("，")}
                  </p>
                ) : null}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
    </section>
  );
}

interface SessionsPanelProps {
  sessions: SessionSnapshot[];
  currentID?: string;
  loading: boolean;
  error?: string;
  onRefresh: () => void;
  onOpen: (id: string) => void;
  onDelete: (id: string) => void;
}

export function SessionsPanel({ sessions, currentID, loading, error, onRefresh, onOpen, onDelete }: SessionsPanelProps) {
  return (
    <section className="side-card sessions-card" aria-labelledby="sessions-title">
      <div className="side-card-heading">
        <div>
          <p className="eyebrow">本地历史</p>
          <h2 id="sessions-title">Session 历史</h2>
        </div>
        <button className="icon-button refresh-button" type="button" onClick={onRefresh} disabled={loading} aria-label="刷新 Session 历史">
          ↻
        </button>
      </div>
      {error ? <p className="side-error" role="alert">{error}</p> : null}
      {loading ? <p className="muted" role="status">正在读取历史…</p> : null}
      {!loading && sessions.length === 0 ? <p className="muted">暂无本地 Session。完成一次检索后会出现在这里。</p> : null}
      <ul className="session-list">
        {sessions.map((session) => (
          <li className={session.id === currentID ? "current" : ""} key={session.id}>
            <button className="session-open" type="button" onClick={() => onOpen(session.id)}>
              <strong>{session.request?.query || "无标题 Session"}</strong>
              <span>{formatDate(session.created_at)} · {session.candidates?.length || 0} 个候选</span>
            </button>
            <button
              className="session-delete"
              type="button"
              onClick={() => onDelete(session.id)}
              aria-label={`删除 Session：${session.request?.query || session.id}`}
            >
              ×
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}
