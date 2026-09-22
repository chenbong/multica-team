# Team features and deployment configuration

This update includes Skill ZIP downloads, persistent InfoFlow chat with explicit
issue creation, emoji receipts with text fallback, review/blocked notifications,
mobile root login rewriting, and periodic PostgreSQL backups.

The notification delivery contract remains best-effort at-least-once; a crash
between IM delivery and saving state can still produce a duplicate.

## Private deployment settings

Before using the new deployment scripts, set `MULTICA_DATABASE_PASSWORD` in the
ignored `deploy/secrets.env`. For an existing database use its existing password;
this source update does not rotate the live database password. New installations
must choose their own strong password. Do not commit populated configuration.

Set `MULTICA_PGDATA`, `MULTICA_PG_MOUNT`, and `MULTICA_BACKUP_DIR` in the ignored
`deploy/host.env` to match the host. Existing clusters must retain their current
mount path. The generic mount default is `/var/lib/postgresql/multica`.
Backups default to a 30-minute interval and 14-day retention. Database dumps do
not cover Bridge configuration, sessions, chat/notification state, or attachments
stored outside PostgreSQL; preserve those separately.

Publishing these sources does not restart any running service or daemon.
