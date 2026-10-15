# Shared agent chat integration

This sidecar implements the final agent-permission policy, not the superseded visibility-toggle proposal. No visibility fields, switches, new routes, or migration 521 are required. Migration 520 now serves the separate, explicitly approved per-message sender requirement:

```sql
ALTER TABLE chat_message ADD COLUMN IF NOT EXISTS author_user_id uuid;
```

Apply `520_chat_message_author.up.sql` before the new API; it has no foreign key or index and does not rewrite/backfill historical rows. Shared/coalesced historical task initiators are not reliable proof of individual message authorship, so ambiguous old rows stay NULL. Rollback drops the nullable column after rolling back code. Existing generated response/model scans retain their old columns for compatibility; the checked CreateChatMessage query overlay adds an optional write-only author parameter.

## Build registration (main integrator owns these files)

Apply custom table migrations as the application database role. If operations
use a separate migration owner, explicitly grant the application role SELECT,
INSERT, UPDATE and DELETE on `infoflow_robot_directory` and
`infoflow_conversation_notice`. Readiness checks must exercise the binding view
with that non-owner role, not only the database superuser.

1. In `overlays/prepare.py`, process `runpy.run_path(str(HERE / "chat-access-go-patch.py"))["generated"](UPSTREAM, FILES)` after the existing issue-transfer SQL generation. Register each generated replacement in the manifest using the same checked-hash mechanism. Generated files compose with current direct overlays (notably daemon.go); never patch the upstream submodule.
2. Immediately after `h := handler.New(...)` in `overlays/go/files/server/cmd/server/router.go`, add:

```go
hub.SetAuthorizer(newChatScopeAuthorizer(queries, h.CanReadChatSession))
hub.SetDeliveryFilter(h.CanDeliverChatEvent)
```

This intentionally replaces the initial creator-only authorizer installed in upstream main.go. No additional HTTP route is needed. The delivery filter is required: current clients use workspace/user fanout, not just explicit resource subscriptions.

3. Register checked replacements before `plan_patches()` through the shared plan:

```python
runpy.run_path(str(HERE / "chat-access-web-patch.py"))["extend"](CHANGES)
```

Do not call `apply()` after the ordinary web patcher. `extend(CHANGES)` keeps check/apply-twice/restore atomic and preserves brand/login/group patches in the same plan. The standalone `apply()` exists only for isolated worker tests and rejects any target containing `.git`.

## Policy

- The user must currently be a workspace member and satisfy the agent invocation grants (agent owner, workspace grant, or explicit member grant). Workspace owner/admin inventory visibility does not bypass this chat policy.
- Accessible historical/new sessions appear in the sidebar and are readable/addressable by link. Command-only internal sessions remain excluded.
- A permitted participant may send a new turn, with existing per-send invocation checks preserved. `SendDirectChatMessage` already receives the authenticated requester and stamps initiator/originator/accountable identities accordingly; it does not use `session.creator_id`.
- Delete/archive/pin/rename/interrupt/queue edits and draft restoration remain creator-only. Other participants' unread cursors and personal pin state are not exposed or modified.
- Transcript APIs hydrate the actual sender from each user message's `author_user_id`. The direct-send transaction writes the authenticated requester; two inputs sharing one active task retain distinct authors. Unattributed historical rows do not invent an author.
- Chat/task WebSocket subscriptions use the same current ACL. Workspace, user and resource-room outbound frames are filtered at delivery time, including revocation of already subscribed clients.
- Chat attachment metadata/download/content and tool-log reads use the same agent policy. Existing task-token reads remain pinned to the producing run. Uploads accept permitted participants; attaching another user's pending upload still fails existing uploader binding checks.

## Test isolation

The chat worker uses its own local database `chat_access_worker` on the existing loopback test PostgreSQL. It copies only the schema from the shared fixture database and does not reset any other worker's data. No real agent CLI is executed.

## Storage boundary to retain in rollout review

Local chat objects are now refused on the unauthenticated `/uploads/*` route, even if a client spoofs identity headers. Chat markdown URLs use the authenticated attachment path. Existing signed-download capabilities expire after 60 seconds and remain bearer capabilities until expiration, as before. External publicly readable object-store URLs cannot be made revocable by a chat ACL alone; do not claim this feature revokes an already downloaded file or previously minted storage URL.

## Worker verification

- 24 targeted Go tests/subcases passed across handler, realtime, and server command packages (including sender/steered attribution, membership/ACL revocation, HTTP/tool-log/attachment gates, bare full task/status/model event shapes, user/resource/workspace fanout, and creator-only mutations).
- 88 focused Views tests passed; 2 additive/malformed sender schema tests passed.
- Views TypeScript checking passed in the worker's generated tree.
- Shared-plan check/apply-twice/restore, late-anchor atomic failure, and login-copy/owner-control preservation: 3 tests passed.
- No actual agent invocation, production calls, commits, pushes, or upstream source writes were performed by the chat worker.

All Go checks used `build/chat-access-overlay.json`; the main integrator must regenerate the unified manifest after registering the generator. Apply migration 520 to the integrator's own test database before running the updated suite. The worker applied it only to its independent `chat_access_worker` database.
