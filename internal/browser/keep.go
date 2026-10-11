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
)

// portFile is where Chrome writes its DevTools port when started with --remote-debugging-port=0.
const portFile = "DevToolsActivePort"

// Open attaches to the profile's long-running Chrome, starting it first if it is not running.
// The returned cancel closes only the new tab, so the browser, the login and the open chat stay warm
// between runs, and Chrome never sees an unclean shutdown.
func Open(ctx context.Context, profile string) (context.Context, context.CancelFunc, error) {
	ws, err := running(profile)
	if err != nil {
		if ws, err = launch(profile); err != nil {
			return nil, nil, err
		}
	}
	actx, cancelAlloc := remote.NewAllocator(ctx, ws, remote.NoModifyURL)
	tctx, cancelTab := chromedp.NewContext(actx)
	if err := chromedp.Do(tctx); err != nil {
		cancelTab()
		cancelAlloc()
		return nil, nil, fmt.Errorf("attach to Chrome: %w", err)
	}
	return tctx, func() {
		_ = chromedp.Cancel(tctx)
		cancelTab()
		cancelAlloc()
	}, nil
}

// Stop quits the profile's Chrome cleanly. It reports false when Chrome was not running.
func Stop(ctx context.Context, profile string) (bool, error) {
	ws, err := running(profile)
	if err != nil {
		return false, nil
	}
	actx, cancelAlloc := remote.NewAllocator(ctx, ws, remote.NoModifyURL)
	defer cancelAlloc()
	bctx, cancel := chromedp.NewContext(actx)
	defer cancel()
	err = chromedp.Do(bctx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		_, err := cdp.Call(ctx, t, browser.Close, cdp.Empty{})
		return err
	}))
	if err != nil && !strings.Contains(err.Error(), "closed") {
		return true, err
	}
	_ = os.Remove(filepath.Join(profile, portFile))
	return true, nil
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
func launch(profile string) (string, error) {
	exe, err := chromePath()
	if err != nil {
		return "", err
	}
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
		"about:blank",
	)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start Chrome: %w", err)
	}
	go cmd.Wait()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if ws, err := running(profile); err == nil {
			return ws, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", fmt.Errorf("start Chrome: no DevTools port after 20s (is another Chrome using %s?)", profile)
}

func chromePath() (string, error) {
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
		candidates = []string{
			filepath.Join(os.Getenv("ProgramFiles"), `Google\Chrome\Application\chrome.exe`),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), `Google\Chrome\Application\chrome.exe`),
			filepath.Join(os.Getenv("LocalAppData"), `Google\Chrome\Application\chrome.exe`),
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
