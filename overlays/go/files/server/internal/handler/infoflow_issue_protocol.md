# InfoFlow issue relay contract

All routes are POST, outside ordinary Auth, authenticated exclusively with
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
the current binding state. A successful unbind returns `chat` while retaining
the previous issue's link metadata. Cached notices refresh this field from the
current state. The Bridge adds mode labels at IM send time only; no label is
inserted into issue comments. Agent deliveries always retain issue-origin mode.
A stale binding is handled and never falls through to Chat.
Mutation retries use the exact same message_id and payload. A conflicting reuse
is HTTP 409; bind/unbind/create are idempotent as well as comments. Recheck
membership before returning a cached result. No-invoke members can save a bound
comment; only canInvokeAgent permits a run. Admin is not an invoke bypass.

Bind/unbind/create binding changes require the agent owner, workspace owner/admin,
or current binder, all still current workspace members. Create also requires
invoke and uses content as the issue title/description. It binds the new issue.
Each switch/unbind changes generation and invalidates old routing. Binding the
same issue retains generation. All operations on a conversation serialize in DB.

Attachments: at most 5, maximum 20 MiB decoded per file and 20 MiB total; JSON body
maximum 29 MiB. Only message/create accepts them, after membership/dedup checks.
They use the existing upload and comment/issue handlers under the sender identity.
Uploads and source rows commit with the comment/issue; storage objects are removed
on rollback. Empty content with attachments uses a short file placeholder.

`/api/integrations/infoflow/issue-deliveries` accepts
`{robot_id,agent_id,workspace_id,limit?:number}` (default 20, maximum 50) and returns
`{deliveries:[{id,conversation:{kind,id},content,issue_id,comment_id,reply_to_message_id}]}`.
Only comments authored by this agent with explicit source ancestry/run origin are
eligible. No background broadcast, no requirement that all group participants
have access. Private destinations recheck their recipient membership. Ambiguous
cross-conversation runs fail closed. No forced mentions are added to output.

`/api/integrations/infoflow/issue-deliveries/{id}/ack` accepts the same scope plus
optional `message_id` and/or `message_ids` (all externally sent chunk IDs, for
quoting and echo suppression), returns `{handled:true,silent:true}`. Ack is idempotent and checks
scope, binding generation, and current issue workspace. Poll repeats until ack;
Bridge must use delivery id for send idempotency where the transport supports it.
A crash between external send and ack can repeat delivery (at-least-once).

Both notices and deliveries carry fresh `binding.workspace_slug`, `identifier`,
and `issue_id` so Bridge adds the requested complete-task footer using its public
app base URL. Delivery content itself is unchanged. Unknown/pre-binding external
quote IDs create a top-level comment; mapped quote IDs resolve only in the same
conversation and binding generation. Authorized business failures return 200 with
a generic actionable notice; protocol/credential/storage failures remain closed.

Backend tests use only the explicitly provided disposable DATABASE_URL. Migrations
start at 477; index migrations are individual concurrent statements. No upstream
source writes, production writes, real model invocations, or service restarts.
