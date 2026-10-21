//go:build !windows

package lockfile

import (
	"errors"
	"syscall"
)

// Alive reports whether a process with pid exists.
func Alive(pid int) bool {
	// EPERM still means the process exists; a sandbox may forbid signalling it.
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
