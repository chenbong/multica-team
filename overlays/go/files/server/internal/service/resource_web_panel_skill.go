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
		skills[index].Content = strings.Replace(skills[index].Content, "issue and PR contracts,", "personal secrets, issue panels, issue and PR contracts,", 1)
		skills[index].Content = strings.Replace(skills[index].Content, "| `references/issues.md` | Issues:", "| `references/task-panels.md` | Add a web page to the current issue panel when the user requests it; workspace panels stay manual |\n| `references/issues.md` | Issues:", 1)
		skills[index].Files = append(skills[index].Files, AgentSkillFileData{Path: "references/task-panels.md", Content: resourceWebPanelSkillReference}, AgentSkillFileData{Path: "references/personal-secrets.md", Content: personalSecretsSkillReference}, AgentSkillFileData{Path: "scripts/team-mesh-tools.py", Content: platformExpansionTools})
		skills[index].Content += "\nFor personal credentials needed for a user-requested operation, read references/personal-secrets.md. Never disclose secret values.\n"
		skills[index].Files = append(skills[index].Files, AgentSkillFileData{Path: "references/managed-issues.md", Content: ownerManagedIssuesSkillReference})
		skills[index].Content += "\nTo read a task in another workspace, use the ordinary multica issue commands; see references/managed-issues.md. Access follows the agent owner and is read-only. Task deletion and administrator changes require a human.\n"
	}
	return skills
}

const resourceWebPanelSkillReference = `# Manage the current task panel

Use only when the user asks to list, add, change or remove pages in the current
issue panel. Workspace panels remain manual. Chat runs without an issue cannot
use this tool. The server derives the issue from the authenticated live run;
there is no user-selectable issue or workspace parameter.

    python3 scripts/team-mesh-tools.py panels list
    python3 scripts/team-mesh-tools.py panels add --name "Dashboard" --url "https://example.test/results"
    python3 scripts/team-mesh-tools.py panels update --id PANEL_UUID --name "New name" --url "https://example.test/new"
    python3 scripts/team-mesh-tools.py panels delete --id PANEL_UUID

List first to identify a panel's exact UUID. Only operate on the requested
panel; ask if the name matches multiple items. Names are optional; an empty
name uses the URL. Keep queries and fragments exactly as supplied. Never put
credentials in a link. Only HTTP(S) links without embedded credentials pass
validation. The tool has name and url only for add, and adds the panel UUID for
update/delete; it cannot target another task. Added pages are visible to all
issue readers. Existing human-created panels may also be managed on request.

Existing CLI add remains available:
    multica issue panel add --name "Dashboard" --url "https://example.test/results"
An add response with already_exists=true means the identical URL already
exists; do not rename or duplicate it. All operations require the current live
task token. Never print, persist or copy MULTICA_TOKEN. Do not work around it
with personal credentials, daemon credentials or direct database writes.
A 403 indicates the run is unauthorized or has ended; stop and report failure.
A duplicate URL is a 409 conflict on update. Do not force it open or reload the
whole page. After success, briefly report the actual changes; the panel list
updates automatically. Do not claim success on a failed/uncertain request.
`
