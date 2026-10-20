-- Existing subscriptions start the new policy at deployment, without replay.
-- Rebinding is additionally fenced by the existing bound_at/generation fields.
ALTER TABLE infoflow_issue_binding
    ADD COLUMN IF NOT EXISTS broadcast_started_at TIMESTAMPTZ NOT NULL DEFAULT now();
