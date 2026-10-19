package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// State lives beside the executable, never in an agent's task/config directory.
// It contains paths, versions and digests only: no tokens, environment or tasks.
type DaemonUpdateRecord struct {
	Protocol int    `json:"protocol"`
	Target   string `json:"target"`
	Previous string `json:"previous_sha256"`
	Next     string `json:"next_sha256"`
	Status   string `json:"status"`
}

type DaemonUpdateLease struct {
	Executable string
	file       *os.File
	exclusive  bool
}

func (l *DaemonUpdateLease) File() *os.File { return l.file }
func (l *DaemonUpdateLease) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}
func (l *DaemonUpdateLease) stateDir() string { return l.Executable + ".teammesh-update" }

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func updateStateDir(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("unsafe update state directory: %s", path)
	}
	return nil
}

func atomicUpdateFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".teammesh-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Chmod(mode)
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (l *DaemonUpdateLease) save(record *DaemonUpdateRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return atomicUpdateFile(filepath.Join(l.stateDir(), "attempt.json"), data, 0o600)
}

func (l *DaemonUpdateLease) record() (*DaemonUpdateRecord, error) {
	if l == nil {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(l.stateDir(), "attempt.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record DaemonUpdateRecord
	if err = json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if record.Protocol != 1 || !IsForkReleaseVersion(record.Target) || len(record.Previous) != 64 || len(record.Next) != 64 {
		return nil, fmt.Errorf("invalid daemon update record")
	}
	if record.Status != "pending" && record.Status != "confirmed" && record.Status != "rolled_back" {
		return nil, errors.New("invalid update transaction status")
	}
	return &record, nil
}

func (l *DaemonUpdateLease) Pending() (*DaemonUpdateRecord, error) {
	record, err := l.record()
	if err != nil || record == nil || record.Status != "pending" {
		return nil, err
	}
	return record, nil
}

func (l *DaemonUpdateLease) blocked() (map[string]bool, error) {
	blocked := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(l.stateDir(), "blocked.json"))
	if errors.Is(err, os.ErrNotExist) {
		return blocked, nil
	}
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(data, &blocked)
	return blocked, err
}

func (l *DaemonUpdateLease) Begin(target string) error {
	if runtime.GOOS != "linux" || !IsForkReleaseVersion(target) {
		return fmt.Errorf("automatic daemon updates require Linux and a numeric Team Mesh overlay release")
	}
	if err := l.Eligible(); err != nil {
		return err
	}
	if err := l.Exclusive(); err != nil {
		return err
	}
	fail := func(err error) error { _ = l.Shared(); return err }
	if err := updateStateDir(l.stateDir()); err != nil {
		return fail(err)
	}
	blocked, err := l.blocked()
	if err != nil {
		return fail(err)
	}
	if blocked[strings.TrimPrefix(target, "v")] {
		return fail(fmt.Errorf("release %s previously failed startup; waiting for a different release", target))
	}
	if record, err := l.Pending(); err != nil {
		return fail(err)
	} else if record != nil {
		return fail(fmt.Errorf("an unfinished daemon upgrade needs recovery before another update"))
	}
	return nil
}

func probeDaemonCandidate(path, target string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "daemon", "update-capabilities")
	// The hidden probe must not resolve agents or read a user's login profile.
	cmd.Env = []string{"PATH=/usr/bin:/bin", "DO_NOT_TRACK=1"}
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("candidate has no supported startup-confirmation protocol: %w", err)
	}
	var capability struct {
		Protocol int    `json:"protocol"`
		Version  string `json:"version"`
		OS       string `json:"os"`
		Arch     string `json:"arch"`
	}
	if json.Unmarshal(output, &capability) != nil || capability.Protocol != 1 || capability.OS != runtime.GOOS || capability.Arch != runtime.GOARCH || strings.TrimPrefix(capability.Version, "v") != strings.TrimPrefix(target, "v") {
		return fmt.Errorf("candidate version/platform/startup protocol does not match release %s", target)
	}
	return nil
}

func (l *DaemonUpdateLease) Install(target string, data []byte) error {
	if !l.exclusive {
		return fmt.Errorf("daemon installation is not exclusively locked")
	}
	info, err := os.Stat(l.Executable)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("daemon executable must be a regular file")
	}
	candidate := filepath.Join(l.stateDir(), "candidate")
	if err = atomicUpdateFile(candidate, data, info.Mode().Perm()); err != nil {
		return err
	}
	defer os.Remove(candidate)
	if err = probeDaemonCandidate(candidate, target); err != nil {
		return err
	}
	old, err := os.ReadFile(l.Executable)
	if err != nil {
		return err
	}
	if err = atomicUpdateFile(filepath.Join(l.stateDir(), "previous"), old, info.Mode().Perm()); err != nil {
		return err
	}
	record := &DaemonUpdateRecord{Protocol: 1, Target: strings.TrimPrefix(target, "v"), Previous: digest(old), Next: digest(data), Status: "pending"}
	if err = l.save(record); err != nil {
		return err
	}
	if err = atomicUpdateFile(l.Executable, data, info.Mode().Perm()); err != nil {
		return errors.Join(err, l.Rollback())
	}
	return nil
}

func (l *DaemonUpdateLease) Confirm() error {
	record, err := l.Pending()
	if err != nil || record == nil {
		return errors.Join(err, errors.New("no pending update to confirm"))
	}
	current, err := os.ReadFile(l.Executable)
	if err != nil || digest(current) != record.Next {
		return errors.New("candidate executable changed before confirmation")
	}
	record.Status = "confirmed"
	return l.save(record)
}

// Rollback only restores the verified previous executable. It never touches
// configuration, credentials, workspace data, or other files in the install.
func (l *DaemonUpdateLease) Rollback() error {
	return l.RollbackStartup(true)
}

// An unavailable API during failed initialization is not evidence that the
// release is broken. Restore it safely but allow a later periodic retry.
func (l *DaemonUpdateLease) RollbackStartup(quarantine bool) error {
	record, err := l.record()
	if err != nil || record == nil {
		return err
	}
	if record.Status == "rolled_back" {
		return nil
	}
	if !l.exclusive {
		return errors.New("rollback requires the exclusive installation lease")
	}
	current, err := os.ReadFile(l.Executable)
	if err != nil || (digest(current) != record.Next && digest(current) != record.Previous) {
		return errors.New("installed executable changed outside this update; refusing to overwrite it")
	}
	previousPath := filepath.Join(l.stateDir(), "previous")
	old, err := os.ReadFile(previousPath)
	if err != nil || digest(old) != record.Previous {
		return errors.New("previous executable is unavailable or corrupted; refusing an unsafe rollback")
	}
	info, err := os.Stat(previousPath)
	if err != nil {
		return err
	}
	if err = atomicUpdateFile(l.Executable, old, info.Mode().Perm()); err != nil {
		return err
	}
	if quarantine {
		blocked, err := l.blocked()
		if err != nil {
			return err
		}
		blocked[record.Target] = true
		data, err := json.Marshal(blocked)
		if err != nil {
			return err
		}
		if err = atomicUpdateFile(filepath.Join(l.stateDir(), "blocked.json"), data, 0o600); err != nil {
			return err
		}
	}
	record.Status = "rolled_back"
	return l.save(record)
}

// UpdateProbation keeps an unconfirmed successor from claiming tasks. The
// inherited pipes belong to the still-running old guardian, not the API.
type UpdateProbation struct {
	Control *os.File
	Ready   *os.File
	Done    chan struct{}
}

func (p *UpdateProbation) Confirm(ctx context.Context, version string) error {
	if p == nil {
		return nil
	}
	if err := json.NewEncoder(p.Ready).Encode(map[string]any{"protocol": 1, "version": version, "pid": os.Getpid()}); err != nil {
		return err
	}
	_ = p.Ready.Close()
	answer := make(chan error, 1)
	go func() {
		data := make([]byte, len("commit\n"))
		_, err := io.ReadFull(p.Control, data)
		if err == nil && string(data) != "commit\n" {
			err = errors.New("invalid update guardian confirmation")
		}
		answer <- err
	}()
	timer := time.NewTimer(90 * time.Second)
	defer timer.Stop()
	defer p.Control.Close()
	select {
	case err := <-answer:
		if err != nil {
			return fmt.Errorf("update guardian did not commit startup: %w", err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("update guardian confirmation timed out")
	}
}
