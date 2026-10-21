//go:build !windows

package lockfile

import (
	"errors"
	"os"
	"syscall"
)

// tryLock opens path and takes an exclusive flock on it without waiting. Closing the file, or the
// process ending, releases it.
func tryLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errBusy
		}
		return nil, err
	}
	return f, nil
}
