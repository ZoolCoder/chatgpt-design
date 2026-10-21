//go:build !windows

package browser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestHolder(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Skip(err)
	}
	// Without ps (as in a sandbox) isChrome trusts the lock, so only check it where ps runs.
	if exec.Command("ps", "-p", strconv.Itoa(os.Getpid())).Run() == nil && isChrome(os.Getpid()) {
		t.Error("the test binary passed for Chrome")
	}
	real := isChrome
	isChrome = func(int) bool { return true }
	t.Cleanup(func() { isChrome = real })
	for name, tc := range map[string]struct {
		target string
		alive  bool
	}{
		"live":       {host + "-" + strconv.Itoa(os.Getpid()), true},
		"dead":       {host + "-999999999", false},
		"other host": {"elsewhere.local-" + strconv.Itoa(os.Getpid()), false},
		"garbage":    {"nonsense", false},
	} {
		dir := t.TempDir()
		if err := os.Symlink(tc.target, filepath.Join(dir, "SingletonLock")); err != nil {
			t.Fatal(err)
		}
		if _, alive := holder(dir); alive != tc.alive {
			t.Errorf("%s: alive = %v, want %v", name, alive, tc.alive)
		}
	}
	if _, alive := holder(t.TempDir()); alive {
		t.Error("no lock: alive")
	}
}
