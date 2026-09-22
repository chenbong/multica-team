# Issue review notifications

An enabled task robot sends a direct message to its binding account when an
issue assigned to its bound agent transitions into the `in_review` or `blocked`
category.
This works for issues created through the web UI, API or InfoFlow.

The bridge scans assigned issues every 15 seconds and checks their persisted
status-change timeline. Each transition has an event ID; successful deliveries
are recorded, pending deliveries retried, and the records survive restarts.
Historical transitions before a binding's first notification scan are skipped.
Custom statuses in the review category are supported. Returning from review to
work and later entering review again produces another notification.

The notification label is `任务已进入审核中` for `in_review` and `任务已阻塞`
for `blocked`. The recipient is verified using the binding profile's `/api/me` email. New
bindings also record `boundByEmail` from the authenticated binding-page session.
The private-message allowlist is not used to infer notification ownership.
Missing credentials, inaccessible workspaces, identity mismatch or missing
agents prevent notification and are reported in Bridge logs.

State is stored at `state.json.review-notifications.json` with mode 0600.
Include this file in instance-state backups; do not commit it to Git.
Delivery is best-effort at-least-once: the IM API has no supplied idempotency
key, so a crash after successful delivery but before saving may duplicate a
notification. Polling cannot recover an issue deleted before it is observed.

Private chat completion replies to the binding owner are suppressed for issues
currently in review, because this notification replaces them. Existing group
completion replies remain; the owner's direct notification is separate.
