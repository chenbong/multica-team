package service

import "strings"

// Server-delivered skill bundles change with this content. New runs receive
// it through the existing claim/resolve flow without a daemon restart. The
// installed CLI must include the issue panel add command.
func withResourceWebPanelSkill(skills []AgentSkillData) []AgentSkillData {
	for index := range skills {
		if skills[index].Name != PlatformSkillName {
			continue
		}
		skills[index].Content = strings.Replace(skills[index].Content, "issue and PR contracts,", "issue panels, issue and PR contracts,", 1)
		skills[index].Content = strings.Replace(skills[index].Content, "| `references/issues.md` | Issues:", "| `references/task-panels.md` | Add a web page to the current issue panel when the user requests it; workspace panels stay manual |\n| `references/issues.md` | Issues:", 1)
		skills[index].Files = append(skills[index].Files, AgentSkillFileData{Path: "references/task-panels.md", Content: resourceWebPanelSkillReference})
	}
	return skills
}

const resourceWebPanelSkillReference = `# Add a page to the current issue panel

Use this only when the user explicitly asks you to add/pin a page or link to
the current issue's panel (任务面板). Merely encountering a URL is not a request.
Use the exact supplied HTTP(S) URL, preserving its query and fragment. Supply
a short descriptive name; if omitted, infer a name, not a different URL.
Never put credentials into a URL. Added links are shared with issue readers.

## Command

    multica issue panel add --name "Performance dashboard" --url "https://example.test/dashboard#/results"

Replace the example name and URL with the requested values and quote both
as shell arguments. The command sends name and url only. It has no issue-ID
argument or target scope flag. The server derives the target from your
authenticated running issue run. A chat without a current issue cannot use it.
Read the current issue context through the CLI first if it is not already known.

Only the current task token is accepted. Do not obtain or fall back to a
personal token, daemon credential, another run's token, or database access.
Never print, copy into a comment, or persist MULTICA_TOKEN.

The command is add-only. Workspace panels (工作区面板) remain manual; there is
no agent permission to add/edit/remove them, nor to edit/remove issue panels.
If asked to change a workspace panel, explain that it must be done in the UI.

Successful JSON with already_exists=false means added; already_exists=true
means that the exact URL is
already present; reuse that item, without renaming or taking ownership of it.
A later explicit retry of an uncertain result does not create a duplicate
link. On a CLI error, stop and report it; do not claim success or bypass the
boundary. A 403 indicates missing current-issue/live-run authorization; a
409 web_panel_limit requires a human to remove an existing item first.

If the command is unavailable, ask the workspace owner to update the Multica
CLI to a version that includes it. Do not work around it with curl, wget,
Python, personal credentials or direct database writes.

After success, briefly confirm the saved name and link in the conversation.
The task panel list updates automatically. Do not force it open or reload the
whole task page; let the user choose when to open the new entry.
`
