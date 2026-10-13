# InfoFlow issue binding

Development candidate only. This change does not deploy, restart a daemon, or
include the paused personal-runtime refactor or either open community PR.

## Interaction

Enable the feature only after the server endpoints and migrations exist.
Private chats and group mentions then accept:

- `/bind PREFIX-123`: bind an existing issue in the robot's workspace.
- `/binding`: show the current binding and issue link.
- `/unbind`: remove the binding without deleting the issue or comments.
- `/issue description`: create a new issue assigned to the robot's agent and
  bind the conversation. Explicit natural-language issue creation still works.

A private binding belongs to the robot, workspace, agent and sender. A group
binding belongs to the robot, workspace, agent and group, not one sender. Binding
changes require the agent owner, a workspace owner/admin, or the current binder,
who must still be a workspace member. Creating an assigned issue also requires
permission to invoke the agent. Switching a binding does not cancel existing
work, but stops routing old-generation answers to the conversation.

With no binding, ordinary messages keep using Chat. With a binding, messages
addressed to the robot become comments under the actual sender's platform
identity. Unrelated group messages are not ingested. Other slash commands show
the binding instead of switching to owner-authority legacy actions.

| Sender access | Comment | Agent execution | Response |
| --- | --- | --- | --- |
| No verified account or workspace membership | None | None | `没有工作区权限，无法评论，请向[工作区 owner]申请工作区权限` |
| Member, no agent invoke permission | Saved as sender | None | `没有智能体权限，当前仅新增评论，但无法调用智能体，请向[智能体 owner]申请智能体权限` |
| Member with invoke permission | Saved as sender | Existing platform trigger rules | The agent's reply |

Owner placeholders are replaced with server-resolved names. Workspace admin is
not a bypass for invoking a private agent. The configured robot owner's PAT is
not used as the author of bound comments. The existing Chat mode still uses the
robot's configured account; this feature is not a sandbox for arbitrary runtime
shell commands or host credentials.

Replies preserve agent text, add no forced mentions, and append
`完整任务对话：<issue URL>` to every message part. Images use the platform's
existing attachment handler after the sender's membership check. Incoming quote
IDs link to known comments; no arbitrary cross-issue parent ID is accepted.

## Identity and credential boundary

The API exposes dedicated, narrow integration routes. They accept only a
separate service credential, not browser cookies, ordinary PATs, task tokens or
caller-provided actor headers. The trusted Bridge resolves identity exclusively
from SDK-received sender fields, never from the message text.

The server maps the sender's account identifier to an existing **verified** user
email in the configured corporate identity domain and checks current membership
on every action. It does not create users, accept caller-selected platform user
IDs, or fall back to the robot owner's identity. This mapping assumes the chosen
InfoFlow tenant and the platform email domain share that verified account
namespace; do not enable it for arbitrary external tenants/domains.
The current user schema has no separate email-verification flag. Identity
assurance comes from the authenticated corporate IM sender and matching login
namespace, not a caller-provided email or an extra verification column.

Set these only in restricted deployment environment files:

| Process | Variable | Meaning |
| --- | --- | --- |
| API + Bridge | `INFOFLOW_ISSUE_RELAY_TOKEN` | Same independent random service secret, at least 32 characters |
| API | `INFOFLOW_ISSUE_IDENTITY_DOMAIN` | Verified platform email domain matching the IM identity namespace |
| Bridge | `INFOFLOW_ISSUE_RELAY_ENABLED=true` | Explicit opt-in; default off |

The Bridge also needs explicit `multica.serverUrl`, `multica.webUrl` and a pinned
robot workspace and agent. Prefer the agent UUID. Secrets never go to browser
responses, daemon environments, prompts or Git. Keep the service channel on
loopback/a protected network or authenticated TLS; bearer auth alone does not
encrypt traffic. Do not expose the service secret to arbitrary agent scripts.

## Public group delivery and source routing

Group replies are intentionally public to every participant, even participants
without workspace membership. This is the requested product policy: there is
no all-members readership gate. Only the incoming author's permission controls
comment creation and agent invocation.

Outbound messages route by stored comment/task origin, not by text such as
`@username`. Private-origin replies are not broadcast into a group merely because
it is bound to the same issue. The first version returns this agent's attributable
replies, not every unrelated human comment or historical/background issue event.
Binding changes/deletion/moves invalidate old routing. Private deliveries also
recheck the recipient's membership.

## Reliability and storage

Team migrations **477–487** create separate binding, source-receipt and delivery
tables plus indexes. They do not apply personal-runtime migrations 467–476.
Run each migration file independently; concurrent indexes must not run inside
an enclosing transaction. Apply them to a disposable database before rollout.

Incoming changes and source receipts are atomic, serialized per conversation,
and idempotent by external message ID. WebSocket retries cannot duplicate an
accepted comment or issue. Current membership is rechecked on replay. Outbound
deliveries repeat until acknowledged. Bridge checkpoints completed long-message
parts and persists failed input for retries. One failed conversation does not
prevent other conversations from being delivered.

The server database and Bridge `*.issue-deliveries-*.json*` files are durable
state. Inboxes can contain message bodies and media; keep permissions restricted
and include them in private operational backups, never Git. Permanent malformed-input
or idempotency conflicts are retained in a separate private quarantine directory
for operator inspection rather than occupying active inbox capacity. Monitor its
disk usage and apply the deployment's diagnostic retention policy. A crash after an IM
send succeeds but before its receipt is persisted can still repeat that send;
the system does **not** promise exactly-once external delivery. Invalid/unsafe
message IDs are not guessed or rounded. Operators must monitor failed/full
inboxes; the local inbox is capped at 500 entries and 64 MiB.
Legacy Chat submission has no server idempotency key. A failure before submission
is retried; an uncertain outcome after its POST starts is retained for manual
inspection, not blindly resubmitted as a second agent task. Already persisted
Chat task IDs continue through the existing reply poller.

## Testing and future rollout

Local verification on 2026-10-13: 83 Bridge tests; 127 selected handler,
permission, comment and transfer cases with the race detector; 12 real-router
HTTP cases with the race detector; syntax checks, Go vet and a Linux/amd64 API
build passed. No test was skipped in those runs. Team migrations 477–487 passed
up/down/up on a separate empty database with no invalid indexes afterwards.
The SQL overlay guard test and upstream cleanliness checks also passed.
These are synthetic/local checks, not live InfoFlow account or browser testing.
No production process or client daemon was restarted.

Use synthetic users/messages, a disposable loopback PostgreSQL database and fake
send functions. Never run an installed agent CLI or send to real IM recipients
as a unit test. Set `DATABASE_URL` explicitly: some upstream tests otherwise skip
when no database is reachable. Prepare Go overlays with `overlays/prepare.py`;
do not edit the upstream submodule.

Future rollout requires an API/Bridge update and the team migrations, not a new
daemon protocol or client restart. Back up the production database/config first.
Keep the feature disabled until both processes and credentials are ready. Stop
Bridge intake before API rollback; preserve new tables and inboxes rather
than destructively dropping pending work. Old Chat behavior is restored when
the switch is off, so do not advertise an issue binding as active in that mode.

The existing issue-workspace transfer rejects an active InfoFlow binding. Unbind
first, transfer, and then configure an authorized binding in the destination.
Old source/delivery rows remain audit history and do not authorize writes into
the former workspace. In-flight IM requests already handed to the transport may
finish during a binding change; unbinding is not cancellation of an active run.
