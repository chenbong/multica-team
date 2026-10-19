//go:build linux

package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/multica-ai/multica/server/internal/selfexec"
	"golang.org/x/sys/unix"
)

const DaemonUpdateHandoffEnv = "TEAM_MESH_UPDATE_HANDOFF"

// OpenDaemonInstallationLease is also used by isolated installation checks.
// It never resolves credentials or executes the binary at the supplied path.
func OpenDaemonInstallationLease(executable string) (*DaemonUpdateLease, error) {
	path, err := filepath.Abs(executable)
	if err != nil {
		return nil, err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("installation executable is not a regular file")
	}
	return openDaemonLease(path, nil)
}

func openDaemonLease(executable string, inherited *os.File) (*DaemonUpdateLease, error) {
	path := executable + ".teammesh-update.lock"
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if inherited != nil {
		want, err1 := file.Stat()
		got, err2 := inherited.Stat()
		_ = file.Close()
		if err1 != nil || err2 != nil || !os.SameFile(want, got) {
			return nil, errors.New("update guardian passed an invalid installation lock")
		}
		unix.CloseOnExec(int(inherited.Fd()))
		return &DaemonUpdateLease{Executable: executable, file: inherited, exclusive: true}, nil
	}
	if err = unix.Flock(fd, unix.LOCK_SH|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("installation is being upgraded: %w", err)
	}
	return &DaemonUpdateLease{Executable: executable, file: file}, nil
}

// OpenDaemonUpdateLease registers every daemon sharing this executable, even
// when polling is disabled. An upgrade needs an exclusive installation lock.
func OpenDaemonUpdateLease() (*DaemonUpdateLease, *UpdateProbation, error) {
	executable, err := selfexec.Resolve()
	if err != nil {
		return nil, nil, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, nil, err
	}
	if os.Getenv(DaemonUpdateHandoffEnv) != "" {
		_ = os.Unsetenv(DaemonUpdateHandoffEnv)
		if os.Getppid() == 1 {
			return nil, nil, errors.New("update guardian is no longer running")
		}
		lease, err := openDaemonLease(executable, os.NewFile(3, "update-installation-lock"))
		if err != nil {
			return nil, nil, err
		}
		for _, fd := range []int{4, 5} {
			unix.CloseOnExec(fd)
			var stat unix.Stat_t
			if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFIFO {
				lease.Close()
				return nil, nil, errors.New("invalid update guardian pipe")
			}
		}
		if record, err := lease.Pending(); err != nil || record == nil {
			lease.Close()
			return nil, nil, errors.New("update successor has no pending installation")
		}
		return lease, &UpdateProbation{Control: os.NewFile(4, "update-control"), Ready: os.NewFile(5, "update-ready"), Done: make(chan struct{})}, nil
	}
	lease, err := openDaemonLease(executable, nil)
	return lease, nil, err
}

func daemonUpdateLaunchModeError(pid int, getenv func(string) string) error {
	if pid == 1 {
		return errors.New("automatic update is unsupported when daemon is container PID 1; use an external launcher")
	}
	if getenv("INVOCATION_ID") != "" || getenv("JOURNAL_STREAM") != "" || getenv("SUPERVISOR_ENABLED") != "" || getenv("MULTICA_LAUNCHED_BY") == "desktop" {
		return errors.New("automatic update is unsupported for a managed daemon; update through its launcher")
	}
	return nil
}

func (l *DaemonUpdateLease) Eligible() error {
	if l == nil || l.file == nil {
		return errors.New("automatic update requires a writable installation lease")
	}
	if err := daemonUpdateLaunchModeError(os.Getpid(), os.Getenv); err != nil {
		return err
	}
	// Legacy daemons do not hold a shared lease. Refuse known other daemon
	// processes using the same executable instead of assuming they are idle.
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		base := filepath.Join("/proc", entry.Name())
		path, err := os.Readlink(filepath.Join(base, "exe"))
		if err != nil || strings.TrimSuffix(path, " (deleted)") != l.Executable {
			continue
		}
		args, err := os.ReadFile(filepath.Join(base, "cmdline"))
		if err != nil {
			return errors.New("cannot inspect another process sharing the daemon installation")
		}
		seenDaemon, daemonProcess := false, false
		for _, arg := range strings.Split(string(args), "\x00") {
			if seenDaemon && (arg == "start" || arg == "restart") {
				daemonProcess = true
			}
			if arg == "daemon" {
				seenDaemon = true
			}
		}
		if daemonProcess {
			return errors.New("another daemon shares this installation; upgrade it manually when all profiles are idle")
		}
	}
	return nil
}

func (l *DaemonUpdateLease) Exclusive() error {
	if err := unix.Flock(int(l.file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		// A failed flock conversion can drop the shared lock. Restore it
		// before returning to normal task execution.
		_ = l.Shared()
		return fmt.Errorf("another daemon shares this installation: %w", err)
	}
	l.exclusive = true
	return nil
}

func (l *DaemonUpdateLease) Shared() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unix.Flock(int(l.file.Fd()), unix.LOCK_SH)
	if err == nil {
		l.exclusive = false
	}
	return err
}
