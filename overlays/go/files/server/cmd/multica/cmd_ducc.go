package main

import (
	"fmt"
	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	command := &cobra.Command{Use: "ducc", Short: "Prepare the current user's ducc and ducx runtimes"}
	command.AddCommand(&cobra.Command{Use: "setup", Short: "Prepare ducc and ducx using your shared login credential", RunE: func(cmd *cobra.Command, args []string) error {
		profile := resolveProfile(cmd)
		cfg, err := cli.LoadCLIConfigForProfile(profile)
		if err != nil {
			return err
		}
		if cfg.Token == "" {
			return fmt.Errorf("log into Multica first")
		}
		id, err := daemon.EnsureDaemonID(profile)
		if err != nil {
			return err
		}
		if err = daemon.PrepareDucc(cmd.Context(), resolveDaemonServerURL(cmd, profile), cfg.Token, id, version, func(message string) { fmt.Fprintln(cmd.OutOrStdout(), message) }); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Runtime preparation finished; credential policy and provider check results are shown above")
		return nil
	}})
	rootCmd.AddCommand(command)
}
