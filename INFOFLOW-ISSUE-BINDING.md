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

A private binding still belongs to the robot, workspace, agent and sender.
A group binds directly to an issue, independently of which robot received the
command. A group cannot have two active issue associations through different
robots or workspaces. Unbind before moving that group to another workspace.
For an existing issue assigned to a squad, the configured squad leader is the
coordinator. For an issue assigned to one agent, that agent is the coordinator.
An unassigned or human-assigned issue must first be assigned to an agent/squad.
Binding administration requires the relevant owner, a workspace owner/admin, or
the current binder, who must still be a workspace member. Creating an assigned
issue also requires permission to invoke the agent. Switching a binding does
not cancel existing work, but stops routing old-generation answers.
Replacing an existing group binding first requires management authority over
that existing binding; owning a different target task's leader is not enough.
The target issue's binding requirements are then checked separately.

## Required leader presence and sending identity

Association and activation are separate. A new group association is pending
until Bridge has verified that the coordinator's configured InfoFlow robot is
actually a group member. For a squad this means the leader robot; for a single
agent it means that agent's own robot. The robot that received `/bind` is not
implicitly made the leader. Missing or ambiguous robot mappings fail closed.

Bridge may invite a missing coordinator with `POST /robot/group/addMembers`.
It reads `POST /robot/group/memberList` with `recallType: 0` and matches the
returned `agentInfoList[].agentId` against the robot application ID, not the
platform agent UUID or the different InfoFlow `imId`. Both APIs require the
corresponding permissions to be enabled on the calling robot. Invitation code
`201` means waiting for administrator approval; even code `0` is not proof of
membership. Partial failures are inspected and membership is read again.
Invitation attempts are persisted and bounded; pending approval never counts
as an active binding. No real invitations are made by default tests.

The leader's presence is rechecked before group input/output and represented
server-side as a short-lived verification lease. Loss of membership, unreadable
membership, or changed issue assignee/squad topology pauses group synchronization
until the required robot is verified again. Existing agent runs are not stopped.

The leader relay coordinates outbound deliveries for the entire assigned team:

| Reply author | Verified membership | Sender |
| --- | --- | --- |
| Leader / sole assigned agent | Required to be present | Its own robot |
| Squad member | Its robot is in the group | The member's own robot |
| Squad member | Its robot is absent, or no robot is configured | Leader robot, with `【member name · 队长代发】` |
| Any author | Membership unknown, robot mapping ambiguous, or selected robot unavailable | Pause; do not guess another identity |

The sending identity is pinned before transmission and retained with multipart
checkpoints. A retry does not switch between member and leader. Each delivery
has one coordinator, not a separate poller per author. Robot-origin input is
filtered to prevent forwarded agent replies from starting robot conversations.
Mode/author labels are transport-only; canonical issue comments are unchanged.
When a group addresses the assigned squad's leader, the internal trigger retains
the squad ID and leader role so the runtime receives the squad briefing. Other
members and single-agent assignments keep ordinary agent invocation semantics.
Robot names from older configurations are resolved using each robot's own
account, including configured-but-offline robots. Hidden, missing or ambiguous
names are not interpreted as an absent robot; prefer stable agent UUIDs.
The server's current task roster supplies participant names solely to exclude
unrelated legacy configurations before lookup. Only a complete roster can make
that exclusion; an unresolved matching participant still blocks routing. An
authorized bootstrap explicitly includes the addressed inviter when it is not
part of the task, without treating it as the task's leader.

Example: an issue is assigned to a squad led by A with member B. A's robot must
be in the group. If B's robot is present, B sends:

```text
[任务模式]

性能测试已完成。

完整任务对话：https://team.example.test/workspace/issues/TASK-42
```

If B's robot is absent, A sends the same reply with explicit attribution:

```text
[任务模式]

【B · 队长代发】
性能测试已完成。

完整任务对话：https://team.example.test/workspace/issues/TASK-42
```

If the issue is assigned directly to A (not a squad), only A's robot is required
and A sends its own replies. No fictitious squad or additional leader is created.

## Open the associated group from an issue

The issue header shows **Open InfoFlow group / 打开如流群聊** for active,
verified group bindings. One group opens directly; multiple groups use a menu.
Pending bindings, private chats, and inaccessible issues do not expose links.
The normal member-authenticated `GET /api/issues/{id}/infoflow-groups` route
returns only group destinations; service credentials are never sent to the UI.

Links use the official `infoflow://APICenter?data=` protocol with URL-encoded
Base64 JSON: `APIName: BdHiJs.service.hi.open`, `version: 65`, and
`data: {id: <decimal group ID string>, type: "2"}`. No draft, automatic send,
invitation, or membership grant is encoded. The frontend constructs this one
allowed action from a validated ID instead of trusting arbitrary response URLs.
Launching requires the user's InfoFlow client and browser permission. Actual
desktop/mobile protocol launch is a separate manual acceptance check.

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

Every IM text reply starts with `[聊天模式]` or `[任务模式]`, including each
long-message part. Labels are added only at the final transport boundary, never
to platform Chat input, issue comments, prompts, or canonical delivery content.
The optional `conversation_mode` response metadata distinguishes an active issue
binding from the old issue link retained on a successful unbind notice. Bind
success is issue mode; unbind success is Chat mode. Delayed answers keep their
origin's mode. Existing plaintext delivery digests/checkpoints remain unchanged.
Verification-code messages are not conversation replies and are not relabeled.

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

The standalone `bridgectl.py start/restart` path loads the relay token from the
restricted `deploy/secrets.env` file, just like the deployment entry point. Only
that value is imported; unrelated database/JWT/plugin secrets are not copied
from the file. Environment-only container configuration is also supported.
Missing or invalid relay credentials are rejected before stopping an existing
process or launching a replacement. Disabled relay mode does not require a token.

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
it is bound to the same issue. Group deliveries include attributable replies from
the currently assigned leader and squad members, including delegated task
lineage; private deliveries retain the original single-agent scope. This is not
a broadcast of every unrelated human comment or historical/background event.
Binding changes/deletion/moves invalidate old routing. Private deliveries also
recheck the recipient's membership.

Ordinary bound input pins `expected_binding_id` and `expected_generation` from
its first preflight before persistence/submission. Retries retain that target;
an uncommitted message cannot silently move to another issue after a rebind.
The server checks existing receipts before the target-version fence, so an
already accepted input remains idempotent after binding changes. Conflicting
or legacy unversioned prepared requests require review instead of being rerouted.
Completed Chat input is checked before issue routing, preserving its original
destination after switching to task mode; successful chat commands also retain
deduplication records.

## Reliability and storage

Team migrations **477–490** create separate binding, source-receipt and delivery
tables plus indexes. The new 488–490 migrations add group coordination and
uniqueness guards; they refuse ambiguous older group bindings instead of
silently deleting data. They do not apply personal-runtime migrations 467–476.
Run each migration file independently; concurrent indexes must not run inside
an enclosing transaction. Apply them to a disposable database before rollout.

Incoming changes and source receipts are atomic, serialized per conversation,
and idempotent by external message ID. WebSocket retries cannot duplicate an
accepted comment or issue. Current membership is rechecked on replay. Outbound
deliveries repeat until acknowledged. Bridge checkpoints completed long-message
parts and persists failed input for retries. One failed conversation does not
prevent other conversations from being delivered.

The server database and Bridge `*.issue-deliveries-*.json*`,
`*.issue-group-deliveries.json`, and `*.issue-group-invites.json` files are durable
state. Group checkpoints are shared across robot coordinators, so changing the
leader does not reset sent-part progress or sender identity. Inboxes can contain
message bodies and media; keep permissions restricted
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

Local verification on 2026-10-13: 157 Bridge tests; 182 selected handler,
permission, comment and transfer cases with the race detector; 12 real-router
HTTP cases with the race detector; 13 isolated lifecycle/secret/gitignore tests;
153 frontend API/query/component regressions
(including 26 new group-entry cases); 10 existing legacy-browser compatibility
tests; core/views type checks; production Web build; syntax checks, Go vet and a
Linux/amd64 API build passed. No test was skipped in those runs. New migrations
488–490 passed conflict refusal, down/up/down/up/repeat-up on a second isolated
database with no invalid indexes afterwards. The earlier 477–487 migration
round trip remains part of the previous PR verification. SQL overlay checks and
upstream cleanliness checks also passed.
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
