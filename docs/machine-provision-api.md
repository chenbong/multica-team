# Delegated machine installation command

The provisioning API prepares the account, joins it to an existing workspace, and returns a Bash script for a
trusted allocation backend to execute on the user's Linux or macOS machine.
The API does not execute the script or create a workspace.

## Request and response

`POST /api/integrations/machine-provision/command`

```http
Authorization: Bearer <dedicated-service-secret>
Content-Type: application/json
```

```json
{"username":"zhangsan","machine_name":"zhangsan-dev","workspace_name":"pdc-loop"}
```

`username` is required; it is the canonical account name, not a full email.
`machine_name`, `workspace_name`, `skill_list` and `system_prompt` are optional. An omitted or blank
`workspace_name` defaults to `pdc-loop`. Other fields, including the former `agent_name`,
`runtime`, `model`, `thinking_level` and `workspace_id`, are rejected.
Browser Origin requests and query parameters are rejected. User PATs and
session cookies do not authorize this operator endpoint.

To include existing skills and custom instructions in the generated agent command:

```json
{"username":"zhangsan","machine_name":"zhangsan-dev","skill_list":["search","report-writing"],"system_prompt":"Read project rules first. Run relevant tests after changes."}
```

`skill_list` is an array of 1–100 exact skill names or full IDs. Names are trimmed
and deduplicated, at most 128 characters each, and cannot contain control
characters. The executing CLI checks existence, ambiguity and the user's
permission; the API does not import skills or query them on the operator's behalf.
`system_prompt` is a UTF-8 string of at most 64 KiB without NUL bytes. It preserves
whitespace and newlines. An empty string explicitly clears custom instructions.
Both options require a nonempty `machine_name`, since this is also the agent name.
Omitted or null options preserve the original script behavior; an explicit empty
skill array is rejected. JSON request bodies are limited to 512 KiB.

The API appends shell-quoted `--skill-list` and `--system-prompt` arguments only to
`tmesh-machine agent`. Commas and quotes in skill names use standard CSV escaping;
prompt text is passed literally, including shell-like text. The CLI must support
the options (System Prompt support is available in `v0.6.1-overlay.12` and later).
As before, the API only returns the script; it does not execute these commands.

A successful response is `200 text/plain` containing the complete script.
It is not a JSON response and contains the user's reusable login token. The
response is marked `no-store`. Keep it out of logs, Git and shared artifacts.

The API resolves one existing workspace by its exact display name, adds the
user as a regular `member` if they are not already present, and issues the
login credential in the same transaction as account preparation. Existing
member roles are retained, including owner/admin. Repeated requests are
idempotent. A missing target returns 404 and an ambiguous name returns 409;
neither failure leaves a new account, token, or membership. Joining an existing
workspace works even when creating new workspaces is disabled. The new
membership is announced to the user's running daemon after commit.

The API does not create a workspace. The returned script uses the selected
name for workspace ensure, machine rename and agent creation. With a machine
name, that name still applies to both the machine and the agent. Without it,
the script still prepares the selected workspace and daemon, but skips rename
and agent creation.

## Script contract

The script runs the same installer command as the Web add-computer flow,
without querying the platform for a release, passing a `--version` argument,
or prechecking CLI help. By default the installer uses GitHub Latest. An
operator can set `MULTICA_VERSION` in the environment when invoking the script
to select a different published version. The selected release must support the
Team Mesh workspace and machine commands; an incompatible release fails when
the affected command is reached.

The script performs non-interactive login first while retaining any existing
profile default, and then ensures the selected workspace using the existing CLI behavior. If the
execution environment contains a nonempty `MULTICA_TOKEN`, subsequent CLI
commands use that value ahead of the saved profile credential. The script does
not set a default workspace if the profile lacks one. It configures the runtime,
sets the ducc default model to `Opus 4.8`, and restarts the profile's daemon on every execution. It then invokes separate machine-rename
and agent commands when a machine name was provided. The agent is bound to the
current machine's online `ducc` runtime.

The script stops on the first failed command and returns its process exit status:
zero means all steps completed, and nonzero means a step failed. Individual CLI commands may print their own results.
There is no single aggregated JSON document or `failed_step` field. Completed
changes remain in place after failure. Output must not contain the login token.
An explicit retry can restart the daemon again.

`tmesh-machine enroll` was removed. Previously generated scripts that invoke
it must be regenerated before they install the new release.

## Identity and permissions

Existing verified email identities take precedence when resolving usernames
containing underscores. Conflicting root-domain and child-domain candidates
return HTTP 409; they are never merged. New valid `v_` contractor names use
the root domain; other supported child-domain names follow the configured
company mapping. Disabled accounts, signup restrictions and workspace-creation
policy still apply.

The standalone CLI workspace operation is unchanged: it reuses one exact-name workspace accessible to the
user, including membership as an ordinary member. Multiple matches fail with
candidate identifiers. If no accessible match exists, the CLI creates a new
workspace owned by that user; an inaccessible workspace with the same name is
not adopted. Concurrent ensures for the same user are serialized. The new
workspace notification allows a running daemon to discover it. The generated
script leaves the profile default unchanged even when none exists.

Machine names apply to the current user's stable daemon identity across
workspaces. The agent command selects only that user's online runtime on that
machine in the target workspace. New agents have workspace visibility; an
existing same-name agent owned by the user retains its ID and configuration.
Archived agents are restored. Another owner's same-name agent and ambiguous
same-name agents fail. A rebind cancels the existing agent's running and queued
tasks first. A database-backed streaming lock rejects simultaneous same-name
agent operations and is released on disconnect.

## Operator configuration

- `MULTICA_MACHINE_PROVISION_TOKEN`: dedicated service credential, at least 32 characters.
- `MULTICA_MACHINE_PROVISION_EMAIL_DOMAIN`: company root domain.
- `MULTICA_WEB_BUILD_CONFIG`: protected manifest providing the installer URL and runtime defaults.
- `MULTICA_PLUGIN_SECRET_KEY`: existing key for encrypted daemon credentials.
- `MULTICA_DAEMON_SERVER_URL` and `MULTICA_APP_URL`: platform addresses used in the generated script.
- `MULTICA_MACHINE_RELEASE_API_URL` (optional): metadata endpoint for the independent release-query API; the generated script does not call it.

An absent or invalid setting fails closed with HTTP 503. Invalid input returns
400; bad service credentials return 401; account policy failures return 403;
ambiguous account identities or workspace names return 409, and missing
workspaces return 404. CLI-time permission or membership changes are still
handled by the existing CLI workspace operation.

The service credential can obtain a full user token for any permitted username,
including privileged users. The credential can also add permitted users to a named existing workspace.
The caller must derive usernames and workspace targets from trusted allocation data. Use a protected transport: internal HTTP does not encrypt the
Bearer or returned user token. Rotating the service credential does not revoke
tokens already issued; revoking a shared user token can affect other machines.

## Release and verification

The API script and the CLI release must be deployed together: an old CLI does
not have the new workspace command, while an old script invokes the removed
`enroll` command. Stage the release, back up the existing server and data, and
coordinate the cutover. Exercise new-user/no-workspace login, existing profile
defaults, concurrent workspace and agent operations, same-name ownership
conflicts, failures and retries against an isolated database and fixture CLIs.
