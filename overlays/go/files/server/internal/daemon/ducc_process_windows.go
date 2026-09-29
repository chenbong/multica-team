//go:build windows

package daemon

import (
	"os/exec"
	"time"
)

func configureDuccProcess(cmd *exec.Cmd) { cmd.WaitDelay = 2 * time.Second }
