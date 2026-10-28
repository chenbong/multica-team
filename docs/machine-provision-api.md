# Delegated machine installation command

The provisioning API prepares the account and returns a Bash script for a
trusted allocation backend to execute on the user's Linux or macOS machine.
The API does not execute the script or create a workspace.

## Request and response

`POST /api/integrations/machine-provision/command`

```http
Authorization: Bearer <dedicated-service-secret>
Content-Type: application/json
```

```json
{"username":"zhangsan","machine_name":"zhangsan-dev"}
```

`username` is required; it is the canonical account name, not a full email.
`machine_name` is optional. Other fields, including the former `agent_name`,
`runtime`, `model`, `thinking_level` and `workspace_id`, are rejected.
Browser Origin requests and query parameters are rejected. User PATs and
session cookies do not authorize this operator endpoint.

A successful response is `200 text/plain` containing the complete script.
It is not a JSON response and contains the user's reusable login token. The
response is marked `no-store`. Keep it out of logs, Git and shared artifacts.

The account and token can be created during the API request. A workspace is
created only when the script's user-authenticated CLI step executes. With a
machine name, the script uses that name for both the machine and the agent.
Without one, it still prepares the username workspace and daemon but does not
rename a machine or create an agent.

## Script contract

The script installs the latest release and checks for the required CLI
features before proceeding. It prepares the username workspace using the user
credential, performs non-interactive login while retaining any existing
profile default, configures the runtime, sets the ducc default model to
`Opus 4.8`, and restarts the profile's daemon on every execution. It then
invokes separate machine-rename and agent commands when a machine name was
provided. The agent is bound to the current machine's online `ducc` runtime.

Standard output contains one JSON object with `success`, `completed_steps`,
workspace, machine and agent results. On failure it also contains `exit_code`
and `failed_step`; completed changes are retained. Ordinary logs go to stderr.
Neither result channel should contain the login token. An explicit retry can
restart the daemon again.

`tmesh-machine enroll` was removed. Previously generated scripts that invoke
it must be regenerated before they install the new release.

## Identity and permissions

Existing verified email identities take precedence when resolving usernames
containing underscores. Conflicting root-domain and child-domain candidates
return HTTP 409; they are never merged. New valid `v_` contractor names use
the root domain; other supported child-domain names follow the configured
company mapping. Disabled accounts, signup restrictions and workspace-creation
policy still apply.

The CLI workspace operation reuses one exact-name workspace accessible to the
user, including membership as an ordinary member. Multiple matches fail with
candidate identifiers. If no accessible match exists, the CLI creates a new
workspace owned by that user; an inaccessible workspace with the same name is
not adopted. Concurrent ensures for the same user are serialized. The new
workspace notification allows a running daemon to discover it.

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

An absent or invalid setting fails closed with HTTP 503. Invalid input returns
400; bad service credentials return 401; account policy failures return 403;
ambiguous account identities return 409. Workspace policy and name conflicts
are reported later by the CLI operation.

The service credential can obtain a full user token for any permitted username,
including privileged users. The caller must derive usernames from trusted
allocation data. Use a protected transport: internal HTTP does not encrypt the
Bearer or returned user token. Rotating the service credential does not revoke
tokens already issued; revoking a shared user token can affect other machines.

## Release and verification

The API script and the CLI release must be deployed together: an old CLI does
not have the new workspace command, while an old script invokes the removed
`enroll` command. Stage the release, back up the existing server and data, and
coordinate the cutover. Exercise new-user/no-workspace login, existing profile
defaults, concurrent workspace and agent operations, same-name ownership
conflicts, failures and retries against an isolated database and fixture CLIs.
