CREATE TABLE IF NOT EXISTS attachment_storage_alias (
 legacy_key text NOT NULL,
 attachment_id uuid NOT NULL,
 old_url text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS attachment_object_delete (
 storage_key text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 attempts integer NOT NULL DEFAULT 0
);
