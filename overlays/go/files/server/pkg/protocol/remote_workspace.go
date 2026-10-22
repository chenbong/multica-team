package protocol

// Remote workspace requests never accept a filesystem root from the browser.
// The daemon resolves session IDs against directories recorded by its executor.
const RemoteWorkspaceCapability = "remote-workspace-v1"
const RemoteWorkspaceRequestEvent = "daemon:workspace_request"
const RemoteWorkspaceResponseEvent = "daemon:workspace_response"

type WorkspaceReference struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Revision  string `json:"revision,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}
type WorkspaceRequest struct {
	ID          string               `json:"id"`
	WorkspaceID string               `json:"workspace_id"`
	RuntimeID   string               `json:"runtime_id"`
	SessionID   string               `json:"session_id"`
	Operation   string               `json:"operation"`
	Binding     string               `json:"binding,omitempty"`
	Path        string               `json:"path,omitempty"`
	Offset      int                  `json:"offset,omitempty"`
	References  []WorkspaceReference `json:"references,omitempty"`
}
type WorkspaceEntry struct {
	AbsolutePath string `json:"absolute_path"`
	Name         string `json:"name"`
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	Size         int64  `json:"size"`
}
type WorkspaceResponse struct {
	AbsolutePath string           `json:"absolute_path,omitempty"`
	ID           string           `json:"id"`
	Error        string           `json:"error,omitempty"`
	Binding      string           `json:"binding,omitempty"`
	RootName     string           `json:"root_name,omitempty"`
	Path         string           `json:"path,omitempty"`
	Entries      []WorkspaceEntry `json:"entries,omitempty"`
	NextOffset   *int             `json:"next_offset,omitempty"`
	Content      string           `json:"content,omitempty"`
	Revision     string           `json:"revision,omitempty"`
	Context      string           `json:"context,omitempty"`
}
