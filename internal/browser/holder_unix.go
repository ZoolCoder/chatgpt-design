//go:build !windows

package browser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/zoolcoder/chatgpt-design/internal/lockfile"
)

// holder returns the live Chrome that holds profile, read from the SingletonLock link
// (host-pid) Chrome keeps there while it runs.
func holder(profile string) (int, bool) {
	target, err := os.Readlink(filepath.Join(profile, "SingletonLock"))
	if err != nil {
		return 0, false
	}
	i := strings.LastIndex(target, "-")
	if i < 0 {
		return 0, false
	}
	pid, err := strconv.Atoi(target[i+1:])
	if err != nil || pid <= 0 {
		return 0, false
	}
	// A lock from another host names a pid that means nothing here.
	if h, err := os.Hostname(); err == nil && h != target[:i] {
		return 0, false
	}
	return pid, lockfile.Alive(pid) && isChrome(pid)
}

// isChrome guards against a reused pid: after a crash the lock can name a process that is now
// something else, which must neither block a launch nor be killed by a stop. When ps cannot say,
// it trusts the lock.
var isChrome = func(pid int) bool {
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	return err != nil || strings.Contains(strings.ToLower(string(out)), "chrom")
}

func terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
