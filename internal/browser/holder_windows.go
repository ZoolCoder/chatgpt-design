package browser

import (
	"errors"
	"os"
	"path/filepath"
)

// holder reports whether a live Chrome holds profile: Chrome keeps its lockfile open while it
// runs, so the file cannot be removed. Windows gives no pid for it.
func holder(profile string) (int, bool) {
	p := filepath.Join(profile, "lockfile")
	if _, err := os.Stat(p); err != nil {
		return 0, false
	}
	return 0, os.Remove(p) != nil
}

func terminate(int) error {
	return errors.New("Chrome has no DevTools port to close it by: close the chatgpt-design Chrome window by hand")
}
