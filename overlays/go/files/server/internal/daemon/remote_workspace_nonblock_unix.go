//go:build !windows

package daemon

import "syscall"

// Avoid hanging the bounded reader pool if a regular file is raced into a FIFO.
const workspaceNonblock = syscall.O_NONBLOCK | syscall.O_NOFOLLOW
