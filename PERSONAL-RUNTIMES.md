# Personal runtimes candidate — not deployed

This branch preserves the staged runtime/workspace decoupling experiment on top
of the current `main` baseline (`5c9e909`). Publishing this branch does not deploy
it, run migrations, change production configuration, or update client daemons.
It is **not ready to merge or deploy** until the deletion-contract regression
items below have been reviewed and resolved.

## Intended behavior

- One owner-level runtime profile/configuration is shared across the owner's
  authorized workspaces. Updates propagate to the compatibility registrations.
- Users see and manage only their own computers, including workspace owners and
  admins. The workspace-wide "Only mine" switch is absent on this branch.
- Share an agent using existing invocation permissions, not its host runtime.
  Invoking a shared agent does not grant access to the host owner's computer,
  credentials, agent configuration, or agent-builder workflows.
- Owner-initiated live agent tasks retain the existing assistant/Mika workflow;
  shared callers, completed tasks, and unrelated machine credentials cannot
  borrow the host owner's management rights.
- Personal runtime mutation and secret-reveal actions remain human-owner-only.
- Offline computer deletion plans cover every workspace registration and bound
  agent. Unfinished runs block deletion; task/chat history is retained.

## Storage and compatibility

`personal_runtime_profile` and `personal_runtime` are the authoritative records.
Existing `runtime_profile` and `agent_runtime` rows remain workspace projections
so installed daemons and historical task/agent IDs keep their existing routing.
Legacy workspace URLs are compatibility entry points, not separately editable
configuration copies. Query caches are account-isolated and invalidated across
workspace projections after edits.

The migrations validate ownership and conflicting definitions before importing
legacy rows. Canonical records are locked before their projections; only database
errors that guarantee an aborted transaction are retried. Retired daemon
identities cannot be freshly projected while old aliases drain. Personal records
are excluded from legacy per-workspace offline TTL cleanup.

No CLI/daemon upgrade, OneAPI change, Bridge change, or deployment configuration
is introduced by this branch. The latest main-line fixes remain in its ancestry.

## Migration and rollback boundary

The custom migration manifest is `migrations/467_*` through `476_*`: ten up/down
pairs, separate from the official upstream migration directory and ledger.
Apply each file independently in ascending order; do not wrap the manifest in
one transaction because index builds use `CREATE INDEX CONCURRENTLY`.
Start the candidate API only after all ten stages have completed successfully.

Any future rollout needs separate approval, a fresh backup, active-run checks,
and a rehearsal against a restored staging database. Check conflicting profile
names, mismatched bindings, and index validity before cutover. This branch does
not enable or schedule that rollout.

Rollback files run in descending order and refuse lossy cases, including
unprojected configurations, unresolved retired identities, and legacy name
collisions. Resolve those conditions explicitly. Do not force rollback or
restore an old snapshot over subsequent user writes.

## Validation on 2026-10-12

Revalidated locally with synthetic data in a newly initialized PostgreSQL
cluster; no production database, credentials, or provider calls were used.

- 38 migration/concurrency/rollback tests passed.
- 48 authenticated real-router HTTP assertions passed, including cross-workspace
  synchronization, owner/admin isolation, shared-agent invocation, owner assistant
  creation, and unfinished-task deletion refusal.
- 510 targeted frontend tests and shared-view type checking passed.
- 10 patch composition/idempotence/rollback tests passed.
- API compilation and the production frontend build passed.
- Expanded selected handler regressions: 321 passing cases (including subtests)
  and **11 failing cases**. These failures are not hidden or skipped.

The old deletion tests still expect profile-backed instance deletion to be
refused in favor of automatic workspace cleanup, or construct orphaned profiles
that the new projection-integrity guards reject. The candidate instead enters
personal-runtime deletion and enforces the all-workspace dependency guards.
Reconcile the intended deletion contract and its tests before merging; in
particular, review online-instance deletion rather than simply changing every
expected conflict into a success.

Affected tests in `runtime_delete_guidance_test.go`:

- `TestDeleteAgentRuntime_OfflineProfileInstanceRefusalPointsAtAutoCleanup`
- `TestDeleteAgentRuntime_OnlineProfileInstanceRefusalSaysStopTheDaemon`
- `TestUnbindAgentsAndDeleteRuntime_ProfileInstanceRefusalCarriesGuidance`
- `TestDeleteAgentRuntime_OfflineProfileInstanceWithBoundAgentsDoesNotPromiseCleanup`
- `TestDeleteAgentRuntime_OfflineInstanceHeldByMikaGetsMikasOwnRemedy`
- `TestDeleteAgentRuntime_OfflineInstanceWithUnfinishedTaskDoesNotPromiseCleanup`
- `TestMikaRemedyMatchesWhatMikaCanDo`

Affected tests in `runtime_unbind_delete_test.go`:

- `TestDeleteAgentRuntime_CustomProfileInstanceRefusesDirectDelete`
- `TestDeleteAgentRuntime_OrphanedProfileAllowsDirectDelete`
- `TestUnbindAgentsAndDeleteRuntime_CustomProfileInstanceRefusesDirectDelete`
- `TestUnbindAgentsAndDeleteRuntime_OrphanedProfileAllowsCascade`

## Running isolated checks

Prepare the Go overlay without modifying the pinned upstream checkout:

```sh
git submodule update --init multica
python3 overlays/prepare.py
python3 overlays/prepare.py --check
python3 scripts/v061-web-patch-test.py
```

The migration test deliberately accepts only the disposable database
`runtime_scope_migration_test` on `127.0.0.1:25439`. Create an isolated PostgreSQL
cluster there, migrate an empty fixture database with the native upstream
migrator plus custom migrations through 466 (including the bootstrap-token
migration), and export that fixture with `pg_dump -Fc`. Never use a production
backup for these tests. Then supply the dump explicitly:

```sh
python3 scripts/personal-runtime-migration-test.py --print-manifest
python3 scripts/personal-runtime-migration-test.py --restore --dump /path/to/fixture.dump
```

For the HTTP contract, restore a separate `runtime_scope_http_test` database on
that same isolated cluster and apply all custom migrations through 476. Prepare
an isolated frontend staging tree and install its dependencies (including `pg`).
From `multica/server`, run the opt-in test with absolute paths:

```sh
DATABASE_URL='postgres://postgres@127.0.0.1:25439/runtime_scope_http_test?sslmode=disable' \
PERSONAL_RUNTIME_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:25439/runtime_scope_http_test?sslmode=disable' \
MULTICA_PERSONAL_RUNTIME_HTTP_TEST=1 \
PERSONAL_RUNTIME_TEST_SCRIPT=/path/to/repository/scripts/personal-runtime-http-test.mjs \
TEST_NODE_PROJECT=/path/to/frontend-staging \
go test -overlay=/path/to/repository/overlays/go-overlay.json ./cmd/server \
  -run '^TestPersonalRuntimeHTTPContract$' -count=1 -v
```

The test starts an ephemeral loopback HTTP listener and closes it on exit. It
generates test users/tokens in memory and never connects a real runtime. Always
set `DATABASE_URL` explicitly for other Go integration tests: upstream test
helpers otherwise have a default database address. Shut down the disposable
cluster after testing. Keep dumps, test logs, builds, and private settings out
of commits.
