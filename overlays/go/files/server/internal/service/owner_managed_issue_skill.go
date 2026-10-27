package service

const ownerManagedIssuesSkillReference = `# Read issues administered by your agent owner

Use an existing Python 3 interpreter (3.4 or later); an absolute interpreter
path is fine when python3 is not on PATH. No extra packages are needed.

When the user's work requires context from another task or workspace, use the
current live run credential. The server resolves the executing agent's owner;
this scope does not follow the person who invoked the shared agent. It includes
only tasks whose current administrator list includes that owner and whose
workspace the owner still belongs to. Workspace ownership alone is insufficient.
This delegated access is available to every agent, including shared-agent runs.
Read only content needed for the requested work.

    python3 scripts/team-mesh-tools.py issues list --query "deployment"
    python3 scripts/team-mesh-tools.py issues list --after LAST_UUID
    python3 scripts/team-mesh-tools.py issues get --id ISSUE_UUID
    python3 scripts/team-mesh-tools.py issues get --id ISSUE_UUID --section comments
    python3 scripts/team-mesh-tools.py issues get --id ISSUE_UUID --section timeline
    python3 scripts/team-mesh-tools.py issues get --id ISSUE_UUID --section runs
    python3 scripts/team-mesh-tools.py issues get --id ISSUE_UUID --section panels
    python3 scripts/team-mesh-tools.py issues get --id ISSUE_UUID --section attachments
    python3 scripts/team-mesh-tools.py issues get --id ISSUE_UUID --section properties
    python3 scripts/team-mesh-tools.py issues get --id ISSUE_UUID --section administrators
    python3 scripts/team-mesh-tools.py issues messages --id ISSUE_UUID --run-id RUN_UUID --since 100
    python3 scripts/team-mesh-tools.py issues attachment --id ISSUE_UUID --attachment-id ATTACHMENT_UUID

List returns at most 100 issues plus next_after. Use a UUID returned by the list;
identifiers may collide across workspaces. Comments and timeline default to a
50-entry page; use --before or --after with the native endpoint cursor to page.
Attachment metadata resolves fresh download URLs. A live authenticated binary
read is also available at /api/agent/managed-issues/ISSUE_UUID/attachments/ATTACHMENT_UUID/download.
Only use attachment/run IDs attached to the authorized issue. Private chats keep
their own permissions; task administration does not grant unrelated chat access.

These dedicated endpoints are GET-only. Do not change workspace headers on
ordinary endpoints to bypass the task token's workspace. This capability grants
no cross-workspace writes, deletion or administrator management. Do not use a
personal PAT, owner secret or direct database access as a fallback. A 403 means
the run has ended or was revoked; a 404 means this task is not in the current
owner-managed set. Permission changes apply on every request. This capability is minted for new
runs; a run that started before the feature must start a new turn to use it.
Machine ownership, the invoker and the agent owner are separate identities.
`
