# Task collaboration, remote project folders and attachment storage

These overlays reuse the pinned upstream execution engine. They add no daemon
protocol and require no daemon restart. New server-delivered tools and per-run
read capabilities become available on the next run. Existing running tasks keep
their original permissions.

## Personal OneAPI quota

Configure `MULTICA_ONEAPI_QUOTA_URL` with the deployment's trusted HTTPS quota
endpoint. The server uses only the signed-in person's existing imported ducc
login credential, sends it in the provider's Authorization header, and returns
quota amounts without the credential. No username selector is accepted.

The sidebar shows used / monthly total with the used proportion. Remaining
quota and last successful query time appear on hover. Visible pages refresh
every ten minutes; hidden pages pause. Per-user credential-version-aware cache
and concurrent request coalescing prevent repeated upstream requests. Unlimited
quota, unlinked credentials, re-import requirements and stale data have explicit
states. A failed query never becomes a fictitious zero balance. With no endpoint
configured the optional quota display is hidden.

## Project working directories

The Web form stores one project-wide `working_directory` resource containing
only an absolute `local_path`; no execution machine is selected. The database
has a partial unique index so simultaneous saves cannot create two preferences.
The API keeps the existing project/workspace authorization checks and never
checks its own filesystem for this remote path.

A compatible daemon resolves this preference once for each claimed worker task,
before locking or preparing the execution environment. An existing readable,
writable directory becomes a native `in_place` assignment. A missing directory
becomes no directory assignment, preserving the original default preparation,
repository checkout and prior-workdir reuse behavior. Missing paths are not
created. Files, permission errors and unsafe system roots report an error;
only a missing path selects the default. Native coordinator exemptions remain.

Legacy per-machine `local_directory` resources are retained. A project-wide
preference takes priority, including when its path is missing; removing the
preference restores those legacy resources. Existing legacy-only projects keep
their original behavior. There is no automatic migration or deletion of these
settings.

The CLI loads its normal project rules, configuration and skills from the
chosen working directory (`CLAUDE.md`, `AGENTS.md`, `.claude`, `.codex`, etc.,
as supported by the installed runtime). There is no new rules loader or project
file copying. Native directory serialization and cleanup protection are reused.

`tmesh-project-working-directory-v1` is advertised on both daemon transports.
The final claim gate rejects new preferences on older clients with an explicit
upgrade message instead of letting them silently ignore the path. Development
and tests do not install, restart or upgrade any existing client daemon.

## Human task administrators

Task administrators are real current workspace members. The human creator is
the default administrator. Agent-created sub-tasks copy their parent's current
administrator list once; subsequent edits are independent. Independent tasks
requested by a human use the real initiating person. Autonomous independent
creation inherits trusted source-task administrators, otherwise the agent owner;
unresolvable historical records fall back to the current workspace owner.

Only listed human task administrators can delete a task. Task administrators and
workspace owner/admin may edit the list, which must retain at least one member.
A workspace owner/admin does not implicitly gain deletion rights: they can first
explicitly add themselves. Agent/cloud credentials cannot delete tasks or edit
the list. Member departure removes that person's authority and adds a workspace
owner if nobody eligible remains. Updates, cancellation authorization and final
deletion checks are serialized against transfers and list changes.

Endpoints: `GET/PUT /api/issues/{id}/administrators` (`user_ids` on PUT).

## Read-only owner-managed task context

`GET /api/agent/managed-issues` lists tasks administered by the executing agent's
owner across workspaces. `q` and `after` provide search and pagination. The
request must use a live task credential. A server-only snapshot binds each new
token to the agent owner at claim time. The machine owner and invoker are not
substituted for that owner. Ownership changes do not upgrade an old token.

`GET /api/agent/managed-issues/{uuid}` returns the task. Read sections include
`comments`, `timeline`, `runs`, `panels`, `properties`, `administrators`,
`attachments`, `runs/{runId}/messages`, and attachment metadata/content/download.
Each nested resource must belong to the authorized task. Related-task expansion
cannot bypass per-task authorization. Current owner, membership, administrator
list and run/token validity are checked on every request. Existing redaction and
private-chat restrictions remain in effect. Ordinary task-token workspace
selectors are unchanged. No write operation is exposed here.

The server-delivered `multica-platform` skill includes
`references/managed-issues.md` and the `issues` area of
`scripts/team-mesh-tools.py`. The HTTP API can also be used directly. A run that
predates this capability starts a new turn to receive it; the daemon stays up.

## Cross-workspace transfer

`POST /api/issues/{id}/transfer-preview` is read-only. It accepts a destination,
`include_subissues` and source-to-destination `status_mappings`. The preview
returns the affected task IDs, status choices, counts, effects and fingerprint.

`POST /api/issues/{id}/transfer` additionally requires that fingerprint,
`confirm_cleanup: true`, and `confirm_unassign: true` when needed. Only a human
who administers every moved task or is source workspace owner/admin may apply;
they must also belong to the destination. Choices are current task only, or its
entire descendant sub-task tree. Invalid status mappings require a user choice;
any active destination status may be selected. Structural permission/configuration
changes require a fresh confirmation; streaming chunks do not make a running
task impossible to move.

A single transaction cancels selected active runs, settles supplements, revokes
old tokens/capabilities, clears incompatible workspace settings and routing,
allocates destination numbers, and moves content/access metadata. The project,
wakeups, labels, custom properties, pins, PR associations and InfoFlow bindings
are removed. External parent/dependency/duplicate edges are detached; compatible
internal relationships stay. Eligible administrators remain; destination owner
is the fallback. Comments, file metadata, immutable captured context and run
history are retained. Private chats are not moved or granted new access.
No extra stop request goes to an external job platform. Offline daemons do not
block the platform transaction. Old identifiers stop resolving.

The accepted InfoFlow sender must be a workspace member allowed to invoke the
agent in both unbound Chat and task mode. The trusted Bridge chat intake records
the real person as message author, run originator and independent-task creator;
it cannot fall back to a robot-owner credential on authorization failure.
Owner-profile image staging is restricted to the exact session's unpublished
staging rows and does not substitute the owner for the message author.

## Private BOS attachments

Set the following only in the protected deployment environment, reusing an
existing private bucket and a prefix separate from backups and exported images:

```text
MULTICA_ATTACHMENT_BOS_ENABLED=true
MULTICA_ATTACHMENT_BOS_PREFIX=application/attachments
MULTICA_ATTACHMENT_PUBLIC_APP_URL=https://mesh.example.com
S3_BUCKET=private-bucket
S3_REGION=region
AWS_ENDPOINT_URL=https://s3.region.example.com
S3_USE_PATH_STYLE=false
AWS_ACCESS_KEY_ID=<from the protected credential store>
AWS_SECRET_ACCESS_KEY=<from the protected credential store>
ATTACHMENT_DOWNLOAD_MODE=proxy
```

TLS/hostname verification stays enabled. Existing CA configuration may be
supplemented with already-trusted storage CAs. Bucket policy, exported-image
retention and backup configuration are not changed.

Each new file is limited to 50 MiB. The API allows multipart overhead, spools
large bodies temporarily and streams to object storage. The Web proxy allows
52 MiB. Images/file cards remain normal attachments; PostgreSQL stores metadata.
Successful new uploads have no persistent local copy. Failure is explicit; there
is no persistent local-upload fallback. Avatar classification still rejects
publishing a bound private task/chat image, including through storage routing
prefixes.

The legacy local backend remains read-only for historical reads. Explicit
maintenance copies registered local attachments, downloads and verifies their
size/hash, commits the new URL and durable legacy alias, updates existing avatar
references, and only then removes an unreferenced local copy. It never deletes
unregistered files by guessing. Already-missing historical originals are counted
as `legacy_missing` and retain their original metadata, without invented objects
or aliases; they do not block migration of files that still exist. Historical Markdown bodies are not rewritten;
alias metadata maps them to current media URLs, and old bookmarks resolve via the
configured app origin and current membership checks. Alias tombstones prevent a
removed record from falling back to an old local file.

An independent bounded cleanup pass runs every ten minutes. Drafts older than
24 hours are removed only when they have no issue/comment/message/task/frozen
context or other recognized published reference. Current avatars and published
content are protected. Object-deletion failures remain in a retry ledger. Cleanup
never lists the bucket or touches backup/export namespaces. Existing source and
channel object-intent reconcilers keep their original lifecycle behavior.

Operator-only modes use the existing API binary without starting an HTTP server:
`MULTICA_ATTACHMENT_MAINTENANCE=verify`, `migrate` or `cleanup`. Verify uses a
non-sensitive unique object, checks authenticated bytes and anonymous denial,
and deletes the sample. Run with the intended protected environment and database.
