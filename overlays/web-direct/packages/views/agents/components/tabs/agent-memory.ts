// Agent memory helpers (multica-team overlay). Pure logic shared by the
// Memory tab and the runtime import dialog; mirrors the server rules in
// server/internal/handler/agent_memory.go so problems surface before Save.

import { api } from "@multica/core/api";
import type {
  AgentMemoryFile,
  AgentMemoryFileInput,
  AgentMemoryLimits,
  AgentMemorySource,
  RuntimeLocalMemoryFile,
  RuntimeLocalMemoryReadError,
  RuntimeLocalMemoryRequest,
  RuntimeLocalMemorySummary,
} from "@multica/core/types/agent-memory";

export const DEFAULT_MEMORY_LIMITS: AgentMemoryLimits = {
  max_files: 64,
  max_file_bytes: 256 * 1024,
  max_total_bytes: 1024 * 1024,
};

const MAX_PATH_LENGTH = 200;
const MAX_PATH_DEPTH = 8;
const RESERVED_PATH_CHARS = '<>:"|?*';

function isInvalidPathChar(ch: string): boolean {
  const code = ch.charCodeAt(0);
  return code < 0x20 || code === 0x7f || RESERVED_PATH_CHARS.includes(ch);
}

export type MemoryPathError =
  | "empty"
  | "absolute"
  | "invalid_char"
  | "outside"
  | "too_long"
  | "too_deep"
  | "bad_segment";

export function normalizeMemoryPath(
  raw: string,
): { ok: true; path: string } | { ok: false; error: MemoryPathError } {
  const p = raw.trim().replace(/\\/g, "/");
  if (!p) return { ok: false, error: "empty" };
  if (p.startsWith("/")) return { ok: false, error: "absolute" };
  if ([...p].some(isInvalidPathChar)) return { ok: false, error: "invalid_char" };
  const segments: string[] = [];
  for (const seg of p.split("/")) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") {
      if (segments.length === 0) return { ok: false, error: "outside" };
      segments.pop();
      continue;
    }
    segments.push(seg);
  }
  if (segments.length === 0) return { ok: false, error: "outside" };
  for (const seg of segments) {
    if (seg.startsWith(".") || seg.endsWith(".") || seg.endsWith(" ")) {
      return { ok: false, error: "bad_segment" };
    }
  }
  const path = segments.join("/");
  // The server measures bytes (Go len), so CJK paths hit the limit sooner.
  if (new TextEncoder().encode(path).length > MAX_PATH_LENGTH) return { ok: false, error: "too_long" };
  if (segments.length > MAX_PATH_DEPTH) return { ok: false, error: "too_deep" };
  return { ok: true, path };
}

/** Turns an arbitrary file name into a usable memory path segment. */
export function sanitizeMemoryFileName(name: string): string {
  const base = name.split(/[\\/]/).pop() ?? "";
  const cleaned = [...base]
    .map((ch) => (isInvalidPathChar(ch) ? "-" : ch))
    .join("")
    .replace(/^[.\s]+/, "")
    .replace(/[.\s]+$/, "");
  return cleaned || "memory.md";
}

export function byteLength(text: string): number {
  return new TextEncoder().encode(text).length;
}

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(bytes < 10 * 1024 ? 1 : 0)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export interface DraftMemoryFile {
  localId: string;
  path: string;
  content: string;
  source: AgentMemorySource;
  sourceRuntimeId?: string;
  sourcePath?: string;
  /** Only enabled files are delivered with the agent's runs. */
  enabled: boolean;
}

let nextLocalId = 0;
export function newLocalId(): string {
  nextLocalId += 1;
  return `memory-${nextLocalId}`;
}

export function draftFromServer(files: AgentMemoryFile[]): DraftMemoryFile[] {
  return files.map((f) => ({
    localId: newLocalId(),
    path: f.path,
    content: f.content,
    source: f.source,
    sourceRuntimeId: f.source_runtime_id ?? undefined,
    sourcePath: f.source_path || undefined,
    // Servers from before the enabled flag treat every file as enabled.
    enabled: f.enabled ?? true,
  }));
}

export function draftToRequest(files: DraftMemoryFile[]): AgentMemoryFileInput[] {
  return files.map((f) => {
    const normalized = normalizeMemoryPath(f.path);
    return {
      path: normalized.ok ? normalized.path : f.path,
      content: f.content,
      source: f.source,
      ...(f.sourceRuntimeId ? { source_runtime_id: f.sourceRuntimeId } : {}),
      ...(f.sourcePath ? { source_path: f.sourcePath } : {}),
      enabled: f.enabled,
    };
  });
}

/** Order-insensitive identity of a draft, used for dirty tracking. */
export function draftSignature(files: DraftMemoryFile[]): string {
  return JSON.stringify(
    draftToRequest(files)
      .map((f) => [f.path, f.content, f.source ?? "manual", f.source_runtime_id ?? "", f.source_path ?? "", f.enabled !== false])
      .sort((a, b) => (String(a[0]) < String(b[0]) ? -1 : String(a[0]) > String(b[0]) ? 1 : 0)),
  );
}

export type MemoryDraftIssue =
  | { kind: "path"; localId: string; error: MemoryPathError }
  | { kind: "duplicate"; localId: string; path: string }
  | { kind: "folder_conflict"; localId: string; path: string }
  | { kind: "file_too_large"; localId: string; path: string }
  | { kind: "too_many_files"; count: number }
  | { kind: "total_too_large"; bytes: number };

export function validateMemoryDraft(
  files: DraftMemoryFile[],
  limits: AgentMemoryLimits = DEFAULT_MEMORY_LIMITS,
): MemoryDraftIssue[] {
  const issues: MemoryDraftIssue[] = [];
  if (files.length > limits.max_files) {
    issues.push({ kind: "too_many_files", count: files.length });
  }
  const seen = new Map<string, string>();
  let total = 0;
  for (const f of files) {
    const normalized = normalizeMemoryPath(f.path);
    if (!normalized.ok) {
      issues.push({ kind: "path", localId: f.localId, error: normalized.error });
      continue;
    }
    const folded = normalized.path.toLowerCase();
    if (seen.has(folded)) {
      issues.push({ kind: "duplicate", localId: f.localId, path: normalized.path });
    } else {
      seen.set(folded, f.localId);
    }
    const size = byteLength(f.content);
    total += size;
    if (size > limits.max_file_bytes) {
      issues.push({ kind: "file_too_large", localId: f.localId, path: normalized.path });
    }
  }
  for (const [folded, localId] of seen) {
    const parts = folded.split("/");
    for (let i = 1; i < parts.length; i += 1) {
      if (seen.has(parts.slice(0, i).join("/"))) {
        issues.push({ kind: "folder_conflict", localId, path: folded });
        break;
      }
    }
  }
  if (total > limits.max_total_bytes) {
    issues.push({ kind: "total_too_large", bytes: total });
  }
  return issues;
}

/** Returns path, or path with a -2/-3… suffix before the extension, unused in `taken`. */
export function uniqueMemoryPath(path: string, taken: Set<string>): string {
  if (!taken.has(path.toLowerCase())) return path;
  const slash = path.lastIndexOf("/");
  const dot = path.lastIndexOf(".");
  const hasExt = dot > slash + 1;
  const stem = hasExt ? path.slice(0, dot) : path;
  const ext = hasExt ? path.slice(dot) : "";
  for (let n = 2; ; n += 1) {
    const candidate = `${stem}-${n}${ext}`;
    if (!taken.has(candidate.toLowerCase())) return candidate;
  }
}

/**
 * Adds incoming files to the draft. A file that re-imports the same source
 * (same runtime + source path, or same upload name at the same path) replaces
 * the existing entry (keeping its enabled state); any other path collision
 * is renamed with a suffix.
 * Returns the merged draft and the local ids of the files that were added or
 * replaced.
 */
export function mergeMemoryFiles(
  draft: DraftMemoryFile[],
  incoming: Omit<DraftMemoryFile, "localId">[],
): { files: DraftMemoryFile[]; touched: string[] } {
  const files = [...draft];
  const touched: string[] = [];
  for (const item of incoming) {
    const normalized = normalizeMemoryPath(item.path);
    const path = normalized.ok ? normalized.path : sanitizeMemoryFileName(item.path);
    const sameIndex = files.findIndex((f) => {
      const fp = normalizeMemoryPath(f.path);
      return (
        (fp.ok ? fp.path : f.path).toLowerCase() === path.toLowerCase() &&
        f.source === item.source &&
        (f.sourcePath ?? "") === (item.sourcePath ?? "") &&
        (f.sourceRuntimeId ?? "") === (item.sourceRuntimeId ?? "")
      );
    });
    if (sameIndex >= 0) {
      const existing = files[sameIndex]!;
      files[sameIndex] = { ...existing, content: item.content };
      touched.push(existing.localId);
      continue;
    }
    const taken = new Set(
      files.map((f) => {
        const fp = normalizeMemoryPath(f.path);
        return (fp.ok ? fp.path : f.path).toLowerCase();
      }),
    );
    const localId = newLocalId();
    files.push({ ...item, path: uniqueMemoryPath(path, taken), localId });
    touched.push(localId);
  }
  return { files, touched };
}

export const MEMORY_UPLOAD_ACCEPT = ".md,.markdown,.mdc,.txt,text/markdown,text/plain";

export type UploadRejection = { name: string; reason: "too_large" | "binary" | "unreadable" };

/** Reads browser-selected files into draft entries, rejecting binaries and oversize files. */
export async function readMemoryUploads(
  files: File[],
  limits: AgentMemoryLimits = DEFAULT_MEMORY_LIMITS,
): Promise<{ accepted: Omit<DraftMemoryFile, "localId">[]; rejected: UploadRejection[] }> {
  const accepted: Omit<DraftMemoryFile, "localId">[] = [];
  const rejected: UploadRejection[] = [];
  for (const file of files) {
    const displayName =
      (file as File & { webkitRelativePath?: string }).webkitRelativePath || file.name;
    if (file.size > limits.max_file_bytes) {
      rejected.push({ name: displayName, reason: "too_large" });
      continue;
    }
    let text: string;
    try {
      text = await file.text();
    } catch {
      rejected.push({ name: displayName, reason: "unreadable" });
      continue;
    }
    if (text.includes("\u0000") || text.includes("\ufffd")) {
      rejected.push({ name: displayName, reason: "binary" });
      continue;
    }
    accepted.push({
      path: sanitizeMemoryFileName(file.name),
      content: text,
      source: "upload",
      sourcePath: displayName,
      enabled: true,
    });
  }
  return { accepted, rejected };
}

// ---------------------------------------------------------------------------
// Runtime-machine import (list / read round trips through the daemon)
// ---------------------------------------------------------------------------

const POLL_INTERVAL_MS = 500;
// The server times a request out after 45s pending + 60s running.
const POLL_TIMEOUT_MS = 110_000;

async function pollLocalMemory(
  runtimeId: string,
  initial: RuntimeLocalMemoryRequest,
  signal?: AbortSignal,
): Promise<RuntimeLocalMemoryRequest> {
  const start = Date.now();
  let current = initial;
  while (current.status === "pending" || current.status === "running") {
    if (signal?.aborted) throw new DOMException("aborted", "AbortError");
    if (Date.now() - start > POLL_TIMEOUT_MS) {
      throw new Error("runtime memory request timed out");
    }
    await new Promise((resolve) => setTimeout(resolve, POLL_INTERVAL_MS));
    current = await api.getLocalMemoryRequest(runtimeId, initial.id);
  }
  if (current.status !== "completed") {
    throw new Error(current.error || "runtime memory request failed");
  }
  return current;
}

export async function listRuntimeLocalMemory(
  runtimeId: string,
  signal?: AbortSignal,
): Promise<RuntimeLocalMemorySummary[]> {
  const initial = await api.initiateLocalMemoryRequest(runtimeId, { kind: "list" });
  const done = await pollLocalMemory(runtimeId, initial, signal);
  return done.entries ?? [];
}

export async function readRuntimeLocalMemory(
  runtimeId: string,
  keys: string[],
  signal?: AbortSignal,
): Promise<{ files: RuntimeLocalMemoryFile[]; errors: RuntimeLocalMemoryReadError[] }> {
  const initial = await api.initiateLocalMemoryRequest(runtimeId, { kind: "read", keys });
  const done = await pollLocalMemory(runtimeId, initial, signal);
  return { files: done.files ?? [], errors: done.errors ?? [] };
}

export function runtimeMemoryToDraft(
  runtimeId: string,
  files: RuntimeLocalMemoryFile[],
): Omit<DraftMemoryFile, "localId">[] {
  return files.map((f) => ({
    path: f.suggested_path || sanitizeMemoryFileName(f.source_path),
    content: f.content,
    source: "runtime" as const,
    sourceRuntimeId: runtimeId,
    sourcePath: f.source_path,
    enabled: true,
  }));
}
