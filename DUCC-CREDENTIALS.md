# Personal ducc credential provisioning

The profile settings page exposes user-scoped credential metadata and an
opt-in automatic-preparation switch. Computers are deduplicated across the
owner's workspaces; older daemons are shown as requiring an upgrade.

New clients run `multica --profile <name> ducc setup` after Multica login and
before `daemon start`. The command name remains compatible and now prepares
both ducc and ducx when the account's automatic-preparation switch is enabled.
This avoids depending on an already-registered runtime to install its CLI.
The daemon additionally polls once per minute for
manual imports and missing local credentials. Existing valid files are never
overwritten. Automatic preparation defaults to off per user.

Provider files come only from the daemon OS user's
`~/.comate/login-user/<authenticated-email-local-part>`. Directory traversal,
symlinks, oversized files and replacement of existing files are refused.
New files are published without overwriting, with mode 0600 under a 0700
directory. No generic remote path or shell execution endpoint is introduced.

Import uses the source's named ducc `auth status` check and requires its
`api_key_helper` login state. This is a CLI usability check, not independent
JWT signature/account attestation; the authenticated source daemon is trusted
to report its local result. The server encrypts the opaque file using the
existing AES-GCM secretbox and stores no plaintext credential in logs or
browser metadata. Application-level user namespaces prevent one user's
daemon from importing or retrieving another user's record. Task/cloud-node
tokens and browser cookie sessions cannot use the daemon transfer endpoint.

Concurrent imports use a row lock and expected version; manual import also
uses a request identifier. Removing the server copy disables automatic sync
and invalidates pending requests, but does not revoke or delete credentials
already delivered to a computer.

Transport uses the configured Multica API URL. This deployment explicitly
permits internal HTTP, so at-rest encryption does not protect against a
network observer. Use HTTPS for deployments requiring transport secrecy.
Redirects are refused for credential transfers to prevent body forwarding.

The installer entrypoint is the official HTTPS ducc install script. Installation
is bounded by a timeout and serialized per OS home. Linux and macOS are
supported by this preparation implementation; other platforms return a status
error. Automatic updates of an already-installed ducc are not performed.

Interactive setup prints fixed stage messages, with elapsed-time updates every
10 seconds during installation. Installer output is still withheld to avoid
credential leakage. On Unix, installer/auth helpers run in their own process
group and timeout cancellation kills that group; inherited output pipes also
have a bounded wait. Installation is limited to five minutes and authentication
checks to 45 seconds. These progress messages do not run in background sync.

Daemon profile registration uses the same known ducc install directories as
preparation when PATH cannot resolve `ducc`. This matters because setup is a
separate process and cannot export PATH changes into its caller's shell.
Registrations with no usable runtime back off by 15, 30, 60 and then 120 seconds
per workspace; a changed executable/profile payload bypasses the cooldown.
The server no longer broadcasts an empty registration as a successful
connection. The add-computer dialog snapshots existing computers and confirms
a new, online, user-owned computer against the API before showing success.

Database migrations 464–466 add user credential and daemon capability state;
the user and owner/daemon unique indexes are separate concurrent migrations.
Back up the database with `deploy/secrets.env`, whose
`MULTICA_PLUGIN_SECRET_KEY` is required to decrypt the stored file.

New Go files are supplied by the Go overlay into existing upstream package
directories. `overlays/prepare.py` records absent originals with a null source
hash. Build from the physical upstream path (`cd -P multica/server`) so the
overlay's absolute filenames match compiler inputs.

Validation covers cross-user refusal, task-token refusal, no plaintext in
metadata/storage, revoked/stale imports, file preservation, symlink/path
rejection, redirect refusal and malformed frontend responses. Tests use only
fixture executables and an isolated database; real provider tests are separate.

## DUCX preparation (v0.6.1-overlay.2)

After the existing DUCC credential flow succeeds, explicit `ducc setup`:

1. Resolves ducx from PATH or the standard `.baidu-cx/baidu-cx/bin/ducx` and
   `.comate/baidu-cx/bin/ducx` locations. If missing, it runs the official
   `https://baidu-cc-client.bj.bcebos.com/baidu-cx/install.sh` installer.
   Existing binaries are not upgraded or removed.
2. Requires the same shared `.comate/login-user/<server-resolved-user>` file;
   there is no second credential store or credential transfer API.
3. Preserves a configured model in `.baidu-cx/user.json` or native
   `.baidu-cx/config.toml`. Only when neither supplies a model does it run
   `ducx --username <user> config model 'gpt-6-sol'`, then verify the result.
   Malformed, oversized or symlinked configuration fails closed.
4. Runs `ducx --username <user> doctor --json` and checks
   `checks["auth.credentials"].status == "ok"`. Unrelated overall/CDN warnings
   do not fail this check. Missing/unsupported auth reports and command failures
   do. This checks provider authentication readiness, not token validity on a
   remote service or permission to invoke a particular model; no model task runs.

Subprocess output is bounded and never logged. Model/auth helpers have 45-second
limits with Unix process-group cancellation. Installers are bounded at five
minutes with periodic progress. Repeated setup preserves credentials, installed
versions and existing models. Simultaneous DUCX preparations are serialized.
Profile registration also resolves DUCX's install directories without requiring
the parent shell to run `source` or modify PATH.

Continuous background credential synchronization retains its existing behavior;
it does not repeatedly install DUCX, rewrite models or run its diagnostic. Run
the explicit preparation command on each new computer. DUCX stages and failures
are shown in CLI output; the current profile-settings page still reports the
shared DUCC credential metadata, not a new per-provider dashboard.

The platform's explicitly selected agent model still takes precedence over the
CLI default. Publishing a new release does not restart installed daemons or
retroactively prepare existing computers.
