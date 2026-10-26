# Daily private BOS backup

The host systemd timer runs at 03:00 Asia/Shanghai, including a catch-up run
after downtime. It starts a short-lived official Python image pinned by digest,
with the instance bind mount and Docker socket. It does not depend on the API,
Web or Bridge process to capture the database/files. Docker/PostgreSQL/BOS must
be available. The private failure notification additionally needs the Bridge
container to execute the existing InfoFlow SDK.

The existing local backup implementation, if configured, is left untouched.
New units use the team-mesh-daily-bos-backup name.

The host shell runner owns the three-attempt retry boundary, including Docker
launch failures. It invokes one Python backup operation per attempt, waits ten
minutes between failures and sends at most one terminal-failure notice. The
Python CLI without the host-runner attempt flag retains its standalone retry
mode. The host runner and Python worker have separate locks to prevent overlap.

For systemd 219, install regular unit files in /etc/systemd/system. Omit the
calendar timezone suffix only after verifying that /etc/localtime is
Asia/Shanghai. Do not add I/O-priority settings: restricted hosts can reject
them before the backup process starts.

## Contents and safety

- Custom PostgreSQL dump and role globals; check pg_restore --list.
- Uploads, CLI profiles, Bridge state, protected configuration and encryption keys.
- API/CLI binaries, source for API/Web/Bridge and deployment scripts.
- Container specifications, active code links and file SHA-256 manifest.
- No additional archive encryption, as requested. Configuration and archive
  staging are mode 0600/0700. A private-bucket probe is checked before capture;
  each archive is explicitly private and anonymous access must be denied.
- No signed/download links are produced. All archive bytes are downloaded
  again and SHA-256 compared before declaring success or pruning.
- Only exact objects in the instance-owned successful-backup ledger are
  eligible for deletion after 30 days. Images, other prefixes and old manual
  backups are never recursively deleted. Concurrent invocations use a lock.
- Retry twice at ten-minute intervals; terminal failure triggers one private
  operator notice. Notification failure is recorded rather than replacing it
  with a success. The historical run ledger and last result stay local.

Database contents are transactionally consistent within pg_dump. Files are
copied online; this is not a cross-filesystem snapshot. A file-copy error fails
the attempt instead of silently certifying a partial backup. Keep a periodic
isolated restore drill and verify attachment availability after restoring.
Client-local work directories on other machines are outside server backup scope.

## Configuration

Use a root-owned mode-0600 JSON file outside Git with base, docker,
postgres_container, database, bos_cli, bos_config, bucket, endpoint,
prefix, optional ca_file, and notify_command (an argument array).
The BOS profile points to the existing protected credentials. Never put
AK/SK, database URLs or notification credentials in source, units or argv.

See the service and timer templates. Use a validated absolute instance path
and fixed local Python image digest. Mount the Docker executable read-only
at the configured path. Inspect logs and the next timer occurrence after
installation. Do not run the entire platform startup script.

## Restore drill

Download an exact ledger-listed object with the protected BOS profile. Verify
its SHA-256, safely list archive paths, extract into a private dedicated folder,
and verify every manifest file hash. Restore the database dump into a newly
created isolated database; never point the drill at the live database. Verify
key tables, new migrations and attachment files, then remove only the exact
temporary drill database/directory. Keep the real backup intact.
