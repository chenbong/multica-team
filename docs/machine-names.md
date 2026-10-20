# Personal machine names

The existing Rename machine dialog now saves a private display remark for the
authenticated viewer. It is visible only to that account across workspaces.
Any runtime the viewer can list may be labelled, including public runtimes and
private runtimes visible to workspace administrators. This grants no execution,
filesystem or credential access.

Names are keyed by user ID and runtime mode plus daemon identity. Providers and
workspace registrations of that daemon share the remark; distinct daemons do
not. Runtimes without a daemon ID use their runtime ID. No hostname guessing or
bulk backfill occurs.

The legacy custom_name PATCH field remains compatible, but now always updates
the viewer's private remark. Existing shared agent_runtime.custom_name values
are preserved as fallback defaults. Empty text removes the personal remark.
Responses expose personal_name separately and project it into custom_name for
existing display helpers. Daemon responses retain the original shared metadata.

A user-only daemon:personal-name event invalidates all runtime workspace caches.
No remark text is broadcast. Visibility changes remain owner-only and commit
atomically with a remark, if both fields are supplied.

Apply migrations 529 and 530 before starting the new API. The unique index
runs concurrently outside a transaction. Rollback to the prior API and Web can
leave these additive tables in place; never restore a database snapshot over
new production tasks. No client daemon upgrade or restart is required.

Tests cover privacy, cross-workspace/provider projection, clearing, default
preservation, admin/member permissions, mixed-patch atomicity, concurrency,
user-only event routing, schema compatibility and cache invalidation.
