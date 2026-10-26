# Delegated Linux and macOS machine enrollment command

This opt-in server API lets a trusted provisioning backend obtain a Bash
enrollment command for Linux or macOS. The installer detects the OS/architecture.
The API does not execute the command,
contact the development machine, write a CLI profile, or start/restart a daemon.
The calling platform owns execution. No deployment is required for local tests.

## Request and response

`POST /api/integrations/machine-provision/command`

```http
Authorization: Bearer <dedicated-service-secret>
Content-Type: application/json
```

```json
{"username":"zhangsan"}
```

`username` is required. It is a canonical account name, not a display name or
full email. ASCII letters are lowercased; the normalized name must match
`^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$`. Optional fields are `machine_name`,
`agent_name`, `runtime` (defaults to `ducc`), `model`, and `thinking_level`.
Model/thinking overrides require an agent name. Omitted overrides preserve an
existing agent's compatible values; an explicit empty override selects defaults.
Email domain, roles, workspace, command template, and URLs cannot be supplied.
Query parameters
and browser `Origin` requests are rejected. Session cookies, user PATs and actor
headers do not authorize this endpoint.

Success is `200 text/plain; charset=utf-8`: the complete command string, not a
JSON object, with no separate user/token metadata. Responses use `Cache-Control:
no-store, private`, `Pragma: no-cache` and `X-Content-Type-Options: nosniff`.
The command includes the user's long-lived personal access token. Do not print
responses in job logs or expose them to a browser frontend.

Example backend invocation (saves sensitive output rather than logging it):

```sh
umask 077
curl --fail --silent --show-error \
  -X POST "$TEAM_MESH_API_URL/api/integrations/machine-provision/command" \
  -H "Authorization: Bearer $TEAM_MESH_PROVISION_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"username":"zhangsan"}' \
  --output machine-command.sh
```

Keep that file outside source control and shared artifacts; pass it only to the
intended user's machine through the provisioning platform's protected executor.
The example does not run it.

Errors contain a generic JSON error, never a token, SQL error or secret config:
`400` invalid input/browser/query request, `401` wrong service credential, `403`
account/signup/workspace policy prevents provisioning, `405` wrong method, `415`
wrong content type, `503` disabled/missing configuration, `500` transaction failure.
`409` means an ambiguous account or multiple owned username workspaces; no
workspace or account is guessed.
Use POST for retries; account/workspace/token creation is idempotent.

## Operator configuration

Unset or short service secrets keep the endpoint disabled (`503`). Configure only
the API process, using protected configuration, never public Git or Web settings:

| Setting | Purpose |
| --- | --- |
| `MULTICA_MACHINE_PROVISION_TOKEN` | Dedicated random secret of at least 32 characters; generate with a secure random source. |
| `MULTICA_MACHINE_PROVISION_EMAIL_DOMAIN` | One lowercase company email domain, without `@`, wildcard or whitespace. |
| `MULTICA_WEB_BUILD_CONFIG` | Absolute path to the same protected JSON manifest used to build Web. |
| `MULTICA_PLUGIN_SECRET_KEY` | Existing encryption key for shared daemon PATs; do not rotate it for this feature. |
| `MULTICA_DAEMON_SERVER_URL` / `MULTICA_APP_URL` | Existing daemon setup URLs, using the same resolver as Web's `/api/config`. |

The existing URL resolver also supports its current `MULTICA_PUBLIC_URL` and
`FRONTEND_ORIGIN` fallbacks. Host/forwarded headers are not used to generate URLs.
The manifest supplies `MULTICA_CLI_INSTALL_URL`,
`MULTICA_DAEMON_RUNTIME_DEFAULTS` and `MULTICA_DAEMON_RUNTIME_SHIM_DEFAULTS`.
Runtime defaults are normalized using the same rule as the frontend build.
Do not return or log the manifest, encryption key or service secret.

No database migration is required beyond the existing shared daemon-token schema.
The returned script requires a CLI release containing `multica tmesh-machine`; publish
that release before activating the new API. Ordinary website token issuance shares the same transactional
core and keeps its existing behavior.

## Identity, permissions and retries

- Fixed-domain identity is derived as `<normalized username>@<configured domain>`.
- Existing users keep memberships and roles. Reuse only a workspace named exactly
  after the canonical username in which this user is an owner. Otherwise create
  that workspace with an owner membership and the standard issue-status catalog.
  Never adopt another user's same-name workspace. Multiple owned matches fail.
- The display name retains the username. Slugs replace dots/underscores with
  hyphens and append a short suffix on a collision or reserved name.
- New users are initialized for non-interactive onboarding and receive no access
  to other teams.
- Normal signup allowlists, disabled-account checks and the global workspace-
  creation prohibition still apply. This service cannot bypass those policies.
- `tmesh-machine prepare` selects this workspace only for a profile without a default.
  Existing profile defaults are preserved. Machine/agent enrollment explicitly
  selects the username workspace regardless of that default.
- Shared valid daemon PATs are reused, including those issued through the website.
  Expired, revoked or deleted PATs are replaced according to the existing shared
  issuance behavior. Existing unrelated valid tokens are not revoked.
- User creation, workspace/status initialization and encrypted token persistence
  commit together. Failure rolls back partial resources. Database locks serialize
  concurrent requests across API instances. Audit logs contain user IDs and creation
  flags, never command bodies or credentials.

## Security boundary

The service credential can obtain a full personal token for **any** username in
the fixed domain, including users with privileged existing team memberships. It
is not a limited enrollment credential. Store it only in the two backends, rotate
it on suspected disclosure, and keep command output out of logs and telemetry.
The calling backend must derive usernames from its trusted user/allocator data,
not forward an arbitrary frontend username without authorization. Rotating the
service secret blocks future API calls but does not revoke user tokens already
returned. Revoking a shared user PAT can affect other machines using that PAT.

The agreed deployment uses controlled internal HTTP with Bearer authentication
only, without an application IP allowlist. Both credentials and commands are
plaintext on that transport. The application does not make the network private:
operators must keep this path off public gateways/ingress (including Web's API
proxy), or use an existing secure transport. A public path plus a Bearer secret
is not the same as internal-only access. Nothing here changes firewall, proxy,
TLS or live service configuration automatically.

## Local verification

Use the repository's isolated handler-test database, never production:

```sh
python3 overlays/prepare.py
cd multica/server
go test -race -overlay ../../overlays/go-overlay.json ./internal/handler \
  -run '^TestMachineProvision' -count=1 -v
```

The tests cover real loopback HTTP, scoped DB fixtures, retry/concurrency,
rollback, credentials, input/identity spoofing, existing roles, invalid token
replacement and audit redaction. Node 22+ runs the actual TypeScript Web command
helper with fake inputs and compares full output byte-for-byte. Generated
commands are never executed; no installer, real agent CLI or daemon is run.
## Existing underscore accounts

The database can contain verified root-domain names with underscores. For the
company domain, resolve existing root and last-suffix child email candidates
before inferring a new account. Exactly one existing candidate is reused;
two distinct existing candidates return HTTP 409 ambiguous_account, never a
merged identity or a credential for an arbitrarily chosen account. With no
existing match, the agreed new-name rule still applies. Unrecognized new names
with multiple underscores remain invalid, except valid reserved `v_` names.
Website login retains the full
email identity and routes its verification message to the corresponding
InfoFlow username, including existing contractor names.

An unrecognized valid contractor-style `v_` name is created in the root
domain, for example `v_employee` becomes `v_employee@baidu.com`. The entire
username is preserved, including additional underscores. Existing verified
accounts still take precedence, and root/child collisions remain HTTP 409.
