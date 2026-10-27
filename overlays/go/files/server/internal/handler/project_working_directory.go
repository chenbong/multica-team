package handler

import (
	"encoding/json"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func projectDirectoryClaimBlockReason(resources []ProjectResourceData, runtime db.AgentRuntime, capability, projectDirectoryCapability bool) string {
	var projectDirectories []ProjectResourceData
	for _, r := range resources {
		if r.ResourceType == "working_directory" {
			projectDirectories = append(projectDirectories, r)
		}
	}
	if len(projectDirectories) > 0 {
		if len(projectDirectories) != 1 {
			return "Only one project working directory is allowed. Correct the project resources before running."
		}
		if _, err := validateProjectWorkingDirectoryRef(projectDirectories[0].ResourceRef); err != nil {
			return err.Error()
		}
		if !projectDirectoryCapability {
			return "This machine needs a newer Team Mesh daemon to use the project working directory and its missing-directory fallback. Update this machine before retrying."
		}
		if !runtime.DaemonID.Valid || runtime.DaemonID.String == "" {
			return "A project working directory requires a local execution machine."
		}
		return ""
	}
	configured, matched := false, false
	for _, res := range resources {
		if res.ResourceType != "local_directory" {
			continue
		}
		configured = true
		var ref localDirectoryRef
		if json.Unmarshal(res.ResourceRef, &ref) != nil {
			return "Project working-directory configuration is invalid; correct the project resources before running."
		}
		if runtime.DaemonID.Valid && ref.DaemonID == runtime.DaemonID.String {
			matched = true
		}
	}
	if configured && !matched {
		return "This project has a working directory configured, but none for the selected execution machine. Add a working directory for this machine in Project resources, or select a configured machine."
	}
	return worktreeClaimBlockReason(resources, runtime, capability)
}

func projectDirectoryResourcesForRun(resources []ProjectResourceData, leader bool) []ProjectResourceData {
	if leader {
		return nil
	}
	return resources
}
