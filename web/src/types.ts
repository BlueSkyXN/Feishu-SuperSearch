export type RunMode = "search" | "research" | "ask";

export type SourceID =
  | "docs"
  | "messages"
  | "chats"
  | "people"
  | "minutes"
  | "meetings"
  | "calendar"
  | "tasks"
  | "mail"
  | "base"
  | "sheets";

export type SourceStatus =
  | "ok"
  | "partial"
  | "empty"
  | "unavailable"
  | "missing_scope"
  | "failed"
  | "deadline_exceeded"
  | "budget_exhausted";

export interface ErrorDetail {
  type: string;
  subtype?: string;
  code?: number;
  message: string;
  hint?: string;
  retryable?: boolean;
  provider_id?: string;
  source?: SourceID;
  details?: Record<string, unknown>;
}

export interface ObjectRef {
  platform?: string;
  scope_key?: string;
  kind: string;
  native_id: string;
  canonical_id: string;
  provider_id?: string;
  source?: SourceID;
  url?: string;
}

export interface Candidate {
  ref: ObjectRef;
  source: SourceID;
  kind: string;
  title: string;
  snippet?: string;
  url?: string;
  timestamp?: string;
  native_rank: number;
  fused_score: number;
  projection: string[];
  available_projection: string[];
  provenance?: {
    backend?: string;
    provider_id?: string;
    operation?: string;
  };
}

export interface SourceRun {
  provider_id?: string;
  source: SourceID;
  status: SourceStatus;
  pages: number;
  hit_count: number;
  elapsed_ms: number;
  error?: ErrorDetail;
}

export interface Continuation {
  provider_id: string;
  source: SourceID;
  cursor: string;
  has_more: boolean;
  pages_fetched: number;
}

export interface SearchStats {
  raw_candidates: number;
  unique_candidates: number;
  returned: number;
  sources_completed: number;
  sources_failed: number;
  elapsed_ms: number;
}

export interface SearchSnapshot {
  session_id: string;
  query: string;
  candidates: Candidate[];
  sources: SourceRun[];
  continuations?: Continuation[];
  partial: boolean;
  stats: SearchStats;
}

export interface ContentChunk {
  id?: string;
  kind?: string;
  text: string;
  start?: number;
  end?: number;
  timestamp?: string;
}

export interface Artifact {
  ref: ObjectRef;
  projection: string[];
  chunks?: ContentChunk[];
  summary?: {
    text?: string;
    todos?: Array<Record<string, unknown>>;
    chapters?: Array<Record<string, unknown>>;
    keywords?: string[];
  };
  version?: string;
}

export interface ArtifactBatch {
  session_id: string;
  items: Array<{
    ref: ObjectRef;
    artifact?: Artifact;
    error?: ErrorDetail;
    cached?: boolean;
    shared?: boolean;
  }>;
  partial: boolean;
}

export interface Evidence {
  id: string;
  kind: string;
  claim_type: string;
  text: string;
  quote: string;
  source_ref: ObjectRef;
  projection: string[];
  source_span: {
    chunk_id?: string;
    start?: number;
    end?: number;
  };
  timestamp?: string;
  confidence: number;
  url?: string;
}

export interface ResearchResult {
  planner: string;
  plan_result: {
    session_id: string;
    partial: boolean;
  };
  candidate_pack: {
    query: string;
    session_id: string;
    candidates: Candidate[];
    sources: SourceRun[];
  };
  artifact_pack: {
    session_id: string;
    artifacts: Artifact[];
  };
  evidence_pack: {
    query: string;
    session_id: string;
    evidence: Evidence[];
  };
  summary: string;
  warnings?: string[];
}

export interface Claim {
  text: string;
  evidence_ids: string[];
}

export interface Citation {
  id: string;
  evidence_id: string;
  source_ref: ObjectRef;
  quote: string;
  url?: string;
}

export interface AskResult {
  research: ResearchResult;
  answer: {
    text: string;
    claims?: Claim[];
    citations?: Citation[];
    warnings?: string[];
    partial: boolean;
    refused?: boolean;
  };
  answerer: string;
  partial: boolean;
  warnings?: string[];
}

export interface RetrievalEvent {
  id: string;
  session_id?: string;
  type: string;
  time: string;
  node_id?: string;
  source?: SourceID;
  data?: Record<string, unknown>;
}

export interface WorkflowProgressEvent {
  type: "progress" | "retrieval";
  phase: string;
  message?: string;
  time: string;
  retrieval?: RetrievalEvent;
}

export interface HealthSnapshot {
  ok: boolean;
  backend: string;
  providers: number;
  storage?: string;
  time: string;
  ai?: {
    enabled: boolean;
    planner: boolean;
    rerank: boolean;
    answer: boolean;
  };
}

export interface ProviderCapability {
  descriptor: {
    id: string;
    source: SourceID;
    operations: Record<string, boolean>;
    required_identity?: string[];
    required_scopes?: string[];
    backend: string;
    version?: string;
  };
  status: SourceStatus;
  version?: string;
  error?: ErrorDetail;
  operations?: Record<string, {
    status: SourceStatus;
    version?: string;
    error?: ErrorDetail;
  }>;
}

export interface CapabilitySnapshot {
  generated_at: string;
  providers: ProviderCapability[];
}

export interface SessionSnapshot {
  id: string;
  scope_key: string;
  created_at: string;
  expires_at: string;
  request?: {
    query?: string;
    sources?: SourceID[];
  };
  candidates: Candidate[];
  artifacts: Artifact[];
  source_runs: SourceRun[];
}

export interface WorkspaceResult {
  mode: RunMode;
  query: string;
  sessionID: string;
  candidates: Candidate[];
  sourceRuns: SourceRun[];
  continuations: Continuation[];
  partial: boolean;
  summary?: string;
  evidence: Evidence[];
  answer?: AskResult["answer"];
  answerer?: string;
  warnings: string[];
  elapsedMS?: number;
}
