// Package browser drives a dedicated Chrome profile with chromedp.
package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"

	"github.com/chromedp/chromedp"
)

// ProfileDir is the Chrome profile that holds the ChatGPT login. It is separate from the user's
// everyday Chrome profile, so the tool never touches their other sessions.
func ProfileDir() (string, error) {
	if d := os.Getenv("CHATGPT_DESIGN_PROFILE"); d != "" {
		return d, ensureDir(d)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(base, "chatgpt-design", "chrome-profile")
	return d, ensureDir(d)
}

func ensureDir(d string) error {
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	// Chmod only when needed: a read-only sandbox may still let the run reach a Chrome already up.
	if fi, err := os.Stat(d); err == nil && fi.Mode().Perm() == 0o700 {
		return nil
	}
	return os.Chmod(d, 0o700)
}

// Options picks how Chrome starts.
type Options struct {
	Profile  string // "" runs a throwaway profile
	Headless bool
	Mobile   bool // send a phone's user agent
	Width    int
	Height   int
}

// Start launches Chrome and returns a tab context; cancel closes Chrome.
func Start(ctx context.Context, o Options) (context.Context, context.CancelFunc, error) {
	if o.Width == 0 {
		o.Width, o.Height = 1440, 960
	}
	opts := append([]chromedp.ExecAllocatorOption{},
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.WindowSize(o.Width, o.Height),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
	)
	if o.Headless {
		opts = append(opts, chromedp.Headless, chromedp.Flag("hide-scrollbars", true))
	}
	if o.Profile != "" {
		opts = append(opts, chromedp.UserDataDir(o.Profile))
	}
	if p := os.Getenv("CHATGPT_DESIGN_CHROME"); p != "" {
		opts = append(opts, chromedp.ExecPath(p))
	}
	actx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	tctx, cancelTab := chromedp.NewContext(actx)
	if err := chromedp.Do(tctx); err != nil {
		cancelTab()
		cancelAlloc()
		if errors.Is(err, os.ErrPermission) || strings.Contains(strings.ToLower(err.Error()), "operation not permitted") {
			return nil, nil, sandboxed(err)
		}
		if o.Profile != "" {
			return nil, nil, fmt.Errorf("start Chrome: %w (is another chatgpt-design run using %s?)", err, o.Profile)
		}
		return nil, nil, fmt.Errorf("start Chrome: %w", err)
	}
	if o.Headless || o.Mobile {
		if err := setUserAgent(tctx, o.Mobile); err != nil {
			cancelTab()
			cancelAlloc()
			return nil, nil, fmt.Errorf("start Chrome: %w", err)
		}
	}
	return tctx, func() { cancelTab(); cancelAlloc() }, nil
}

// ErrNotLoggedIn means the profile has no ChatGPT session.
var ErrNotLoggedIn = errors.New("not logged in to ChatGPT: run `chatgpt-design login` first")

// setUserAgent drops "Headless" from the user agent, since sites serve a bot check to
// HeadlessChrome, and makes it a phone's when mobile is set, so sites serve their mobile layout.
func setUserAgent(ctx context.Context, mobile bool) error {
	return chromedp.Do(ctx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		v, err := cdp.Call(ctx, t, browser.GetVersion, cdp.Empty{})
		if err != nil {
			return err
		}
		ua := strings.Replace(v.UserAgent, "HeadlessChrome", "Chrome", 1)
		if mobile {
			ua = mobileUA(ua)
		}
		_, err = cdp.Call(ctx, t, emulation.SetUserAgentOverride, emulation.SetUserAgentOverrideParams{UserAgent: ua})
		return err
	}))
}

var uaPlatform = regexp.MustCompile(`\([^)]*\)`)

// mobileUA turns a desktop Chrome user agent into Android Chrome's, keeping the version. Only the
// first parenthesised group names the platform; the later ones belong to the engine.
func mobileUA(ua string) string {
	if loc := uaPlatform.FindStringIndex(ua); loc != nil {
		ua = ua[:loc[0]] + "(Linux; Android 10; K)" + ua[loc[1]:]
	}
	if !strings.Contains(ua, "Mobile Safari/") {
		ua = strings.Replace(ua, " Safari/", " Mobile Safari/", 1)
	}
	return ua
}
