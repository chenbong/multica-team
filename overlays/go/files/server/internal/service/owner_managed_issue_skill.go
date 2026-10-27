package service

const ownerManagedIssuesSkillReference = `# Read tasks in other workspaces

When the user asks to read or summarize a Team Mesh task in another workspace,
use the ordinary issue commands. Extract the task key from a known Team Mesh
link and use it with the existing CLI:

    multica issue get ISSUE-1
    multica issue comment list ISSUE-1
    multica issue runs ISSUE-1

The server checks the agent owner's current task access on every read,
including shared-agent runs. These commands grant no cross-workspace writes.
A key that matches more than one accessible workspace is ambiguous; use a full
Team Mesh task URL with a CLI version that supports it. If the installed CLI
cannot accept a full URL, report that a CLI update is needed; do not guess.
If the task is unavailable, report that and never use another credential or
bypass access checks. Use native pagination. Read run messages when needed for
the user's request; do not fetch unrelated links.
`
