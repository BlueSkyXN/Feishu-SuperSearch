import type {
  ArtifactBatch,
  AskResult,
  Candidate,
  CapabilitySnapshot,
  HealthSnapshot,
  ResearchResult,
  SearchSnapshot,
  SessionSnapshot,
  SourceID,
  WorkflowProgressEvent,
} from "./types";

export class APIError extends Error {
  readonly status: number;
  readonly type?: string;
  readonly hint?: string;

  constructor(message: string, status: number, type?: string, hint?: string) {
    super(message);
    this.name = "APIError";
    this.status = status;
    this.type = type;
    this.hint = hint;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: {
      Accept: "application/json",
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });
  if (response.status === 204) return undefined as T;

  const payload = (await response.json().catch(() => null)) as
    | { error?: { message?: string; type?: string; hint?: string } }
    | null;
  if (!response.ok) {
    const detail = payload?.error;
    throw new APIError(
      detail?.message || `请求失败（HTTP ${response.status}）`,
      response.status,
      detail?.type,
      detail?.hint,
    );
  }
  return payload as T;
}

interface StreamOptions {
  signal?: AbortSignal;
  onEvent?: (event: WorkflowProgressEvent) => void;
}

interface SSEFrame {
  event: string;
  data: string;
}

function parseFrame(block: string): SSEFrame | undefined {
  let event = "message";
  const data: string[] = [];
  for (const rawLine of block.split(/\r?\n/)) {
    if (rawLine.startsWith(":")) continue;
    const separator = rawLine.indexOf(":");
    const field = separator < 0 ? rawLine : rawLine.slice(0, separator);
    let value = separator < 0 ? "" : rawLine.slice(separator + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    if (field === "event") event = value;
    if (field === "data") data.push(value);
  }
  if (data.length === 0) return undefined;
  return { event, data: data.join("\n") };
}

function streamError(payload: unknown, status = 200): APIError {
  const envelope = payload as { error?: { message?: string; type?: string; hint?: string } } | undefined;
  const detail = envelope?.error;
  return new APIError(detail?.message || `服务端流式请求失败（HTTP ${status}）`, status, detail?.type, detail?.hint);
}

export async function readEventStream<T>(response: Response, onEvent?: (event: WorkflowProgressEvent) => void): Promise<T> {
  if (!response.body) throw new APIError("服务端未返回可读取的事件流", response.status || 200, "stream_incomplete");
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let result: T | undefined;
  let terminal = false;

  const consume = (block: string) => {
    const frame = parseFrame(block);
    if (!frame) return;
	if (terminal) throw new APIError("最终事件之后仍收到额外数据", 200, "stream_invalid_terminal");
    let payload: unknown;
    try {
      payload = JSON.parse(frame.data);
    } catch {
      throw new APIError(`无法解析 SSE ${frame.event} 事件`, 200, "stream_invalid_json");
    }
    if (frame.event === "progress" || frame.event === "retrieval") {
      onEvent?.(payload as WorkflowProgressEvent);
      return;
    }
    if (frame.event === "error") {
      terminal = true;
      throw streamError(payload);
    }
    if (frame.event === "result") {
      terminal = true;
      result = payload as T;
    }
  };

  while (true) {
    const { value, done } = await reader.read();
    buffer += decoder.decode(value, { stream: !done });
    let separator = buffer.match(/\r?\n\r?\n/);
    while (separator?.index !== undefined) {
      const index = separator.index;
      consume(buffer.slice(0, index));
      buffer = buffer.slice(index + separator[0].length);
      separator = buffer.match(/\r?\n\r?\n/);
    }
    if (done) break;
  }
  if (buffer.trim()) consume(buffer);
  if (!terminal || result === undefined) {
    throw new APIError("事件流在返回最终结果前中断", 200, "stream_incomplete");
  }
  return result;
}

async function streamRequest<T>(path: string, body: unknown, options: StreamOptions = {}): Promise<T> {
  const response = await fetch(path, {
    method: "POST",
    signal: options.signal,
    headers: { Accept: "text/event-stream", "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const contentType = response.headers?.get?.("Content-Type") || "";
  if (!response.ok) {
    const payload = await response.json().catch(() => null);
    throw streamError(payload, response.status);
  }
  if (!contentType.toLowerCase().includes("text/event-stream")) {
    return (await response.json()) as T;
  }
  return readEventStream<T>(response, options.onEvent);
}

export const api = {
  health(signal?: AbortSignal) {
    return request<HealthSnapshot>("/v1/health", { signal });
  },
  capabilities(probe = false, signal?: AbortSignal) {
    return request<CapabilitySnapshot>(`/v1/capabilities${probe ? "?probe=true" : ""}`, { signal });
  },
  search(query: string, sources: SourceID[], signal?: AbortSignal, limitPerSource = 8, pagination: "adaptive" | "none" = "adaptive") {
    return request<SearchSnapshot>("/v1/search", {
      method: "POST",
      signal,
      body: JSON.stringify({
        query,
        sources,
        identity: { mode: "auto" },
        limit: 40,
        limit_per_source: limitPerSource,
        budget: { max_pages_per_source: pagination === "none" ? 1 : 2 },
        strategy: { pagination },
      }),
    });
  },
  research(query: string, sources: SourceID[], options: StreamOptions = {}) {
	return streamRequest<ResearchResult>("/v1/research", {
		query,
		sources,
		identity: { mode: "auto" },
		deep: true,
		fetch_top_k: 8,
	}, options);
  },
  ask(query: string, sources: SourceID[], options: StreamOptions = {}) {
	return streamRequest<AskResult>("/v1/ask", {
		query,
		sources,
		identity: { mode: "auto" },
		deep: true,
		fetch_top_k: 8,
	}, options);
  },
  continueSearch(sessionID: string, sources: SourceID[], signal?: AbortSignal) {
    return request<SearchSnapshot>("/v1/search/continue", {
      method: "POST",
      signal,
      body: JSON.stringify({
        session_id: sessionID,
        sources,
        max_additional_pages: 1,
      }),
    });
  },
  fetchCandidate(sessionID: string, candidate: Candidate, signal?: AbortSignal) {
    const preferred = ["summary", "content", "context", "structure", "relations", "attachments"];
    let projection = preferred.filter((item) => candidate.available_projection?.includes(item));
    if (projection.length === 0) projection = ["content"];
    return request<ArtifactBatch>("/v1/fetch", {
      method: "POST",
      signal,
      body: JSON.stringify({
        session_id: sessionID,
        identity: { mode: "auto" },
        items: [{ ref: candidate.ref, projection }],
      }),
    });
  },
  sessions(signal?: AbortSignal) {
    return request<{ sessions: SessionSnapshot[] }>("/v1/sessions", { signal });
  },
  session(id: string, signal?: AbortSignal) {
    return request<SessionSnapshot>(`/v1/sessions/${encodeURIComponent(id)}`, { signal });
  },
  deleteSession(id: string, signal?: AbortSignal) {
    return request<void>(`/v1/sessions/${encodeURIComponent(id)}`, { method: "DELETE", signal });
  },
};
