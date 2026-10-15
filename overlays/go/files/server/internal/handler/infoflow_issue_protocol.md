# InfoFlow issue relay contract

All integration routes below are POST, outside ordinary Auth, authenticated with
`Authorization: Bearer ${INFOFLOW_ISSUE_RELAY_TOKEN}` (minimum 32 characters).
Missing/short token or missing/invalid `INFOFLOW_ISSUE_IDENTITY_DOMAIN` disables
the relay. Bridge must never fall back to Chat on HTTP errors or `handled:true`.

`/api/integrations/infoflow/issue-relay` accepts:

```json
{"robot_id":"robot","agent_id":"agent UUID","workspace_id":"workspace UUID","conversation":{"kind":"private|group","id":"external conversation ID"},"sender":"external username","message_id":"stable external message ID","operation":"message|bind|binding|unbind|create","issue_ref":"UUID or PREFIX-123","content":"plain or Markdown text","reply_to_message_id":"optional external message ID","attachments":[{"filename":"image.png","content_type":"image/png","data_base64":"..."}]}
```

`agent_id` is a resolved UUID; Bridge may resolve configured agent names once
using its owner configuration. That owner is never the incoming actor.
Private conversation id must equal sender. Sender is a username, never an email
or platform user id. Server maps sender plus configured domain to a platform
user and checks current workspace membership and the platform disabled-user gate.
All incoming actor headers are discarded on the internally stamped request.

Response: `{handled:boolean,silent:boolean,notice?:string,binding?:{issue_id,identifier,title,generation},comment_id?:string}`.
`binding` is a read-only permission preflight: send it before downloading media;
`message_id` can be omitted for this read. Add `command` for an explicit slash
lookup; unbound lookup with `command` returns handled:true with a no-binding
notice. Only unbound `message`/preflight `binding` may return `handled:false`.
New unbound message/preflight traffic bypasses platform-user membership checks
and continues through the existing Chat allowlist. Binding mutations still
require membership. Receipt existence is checked before this bypass: retries of
previously accepted inputs remain handled after unbind, and recheck membership
before replaying a cached result. Unbound output echoes also remain handled.
For `/create` media, send `operation:"binding", command:"create"` before download.
This read-only preflight always returns handled:true and checks current membership,
binding-management authority, and canInvokeAgent even when unbound. Its
`can_comment:true` means create media may be downloaded; denial returns false
with the appropriate owner notice. It creates no binding, receipt, attachment,
comment, issue or task. The subsequent create request repeats the permission gates.
Responses always include `can_comment`. No-access is handled:true, silent:false,
can_comment:false with the requested owner permission notice and minimal binding
link metadata (issue_id, identifier, workspace_slug, generation; no title/body).
Member/no-invoke saves the comment and returns the requested agent-owner notice.
`conversation_mode` is optional presentation metadata (`chat` or `issue`) for
the current binding state. Private unbind returns `chat` while retaining
the previous issue's link metadata. Group unbind removes only one association;
remaining bindings keep task mode. Cached notices refresh this field from the
current state. The Bridge adds mode labels at IM send time only; no label is
inserted into issue comments. Agent deliveries always retain issue-origin mode.
A stale binding is handled and never falls through to Chat.
For `operation:"message"` and group `operation:"unbind"`, the first selected
preflight supplies `binding_id` and
`generation`. The Bridge persists these as `expected_binding_id` and
`expected_generation` in the mutation body and never refreshes them on retry.
Both must be valid UUIDs matching the selected binding for a new mutation. Missing
or stale targets fail closed, before uploads, comments, removal or task creation. Receipt replay
is checked first, so an already committed request can be replayed idempotently
after a rebind/unbind without producing another comment. Previously completed
Chat messages are filtered before entering this API; they do not become task
input just because the conversation is now bound.
Mutation retries use the exact same message_id and payload. A conflicting reuse
is HTTP 409; bind/unbind/create are idempotent as well as comments. Recheck
membership before returning a cached result. No-invoke members can save a bound
comment; only canInvokeAgent permits a run. Admin is not an invoke bypass.

Bind/unbind/create binding changes require the agent owner, workspace owner/admin,
or current binder, all still current workspace members. Create also requires
invoke and uses content as the issue title/description. It binds the new issue.
Each switch/unbind changes generation and invalidates old routing. Binding the
same issue retains generation. All operations on a conversation serialize in DB.
Group `/bind` adds an issue association, never replaces other tasks in the group.
An issue can occupy only one group slot, including pending associations. To move
it to another group, an authorized user must explicitly unbind it first. The
target owner cannot overwrite somebody else's association merely by binding
their own task. Private binding replacement remains independent.
Group messages addressing the assigned squad leader preserve a squad trigger
(`is_leader_task=true`, assigned `squad_id`); worker and single-agent messages
keep ordinary agent triggers, with unchanged invocation permission checks.

Attachments: at most 5, maximum 20 MiB decoded per file and 20 MiB total; JSON body
maximum 29 MiB. Only message/create accepts them, after membership/dedup checks.
They use the existing upload and comment/issue handlers under the sender identity.
Uploads and source rows commit with the comment/issue; storage objects are removed
on rollback. Empty content with attachments uses a short file placeholder.

`/api/integrations/infoflow/issue-deliveries` accepts
`{robot_id,agent_id,workspace_id,limit?:number}` (default 20, maximum 50) and returns
`{deliveries:[{id,conversation:{kind,id},content,issue_id,comment_id,reply_to_message_id}]}`.
Private replies are limited to the configured agent. Group replies include the
current issue assignee or assigned squad's leader/members, with explicit source
ancestry/run origin. Group delivery metadata includes `author_agent_id` and
`author_name`, plus the current group binding view. Only the verified leader
coordinator polls and acknowledges group deliveries. No background broadcast,
no requirement that all group participants
have access. Private destinations recheck their recipient membership. Ambiguous
cross-conversation runs fail closed. No forced mentions are added to output.

`/api/integrations/infoflow/issue-deliveries/{id}/ack` accepts the same scope plus
optional `message_id` and/or `message_ids` (all externally sent chunk IDs, for
quoting and echo suppression), returns `{handled:true,silent:true}`. Ack is idempotent and checks
scope, binding generation, and current issue workspace. Poll repeats until ack;
Bridge must use delivery id for send idempotency where the transport supports it.
A crash between external send and ack can repeat delivery (at-least-once).

## Group coordination

Group bindings are issue-scoped, not agent/robot-scoped. An issue has at most one
group association; one group may contain multiple issues, with overlapping teams
or different leaders. Private scopes remain unchanged. Group message handling checks the addressed agent against the
current assigned team and retains per-agent invocation permissions.

Preflight `operation:"binding"` retains the incoming `content` and
`reply_to_message_id`; the server selects the route. Normal message priority is
stored same-scope quote provenance, then explicit task reference, then the
addressed agent's unique associated issue. Ambiguity returns a handled task-mode
notice without a binding, comment or run. Known foreign/stale quotes cannot fall
back to another task. A genuinely unbound group's new ordinary message retains
Chat behavior. Explicit binding-control arguments select their named issue;
`command:"unbind"` marks the read-only preflight for destructive selection.
There is no global default/current issue for a multi-task group.

The Bridge preserves terminal non-selected routing decisions across failed
notice sends and restarts. A redelivered ambiguous input cannot become executable
merely because another association was later removed; the user's clarification
must be a new message. Transport errors remain retryable, not terminal decisions.

Group binding views add `binding_id` and a `group` object containing `state`
(`pending` or `active`), `leader_agent_id`, `agent_ids`, `agent_names` (current
participant UUID-to-name map used to exclude unrelated legacy configs), optional
`leader_robot_id`, and an optional human-readable `reason`. The server derives
the leader from the issue's single-agent assignee or the assigned squad's
`leader_id`; the Bridge cannot designate an arbitrary leader. A binding mutation
does not assert IM membership. Pending state must not be presented as success.
`can_reconcile_group` distinguishes authorized membership reconciliation from
permission to submit a new comment; a pending binding cannot admit comments.

`POST /api/integrations/infoflow/issue-groups` accepts the ordinary relay scope
and returns `{groups:[{conversation:{kind:"group",id},binding}]}` for the
workspace. Multiple entries may have the same conversation ID. Callers key
plans by `binding.binding_id`, not conversation ID. This is trusted service
discovery, not a browser API.

`POST /api/integrations/infoflow/issue-group-status` accepts that scope plus
`conversation`, required `binding_id`, `generation`, `leader_agent_id`, `leader_robot_id`, and
`leader_present`. Presence is reported only after a fresh, validated IM member
list; an invitation response is not sufficient. An affirmative report must be
scoped to the actual leader robot/agent. The server validates the generation and
current topology and grants only a short verification lease. It returns a relay
response with the refreshed binding. Stale topology, reassignment, membership
loss, and expired verification cannot authorize new group input/output.

Bridge chooses the author's own robot when it is verified in the group;
otherwise the leader sends with an attribution label. Unknown membership or an
unavailable in-group member transport does not permit guessing a fallback.
The selected robot, agent and attribution are pinned with multipart progress
before sending. Canonical comment and outbox content remain undecorated.

## User-facing group destinations

`GET /api/issues/{id}/infoflow-groups` is a separate ordinary-authenticated
endpoint. It uses issue access checks and returns
`{groups:[{group_id:string,url:string}]}` with zero or one active group binding.
Private conversation identifiers and service/robot credentials are never
returned. An absent/disabled integration returns an empty group list.
Group IDs remain exact decimal strings. The URL is a URL-encoded Base64
`infoflow://APICenter?data=` action for `BdHiJs.service.hi.open`, version `65`,
with `data.id` set to the group ID and `data.type` set to `"2"`. It grants no
membership and contains no automatic send or draft action.

Both notices and deliveries carry fresh `binding.workspace_slug`, `identifier`,
and `issue_id` so Bridge adds the requested complete-task footer using its public
app base URL. Delivery content itself is unchanged. Unknown/pre-binding external
quote IDs create a top-level comment; mapped quote IDs resolve only in the same
conversation and binding generation. Authorized business failures return 200 with
a generic actionable notice; protocol/credential/storage failures remain closed.

Backend tests use only the explicitly provided disposable DATABASE_URL. Migrations
477–497 retain individual concurrent-index statements. Normal deployment replay
uses `deploy/migrate-team.sh` to avoid recreating retired group uniqueness after
the issue-unique index is valid. No upstream
source writes, production writes, real model invocations, or service restarts.

Explicit private IM `bind` / `create` requests may include `robot_name` and
`robot_chat_id`, reduced from the same robot client's SDK metadata. These are
optional public display fields only; the server validates them and never uses
them as sender, recipient, agent or binding authority. They are frozen in the
prepared request for retries. Missing metadata preserves existing verified
fields, and ordinary messages, lookups, unbinds and group requests cannot write
them. This keeps IM-originated bindings consistent with web bindings without
adding a daemon protocol or migration.
