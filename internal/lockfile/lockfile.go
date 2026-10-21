// Package lockfile is a cross-process lock that a crashed holder never leaves stuck: the OS lock
// goes away with the process that held it.
package lockfile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ErrHeld means another live process holds the lock.
var ErrHeld = errors.New("lock held")

// HeldError names the process that holds a lock; PID is 0 when it cannot be read.
type HeldError struct{ PID int }

func (e HeldError) Error() string   { return fmt.Sprintf("lock held by pid %d", e.PID) }
func (e HeldError) Is(t error) bool { return t == ErrHeld }

// errBusy is what tryLock returns while another process holds the lock.
var errBusy = errors.New("busy")

// Acquire takes the lock at path, waiting up to wait for its holder to let go. The returned func
// releases it. The file stays behind, holding the last owner's pid, so a waiter can name it.
func Acquire(ctx context.Context, path string, wait time.Duration) (func(), error) {
	deadline := time.Now().Add(wait)
	for {
		f, err := tryLock(path)
		if err == nil {
			// The pid only names the holder in messages; the OS lock is what excludes.
			if err := f.Truncate(0); err == nil {
				_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
			}
			return func() { _ = f.Close() }, nil
		}
		if !errors.Is(err, errBusy) {
			return nil, err
		}
		if !time.Now().Before(deadline) {
			return nil, HeldError{readPID(path)}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func readPID(path string) int {
	b, _ := os.ReadFile(path)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}
