package main

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/spf13/cobra"
)

func init() {
	parent := &cobra.Command{Use: "tmesh-workspace", Short: "Prepare an accessible Team Mesh workspace", SilenceUsage: true, SilenceErrors: true}
	parent.GroupID = groupRuntime
	parent.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return machinePrintResult(cmd, map[string]any{"completed_steps": []string{}}, err, "")
	})
	ensure := &cobra.Command{Use: "ensure", Short: "Create or reuse a workspace by display name", Args: machineNoArgs, RunE: runTmeshWorkspaceEnsure}
	ensure.Flags().String("name", "", "Exact workspace display name")
	ensure.Flags().Bool("set-default-if-unset", false, "Set this profile's default only if it has none")
	parent.AddCommand(ensure)
	rootCmd.AddCommand(parent)
}

func machineEnsureWorkspace(ctx context.Context, client *cli.APIClient, name string) (map[string]any, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("--name is required")
	}
	// This is a user-scoped endpoint and must not inherit a workspace header.
	copyClient := *client
	copyClient.WorkspaceID = ""
	var result map[string]any
	if err := copyClient.PostJSON(ctx, "/api/tmesh/workspaces/ensure", map[string]string{"name": name}, &result); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(strVal(result, "workspace_id")); err != nil {
		return nil, errors.New("server returned an invalid workspace ID")
	}
	return result, nil
}

func runTmeshWorkspaceEnsure(cmd *cobra.Command, _ []string) (err error) {
	result := map[string]any{"completed_steps": []string{}}
	token := ""
	defer func() { err = machinePrintResult(cmd, result, err, token) }()
	if err = requireHumanLocalCommand("tmesh-workspace ensure"); err != nil {
		return err
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	token = client.Token
	name, _ := cmd.Flags().GetString("name")
	workspace, err := machineEnsureWorkspace(cmd.Context(), client, name)
	if err != nil {
		return err
	}
	for key, value := range workspace {
		result[key] = value
	}
	machineCompleted(result, "workspace_ready")
	setDefault, _ := cmd.Flags().GetBool("set-default-if-unset")
	if setDefault {
		if err = machinePrepareProfile(resolveProfile(cmd), strVal(workspace, "workspace_id")); err != nil {
			return err
		}
		machineCompleted(result, "profile_prepared")
	}
	return nil
}
