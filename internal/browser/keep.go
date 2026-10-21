package browser

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/remote"

	"github.com/zoolcoder/chatgpt-design/internal/lockfile"
)

const (
	// portFile is where Chrome writes its DevTools port when started with --remote-debugging-port=0.
	portFile = "DevToolsActivePort"
	// launchLock marks a run that is deciding whether to start Chrome.
	launchLock = "chatgpt-design.lock"
	// cdpTimeout bounds a DevTools call that a healthy Chrome answers at once.
	cdpTimeout = 15 * time.Second
)

// Open attaches to the profile's long-running Chrome, starting it first if it is not running.
// The returned cancel closes only the new tab, so the browser, the login and the open chat stay warm
// between runs, and Chrome never sees an unclean shutdown.
func Open(ctx context.Context, profile string) (context.Context, context.CancelFunc, error) {
	// Touch first, so an idle watcher never quits Chrome under a run that is attaching.
	touch(profile)
	ws, err := find(ctx, profile)
	if err != nil {
		return nil, nil, err
	}
	actx, cancelAlloc := remote.NewAllocator(ctx, ws, remote.NoModifyURL)
	tctx, cancelTab := chromedp.NewContext(actx)
	// A Chrome can hold its port and still hang; the tab context lives on, so cut it off by hand.
	hung := time.AfterFunc(cdpTimeout, cancelTab)
	if err := chromedp.Do(tctx); err != nil {
		cancelTab()
		cancelAlloc()
		if !hung.Stop() {
			return nil, nil, fmt.Errorf("Chrome holds its DevTools port but does not answer: run `chatgpt-design browser stop`, then retry")
		}
		return nil, nil, fmt.Errorf("attach to Chrome: %w", err)
	}
	hung.Stop()
	return tctx, func() {
		_ = chromedp.Cancel(tctx)
		cancelTab()
		cancelAlloc()
		touch(profile)
	}, nil
}

// find returns the DevTools URL of the profile's Chrome, starting Chrome only when no live process
// holds the profile. A second Chrome on the same profile hands off to the first and opens a stray
// window, so a Chrome that does not answer is reported, never launched again.
func find(ctx context.Context, profile string) (string, error) {
	ws, err := running(profile)
	if err == nil {
		return ws, nil
	}
	if errors.Is(err, os.ErrPermission) {
		return "", sandboxed(err)
	}
	unlock, err := lockLaunch(ctx, profile)
	if err != nil {
		return "", err
	}
	defer unlock()
	if ws, err = running(profile); err == nil {
		return ws, nil
	}
	pid, alive := holder(profile)
	if !alive {
		return launch(ctx, profile)
	}
	// A Chrome that just started may not have written its port yet.
	_ = waitFor(ctx, 5*time.Second, func() (bool, error) {
		ws, err = running(profile)
		return err == nil, nil
	})
	if err == nil {
		return ws, nil
	}
	return "", fmt.Errorf("Chrome (pid %d) holds %s but its DevTools port does not answer (%v): run `chatgpt-design browser stop`, then retry", pid, profile, err)
}

// ErrSandboxed means a command sandbox keeps this process from Chrome's DevTools port.
var ErrSandboxed = errors.New("a command sandbox blocks localhost, run chatgpt-design outside it")

func sandboxed(err error) error {
	return fmt.Errorf("cannot reach the chatgpt-design Chrome on 127.0.0.1 (%v): %w", err, ErrSandboxed)
}

// lockLaunch lets one run at a time decide whether to start Chrome, so commands started together
// never launch two.
func lockLaunch(ctx context.Context, profile string) (func(), error) {
	unlock, err := lockfile.Acquire(ctx, filepath.Join(profile, launchLock), 30*time.Second)
	var held lockfile.HeldError
	switch {
	case err == nil:
		return unlock, nil
	case errors.As(err, &held):
		return nil, fmt.Errorf("another chatgpt-design run (pid %d) is still starting Chrome: retry in a minute", held.PID)
	case errors.Is(err, os.ErrPermission):
		return nil, sandboxed(err)
	}
	return nil, fmt.Errorf("lock %s: %w", profile, err)
}

// Chrome is what Probe found on the profile.
type Chrome struct {
	Running  bool   `json:"running"`
	PID      int    `json:"pid,omitempty"`
	Endpoint string `json:"endpoint,omitempty"` // DevTools URL; empty when Chrome does not answer
}

// Probe looks at the profile's Chrome without starting it. The error says why a Chrome that holds
// the profile cannot be used.
func Probe(profile string) (Chrome, error) {
	ws, err := running(profile)
	pid, alive := holder(profile)
	switch {
	case err == nil:
		return Chrome{Running: true, PID: pid, Endpoint: ws}, nil
	case errors.Is(err, os.ErrPermission):
		return Chrome{Running: alive, PID: pid}, sandboxed(err)
	case alive:
		return Chrome{Running: true, PID: pid}, fmt.Errorf("Chrome (pid %d) holds the profile but its DevTools port does not answer (%v)", pid, err)
	}
	return Chrome{}, nil
}

// Stop quits the profile's Chrome cleanly. It reports false when Chrome was not running.
func Stop(ctx context.Context, profile string) (bool, error) {
	ws, err := running(profile)
	if errors.Is(err, os.ErrPermission) {
		return false, sandboxed(err)
	}
	pid, alive := holder(profile)
	// SIGTERM is a clean quit for Chrome on macOS and Linux and ends it at once; Browser.Close
	// leaves the window up for about ten seconds. Windows has no SIGTERM, so it closes over DevTools.
	if alive && runtime.GOOS != "windows" {
		if err := terminate(pid); err != nil {
			return true, fmt.Errorf("stop Chrome (pid %d): %w", pid, err)
		}
		_ = os.Remove(filepath.Join(profile, portFile))
		return true, waitGone(ctx, profile, pid)
	}
	if err != nil {
		if !alive {
			return false, nil
		}
		// No DevTools port to ask Chrome to close, so end the process holding the profile.
		if err := terminate(pid); err != nil {
			return true, fmt.Errorf("stop Chrome (pid %d): %w", pid, err)
		}
		return true, waitGone(ctx, profile, pid)
	}
	cctx, cancelCall := context.WithTimeout(ctx, cdpTimeout)
	defer cancelCall()
	actx, cancelAlloc := remote.NewAllocator(cctx, ws, remote.NoModifyURL)
	defer cancelAlloc()
	bctx, cancel := chromedp.NewContext(actx)
	defer cancel()
	err = chromedp.Do(bctx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		_, err := cdp.Call(ctx, t, browser.Close, cdp.Empty{})
		return err
	}))
	if err != nil && !strings.Contains(err.Error(), "closed") {
		if cctx.Err() == nil || !alive {
			return true, err
		}
		// It holds the port but does not answer: end the process instead.
		if err := terminate(pid); err != nil {
			return true, fmt.Errorf("stop Chrome (pid %d): %w", pid, err)
		}
	}
	_ = os.Remove(filepath.Join(profile, portFile))
	return true, waitGone(ctx, profile, pid)
}

// waitGone waits for Chrome to exit, so a run right after a stop starts a fresh one. Chrome drops
// its lock before its process ends, so the pid is watched too.
func waitGone(ctx context.Context, profile string, pid int) error {
	err := waitFor(ctx, 15*time.Second, func() (bool, error) {
		_, held := holder(profile)
		return !held && (pid == 0 || !lockfile.Alive(pid)), nil
	})
	if err != nil {
		return errors.New("Chrome is still shutting down: retry in a moment")
	}
	return nil
}

// running returns the DevTools URL of a live Chrome on profile.
func running(profile string) (string, error) {
	b, err := os.ReadFile(filepath.Join(profile, portFile))
	if err != nil {
		return "", err
	}
	lines := strings.SplitN(strings.TrimSpace(string(b)), "\n", 2)
	if len(lines) != 2 {
		return "", errors.New("bad " + portFile)
	}
	addr := net.JoinHostPort("127.0.0.1", lines[0])
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return "", err
	}
	c.Close()
	return "ws://" + addr + lines[1], nil
}

// launch starts Chrome detached from this process, so it outlives the run.
func launch(ctx context.Context, profile string) (string, error) {
	exe, err := ChromePath()
	if err != nil {
		return "", err
	}
	// No live Chrome holds the profile, so any port file is left from a crash.
	_ = os.Remove(filepath.Join(profile, portFile))
	cmd := exec.Command(exe,
		"--user-data-dir="+profile,
		"--remote-debugging-port=0",
		"--remote-debugging-address=127.0.0.1",
		"--no-first-run",
		"--no-default-browser-check",
		"--hide-crash-restore-bubble",
		"--window-size=1440,960",
		"--disable-blink-features=AutomationControlled",
		"--disable-background-timer-throttling",
		"--disable-backgrounding-occluded-windows",
		"--disable-renderer-backgrounding",
		// The anchor tab holds the window: runs open and close their own tabs beside it, and
		// closing the last tab would close the window and make every run open a new one.
		anchorPage(profile),
	)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start Chrome: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if ws, err := running(profile); err == nil {
			return ws, nil
		}
		select {
		case err := <-exited:
			// Chrome hands a second launch on the same profile to the first one and exits.
			if _, alive := holder(profile); alive {
				return "", fmt.Errorf("start Chrome: another Chrome already uses %s without a DevTools port: quit it, or run `chatgpt-design browser stop`", profile)
			}
			return "", fmt.Errorf("start Chrome: it exited at once (%v)", err)
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("start Chrome: no DevTools port after 20s (is another Chrome using %s?)", profile)
}

// ChromePath finds the Chrome binary: CHATGPT_DESIGN_CHROME, then the usual install places.
func ChromePath() (string, error) {
	if p := os.Getenv("CHATGPT_DESIGN_CHROME"); p != "" {
		return p, nil
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	case "windows":
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			// An unset variable would make a path relative to the working directory.
			if base := os.Getenv(env); base != "" {
				candidates = append(candidates, filepath.Join(base, `Google\Chrome\Application\chrome.exe`))
			}
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", errors.New("Chrome not found: install Google Chrome or set CHATGPT_DESIGN_CHROME")
}
