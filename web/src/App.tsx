import { FormEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, APIError } from "./api";
import { EvidencePanel } from "./components/EvidencePanel";
import { PreviewPanel } from "./components/PreviewPanel";
import { ProgressStatus } from "./components/ProgressStatus";
import { ResultList } from "./components/ResultList";
import { RuntimePanel, SessionsPanel } from "./components/Sidebar";
import { SourceStatusPanel } from "./components/SourceStatusPanel";
import { MODE_LABELS, SOURCE_LABELS, SOURCE_ORDER } from "./labels";
import type {
  Artifact,
  Candidate,
  CapabilitySnapshot,
  HealthSnapshot,
  RunMode,
  SessionSnapshot,
  SourceID,
  WorkflowProgressEvent,
  WorkspaceResult,
} from "./types";

function errorMessage(error: unknown): string {
  if (error instanceof APIError) return `${error.message}${error.hint ? ` · ${error.hint}` : ""}`;
  if (error instanceof Error) return error.message;
  return "发生未知错误";
}

function isAbort(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

function sourceFailed(status: string): boolean {
  return ["partial", "unavailable", "missing_scope", "failed", "deadline_exceeded", "budget_exhausted"].includes(status);
}

export default function App() {
  const [mode, setMode] = useState<RunMode>("search");
  const [query, setQuery] = useState("");
  const [selectedSources, setSelectedSources] = useState<Set<SourceID>>(new Set());
  const [health, setHealth] = useState<HealthSnapshot>();
  const [capabilities, setCapabilities] = useState<CapabilitySnapshot>();
  const [runtimeLoading, setRuntimeLoading] = useState(true);
  const [runtimeError, setRuntimeError] = useState<string>();
  const [probing, setProbing] = useState(false);
  const [sessions, setSessions] = useState<SessionSnapshot[]>([]);
  const [sessionsLoading, setSessionsLoading] = useState(true);
  const [sessionsError, setSessionsError] = useState<string>();
  const [result, setResult] = useState<WorkspaceResult>();
  const [requestError, setRequestError] = useState<string>();
  const [statusMessage, setStatusMessage] = useState("准备就绪");
  const [loading, setLoading] = useState(false);
  const [elapsedSeconds, setElapsedSeconds] = useState(0);
  const [progress, setProgress] = useState<WorkflowProgressEvent>();
  const [pendingPreviewID, setPendingPreviewID] = useState<string>();
  const [preview, setPreview] = useState<{ candidate: Candidate; artifact?: Artifact; error?: string }>();
  const requestController = useRef<AbortController>();
  const previewRef = useRef<HTMLDivElement>(null);

  const searchableSources = useMemo(() => {
    const available = new Set<SourceID>();
    for (const provider of capabilities?.providers || []) {
      if (provider.descriptor.operations?.search && !["unavailable", "failed"].includes(provider.status)) {
        available.add(provider.descriptor.source);
      }
    }
    return available;
  }, [capabilities]);

  const refreshSessions = useCallback(async (silent = false) => {
    if (!silent) setSessionsLoading(true);
    setSessionsError(undefined);
    try {
      const response = await api.sessions();
      setSessions(response.sessions || []);
    } catch (error) {
      setSessionsError(errorMessage(error));
    } finally {
      setSessionsLoading(false);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void Promise.all([api.health(controller.signal), api.capabilities(false, controller.signal)])
      .then(([nextHealth, nextCapabilities]) => {
        setHealth(nextHealth);
        setCapabilities(nextCapabilities);
        const defaults = new Set<SourceID>();
        for (const provider of nextCapabilities.providers || []) {
          if (provider.descriptor.operations?.search && !["unavailable", "failed"].includes(provider.status)) {
            defaults.add(provider.descriptor.source);
          }
        }
        setSelectedSources(defaults);
        setRuntimeError(undefined);
      })
      .catch((error) => {
        if (!isAbort(error)) setRuntimeError(errorMessage(error));
      })
      .finally(() => setRuntimeLoading(false));
    void refreshSessions();
    return () => controller.abort();
  }, [refreshSessions]);

  useEffect(() => {
    if (!loading) {
      setElapsedSeconds(0);
      return;
    }
    const started = Date.now();
    const timer = window.setInterval(() => setElapsedSeconds(Math.floor((Date.now() - started) / 1000)), 500);
    return () => window.clearInterval(timer);
  }, [loading]);

  const askUnavailable = Boolean(health && !health.ai?.answer);

  function toggleSource(source: SourceID) {
    setSelectedSources((current) => {
      const next = new Set(current);
      if (next.has(source)) next.delete(source);
      else next.add(source);
      return next;
    });
  }

  async function run(event?: FormEvent) {
    event?.preventDefault();
    const trimmed = query.trim();
    if (!trimmed) {
      setRequestError("请输入要查找的问题或关键词。");
      return;
    }
    if (selectedSources.size === 0) {
      setRequestError("至少选择一个可搜索来源。");
      return;
    }
    if (mode === "ask" && askUnavailable) {
      setRequestError("当前运行时没有启用 AI Answer，请改用深度检索查看证据。");
      return;
    }

    requestController.current?.abort();
    const controller = new AbortController();
    requestController.current = controller;
    setLoading(true);
    setRequestError(undefined);
    setStatusMessage(`${MODE_LABELS[mode]}请求已发送`);
    setProgress(undefined);
    setPreview(undefined);
    const sources = SOURCE_ORDER.filter((source) => selectedSources.has(source));
    try {
      if (mode === "search") {
        const demo = health?.backend === "mock";
        const response = await api.search(trimmed, sources, controller.signal, demo ? 1 : 8, demo ? "none" : "adaptive");
        setResult({
          mode,
          query: response.query,
          sessionID: response.session_id,
          candidates: response.candidates || [],
          sourceRuns: response.sources || [],
          continuations: response.continuations || [],
          partial: response.partial,
          evidence: [],
          warnings: [],
          elapsedMS: response.stats?.elapsed_ms,
        });
        setStatusMessage(`搜索完成：${response.candidates?.length || 0} 个候选`);
      } else if (mode === "research") {
        const response = await api.research(trimmed, sources, {
          signal: controller.signal,
          onEvent: (event) => {
            setProgress(event);
            if (event.message) setStatusMessage(event.message);
          },
        });
        setResult({
          mode,
          query: response.candidate_pack.query,
          sessionID: response.plan_result.session_id || response.candidate_pack.session_id,
          candidates: response.candidate_pack.candidates || [],
          sourceRuns: response.candidate_pack.sources || [],
          continuations: [],
          partial: response.plan_result.partial,
          summary: response.summary,
          evidence: response.evidence_pack.evidence || [],
          warnings: response.warnings || [],
        });
        setStatusMessage(`深度检索完成：${response.evidence_pack.evidence?.length || 0} 条证据`);
      } else {
        const response = await api.ask(trimmed, sources, {
          signal: controller.signal,
          onEvent: (event) => {
            setProgress(event);
            if (event.message) setStatusMessage(event.message);
          },
        });
        const research = response.research;
        setResult({
          mode,
          query: research.candidate_pack.query,
          sessionID: research.plan_result.session_id || research.candidate_pack.session_id,
          candidates: research.candidate_pack.candidates || [],
          sourceRuns: research.candidate_pack.sources || [],
          continuations: [],
          partial: response.partial,
          summary: research.summary,
          evidence: research.evidence_pack.evidence || [],
          answer: response.answer,
          answerer: response.answerer,
          warnings: [...(response.warnings || []), ...(response.answer.warnings || [])],
        });
        setStatusMessage(`AI 问答完成：${response.answer.citations?.length || 0} 条引用`);
      }
      await refreshSessions(true);
    } catch (error) {
      if (requestController.current !== controller) return;
      if (isAbort(error)) setStatusMessage("请求已取消");
      else {
        setRequestError(errorMessage(error));
        setStatusMessage("请求失败");
      }
    } finally {
      if (requestController.current === controller) {
        requestController.current = undefined;
        setLoading(false);
      }
    }
  }

  async function runDoctor() {
    setProbing(true);
    setRuntimeError(undefined);
    try {
      const response = await api.capabilities(true);
      setCapabilities(response);
    } catch (error) {
      setRuntimeError(errorMessage(error));
    } finally {
      setProbing(false);
    }
  }

  async function continueSearch() {
    if (!result?.sessionID) return;
    const sources = result.continuations.filter((item) => item.has_more).map((item) => item.source);
    if (sources.length === 0) return;
    setLoading(true);
    setRequestError(undefined);
    try {
      const response = await api.continueSearch(result.sessionID, sources);
      setResult({
        ...result,
        candidates: response.candidates || [],
        sourceRuns: response.sources || [],
        continuations: response.continuations || [],
        partial: response.partial,
        elapsedMS: response.stats?.elapsed_ms,
      });
      setStatusMessage(`已继续加载：共 ${response.candidates?.length || 0} 个候选`);
      await refreshSessions(true);
    } catch (error) {
      setRequestError(errorMessage(error));
    } finally {
      setLoading(false);
    }
  }

  async function openPreview(candidate: Candidate) {
    if (!result?.sessionID) {
      setRequestError("当前结果没有可用 Session，无法读取正文。");
      return;
    }
    const identity = candidate.ref.canonical_id || candidate.ref.native_id;
    setPendingPreviewID(identity);
    setPreview({ candidate });
    window.setTimeout(() => previewRef.current?.scrollIntoView({ behavior: "smooth", block: "start" }), 0);
    try {
      const response = await api.fetchCandidate(result.sessionID, candidate);
      const item = response.items?.[0];
      setPreview({ candidate, artifact: item?.artifact, error: item?.error?.message || (!item?.artifact ? "上游没有返回可预览内容。" : undefined) });
      await refreshSessions(true);
    } catch (error) {
      setPreview({ candidate, error: errorMessage(error) });
    } finally {
      setPendingPreviewID(undefined);
    }
  }

  async function openSession(id: string) {
    setSessionsError(undefined);
    try {
      const session = await api.session(id);
      const partial = (session.source_runs || []).some((run) => sourceFailed(run.status));
      setResult({
        mode: "search",
        query: session.request?.query || "历史 Session",
        sessionID: session.id,
        candidates: session.candidates || [],
        sourceRuns: session.source_runs || [],
        continuations: [],
        partial,
        summary: session.artifacts?.length ? `该 Session 已读取 ${session.artifacts.length} 个对象。` : undefined,
        evidence: [],
        warnings: [],
      });
      setQuery(session.request?.query || "");
      setStatusMessage("已恢复本地 Session");
      setPreview(undefined);
      window.scrollTo({ top: 0, behavior: "smooth" });
    } catch (error) {
      setSessionsError(errorMessage(error));
    }
  }

  async function deleteSession(id: string) {
    const target = sessions.find((session) => session.id === id);
    if (!window.confirm(`删除本地 Session“${target?.request?.query || id}”？该操作不会删除飞书内容。`)) return;
    try {
      await api.deleteSession(id);
      if (result?.sessionID === id) {
        setResult(undefined);
        setPreview(undefined);
      }
      await refreshSessions(true);
    } catch (error) {
      setSessionsError(errorMessage(error));
    }
  }

  const continuationCount = result?.continuations.filter((item) => item.has_more).length || 0;
  const resultsHeading = result ? `${MODE_LABELS[result.mode]}结果` : "检索结果";

  return (
    <div className="app-shell">
      <a className="skip-link" href="#query-input">跳到检索输入</a>
      <header className="app-header">
        <div className="header-inner">
          <a className="brand" href="#workspace" aria-label="SuperFeishuSearch 工作台首页">
            <span className="brand-mark" aria-hidden="true">S</span>
            <span><strong>SuperFeishuSearch</strong><small>本地知识检索</small></span>
          </a>
          <div className="header-status" aria-live="polite">
            <span className={`connection-dot ${health?.ok ? "online" : "offline"}`} aria-hidden="true" />
            <span>{health?.ok ? `${health.backend} · ${health.storage || "local"}` : "等待本机服务"}</span>
          </div>
        </div>
      </header>

      <div className="loopback-banner" role="region" aria-label="本机访问安全提示">
        <div className="loopback-inner">
          <strong>仅限本机回环访问</strong>
          <span>使用当前实例已配置的身份；不要暴露端口，也不要在页面中粘贴 Token。</span>
        </div>
      </div>

      <main id="workspace" className="workspace">
        <div className="primary-column">
          <section className="query-console" aria-labelledby="query-title">
            <div className="console-heading">
              <div>
                <p className="eyebrow">联邦检索</p>
                <h1 id="query-title">从本机飞书工作区查证信息</h1>
                <p>先召回候选，再按需读取正文；AI 问答只使用 Evidence Pack。</p>
              </div>
              <span className="console-index" aria-hidden="true">01</span>
            </div>

            <div className="mode-switcher" role="radiogroup" aria-label="执行模式">
              {(["search", "research", "ask"] as RunMode[]).map((item) => {
                const disabled = item === "ask" && askUnavailable;
                return (
                  <button
                    type="button"
                    role="radio"
                    aria-checked={mode === item}
                    disabled={disabled}
                    className={mode === item ? "active" : ""}
                    onClick={() => setMode(item)}
                    title={disabled ? "当前运行时未启用 AI Answer" : undefined}
                    key={item}
                  >
                    <strong>{MODE_LABELS[item]}</strong>
                    <small>{item === "search" ? "快速召回" : item === "research" ? "读取证据" : disabled ? "当前未启用" : "生成引用回答"}</small>
                  </button>
                );
              })}
            </div>

            <form className="query-form" onSubmit={run} aria-busy={loading}>
              <label htmlFor="query-input">问题或关键词</label>
              <div className="query-input-wrap">
                <textarea
                  id="query-input"
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  onKeyDown={(event) => {
                    if (!event.nativeEvent.isComposing && event.key === "Enter" && (event.metaKey || event.ctrlKey)) void run();
                  }}
                  placeholder="例如：A 项目为什么延期？有哪些未完成事项？"
                  rows={3}
                  autoFocus
                />
                <span className="keyboard-hint">⌘ / Ctrl + Enter</span>
              </div>

              <fieldset className="source-selector">
                <legend>搜索来源</legend>
                <div className="source-selector-actions">
                  <button type="button" onClick={() => setSelectedSources(new Set(searchableSources))}>选择全部</button>
                  <button type="button" onClick={() => setSelectedSources(new Set())}>清空</button>
                </div>
                <div className="source-chips">
                  {SOURCE_ORDER.map((source) => {
                    const available = searchableSources.has(source);
                    return (
                      <label
                        className={!available ? "disabled" : ""}
                        title={!available ? "当前运行时不支持此来源" : undefined}
                        key={source}
                      >
                        <input
                          type="checkbox"
                          checked={selectedSources.has(source)}
                          disabled={!available}
                          onChange={() => toggleSource(source)}
                        />
                        <span className="source-chip">
                          <span className="source-check" aria-hidden="true" />
                          <span>{SOURCE_LABELS[source]}</span>
                        </span>
                      </label>
                    );
                  })}
                </div>
              </fieldset>

              <div className="submit-row">
                <p>{mode === "search" ? "只返回候选，不自动读取全部正文。" : mode === "research" ? "默认读取最多 8 个高价值对象。" : "回答会同时显示 Claims 与 Citations。"}</p>
                <button className="primary-button" type="submit" disabled={loading || runtimeLoading}>
                  {loading ? "执行中…" : `开始${MODE_LABELS[mode]}`}
                </button>
              </div>
            </form>
          </section>

          {loading ? <ProgressStatus mode={mode} elapsedSeconds={elapsedSeconds} event={progress} onCancel={() => requestController.current?.abort()} /> : null}
          {requestError ? <div className="request-error" role="alert"><strong>请求未完成</strong><span>{requestError}</span></div> : null}
          {result?.partial ? (
            <div className="partial-banner" role="status">
              <strong>结果为部分成功</strong>
              <span>至少一个来源失败、超时或缺少权限。下方状态会保留具体原因，不应把当前结果视为完整覆盖。</span>
            </div>
          ) : null}

          {result ? <SourceStatusPanel runs={result.sourceRuns} /> : null}
          {result ? <EvidencePanel result={result} /> : null}

          <section className="results-section" aria-labelledby="results-title">
            <div className="section-heading">
              <div>
                <p className="eyebrow">候选结果</p>
                <h2 id="results-title">{resultsHeading}</h2>
              </div>
              <div className="result-summary" aria-live="polite">
                {result ? <><strong>{result.candidates.length}</strong><span> 个候选{result.elapsedMS ? ` · ${result.elapsedMS} ms` : ""}</span></> : <span>{statusMessage}</span>}
              </div>
            </div>
            <ResultList candidates={result?.candidates || []} pendingPreviewID={pendingPreviewID} onPreview={openPreview} />
            {continuationCount > 0 ? (
              <button className="secondary-button load-more" type="button" onClick={continueSearch} disabled={loading}>
                继续加载 {continuationCount} 个来源的下一页
              </button>
            ) : null}
          </section>

          {preview ? <div ref={previewRef}><PreviewPanel {...preview} onClose={() => setPreview(undefined)} /></div> : null}
        </div>

        <aside className="sidebar" aria-label="运行时与历史">
          <RuntimePanel
            health={health}
            capabilities={capabilities}
            loading={runtimeLoading}
            probing={probing}
            error={runtimeError}
            onProbe={runDoctor}
          />
          <SessionsPanel
            sessions={sessions}
            currentID={result?.sessionID}
            loading={sessionsLoading}
            error={sessionsError}
            onRefresh={() => void refreshSessions()}
            onOpen={openSession}
            onDelete={deleteSession}
          />
        </aside>
      </main>

      <footer className="app-footer">
        <div className="footer-inner">
          <span>SuperFeishuSearch · 单用户本地工作台</span>
          <span>候选不等于证据 · 证据不足时明确说明</span>
        </div>
      </footer>
    </div>
  );
}
