# Basic issue workspace transfer

Implemented on top of `main` (5c9e909), independently of the paused personal
runtime experiment. This is a development candidate, not a production rollout.

## User workflow

Source workspace owners/admins get **Move to workspace** in an issue's
three-dot menu. Choose a destination, review history counts and visibility,
map an incompatible status to the same lifecycle category if needed, and
explicitly confirm clearing an unavailable assignee. A successful transfer
opens the destination issue with its newly allocated number.

Only human sessions/PATs can transfer. The user must still be a source workspace
owner/admin and a destination member when the transaction executes. The target
audience can read the moved issue and history; the confirmation makes that
scope change explicit. The issue UUID remains stable, but the old workspace
URL and human-readable number do not redirect in this version.

## Preserved and excluded data

Descriptions, comments, activity, reactions, terminal task records and
transcripts retain their IDs/content. Workspace-scoped comments/activity/inbox
and reaction rows move together. Historical agent/runtime identities and usage
accounting are not rewritten or moved. Ended-run credentials are revoked;
subscriptions unavailable in the destination are disabled. Active assignee
bindings survive only for member assignees who belong to the destination.

No run, plugin hook, or robot message is launched by the transfer. There is a
dedicated `issue:workspace_changed` cache-invalidation event and an audit entry.
Old runs remain readable but cannot be rerun against their former-workspace
agent. Task insertion/retry and subscription query guards also reject stale
source-workspace writes after a transfer.

The first version refuses, rather than drops, unsupported associations:

- unfinished or chat/automation-linked runs;
- parents, children, duplicates and dependencies;
- projects, labels, pins, PR associations;
- attachments, source-context snapshots, wakeups and supplements;
- custom properties/metadata/context references or triage state;
- historical notifications whose recipients are not destination members.

This covers a standalone cancelled issue with ended runs, comments and
transcripts, such as the PiCoKV migration use case. There is no batch migration,
automatic parent/child migration, attachment migration or old-link aliasing.

## Implementation and concurrency

- `POST /api/issues/{id}/transfer-preview` returns blockers, status choices,
  consequences, counts and a confirmation fingerprint. It performs no writes.
- `POST /api/issues/{id}/transfer` rechecks membership and the fingerprint,
  allocates the target number and moves related data in one transaction.
- No schema migration is required. Do not apply personal-runtime migrations.
- Some legacy relationship writers have no parent-row fence. The commit path
  takes short, NOWAIT write fences on the involved tables, with a five-second
  operation deadline. A busy writer produces a retryable refusal rather than
  a partial transfer. This conservative baseline is intended for infrequent
  administrative moves, not bulk migration; it briefly excludes other writes.
- Task SQL guards add row locking to workspace checks so an enqueue which
  started before a transfer cannot dispatch on the old host after it commits.
  `overlays/prepare.py` materializes checked SQL-constant edits on top of any
  existing full-file overlay; the upstream checkout remains unchanged.
- Unknown related-data cases fail closed. Do not bypass blockers with manual
  database updates as part of this feature.

## Local verification

Use a disposable loopback PostgreSQL cluster and synthetic data. The fixture
schema needs native upstream migrations and this repository's custom migrations
through 466, including the bootstrap-token migration. Do not use production data
or credentials, and always set `DATABASE_URL` explicitly for Go tests.

```sh
python3 overlays/prepare.py
python3 overlays/prepare.py --check
python3 scripts/v061-web-patch-test.py
python3 scripts/issue-transfer-sql-test.py
```

From `multica/server`, set the absolute overlay path and run:

```sh
go test -overlay=/path/to/repository/overlays/go-overlay.json ./internal/handler \
  -run 'TestIssueTransfer|Test.*Subscribe|Test.*Subscriber' -count=1
go test -overlay=/path/to/repository/overlays/go-overlay.json ./internal/service \
  -run 'TestRerun|Test.*Enqueue' -count=1
```

The opt-in `TestIssueTransferAuthenticatedHTTP` uses the real router, generated
fixture PAT, an ephemeral loopback listener, and a database named exactly
`issue_transfer_http_test` on `127.0.0.1`. Enable it with
`MULTICA_ISSUE_TRANSFER_HTTP_TEST=1`. It checks source/target access, comments,
run transcripts and refusal to rerun the old host. It starts no agent process.

Frontend checks run in a separate tree prepared from the pinned submodule,
then overlaid and patched. Use pnpm 10.28.2. Tests cover the confirmation dialog,
blocked cases, assignee consent, API response validation and realtime sync.
On Node 26, use `NODE_OPTIONS=--no-experimental-webstorage` for the jsdom tests
so Node's unset native localStorage does not shadow jsdom's implementation.
The normal production build also installs `apps/web/compat` dependencies and
uses the repository's font mock as described in `deploy/build.sh`.

## Deployment boundary

Validation recorded on 2026-10-12: 50 handler/permission/subscription cases,
39 selected enqueue/rerun service cases, the authenticated HTTP contract,
56 issue-action/dialog frontend cases, 111 shared API/realtime cases,
9 web-patch checks, the SQL guard check and 10 browser-compatibility checks
passed. Views type checking, Go vet, API compilation and the production Web
build passed. One existing Redis-specific enqueue notification test was skipped
because `REDIS_TEST_URL` was not configured. No live browser/device E2E or
production migration was performed for this candidate.

Publishing/deploying is a separate action. Activation requires the API and Web
builds together; old APIs do not implement the preview endpoint. No client
daemon release or restart is needed. Take a fresh database backup before any
future production rollout. A failed transaction rolls back automatically;
moving an issue back after success is a new permission-checked transfer, not
an overwrite of subsequent edits or a whole-database restore.
