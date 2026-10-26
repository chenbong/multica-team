package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/spf13/cobra"
)

type machineOptions struct {
	MachineName string
	AgentName   string
	Runtime     string
	Model       *string
	Thinking    *string
	Wait        time.Duration
}

func init() {
	command := newMachineCommand()
	command.GroupID = groupRuntime
	rootCmd.AddCommand(command)
}

func newMachineCommand() *cobra.Command {
	parent := &cobra.Command{Use: "tmesh-machine", Short: "Enroll the current computer using your signed-in account", SilenceUsage: true, SilenceErrors: true}
	parent.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return machinePrintResult(cmd, map[string]any{"completed_steps": []string{}}, err, "")
	})
	prepare := &cobra.Command{Use: "prepare", Short: "Set a workspace only when this profile has no default", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
			result := map[string]any{"completed_steps": []string{}}
			defer func() { err = machinePrintResult(cmd, result, err, "") }()
			if err := requireHumanLocalCommand("tmesh-machine prepare"); err != nil {
				return err
			}
			ws := resolveWorkspaceID(cmd)
			if _, err := uuid.Parse(ws); err != nil {
				return errors.New("a full --workspace-id is required")
			}
			profile := resolveProfile(cmd)
			if err = machinePrepareProfile(profile, ws); err == nil {
				result["workspace_id"] = ws
				machineCompleted(result, "profile_prepared")
			}
			return err
		}}
	parent.AddCommand(prepare)
	for _, name := range []string{"enroll", "rename", "agent"} {
		command := &cobra.Command{Use: name, Short: "Rename this computer and/or create or rebind an owned agent", Args: cobra.NoArgs, RunE: runMachineEnroll}
		command.Flags().String("machine-name", "", "Computer display name (OS hostname is unchanged)")
		command.Flags().String("agent-name", "", "Agent name in the explicitly selected workspace")
		command.Flags().String("runtime", "ducc", "Registered runtime command on this computer")
		command.Flags().String("model", "", "Optional model override")
		command.Flags().String("thinking-level", "", "Optional thinking override; empty uses the runtime default")
		command.Flags().Duration("wait", 90*time.Second, "Maximum wait for this computer/runtime to register")
		parent.AddCommand(command)
	}
	return parent
}

func machinePrepareProfile(profile, workspaceID string) error {
	path, err := cli.CLIConfigPathForProfile(profile)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	config := map[string]json.RawMessage{}
	if len(data) > 0 {
		if err = json.Unmarshal(data, &config); err != nil {
			return err
		}
	}
	if config == nil {
		return errors.New("invalid profile configuration")
	}
	var existing string
	if raw, ok := config["workspace_id"]; ok {
		if err = json.Unmarshal(raw, &existing); err != nil {
			return errors.New("invalid profile workspace")
		}
	}
	if existing != "" {
		return nil
	}
	config["workspace_id"], _ = json.Marshal(workspaceID)
	data, err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".machine-profile-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err = temporary.Write(append(data, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

func runMachineEnroll(cmd *cobra.Command, _ []string) (err error) {
	result := map[string]any{"completed_steps": []string{}}
	token := ""
	defer func() { err = machinePrintResult(cmd, result, err, token) }()
	if err := requireHumanLocalCommand("tmesh-machine " + cmd.Name()); err != nil {
		return err
	}
	options := machineOptions{}
	options.MachineName, _ = cmd.Flags().GetString("machine-name")
	options.AgentName, _ = cmd.Flags().GetString("agent-name")
	options.Runtime, _ = cmd.Flags().GetString("runtime")
	options.Wait, _ = cmd.Flags().GetDuration("wait")
	if cmd.Flags().Changed("model") {
		value, _ := cmd.Flags().GetString("model")
		options.Model = &value
	}
	if cmd.Flags().Changed("thinking-level") {
		value, _ := cmd.Flags().GetString("thinking-level")
		options.Thinking = &value
	}
	if cmd.Name() == "rename" && strings.TrimSpace(options.MachineName) == "" {
		return errors.New("--machine-name is required")
	}
	if cmd.Name() == "agent" && strings.TrimSpace(options.AgentName) == "" {
		return errors.New("--agent-name is required")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	token = client.Token
	result["workspace_id"] = client.WorkspaceID
	if _, err = uuid.Parse(client.WorkspaceID); err != nil {
		return errors.New("a full --workspace-id is required")
	}
	// The stable machine ID is shared with the daemon; provider IDs and display
	// names are not machine identifiers. No daemon is started by these commands.
	id, err := daemon.EnsureDaemonID(resolveProfile(cmd))
	if err != nil {
		return err
	}
	result["daemon_id"] = id
	enrolled, err := enrollMachine(cmd.Context(), client, id, options)
	if enrolled != nil {
		result = enrolled
	}
	return err
}

func machineCompleted(result map[string]any, step string) {
	steps, _ := result["completed_steps"].([]string)
	result["completed_steps"] = append(steps, step)
}

func machinePrintResult(cmd *cobra.Command, result map[string]any, err error, token string) error {
	result["success"] = err == nil
	if err != nil {
		message := cli.FormatError(err, false)
		if token != "" {
			message = strings.ReplaceAll(message, token, "<redacted>")
		}
		result["error"] = message
		// Returning a sanitized error keeps the process exit nonzero without
		// exposing an HTTP response or user credential through the root logger.
		err = errors.New(message)
	}
	if outputErr := cli.PrintJSON(cmd.OutOrStdout(), result); outputErr != nil {
		return outputErr
	}
	return err
}

func validMachineText(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= 128 && strings.IndexFunc(value, unicode.IsControl) < 0
}

func machineRuntimeMatches(runtime map[string]any, requested string, profiles map[string]string) bool {
	if profile := strVal(runtime, "profile_id"); profile != "" {
		return profiles[profile] == requested
	}
	return strVal(runtime, "provider") == requested
}

func waitMachineRuntime(ctx context.Context, client *cli.APIClient, machineID, ownerID string, options machineOptions) (map[string]any, error) {
	if options.Wait <= 0 || options.Wait > 10*time.Minute {
		return nil, errors.New("--wait must be between 0 and 10 minutes")
	}
	ctx, cancel := context.WithTimeout(ctx, options.Wait)
	defer cancel()
	for {
		var runtimes []map[string]any
		if err := client.GetJSON(ctx, "/api/runtimes", &runtimes); err != nil {
			return nil, err
		}
		profiles := map[string]string{}
		if options.AgentName != "" {
			var catalog struct {
				RuntimeProfiles []struct {
					ID          string `json:"id"`
					CommandName string `json:"command_name"`
					Enabled     bool   `json:"enabled"`
				} `json:"runtime_profiles"`
			}
			if err := client.GetJSON(ctx, runtimeProfilesPath(client.WorkspaceID), &catalog); err != nil {
				return nil, err
			}
			for _, profile := range catalog.RuntimeProfiles {
				if profile.Enabled {
					profiles[profile.ID] = profile.CommandName
				}
			}
		}
		var matches []map[string]any
		for _, runtime := range runtimes {
			if strVal(runtime, "daemon_id") != machineID || strVal(runtime, "owner_id") != ownerID ||
				strVal(runtime, "workspace_id") != client.WorkspaceID || strVal(runtime, "runtime_mode") != "local" ||
				strVal(runtime, "status") != "online" {
				continue
			}
			if options.AgentName == "" || machineRuntimeMatches(runtime, options.Runtime, profiles) {
				matches = append(matches, runtime)
			}
		}
		if len(matches) > 0 {
			if options.AgentName != "" && len(matches) != 1 {
				return nil, errors.New("multiple matching runtimes on this computer; no changes were made")
			}
			if _, err := uuid.Parse(strVal(matches[0], "id")); err != nil {
				return nil, errors.New("server returned an invalid runtime ID")
			}
			return matches[0], nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("this computer's requested runtime %q is not online; no alternative runtime was selected", options.Runtime)
		case <-time.After(time.Second):
		}
	}
}

func machineAgentPayload(existing map[string]any, runtime map[string]any, options machineOptions) (map[string]any, error) {
	provider := strVal(runtime, "provider")
	model, thinking, tier := strVal(existing, "model"), strVal(existing, "thinking_level"), strVal(existing, "service_tier")
	if options.Model != nil {
		model = *options.Model
	} else if agent.ModelKnownIncompatibleWithProvider(provider, model) {
		model = ""
	}
	if agent.ModelKnownIncompatibleWithProvider(provider, model) {
		return nil, errors.New("specified model is incompatible with the requested runtime")
	}
	if options.Thinking != nil {
		thinking = *options.Thinking
	} else if !agent.IsKnownThinkingValue(provider, thinking) {
		thinking = ""
	}
	if model == "" && model != strVal(existing, "model") && options.Thinking == nil {
		thinking, tier = "", ""
	}
	if thinking != "" && !agent.IsKnownThinkingValue(provider, thinking) {
		return nil, errors.New("specified thinking level is incompatible with the requested runtime")
	}
	if thinking != "" && model == "" && agent.ThinkingLevelRejectedWithoutModel(provider) {
		if options.Thinking != nil {
			return nil, errors.New("an explicit model is required with this thinking level")
		}
		thinking = ""
	}
	if tier != "" && !agent.IsKnownServiceTier(provider, tier) {
		tier = ""
	}
	body := map[string]any{"runtime_id": strVal(runtime, "id")}
	if existing == nil {
		body["name"] = options.AgentName
		body["visibility"] = "private"
	}
	if options.Model != nil || model != strVal(existing, "model") {
		body["model"] = model
	}
	if options.Thinking != nil || thinking != strVal(existing, "thinking_level") {
		body["thinking_level"] = thinking
	}
	if tier != strVal(existing, "service_tier") {
		body["service_tier"] = tier
	}
	return body, nil
}

type machineModelCatalog struct {
	ID        string        `json:"id"`
	Status    string        `json:"status"`
	Supported bool          `json:"supported"`
	Models    []agent.Model `json:"models"`
}

func machineValidateCatalog(ctx context.Context, client *cli.APIClient, runtimeID string, existing, body map[string]any, options machineOptions) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	path := "/api/runtimes/" + url.PathEscape(runtimeID) + "/models"
	var catalog machineModelCatalog
	if err := client.PostJSON(ctx, path, map[string]any{}, &catalog); err != nil {
		return fmt.Errorf("model compatibility query failed before cancelling tasks: %w", err)
	}
	for catalog.Status != "completed" {
		if catalog.Status != "pending" && catalog.Status != "running" {
			return errors.New("model compatibility query failed; retry without cancelling existing tasks")
		}
		if _, err := uuid.Parse(catalog.ID); err != nil {
			return errors.New("invalid model catalog request")
		}
		select {
		case <-ctx.Done():
			return errors.New("model compatibility query timed out before cancelling tasks")
		case <-time.After(time.Second):
		}
		if err := client.GetJSON(ctx, path+"/"+url.PathEscape(catalog.ID), &catalog); err != nil {
			return err
		}
	}
	if !catalog.Supported {
		return nil
	}
	model, thinking, tier := strVal(existing, "model"), strVal(existing, "thinking_level"), strVal(existing, "service_tier")
	if value, ok := body["model"]; ok {
		model, _ = value.(string)
	}
	if value, ok := body["thinking_level"]; ok {
		thinking, _ = value.(string)
	}
	if value, ok := body["service_tier"]; ok {
		tier, _ = value.(string)
	}
	if model == "" {
		if options.Thinking == nil && thinking != "" {
			body["thinking_level"] = ""
		}
		return nil
	}
	var selected *agent.Model
	for i := range catalog.Models {
		if catalog.Models[i].ID == model {
			selected = &catalog.Models[i]
			break
		}
	}
	if selected == nil {
		// Preserve explicit custom model IDs, as the ordinary model picker does.
		// An implicit old model absent from the new catalog falls back to defaults.
		if options.Model == nil {
			body["model"] = ""
			if options.Thinking == nil {
				body["thinking_level"] = ""
			}
			body["service_tier"] = ""
		}
		return nil
	}
	if thinking != "" {
		valid := false
		if selected.Thinking != nil {
			for _, level := range selected.Thinking.SupportedLevels {
				if level.Value == thinking {
					valid = true
				}
			}
		}
		if !valid {
			if options.Thinking != nil {
				return errors.New("specified thinking level is unsupported by this runtime/model")
			}
			body["thinking_level"] = ""
		}
	}
	if tier != "" {
		valid := tier == "default" && selected.SupportsExplicitStandardServiceTier
		for _, level := range selected.ServiceTiers {
			if level.ID == tier {
				valid = true
			}
		}
		if !valid {
			body["service_tier"] = ""
		}
	}
	return nil
}

func enrollMachine(ctx context.Context, client *cli.APIClient, machineID string, options machineOptions) (map[string]any, error) {
	options.MachineName, options.AgentName = strings.TrimSpace(options.MachineName), strings.TrimSpace(options.AgentName)
	result := map[string]any{"workspace_id": client.WorkspaceID, "daemon_id": machineID, "completed_steps": []string{}}
	if !validMachineText(options.MachineName) || !validMachineText(options.AgentName) {
		return nil, errors.New("invalid machine or agent name")
	}
	for _, value := range []*string{options.Model, options.Thinking} {
		if value != nil && (!utf8.ValidString(*value) || len(*value) > 256 || strings.IndexFunc(*value, unicode.IsControl) >= 0) {
			return nil, errors.New("invalid model or thinking level")
		}
	}
	if options.AgentName == "" && (options.Model != nil || options.Thinking != nil) {
		return nil, errors.New("--agent-name is required with model or thinking overrides")
	}
	if options.Runtime == "" {
		options.Runtime = "ducc"
	}
	var me map[string]any
	if err := client.GetJSON(ctx, "/api/me", &me); err != nil {
		return nil, err
	}
	owner := strVal(me, "id")
	if _, err := uuid.Parse(owner); err != nil {
		return nil, errors.New("invalid account identity")
	}
	var members []struct {
		UserID string `json:"user_id"`
		Role   string `json:"role"`
	}
	if err := client.GetJSON(ctx, "/api/workspaces/"+url.PathEscape(client.WorkspaceID)+"/members", &members); err != nil {
		return nil, err
	}
	memberFound := false
	for _, member := range members {
		if member.UserID == owner && (member.Role == "owner" || member.Role == "admin" || member.Role == "member") {
			memberFound = true
		}
	}
	if !memberFound {
		return result, errors.New("the signed-in user is not a member of the selected workspace")
	}
	machineCompleted(result, "account_verified")
	runtime, err := waitMachineRuntime(ctx, client, machineID, owner, options)
	if err != nil {
		return result, err
	}
	result["runtime_id"] = strVal(runtime, "id")
	machineCompleted(result, "runtime_ready")
	if options.MachineName != "" {
		var renamed map[string]any
		if err := client.PatchJSON(ctx, "/api/runtimes/"+url.PathEscape(strVal(runtime, "id")), map[string]any{"custom_name": options.MachineName, "apply_to_machine": true}, &renamed); err != nil {
			return result, err
		}
		result["machine_name"] = options.MachineName
		machineCompleted(result, "machine_renamed")
	}
	if options.AgentName == "" {
		return result, nil
	}
	var catalog []map[string]any
	if err := client.GetJSON(ctx, "/api/agents?include_archived=true", &catalog); err != nil {
		return result, err
	}
	var existing map[string]any
	for _, entry := range catalog {
		if strVal(entry, "name") == options.AgentName {
			if strVal(entry, "owner_id") != owner || strVal(entry, "workspace_id") != client.WorkspaceID {
				return result, errors.New("the same-name agent is not owned by this user in this workspace")
			}
			if existing != nil {
				return result, errors.New("ambiguous same-name agents; no agent was changed")
			}
			existing = entry
		}
	}
	body, err := machineAgentPayload(existing, runtime, options)
	if err != nil {
		return result, err
	}
	if (existing != nil && strVal(existing, "runtime_id") != strVal(runtime, "id")) || options.Model != nil || options.Thinking != nil {
		if err = machineValidateCatalog(ctx, client, strVal(runtime, "id"), existing, body, options); err != nil {
			return result, err
		}
	}
	var saved map[string]any
	if existing == nil {
		if err = client.PostJSON(ctx, "/api/agents", body, &saved); err != nil {
			return result, err
		}
		result["agent_created"] = true
	} else {
		id := strVal(existing, "id")
		if _, err = uuid.Parse(id); err != nil {
			return result, errors.New("invalid agent identity")
		}
		result["agent_id"], result["agent_name"] = id, options.AgentName
		path := "/api/agents/" + url.PathEscape(id)
		var current map[string]any
		if err = client.GetJSON(ctx, path, &current); err != nil {
			return result, err
		}
		for _, key := range []string{"id", "owner_id", "workspace_id", "name", "runtime_id", "updated_at", "archived_at", "model", "thinking_level", "service_tier"} {
			if !reflect.DeepEqual(existing[key], current[key]) {
				return result, errors.New("agent changed during preparation; retry before cancelling or rebinding")
			}
		}
		changed := strVal(existing, "runtime_id") != strVal(runtime, "id")
		for key, value := range body {
			if key != "runtime_id" && !reflect.DeepEqual(value, existing[key]) {
				changed = true
			}
		}
		if existing["archived_at"] != nil && strVal(existing, "archived_at") != "" {
			if err = client.PostJSON(ctx, path+"/restore", nil, &saved); err != nil {
				return result, err
			}
			result["agent_restored"] = true
			machineCompleted(result, "agent_restored")
		}
		if changed {
			var cancelled map[string]any
			if err = client.PostJSON(ctx, path+"/cancel-tasks", nil, &cancelled); err != nil {
				return result, err
			}
			result["cancelled_tasks"] = cancelled["cancelled"]
			machineCompleted(result, "agent_tasks_cancelled")
			if err = client.PutJSON(ctx, path, body, &saved); err != nil {
				return result, err
			}
		} else {
			saved = current
		}
		result["agent_created"] = false
	}
	result["agent_id"], result["agent_name"] = saved["id"], saved["name"]
	machineCompleted(result, "agent_ready")
	return result, nil
}
