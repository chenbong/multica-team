# Personal ducc credential provisioning

The profile settings page exposes user-scoped credential metadata and an
opt-in automatic-preparation switch. Computers are deduplicated across the
owner's workspaces; older daemons are shown as requiring an upgrade.

New clients run `multica --profile <name> ducc setup` after Multica login and
before `daemon start`. This avoids depending on an already-registered ducc
runtime to install ducc. The daemon additionally polls once per minute for
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
