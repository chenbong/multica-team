-- Resource relationships are validated and cleaned up by the application.
-- An issue panel derives its workspace from the issue, including after transfer.
CREATE TABLE IF NOT EXISTS resource_web_panel (
    id UUID NOT NULL,
    workspace_id UUID,
    issue_id UUID,
    created_by UUID NOT NULL,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    url TEXT NOT NULL CHECK (octet_length(url) BETWEEN 1 AND 4096),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((workspace_id IS NULL) <> (issue_id IS NULL))
);
