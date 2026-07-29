import type { AskResult, CapabilitySnapshot, HealthSnapshot, ResearchResult, SearchSnapshot } from "../types";

export const health: HealthSnapshot = {
  ok: true,
  backend: "mock",
  providers: 2,
  storage: "memory",
  time: "2026-07-29T10:00:00Z",
  ai: { enabled: true, planner: false, rerank: false, answer: true },
};

export const capabilities: CapabilitySnapshot = {
  generated_at: "2026-07-29T10:00:00Z",
  providers: [
    {
      descriptor: {
        id: "mock.docs",
        source: "docs",
        operations: { search: true, fetch: true },
        backend: "mock",
      },
      status: "ok",
    },
    {
      descriptor: {
        id: "mock.messages",
        source: "messages",
        operations: { search: true, fetch: true },
        backend: "mock",
      },
      status: "ok",
    },
  ],
};

export const candidate = {
  ref: {
    platform: "feishu",
    scope_key: "mock",
    kind: "document",
    native_id: "doc-1",
    canonical_id: "feishu:mock:document:doc-1",
    provider_id: "mock.docs",
    source: "docs" as const,
    url: "https://example.invalid/doc-1",
  },
  source: "docs" as const,
  kind: "document",
  title: "A 项目复盘",
  snippet: "测试环境交付延迟，导致回归时间后移。",
  native_rank: 1,
  fused_score: 0.0164,
  projection: ["head", "snippet"],
  available_projection: ["content", "summary"],
};

export const searchResult: SearchSnapshot = {
  session_id: "rs_1234567890abcdefabcd",
  query: "A 项目延期",
  candidates: [candidate],
  sources: [
    { provider_id: "mock.docs", source: "docs", status: "ok", pages: 1, hit_count: 1, elapsed_ms: 4 },
    {
      provider_id: "mock.messages",
      source: "messages",
      status: "missing_scope",
      pages: 0,
      hit_count: 0,
      elapsed_ms: 2,
      error: { type: "missing_scope", message: "缺少消息读取权限" },
    },
  ],
  continuations: [],
  partial: true,
  stats: {
    raw_candidates: 1,
    unique_candidates: 1,
    returned: 1,
    sources_completed: 1,
    sources_failed: 1,
    elapsed_ms: 7,
  },
};

export const evidence = {
  id: "ev_a-project-delay",
  kind: "text",
  claim_type: "fact",
  text: "测试环境比计划晚交付三天。",
  quote: "测试环境比计划晚交付三天。",
  source_ref: candidate.ref,
  projection: ["content"],
  source_span: { chunk_id: "chunk-1" },
  confidence: 0.91,
  url: candidate.ref.url,
};

export const researchResult: ResearchResult = {
  planner: "rules",
  plan_result: { session_id: searchResult.session_id, partial: false },
  candidate_pack: {
    query: "A 项目为什么延期",
    session_id: searchResult.session_id,
    candidates: [candidate],
    sources: [searchResult.sources[0]],
  },
  artifact_pack: {
    session_id: searchResult.session_id,
    artifacts: [],
  },
  evidence_pack: {
    query: "A 项目为什么延期",
    session_id: searchResult.session_id,
    evidence: [evidence],
  },
  summary: "基于已读取内容，测试环境交付延迟是主要原因。",
  warnings: [],
};

export const askResult: AskResult = {
  research: researchResult,
  answerer: "ai",
  partial: false,
  warnings: [],
  answer: {
    text: "A 项目主要因测试环境晚交付三天而延期。",
    partial: false,
    claims: [{ text: "测试环境晚交付三天", evidence_ids: [evidence.id] }],
    citations: [
      {
        id: evidence.id,
        evidence_id: evidence.id,
        source_ref: candidate.ref,
        quote: evidence.quote,
        url: evidence.url,
      },
    ],
  },
};
