# Skill transfer

The creator of a team or workspace Skill can transfer it to any other registered human account. Personal Skills cannot be transferred. The list action and detail action are labelled **转让**. The picker searches account names/emails; a second confirmation identifies the exact recipient. Transfer is immediate and requires no acceptance. The former creator loses mutation/transfer rights, but keeps any ordinary shared read/use access. There is no reclaim endpoint; the new creator can transfer again.

`GET /api/skills/{id}/transfer-recipients?q=...` requires the current human creator. The search is bounded to 20 results and requires at least two characters. Results contain only user ID, display name and email for disambiguation.

`POST /api/skills/{id}/transfer` accepts `{"recipient_user_id":"<user UUID>"}`. It returns the Skill with its current creator and files. Unknown fields, self-transfer, non-existent recipients and task/agent/daemon credentials are rejected. Normal workspace selection/authentication still applies; the recipient does not need membership in the source workspace.

A row lock rechecks current creator/scope before the atomic identity update and audit insert. Other mutating paths serialize or condition on the current creator. ID, name, scope, content, origin, files, labels and agent assignments are unchanged. `team_mesh_skill_transfer_audit` retains the Skill ID/name, old/new creator IDs and timestamp even if accounts or Skills are later deleted. No public history UI is added. Migration 558 is additive; application rollback retains audit records and does not reverse completed transfers.

# Enter to send

The shared Send action defaults to Enter (chat, task comments and task submission); Shift+Enter retains native editor paragraph/list/code behavior. IME composition, suggestion selection and held-key protection remain intact. Title fields retain their native navigation behavior.

Keyboard preferences remain browser-local. Storage version 3 resets only the prior Send override during a one-time migration (including disabled Send), preserving all other shortcuts. Subsequent manual choices, including Command/Ctrl+Enter or disabling Send, survive reloads. Restoring defaults uses Enter. Old tabs must reload to use the new code.
