-- Personal labels never modify shared runtime defaults.
CREATE TABLE IF NOT EXISTS user_machine_name (
 user_id UUID NOT NULL,
 machine_key TEXT NOT NULL CHECK (btrim(machine_key) <> ''),
 name TEXT NOT NULL CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 100),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
