package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	daemonCmd.AddCommand(&cobra.Command{Use: "update-capabilities", Hidden: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			protocol := 0
			if runtime.GOOS == "linux" {
				protocol = 1
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
				"protocol": protocol, "version": version, "os": runtime.GOOS, "arch": runtime.GOARCH,
			})
		}})
}

func prepareDaemonUpdateStartup(cfg *daemon.Config, logger *slog.Logger) error {
	handoff := os.Getenv(cli.DaemonUpdateHandoffEnv) != ""
	lease, probation, err := cli.OpenDaemonUpdateLease()
	if err != nil {
		if handoff {
			return err
		}
		// An unwritable install must not prevent an otherwise configured
		// daemon from working. Automatic installation will fail closed.
		logger.Warn("safe automatic update is unavailable", "error", err)
		return nil
	}
	if lease != nil && probation == nil {
		pending, err := lease.Pending()
		if err != nil {
			lease.Close()
			return err
		}
		if pending != nil {
			defer lease.Close()
			if err := lease.Exclusive(); err != nil {
				return err
			}
			if err := lease.Rollback(); err != nil {
				return err
			}
			return fmt.Errorf("recovered an unfinished daemon upgrade; previous executable restored, start the daemon again")
		}
	}
	cfg.UpdateLease, cfg.UpdateProbation = lease, probation
	return nil
}

func removeOwnedDaemonPID(path string, pid int) {
	data, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(data)) == strconv.Itoa(pid) {
		_ = os.Remove(path)
	}
}

func cleanUpdateHandoffEnv(env []string) []string {
	result := make([]string, 0, len(env))
	for _, item := range env {
		if !strings.HasPrefix(item, cli.DaemonUpdateHandoffEnv+"=") {
			result = append(result, item)
		}
	}
	return result
}
