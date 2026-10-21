package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zoolcoder/chatgpt-design/internal/lockfile"
)

const (
	// lastUsedFile's modification time is when a run last opened or closed a tab.
	lastUsedFile = "chatgpt-design.last-used"
	// watchLock keeps to one watcher per profile.
	watchLock = "chatgpt-design.watch.lock"
	// anchorTitle names the tab that holds the window open between runs.
	anchorTitle = "chatgpt-design"
)

// anchorFile holds the page of the tab that keeps the window open.
const anchorFile = "chatgpt-design.html"

// anchorHTML says what the window is, because it outlives the run that opened it.
const anchorHTML = `<!doctype html><meta charset="utf-8"><title>` + anchorTitle + `</title>
<body style="font:16px/1.5 system-ui,sans-serif;max-width:36em;margin:15vh auto;padding:0 16px;color:#444">
<h1 style="font-size:20px;color:#111">chatgpt-design</h1>
<p>This Chrome keeps you signed in to ChatGPT between chatgpt-design runs. Each run opens its own tab here and closes it when done.</p>
<p>It quits by itself once no run has used it for a while. To close it now, run <code>chatgpt-design browser stop</code>.</p>
`

// anchorPage writes the anchor page into the profile and returns its URL, or about:blank when it
// cannot be written.
func anchorPage(profile string) string {
	f := filepath.Join(profile, anchorFile)
	if err := os.WriteFile(f, []byte(anchorHTML), 0o600); err != nil {
		return "about:blank"
	}
	p := filepath.ToSlash(f)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // a Windows drive path
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// touch marks the profile's Chrome as just used.
func touch(profile string) {
	f := filepath.Join(profile, lastUsedFile)
	now := time.Now()
	if err := os.Chtimes(f, now, now); errors.Is(err, os.ErrNotExist) {
		_ = os.WriteFile(f, nil, 0o600)
	}
}

// Watch quits the profile's Chrome once no run has used it for idle, then returns. It returns at
// once when another watcher already runs, and as soon as Chrome is gone.
func Watch(ctx context.Context, profile string, idle time.Duration) error {
	unlock, err := lockfile.Acquire(ctx, filepath.Join(profile, watchLock), 0)
	if errors.Is(err, lockfile.ErrHeld) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unlock()
	tick := min(max(idle/4, time.Second), 30*time.Second)
	for {
		if err := sleep(ctx, tick); err != nil {
			return err
		}
		ws, err := running(profile)
		if err != nil {
			if _, alive := holder(profile); !alive {
				return nil
			}
			continue
		}
		if !idleFor(ws, profile, idle) {
			continue
		}
		_, err = Stop(ctx, profile)
		return err
	}
}

// idleFor reports whether no run has a tab open and none has touched the profile for idle.
func idleFor(ws, profile string, idle time.Duration) bool {
	fi, err := os.Stat(filepath.Join(profile, lastUsedFile))
	if err == nil && time.Since(fi.ModTime()) < idle {
		return false
	}
	n, err := openTabs(ws)
	return err == nil && n == 0
}

// openTabs counts the pages other than the anchor: a run's tab, or one the user opened.
func openTabs(ws string) (int, error) {
	u, err := url.Parse(ws)
	if err != nil {
		return 0, err
	}
	c := http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get("http://" + u.Host + "/json/list")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("DevTools: %s", resp.Status)
	}
	var targets []struct{ Type, Title, URL string }
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return 0, err
	}
	n := 0
	for _, t := range targets {
		// The anchor is about:blank when its page could not be written, or Chrome is from an older version.
		if t.Type == "page" && t.Title != anchorTitle && t.URL != "about:blank" {
			n++
		}
	}
	return n, nil
}

// Detach makes cmd outlive this process and its terminal.
func Detach(cmd *exec.Cmd) { detach(cmd) }
