package lockfile

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestAcquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := Acquire(t.Context(), path, 0)
	if err != nil {
		t.Fatal(err)
	}
	var held HeldError
	if _, err := Acquire(t.Context(), path, 200*time.Millisecond); !errors.As(err, &held) || held.PID != os.Getpid() || !errors.Is(err, ErrHeld) {
		t.Fatalf("second acquire: %v", err)
	}
	unlock()
	unlock, err = Acquire(t.Context(), path, 0)
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	unlock()
}

func TestAcquireWaits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	unlock, _ := Acquire(t.Context(), path, 0)
	time.AfterFunc(200*time.Millisecond, unlock)
	if u, err := Acquire(t.Context(), path, 5*time.Second); err != nil {
		t.Fatalf("waiting acquire: %v", err)
	} else {
		u()
	}
}

// A lock left by a process that died, pid and all, is free at once.
func TestAcquireLeftover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	os.WriteFile(path, []byte(strconv.Itoa(1<<30)), 0o600)
	u, err := Acquire(t.Context(), path, 0)
	if err != nil {
		t.Fatal(err)
	}
	u()
}

// The OS drops the lock when its holder is killed, with no stale-lock guessing.
func TestAcquireKilledHolder(t *testing.T) {
	if os.Getenv("LOCKFILE_HOLD") != "" {
		if _, err := Acquire(context.Background(), os.Getenv("LOCKFILE_HOLD"), 0); err != nil {
			os.Exit(1)
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "x.lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestAcquireKilledHolder$")
	cmd.Env = append(os.Environ(), "LOCKFILE_HOLD="+path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	for i := 0; readPID(path) != cmd.Process.Pid; i++ {
		if i > 100 {
			t.Fatal("child never took the lock")
		}
		time.Sleep(50 * time.Millisecond)
	}
	var held HeldError
	if _, err := Acquire(t.Context(), path, 0); !errors.As(err, &held) || held.PID != cmd.Process.Pid {
		t.Fatalf("while held: %v", err)
	}
	cmd.Process.Kill()
	cmd.Wait()
	u, err := Acquire(t.Context(), path, 2*time.Second)
	if err != nil {
		t.Fatalf("after kill: %v", err)
	}
	u()
}

func TestAcquireCanceled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	unlock, _ := Acquire(t.Context(), path, 0)
	defer unlock()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Acquire(ctx, path, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}
