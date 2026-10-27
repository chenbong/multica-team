package daemon

import (
	"encoding/json"
	"fmt"
	"os"
)

// Resolve once per claimed task, before locking or preparing an environment.
// Keep the native execution path unchanged after this point: a present project
// directory becomes an ordinary in_place assignment; a missing one becomes no
// assignment at all. Later filesystem changes cannot switch this task's choice.
func resolveProjectWorkingDirectory(task Task, daemonID string) (Task, error) {
	var directory *ProjectResourceData
	for i := range task.ProjectResources {
		r := &task.ProjectResources[i]
		if r.ResourceType != "working_directory" {
			continue
		}
		if directory != nil {
			return task, fmt.Errorf("working_directory: multiple project directories; keep only one")
		}
		directory = r
	}
	if directory == nil || task.IsLeaderTask {
		return task, nil
	}
	var ref struct {
		LocalPath string `json:"local_path"`
	}
	if err := json.Unmarshal(directory.ResourceRef, &ref); err != nil {
		return task, fmt.Errorf("working_directory: invalid configuration: %w", err)
	}
	path, err := normalizeLocalPath(ref.LocalPath)
	if err != nil {
		return task, err
	}
	if daemonID == "" {
		return task, fmt.Errorf("working_directory: execution machine identity is missing")
	}
	// Only an absent path falls back. Permissions, files and unsafe roots keep
	// reporting an error instead of silently running in an unintended directory.
	_, statErr := os.Stat(path)
	missing := os.IsNotExist(statErr)
	if statErr != nil && !missing {
		return task, fmt.Errorf("working_directory: inspect path: %w", statErr)
	}
	if !missing {
		if err := validateLocalPath(path); err != nil {
			return task, err
		}
	}
	resources := make([]ProjectResourceData, 0, len(task.ProjectResources))
	for _, r := range task.ProjectResources {
		// A project-wide path takes priority over preserved legacy per-machine
		// resources, including when its absence selects the native default path.
		if r.ResourceType != "working_directory" && r.ResourceType != localDirectoryResourceType {
			resources = append(resources, r)
		}
	}
	if !missing {
		converted := *directory
		converted.ResourceType = localDirectoryResourceType
		converted.ResourceRef, err = json.Marshal(localDirectoryRef{LocalPath: path, DaemonID: daemonID, ExecutionMode: localDirectoryModeInPlace})
		if err != nil {
			return task, err
		}
		resources = append(resources, converted)
	}
	task.ProjectResources = resources
	return task, nil
}
