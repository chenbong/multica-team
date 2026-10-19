//go:build !linux

package main

import (
	"github.com/multica-ai/multica/server/internal/daemon"
	"log/slog"
)

func guardedDaemonHandoff(_ string, _ []string, _ daemon.Config, _, _ string, _ *slog.Logger) (bool, error) {
	return false, nil
}
