# Transferring issues with historical supplement capabilities

A `task_supplement_capability` row records the protocol negotiated for one run.
It is not a pending message or an authorization token. A valid marker belonging
to a completed, failed or cancelled issue-only run does not block transfer,
even when the issue itself is still `in_progress`.

Transfer keeps the run, transcript, protocol flag and creation timestamp
unchanged. Only the marker's `workspace_id` follows the issue in the existing
transfer transaction. Its issue/run identities must agree, and its original
workspace must match the current issue owner; missing runs, inconsistent scope,
chat/autopilot-linked runs and active runs still fail closed.

Actual `task_supplement` receipts remain unsupported by this narrowly scoped
change, including settled receipts that may still reconcile late daemon ACKs.
Pending/delivering messages are never discarded or silently settled to allow a
move. Source contexts, wakeups, automation, InfoFlow bindings and all other
existing transfer checks remain in effect.

The apply-side bounded table fence covers both supplementary tables. Capability
rows also participate in the preview fingerprint, so a negotiation or metadata
change after confirmation requires a fresh preview. Scope changes roll back
with the issue if commit fails. Old daemon workspace credentials remain unable
to claim supplementary input for the moved issue, and finished-run task tokens
are revoked by the existing transfer flow.

No schema, frontend or client daemon change is required. The regressions cover
terminal history, active runs, actual receipts, inconsistent references,
preview invalidation, concurrent writers and commit rollback. Production issues
are not moved as part of development or automated tests.
