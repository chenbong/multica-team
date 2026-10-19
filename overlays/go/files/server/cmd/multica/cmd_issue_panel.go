package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/spf13/cobra"
)

func init() { issueCmd.AddCommand(newIssuePanelCommand()) }

func newIssuePanelCommand() *cobra.Command {
	panel := &cobra.Command{Use: "panel", Short: "Add a page to the current issue panel from a running agent"}
	add := &cobra.Command{Use: "add", Args: cobra.NoArgs, Short: "Pin a named HTTP(S) page to the current run's issue", RunE: func(cmd *cobra.Command, _ []string) error {
		name, _ := cmd.Flags().GetString("name")
		href, _ := cmd.Flags().GetString("url")
		name, href = strings.TrimSpace(name), strings.TrimSpace(href)
		if name == "" || href == "" {
			return fmt.Errorf("--name and --url must be non-empty")
		}
		if !strings.HasPrefix(resolveToken(cmd), "mat_") {
			return fmt.Errorf("issue panel add requires the current issue run's task token")
		}
		client, err := newAPIClient(cmd)
		if err != nil {
			return err
		}
		ctx, cancel := cli.APIContext(context.Background())
		defer cancel()
		var result map[string]any
		if err = client.PostJSON(ctx, "/api/agent/issue-web-panels", map[string]string{"name": name, "url": href}, &result); err != nil {
			return err
		}
		return cli.PrintJSON(cmd.OutOrStdout(), result)
	}}
	add.Long = "Adds only to the authenticated running agent's current issue. No issue ID or workspace target is accepted. An existing identical URL is returned unchanged. Workspace panels and editing/removal remain human-only."
	add.Flags().String("name", "", "Display name of the page")
	add.Flags().String("url", "", "Exact HTTP(S) page URL, including query and fragment")
	_ = add.MarkFlagRequired("name")
	_ = add.MarkFlagRequired("url")
	panel.AddCommand(add)
	return panel
}
