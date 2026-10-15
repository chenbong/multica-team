-- Per-message authorship is independent of a run's initiator. A queued or
-- steered input can share a task with another person's message. Historical
-- rows are deliberately not inferred from that task and remain unattributed.
ALTER TABLE chat_message ADD COLUMN IF NOT EXISTS author_user_id uuid;
