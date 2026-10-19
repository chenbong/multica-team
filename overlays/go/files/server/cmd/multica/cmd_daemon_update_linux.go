//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
)

var updateStartupTimeout = 60 * time.Second
var updateStabilityWindow = 5 * time.Second
var updateHealthInterval = 200 * time.Millisecond

func updateAPIUnavailable(base string) bool {
	if base == "" {
		return false
	}
	normalized, err := daemon.NormalizeServerBaseURL(base)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(strings.TrimRight(normalized, "/") + "/readyz")
	if err != nil {
		return true
	}
	defer response.Body.Close()
	return response.StatusCode >= 500
}

func updateChildHealthy(port, pid int, expectedVersion, profile string) bool {
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/health")
	if err != nil {
		return false
	}
	defer response.Body.Close()
	var health daemon.HealthResponse
	return response.StatusCode == 200 && json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&health) == nil &&
		health.Status == "running" && health.PID == pid && health.Profile == profile &&
		strings.TrimPrefix(health.CLIVersion, "v") == strings.TrimPrefix(expectedVersion, "v") && health.ActiveTaskCount == 0
}

func stopUpdateChild(child *exec.Cmd, exited <-chan error) {
	// This is our own newly-spawned, probation-only process group. It cannot
	// hold agent tasks; never kill a PID obtained from a health/PID file.
	_ = syscall.Kill(-child.Process.Pid, syscall.SIGTERM)
	select {
	case <-exited:
		return
	case <-time.After(3 * time.Second):
	}
	_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
	<-exited
}

func guardedDaemonHandoff(executable string, args []string, cfg daemon.Config, pidPath, logPath string, logger *slog.Logger) (handled bool, result error) {
	lease := cfg.UpdateLease
	if lease == nil {
		return false, nil
	}
	record, err := lease.Pending()
	if err != nil {
		return true, err
	}
	if record == nil {
		return false, nil
	}
	if err := lease.Eligible(); err != nil {
		return true, errors.Join(err, lease.Rollback())
	}
	ctx, cancel := notifyShutdownContext(context.Background())
	defer cancel()
	ctx, deadline := context.WithTimeout(ctx, updateStartupTimeout)
	defer deadline()
	var child *exec.Cmd
	var exited chan error
	started, committed := false, false
	// Cover *all* failures after the old daemon has stopped, including pipe
	// allocation/log-open/spawn failures before a successor exists.
	defer func() {
		if committed {
			return
		}
		if started {
			stopUpdateChild(child, exited)
		}
		if err := lease.RollbackStartup(!updateAPIUnavailable(cfg.ServerBaseURL)); err != nil {
			result = errors.Join(result, err)
			return
		}
		if err := lease.Shared(); err != nil {
			result = errors.Join(result, err)
			return
		}
		if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return
		}
		previous := exec.Command(executable, args...)
		previous.Env = cleanUpdateHandoffEnv(os.Environ())
		previous.Stdout, previous.Stderr = os.Stderr, os.Stderr
		previous.SysProcAttr = daemonSysProcAttr(true)
		if err := previous.Start(); err != nil {
			result = errors.Join(result, fmt.Errorf("previous executable restored but could not restart: %w", err))
			return
		}
		_ = os.WriteFile(pidPath, []byte(strconv.Itoa(previous.Process.Pid)), 0o644)
		_ = previous.Process.Release()
		logger.Error("daemon upgrade rolled back; previous executable restarted", "failed_version", record.Target)
		result = fmt.Errorf("release %s failed startup; previous executable restored and restarted: %w", record.Target, result)
	}()
	controlRead, controlWrite, err := os.Pipe()
	if err != nil {
		return true, err
	}
	defer controlRead.Close()
	defer controlWrite.Close()
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		return true, err
	}
	defer readyRead.Close()
	defer readyWrite.Close()
	logFile, err := openBoundedErrLog(logPath)
	if err != nil {
		return true, err
	}
	defer logFile.Close()
	child = exec.Command(executable, args...)
	child.ExtraFiles = []*os.File{lease.File(), controlRead, readyWrite}
	child.Env = append(cleanUpdateHandoffEnv(os.Environ()), cli.DaemonUpdateHandoffEnv+"=1")
	child.Stdout, child.Stderr = logFile, logFile
	child.SysProcAttr = daemonSysProcAttr(true)
	err = child.Start()
	started = err == nil
	if !started {
		err = errors.New("updated daemon could not be started")
	} else {
		_ = controlRead.Close()
		_ = readyWrite.Close()
		exited = make(chan error, 1)
		go func() { exited <- child.Wait() }()
		ready := make(chan error, 1)
		go func() {
			var message struct {
				Protocol int    `json:"protocol"`
				Version  string `json:"version"`
				PID      int    `json:"pid"`
			}
			err := json.NewDecoder(io.LimitReader(readyRead, 4096)).Decode(&message)
			if err == nil && (message.Protocol != 1 || message.PID != child.Process.Pid || strings.TrimPrefix(message.Version, "v") != record.Target) {
				err = errors.New("successor startup identity does not match the release")
			}
			ready <- err
		}()
		select {
		case err = <-ready:
		case <-ctx.Done():
			err = ctx.Err()
		case exitErr := <-exited:
			exited <- exitErr
			err = fmt.Errorf("updated daemon exited before startup confirmation: %v", exitErr)
		}
		if err == nil {
			ticker := time.NewTicker(updateHealthInterval)
			stable := time.NewTimer(updateStabilityWindow)
			for err == nil {
				if !updateChildHealthy(cfg.HealthPort, child.Process.Pid, record.Target, cfg.Profile) {
					err = errors.New("updated daemon failed local readiness/identity check")
					break
				}
				select {
				case <-ctx.Done():
					err = ctx.Err()
				case exitErr := <-exited:
					exited <- exitErr
					err = fmt.Errorf("updated daemon exited during startup observation: %v", exitErr)
				case <-ticker.C:
					continue
				case <-stable.C:
					ticker.Stop()
					stable.Stop()
					goto observed
				}
			}
			ticker.Stop()
			stable.Stop()
		}
	}
observed:
	if err == nil {
		err = lease.Confirm()
		if err == nil {
			_, err = controlWrite.WriteString("commit\n")
		}
		if err == nil {
			// This is the commit point: the successor may now claim tasks.
			// Do not perform a later health-based rollback across that boundary.
			committed = true
			_ = os.WriteFile(pidPath, []byte(strconv.Itoa(child.Process.Pid)), 0o644)
			logger.Info("daemon upgrade confirmed", "version", record.Target, "pid", child.Process.Pid)
			return true, nil
		}
	}
	return true, err
}
