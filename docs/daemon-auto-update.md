# Minimal Linux daemon auto-update

## Scope

An opted-in Linux daemon periodically queries GitHub Releases for
`chenbong/multica-team`, not the upstream Multica repository. Only numeric fork
releases (`v<major>.<minor>.<patch>-overlay.<revision>`) are automatically ordered.
Branch artifacts use `-overlay.dev.<commit>` and remain ineligible. Plain upstream
versions are not automatically converted to the fork.

There is no new server release controller, database migration, or automatic
fleet rollout. Old clients need one explicit bootstrap
upgrade before they have this protocol. Both old and future release packages
must be distinguished: the candidate must advertise startup-confirmation protocol
1 through the offline `multica daemon update-capabilities` command, with its
exact release version, OS and architecture. Packages without that command are
rejected before replacing the executable.

## Activation

Self-hosted automatic updates remain opt-in. Set these in the **client daemon's
startup environment**, not just the API environment:

```ini
MULTICA_DAEMON_AUTO_UPDATE=true
MULTICA_DAEMON_AUTO_UPDATE_INTERVAL=1h
```

Existing `--no-auto-update` / `disable_auto_update` opt-outs remain effective.
Configuration, credentials and task directories are not rewritten. No installed
daemon is upgraded or restarted by developing/building this change. Changing a
running process's startup environment requires an operator-controlled restart;
the API cannot silently turn the policy on.

The first check retains the existing two-minute startup delay; subsequent checks
use the configured interval (six hours by default). Running tasks or in-flight
claims skip the attempt until another poll, without draining or killing work.
The exact fork release archive and `checksums.txt` must be reachable from the
client. Metadata/download failures resume ordinary work; there is no fallback to
official release assets. Downloads retain SHA-256 verification. This minimal
design trusts the repository's release publisher and does not claim independent
signed metadata or a complete TUF implementation.

## Supported launch modes

- Linux standalone CLI daemon, including a non-PID-1 daemon inside a container.
- Writable installation directory; only one daemon/profile may use that
  installation for automatic updates. Shared Linux file locks and a legacy
  process check refuse known shared installations instead of guessing whether
  another profile is idle.
- Containers whose daemon is PID 1, systemd/supervisor-managed processes,
  Desktop-managed binaries and Homebrew installs are not automatically replaced.
  Use the existing launcher's update mechanism for these cases. Non-Linux fork
  automatic updates are not enabled by this change.
- Read-only installations keep working but cannot automatically install an
  upgrade. Normal daemon startup logs the limitation rather than modifying
  permissions or invoking sudo.

Read-only inspection of the current deployment host found API/Web/Bridge and
PostgreSQL containers, with no client daemon process. That does **not** establish
the launch mode of clients on other machines. Eligibility is checked locally
on each daemon before replacement. No existing client container was reconfigured.

## Handoff and rollback

1. The daemon holds its existing task-claim barrier and acquires an exclusive
   installation lease. New daemons hold shared leases; another running profile
   prevents an unattended replacement.
2. Download and verify the fork archive, then run the candidate's offline
   capability/version probe. Save the previous executable and an fsynced local
   transaction before atomically replacing the executable.
3. The old daemon shuts down normally but its old process stays alive as a
   temporary guardian. It starts the candidate with inherited lock and control
   pipes. The candidate cannot claim tasks or start another updater until the
   guardian commits startup. Garbage collection also waits for confirmation;
   failed startup closes its health listener/context before recovery starts.
4. The candidate completes its normal initialization and reports readiness over
   the inherited pipe. The guardian checks its actual PID, profile, version,
   ready status and zero active tasks through loopback health checks. Startup
   has a 60-second deadline and a five-second healthy observation window.
5. On success, persist confirmation and send the commit signal. The new daemon
   releases its startup task barrier. Only then is a server-requested upgrade
   reported successful; periodic updates log their result locally.
6. On a spawn error, early crash, readiness failure, timeout or setup failure,
   terminate only the owned probation process group, verify and restore the
   previous executable, and restart it with the original arguments/environment.
   Quarantine the failed release so periodic checks do not repeatedly install it.
   A different release can be attempted later. An explicit stop signal restores
   the previous executable but does not resurrect a daemon the user is stopping.

State lives in `<executable>.teammesh-update/` (0700): `previous`, `attempt.json`,
and `blocked.json`; a separate installation lock ends in `.teammesh-update.lock`.
Metadata contains only versions, status and digests, not credentials. A changed
external executable or corrupt backup is not overwritten as a purported rollback.
The previous executable must be able to use the unchanged client state: this
feature introduces no client configuration migrations or task-file cleanup.

## Boundaries

This is **startup-failure rollback**, not rollback of provider/model failures
after task execution has begun. After commit, the guardian must not kill the new
daemon on a later health failure because it may already own tasks. A temporary
API/network outage is not treated as proof that the new program is corrupt;
startup may fail safely and restore the old program, without modifying server
data or claiming any new task first. When the API is unreachable or reports
unready during that failure, restore without permanently quarantining the
release, allowing a later periodic retry.

It is not a general power-loss/PID-1 supervisor. If the guardian itself is killed
or the host loses power, incomplete transaction metadata remains. A later normal
startup restores a verified previous executable under the installation lock and
asks for another start. If the program cannot execute at all, recovery requires
an external launcher/operator. The first version deliberately does not install a
new OS service, replace a container image, run arbitrary remotely supplied
scripts, change user permissions, or touch running tasks.

## Verification

Go overlay preparation and checks leave the upstream submodule untouched.
Focused tests use fake GitHub HTTP servers and temporary executable/process
fixtures only. They cover fork revision ordering, development-build exclusion,
checksum rejection, busy/in-flight claim protection, installation locks, PID-1
and managed-launcher rejection, protocol/version mismatch, backup corruption,
external replacement refusal, startup success/crash/timeout/identity mismatch,
setup failure, PID ownership, failed-version quarantine, and unchanged user
configuration/task files. Actual PID-1 rejection is exercised separately by
running the test as an isolated Docker entrypoint. No real agent CLI, production
login credential or user task is used.
