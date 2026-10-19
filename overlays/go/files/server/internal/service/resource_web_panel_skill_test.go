package service

import (
	"strings"
	"testing"
)

func TestResourceWebPanelSkillDeliveredInBothBundlePaths(t *testing.T) {
	svc := &TaskService{}
	for _, skills := range [][]AgentSkillData{svc.BuiltinSkills("", false), svc.AllBuiltinSkills()} {
		found := false
		for _, skill := range skills {
			if skill.Name != PlatformSkillName {
				continue
			}
			if !strings.Contains(skill.Content, "issue panels,") || !strings.Contains(skill.Content, "references/task-panels.md") {
				t.Fatal("platform skill does not advertise task panels")
			}
			for _, file := range skill.Files {
				if file.Path != "references/task-panels.md" {
					continue
				}
				found = true
				for _, marker := range []string{"multica issue panel add", "name and url only", "Workspace panels", "remain manual", "already_exists=true", "Never print", "Do not force it open", "Do not work around it"} {
					if !strings.Contains(file.Content, marker) {
						t.Fatalf("missing operational contract %q", marker)
					}
				}
			}
		}
		if !found {
			t.Fatal("task-panel reference is absent from server-delivered skill bundle")
		}
	}
}
