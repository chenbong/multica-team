package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/cli"
)

type machineSkill struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// Resolve only the skill catalog visible to this account in the selected
// workspace. Never guess between personal and workspace skills with one name.
func machineResolveSkills(ctx context.Context, client *cli.APIClient, requested []string) ([]string, error) {
	if len(requested) == 0 {
		return nil, fmt.Errorf("--skill-list must include at least one skill name or full ID")
	}
	for _, value := range requested {
		if strings.TrimSpace(value) == "" || !validMachineText(value) {
			return nil, fmt.Errorf("--skill-list contains an empty or invalid skill name/ID")
		}
	}
	var catalog []machineSkill
	if err := client.GetJSON(ctx, "/api/skills", &catalog); err != nil {
		return nil, fmt.Errorf("validate --skill-list: %w", err)
	}
	ids := make([]string, 0, len(requested))
	seen := map[string]bool{}
	for _, raw := range requested {
		value := strings.TrimSpace(raw)
		parsed, parseErr := uuid.Parse(value)
		isID := parseErr == nil && strings.EqualFold(parsed.String(), value)
		var matches []machineSkill
		for _, skill := range catalog {
			if (isID && strings.EqualFold(skill.ID, value)) || (!isID && skill.Name == value) {
				matches = append(matches, skill)
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("skill %q was not found or is not accessible in this workspace", value)
		}
		if len(matches) > 1 {
			return nil, fmt.Errorf("skill name %q is ambiguous; use a full skill ID", value)
		}
		id, err := uuid.Parse(matches[0].ID)
		if err != nil {
			return nil, fmt.Errorf("invalid identity returned for skill %q", value)
		}
		if !seen[id.String()] {
			ids = append(ids, id.String())
			seen[id.String()] = true
		}
	}
	return ids, nil
}

// Use the additive API so unrelated assignments and their enabled states stay
// intact. Explicitly requested, previously disabled skills become enabled.
func machineAddSkills(ctx context.Context, client *cli.APIClient, agentID string, ids []string) error {
	if _, err := uuid.Parse(agentID); err != nil {
		return fmt.Errorf("invalid agent identity while attaching skills")
	}
	path := "/api/agents/" + url.PathEscape(agentID) + "/skills"
	var current []machineSkill
	if err := client.GetJSON(ctx, path, &current); err != nil {
		return fmt.Errorf("read existing agent skills: %w", err)
	}
	bound := map[string]bool{}
	for _, skill := range current {
		bound[skill.ID] = skill.Enabled
	}
	missing := []string{}
	for _, id := range ids {
		if _, ok := bound[id]; !ok {
			missing = append(missing, id)
		}
	}
	var result any
	if len(missing) > 0 {
		if err := client.PostJSON(ctx, path+"/add", map[string]any{"skill_ids": missing}, &result); err != nil {
			return fmt.Errorf("add agent skills: %w", err)
		}
	}
	for _, id := range ids {
		if enabled, exists := bound[id]; exists && !enabled {
			if err := client.PutJSON(ctx, path+"/"+url.PathEscape(id)+"/enabled", map[string]any{"enabled": true}, &result); err != nil {
				return fmt.Errorf("enable agent skill %s: %w", id, err)
			}
		}
	}
	return nil
}
