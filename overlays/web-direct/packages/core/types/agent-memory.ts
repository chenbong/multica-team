// Agent memory (multica-team overlay). Mirrors the server types in
// server/internal/handler/agent_memory.go and runtime_local_memory.go.

export type AgentMemorySource = "manual" | "upload" | "runtime" | "agent";

export interface AgentMemoryFile {
  id: string;
  path: string;
  content: string;
  source: AgentMemorySource;
  source_runtime_id: string | null;
  source_path: string;
  /** Disabled files stay stored but are not delivered to runs. */
  enabled: boolean;
  /** Runtime-imported file kept in sync with its source file. */
  auto_sync?: boolean;
  synced_at?: string | null;
  size_bytes: number;
  updated_by: string | null;
  created_at: string;
  updated_at: string;
}

export interface AgentMemoryLimits {
  max_files: number;
  max_file_bytes: number;
  max_total_bytes: number;
}

export type AgentMemoryWriteback = "off" | "review" | "auto";

export interface AgentMemorySettings {
  writeback: AgentMemoryWriteback;
  capture_runtime_memory: boolean;
}

export interface AgentMemoryResponse {
  agent_id: string;
  files: AgentMemoryFile[];
  limits: AgentMemoryLimits;
  settings?: AgentMemorySettings;
  pending_proposals?: number;
}

export interface UpdateAgentMemorySettingsRequest {
  writeback?: AgentMemoryWriteback;
  capture_runtime_memory?: boolean;
}

export type AgentMemoryProposalStatus = "pending" | "accepted" | "rejected" | "superseded";

/** A memory change an agent run reported (handler/agent_memory_writeback.go). */
export interface AgentMemoryProposal {
  id: string;
  task_id: string | null;
  path: string;
  action: "upsert" | "delete";
  content: string;
  base_content: string | null;
  current_content: string | null;
  origin: "agent" | "runtime";
  status: AgentMemoryProposalStatus;
  note: string;
  created_at: string;
  resolved_at: string | null;
  resolved_by: string | null;
}

export interface AgentMemoryProposalsResponse {
  proposals: AgentMemoryProposal[];
}

export interface AgentMemoryFileInput {
  path: string;
  content: string;
  source?: AgentMemorySource;
  source_runtime_id?: string;
  source_path?: string;
  /** Defaults to true on the server when omitted. */
  enabled?: boolean;
  auto_sync?: boolean;
}

export interface UpdateAgentMemoryRequest {
  files: AgentMemoryFileInput[];
}

export type RuntimeLocalMemoryKind = "list" | "read";

export type RuntimeLocalMemoryStatus =
  | "pending"
  | "running"
  | "completed"
  | "failed"
  | "timeout";

export interface RuntimeLocalMemorySummary {
  key: string;
  tool: string;
  scope: string;
  label?: string;
  source_path: string;
  suggested_path: string;
  size_bytes: number;
  modified_at?: string;
  too_large?: boolean;
}

export interface RuntimeLocalMemoryFile {
  key: string;
  source_path: string;
  suggested_path: string;
  content: string;
  size_bytes: number;
}

export interface RuntimeLocalMemoryReadError {
  key: string;
  error: string;
}

export interface RuntimeLocalMemoryRequest {
  id: string;
  runtime_id: string;
  provider?: string;
  kind: RuntimeLocalMemoryKind;
  keys?: string[];
  status: RuntimeLocalMemoryStatus;
  entries?: RuntimeLocalMemorySummary[];
  files?: RuntimeLocalMemoryFile[];
  errors?: RuntimeLocalMemoryReadError[];
  error?: string;
  created_at: string;
  updated_at: string;
}

export interface CreateRuntimeLocalMemoryRequest {
  kind: RuntimeLocalMemoryKind;
  keys?: string[];
}
