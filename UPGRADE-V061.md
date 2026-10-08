# v0.6.1 overlay upgrade

The clean upstream submodule is pinned to official `v0.6.1`, commit
`2ea01ae4ef55de4310b99af192d2dbd367832883`. CLI releases from this repository
include the daemon and the custom overlays. Do not replace them with unmodified
upstream binaries if the deployment uses these customizations.

## Preserved behavior

- Personal, workspace and team Skills, usage statistics, scope changes and downloads.
- User-owned runtime profiles, deleting offline computers and safe unbinding.
- DUCC credential preparation, installation/path discovery and registration retry.
- DUCC/DUCX command shims; DUCX uses its configured provider and the model selected by the server, while other Codex runtimes retain upstream behavior.
- Combined token-based computer setup, proxy variable reuse and model preparation.
- InfoFlow chat/explicit-issue routing, attachments, reactions and review/blocked notifications.
- Optional login branding, direct login entry, mobile IP link and legacy-browser adaptations.

## Build and release

```sh
git submodule update --init --recursive
python3 overlays/prepare.py
python3 overlays/prepare.py --check
python3 scripts/v061-web-patch-test.py
```

The Go build uses the version from `multica/server/go.mod` and 48 source overlays.
The frontend uses a separate staging tree and checked patches rather than editing
the submodule. Optional branding remains deployment configuration.

The `Build daemon` workflow compiles macOS, Linux and Windows packages for amd64
and arm64. A `v*.*.*` tag publishes the archives and `checksums.txt` only after
the workflow's checks and build matrix pass. The installer defaults to this
repository's latest release; `MULTICA_VERSION` can pin an explicit tag.

Publishing does not require immediately replacing existing client daemons.
Self-hosted daemons disable periodic download/update by default unless explicitly
enabled. Replacing a binary can be a separate auto-reload trigger in newer
versions: check active tasks and keep a rollback binary before installing.
Wait for active tasks to finish before a controlled stop/install/start, and reuse
the same profile so daemon identity and runtime bindings are preserved.

The embedded updater reads this fork's release metadata and does not fall back
to the upstream repository. Official Homebrew updates are refused because that
formula would replace the customized binary. Use this repository's installer
for a Homebrew-managed location instead. Explicit API/artifact mirror settings
remain supported; overlay builds retain the upstream auto-poll opt-out behavior.

## Server data migration

Back up the database, private configuration/encryption keys, profiles, uploads,
Bridge bindings/session/notification state and current application builds before
upgrading. Do not commit any of those artifacts to this repository.

Use the native upstream migrator against a restored staging database first.
Migration keys are full filename stems, not numeric prefixes. Keep custom root
`migrations/` separate from `multica/server/migrations`; do not insert synthetic
upstream ledger entries or renumber already applied migrations. An existing
upgraded database has 589 upstream ledger entries, including native conditional
skips. A second `migrate up` must apply no additional versions.

When restoring without ownership metadata, explicitly create the database with
the application owner and use `pg_restore --role=<application-role> --no-owner
--no-privileges --exit-on-error`. Setting only the database owner does not change
the owner of restored tables. Verify table/sequence/function ownership, app
permissions, authenticated API access and protected data before activation.

Preserve the old database and builds for rollback. Roll back by switching
application/database pointers, not by running down migrations against live data.
Back up and reconcile any writes after activation before reverting to a frozen
old database. Backup scheduling remains an explicit deployment choice.

## Compatibility validation and limits

The upgrade was checked with Go compile/tests, selected PostgreSQL-backed runtime,
credential and Skill cases, frontend typechecks/tests/builds, Bridge tests and
authenticated browser/API checks. The workspace-scoped plugin upsert conflict
target includes the custom partial-index predicate.

Built-in review/blocked status keys continue to work with InfoFlow notifications.
If a deployment has custom statuses formerly categorized as review/blocked,
capture their mapping before the upstream lifecycle-category migration.

Actual old Android WebView, iPhone and Windows execution are separate validation
targets. JavaScript missing-built-in tests on a modern browser are not old-engine
proof. Chrome 97 lacks CSS custom highlights, subgrid and container queries;
list alignment/visibility and local-search highlighting need device validation.
The default DUCC installer is POSIX; Windows DUCC installation needs a compatible
deployment-supplied installer.
