package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zoolcoder/chatgpt-design/internal/browser"
	"github.com/zoolcoder/chatgpt-design/internal/memory"
	"github.com/zoolcoder/chatgpt-design/skill"
)

// check is one doctor finding. Fix says what to do when OK is false, or adds a hint when it is true.
type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
	exit   int
}

// doctor checks, in order, everything a run needs. It stops at what blocks the next check, so the
// first failure is the one to fix.
func doctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	project := fs.String("project", ".", "project directory; memory lives in its design/chatgpt/")
	sessionID := fs.String("session", "", "session id (default: the agent's session)")
	offline := fs.Bool("offline", false, "skip the sign-in check, which starts Chrome when it is not running")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	if err := parse(fs, args); err != nil {
		return err
	}
	checks := []check{{Name: "version", OK: true, Detail: version}}
	checks = append(checks, checkSkill()...)
	checks = append(checks, checkSession(*project, *sessionID))
	checks = append(checks, checkBrowser(ctx, *offline)...)

	code := 0
	for _, c := range checks {
		if !c.OK {
			code = c.exit
			if code == 0 {
				code = exitErr
			}
			break
		}
	}
	err := emit(struct {
		OK     bool    `json:"ok"`
		Checks []check `json:"checks"`
	}{code == 0, checks}, func() {
		for _, c := range checks {
			mark := "ok  "
			if !c.OK {
				mark = "FAIL"
			}
			fmt.Printf("%s %-8s %s\n", mark, c.Name, c.Detail)
			if c.Fix != "" {
				fmt.Printf("     %-8s %s\n", "", c.Fix)
			}
		}
	})
	if err != nil {
		return err
	}
	if code != 0 {
		return quietError{code}
	}
	return nil
}

// checkSkill compares each installed SKILL.md with the one built into this binary.
func checkSkill() []check {
	dirs, err := skillDirs()
	if err != nil {
		return []check{{Name: "skill", Detail: err.Error()}}
	}
	var out []check
	for _, d := range dirs {
		path := filepath.Join(d, "chatgpt-design", "SKILL.md")
		b, err := os.ReadFile(path)
		c := check{Name: "skill", OK: true, Detail: path}
		switch {
		case errors.Is(err, os.ErrNotExist):
			// A missing skill does not stop the CLI, so it is a hint, not a failure.
			c.Detail, c.Fix = path+" not installed", "run `chatgpt-design install-skill`"
		case err != nil:
			c.OK, c.Detail = false, err.Error()
		case !bytes.Equal(b, skill.Markdown):
			c.Detail, c.Fix = path+" differs from this binary's", "run `chatgpt-design install-skill` to update it"
		}
		out = append(out, c)
	}
	return out
}

func checkSession(project, flagID string) check {
	c := check{Name: "session", exit: exitUsage}
	p, err := memory.Open(project)
	if err != nil {
		c.Detail = err.Error()
		return c
	}
	id, err := memory.SessionID(flagID, os.Getenv)
	if err != nil {
		c.Detail, c.Fix = err.Error(), "pass --session ID, or set CHATGPT_DESIGN_SESSION"
		return c
	}
	s, err := p.Session(id, time.Now())
	if err != nil {
		c.Detail = err.Error()
		return c
	}
	c.OK, c.Detail = true, s.Dir()
	return c
}

// checkBrowser looks for Chrome, the profile, the running browser and the sign-in, in that order.
func checkBrowser(ctx context.Context, offline bool) []check {
	exe, err := browser.ChromePath()
	if err != nil {
		return []check{{Name: "chrome", Detail: err.Error(), Fix: "install Google Chrome, or set CHATGPT_DESIGN_CHROME to its binary"}}
	}
	out := []check{{Name: "chrome", OK: true, Detail: exe}}
	dir, err := browser.ProfileDir()
	if err == nil {
		err = writable(dir)
	}
	if err != nil {
		return append(out, check{Name: "profile", Detail: err.Error(), Fix: "run outside any command sandbox, or set CHATGPT_DESIGN_PROFILE to a writable folder"})
	}
	out = append(out, check{Name: "profile", OK: true, Detail: dir})

	st, err := browser.Probe(dir)
	switch {
	case errors.Is(err, browser.ErrSandboxed):
		return append(out, check{Name: "browser", Detail: err.Error(), Fix: "run chatgpt-design outside the command sandbox (Claude Code: dangerouslyDisableSandbox: true)"})
	case err != nil:
		return append(out, check{Name: "browser", Detail: err.Error(), Fix: "run `chatgpt-design browser stop`, then retry"})
	case st.Running:
		out = append(out, check{Name: "browser", OK: true, Detail: fmt.Sprintf("running, pid %d", st.PID)})
	default:
		out = append(out, check{Name: "browser", OK: true, Detail: "not running; the next command starts it"})
	}
	if offline {
		return out
	}
	tab, closeTab, err := browser.Open(ctx, dir)
	if err != nil {
		return append(out, check{Name: "login", Detail: err.Error(), Fix: "see the browser check above"})
	}
	defer closeTab()
	ok, err := browser.LoggedIn(tab)
	switch {
	case err != nil:
		return append(out, check{Name: "login", Detail: err.Error(), Fix: "check the network, then retry"})
	case !ok:
		return append(out, check{Name: "login", Detail: "not signed in to ChatGPT", Fix: "run `chatgpt-design login` and sign in in the window", exit: exitLogin})
	}
	return append(out, check{Name: "login", OK: true, Detail: "signed in"})
}

// writable proves the run can create files in dir; Chrome needs it for its locks.
func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}
