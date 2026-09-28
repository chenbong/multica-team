CREATE TABLE IF NOT EXISTS ducc_credential (
    user_id UUID NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT false,
    sealed BYTEA,
    version BIGINT NOT NULL DEFAULT 0,
    source_daemon TEXT NOT NULL DEFAULT '',
    imported_at TIMESTAMPTZ,
    pending_daemon TEXT NOT NULL DEFAULT '',
    request_id TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS ducc_machine (
    user_id UUID NOT NULL,
    daemon_id TEXT NOT NULL,
    name TEXT NOT NULL,
    client_version TEXT NOT NULL,
    installed BOOLEAN NOT NULL DEFAULT false,
    credential_state TEXT NOT NULL DEFAULT 'unknown',
    error_code TEXT NOT NULL DEFAULT '',
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
