# Task conversation subscriptions

Every valid binding subscribes to new task comments and agent replies. Input
channel is no longer an outbound isolation boundary: private messages may reach
bound groups, and group inputs may reach bound private conversations. Human
inputs are not echoed into their physical source conversation. Agent output
fans out to all valid bindings. Unbound Chat sessions remain unchanged.

Each task allows at most one group and one private conversation in total,
not one private conversation per user. Existing bindings are never deleted to
make room; the previous binding must be explicitly removed before replacement.
Membership/assignment permissions are retained.
Different tasks/workspaces remain isolated. Messages originating from robots
are ignored on ingress, so forwarding cannot create a reflection loop.

## Time and lifecycle

Migration 531 adds broadcast_started_at. Effective delivery start is the later
of this policy timestamp and bound_at. Existing bindings switch without rebind,
but no pre-cutover message is backfilled, including previously failed deliveries.
A run or triggering instruction may predate the binding; only the generated
comment's timestamp gates the output. A rebind gets a new generation/start.

Each destination has its own immutable content/audience snapshot, receipt and
retry progress. Failures do not stall other destinations. A fresh authority
check runs before each external send, including after group-membership lookups.
Unbinding stops pending sends/retries; it does not recall accepted messages.
Already sent comments are not edited or recalled after web edits/deletions.

## Mentions

Human mirrors never trigger AT and show only their author, not a source label.
Agent replies notify requesters and explicitly named people. Every destination
group filters against its current members before selecting up to 50 recipients;
requesters have priority. The existing combined Markdown/AT format and 8192
UTF-16-unit limit remain unchanged.

Wakeup runs snapshot their instruction when created. They use only explicit
mentions from that snapshot, never their creator or older private inputs.
Structured member mentions resolve through workspace membership; plain account
mentions are verified against the destination group's roster. Ambiguous display
names are not guessed. Old in-flight wakeups without a snapshot default to no AT.

## Disclosure and rollout

The binding area and new bound notices state that private and group task content
is shared across all bindings. There is no extra confirmation dialog or footer
on ordinary replies. Deploy API, Bridge and the generated Web together after
backup. Do not restart client daemons. Do not restore stale delivery snapshots
during rollback, as that could replay accepted messages. This is an application
cutover; no actual task is re-run or old report resent by deployment.
