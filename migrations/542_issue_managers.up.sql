CREATE TABLE IF NOT EXISTS issue_manager_state (issue_id uuid NOT NULL);
CREATE TABLE IF NOT EXISTS issue_manager (
 issue_id uuid NOT NULL,
 user_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
