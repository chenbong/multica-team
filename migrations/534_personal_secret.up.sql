CREATE TABLE IF NOT EXISTS personal_secret (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL,
 name text NOT NULL,
 description text NOT NULL DEFAULT '',
 ciphertext bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
