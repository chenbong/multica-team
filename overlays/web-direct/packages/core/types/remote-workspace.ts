export interface WorkspaceReference {
  path: string;
  kind: "file" | "directory";
  revision?: string;
  start_line?: number;
  end_line?: number;
}
export interface WorkspaceContext { binding: string; references: WorkspaceReference[] }
export interface WorkspaceEntry { name: string; path: string; kind: "file" | "directory"; size: number }
export interface WorkspaceResult {
  binding: string;
  root_name: string;
  path?: string;
  entries?: WorkspaceEntry[];
  next_offset?: number;
  content?: string;
  revision?: string;
}
export interface WorkspaceQuery { operation: "list" | "read"; binding?: string; path?: string; offset?: number }
