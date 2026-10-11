// Command chatgpt-design sends screenshots and a design prompt to your own ChatGPT and saves the answer.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/zoolcoder/chatgpt-design/internal/browser"
	"github.com/zoolcoder/chatgpt-design/internal/memory"
	"github.com/zoolcoder/chatgpt-design/internal/output"
	"github.com/zoolcoder/chatgpt-design/internal/prompt"
	"github.com/zoolcoder/chatgpt-design/skill"
)

var version = "dev"

const (
	exitErr      = 1
	exitUsage    = 2
	exitLogin    = 3
	exitLimit    = 4
	maxShots     = 10
	maxShotBytes = 20 << 20
	maxTextBytes = 256 << 10
)

const usage = `chatgpt-design: send screenshots and a design prompt to your own ChatGPT, save the answer.

Usage:
  chatgpt-design login                      sign in once, in a dedicated Chrome profile
  chatgpt-design status                     say whether that profile is signed in
  chatgpt-design session new TOPIC          start a session: a new ChatGPT chat, files in design/chatgpt/
  chatgpt-design session show|list|use ID   see or switch the project's sessions
  chatgpt-design shoot --base URL name=/path ...
                                            screenshot pages of a running app into the session
  chatgpt-design ask --mode MODE            send to the session's chat, save the round in the session
  chatgpt-design install-skill              install the agent skill for Claude Code, Codex and others
  chatgpt-design version

Every ask continues the current session's ChatGPT chat until you start a new session.
Modes: spec (written critique and tokens), redesign (mockup images), logo, chat (--prompt FILE).
Run "chatgpt-design <command> -h" for flags.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(exitUsage)
	}
	cmds := map[string]func(context.Context, []string) error{
		"login": login, "status": status, "session": session, "shoot": shoot, "ask": ask, "install-skill": installSkill,
	}
	name, args := os.Args[1], os.Args[2:]
	switch name {
	case "version", "--version", "-v":
		fmt.Println(version)
		return
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	}
	run, ok := cmds[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", name, usage)
		os.Exit(exitUsage)
	}
	if err := run(ctx, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "chatgpt-design:", err)
		os.Exit(exitCode(err))
	}
}

type usageError struct{ error }

func exitCode(err error) int {
	var ue usageError
	switch {
	case errors.As(err, &ue), errors.Is(err, memory.ErrNoSession):
		return exitUsage
	case errors.Is(err, browser.ErrNotLoggedIn):
		return exitLogin
	case strings.Contains(err.Error(), "usage limit"):
		return exitLimit
	}
	return exitErr
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }

func parse(fs *flag.FlagSet, args []string) error {
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return usageError{err}
	}
	return nil
}

func login(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	wait := fs.Duration("timeout", 10*time.Minute, "how long to wait for the sign-in")
	if err := parse(fs, args); err != nil {
		return err
	}
	tab, closeTab, err := openProfile(ctx, false)
	if err != nil {
		return err
	}
	defer closeTab()
	return browser.Login(tab, *wait, logf)
}

func status(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	if err := parse(fs, args); err != nil {
		return err
	}
	tab, closeTab, err := openProfile(ctx, true)
	if err != nil {
		return err
	}
	defer closeTab()
	ok, err := browser.LoggedIn(tab)
	if err != nil {
		return fmt.Errorf("could not check the sign-in: %w", err)
	}
	if !ok {
		return browser.ErrNotLoggedIn
	}
	fmt.Println("logged in")
	return nil
}

func openProfile(ctx context.Context, headless bool) (context.Context, context.CancelFunc, error) {
	dir, err := browser.ProfileDir()
	if err != nil {
		return nil, nil, err
	}
	return browser.Start(ctx, browser.Options{Profile: dir, Headless: headless})
}

func shoot(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("shoot", flag.ContinueOnError)
	base := fs.String("base", "", "app URL, e.g. http://localhost:3000")
	out := fs.String("out", "", "directory for NN-name.png files (default: the current session's shots/)")
	project := fs.String("project", ".", "project directory; memory lives in its design/chatgpt/")
	width := fs.Int("width", 1440, "viewport width (at most 1600)")
	height := fs.Int("height", 900, "viewport height")
	settle := fs.Duration("wait", 1500*time.Millisecond, "time to let each page settle")
	full := fs.Bool("full", false, "capture the whole page, not just the viewport")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: chatgpt-design shoot --base URL --out DIR name=/path [name=/path ...]")
		fs.PrintDefaults()
	}
	if err := parse(fs, args); err != nil {
		return err
	}
	pages, err := shotTargets(*base, fs.Args())
	if err != nil {
		return usageError{err}
	}
	if *out == "" {
		p, err := memory.Open(*project)
		if err != nil {
			return err
		}
		cur, err := p.Current()
		if err != nil {
			return err
		}
		*out = filepath.Join(cur.Dir(), "shots")
	}
	if *width < 320 || *width > 1600 {
		return usageError{errors.New("--width must be 320 to 1600")}
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	tab, closeTab, err := browser.Start(ctx, browser.Options{Headless: true, Width: *width, Height: *height})
	if err != nil {
		return err
	}
	defer closeTab()
	for i, p := range pages {
		buf, err := browser.Shot(tab, p.url, *width, *height, *settle, *full)
		if err != nil {
			return fmt.Errorf("%s: %w", p.url, err)
		}
		path := filepath.Join(*out, fmt.Sprintf("%02d-%s.png", i+1, p.name))
		if err := os.WriteFile(path, buf, 0o644); err != nil {
			return err
		}
		fmt.Println(path)
	}
	return nil
}

type target struct{ name, url string }

func shotTargets(base string, args []string) ([]target, error) {
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return nil, errors.New("--base must be an http(s) URL")
	}
	if len(args) == 0 {
		return nil, errors.New("name at least one page as name=/path")
	}
	base = strings.TrimRight(base, "/")
	var out []target
	for _, a := range args {
		name, path, ok := strings.Cut(a, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("%q is not name=/path", a)
		}
		if strings.ContainsAny(name, `/\. `) {
			return nil, fmt.Errorf("page name %q must be a plain word like inbox-connect", name)
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		out = append(out, target{name, base + path})
	}
	return out, nil
}

func ask(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	mode := fs.String("mode", "spec", "spec, redesign, logo or chat")
	project := fs.String("project", ".", "project directory; memory lives in its design/chatgpt/")
	newSession := fs.Bool("new", false, "start a new session (and a new ChatGPT chat) first; needs --topic")
	topic := fs.String("topic", "", "topic of the new session, e.g. \"simple onboarding\"")
	sessionID := fs.String("session", "", "use this session instead of the current one")
	briefPath := fs.String("brief", "", "brief file (default SESSION/brief.md)")
	brandPath := fs.String("brand", "", "brand sheet, e.g. design/brand.md")
	promptPath := fs.String("prompt", "", "your own prompt file, - for stdin (required for chat)")
	goal := fs.String("goal", "", "one line on what this round should optimise for")
	timeout := fs.Duration("timeout", 20*time.Minute, "give up after this long")
	dry := fs.Bool("dry-run", false, "print the prompt and files, send nothing")
	var shots multi
	fs.Var(&shots, "shot", "screenshot to attach (repeatable; the first round defaults to SESSION/shots/*)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageError{fmt.Errorf("unexpected argument %q", fs.Arg(0))}
	}
	if *newSession && *sessionID != "" {
		return usageError{errors.New("use --new or --session, not both")}
	}
	if *newSession && strings.TrimSpace(*topic) == "" {
		return usageError{errors.New("--new needs --topic")}
	}
	if !*newSession && *topic != "" {
		return usageError{errors.New("--topic works only with --new")}
	}
	p, err := memory.Open(*project)
	if err != nil {
		return err
	}

	var sess *memory.Session
	switch {
	case *newSession:
		if *dry {
			break // a dry run creates nothing
		}
		if sess, err = p.New(*topic, time.Now()); err != nil {
			return usageError{err}
		}
		logf("new session %s", sess.ID)
	case *sessionID != "":
		if sess, err = p.Session(*sessionID); err != nil {
			return usageError{err}
		}
	default:
		if sess, err = p.Current(); err != nil {
			return err
		}
	}

	firstRound := sess == nil || len(sess.Rounds) == 0
	if sess != nil {
		if *briefPath == "" && *mode != "chat" {
			*briefPath = filepath.Join(sess.Dir(), "brief.md")
		}
		if len(shots) == 0 && firstRound && *mode != "logo" && *mode != "chat" {
			found, err := findShots(filepath.Join(sess.Dir(), "shots"))
			if err != nil {
				return err
			}
			shots = found
		}
	}
	files, err := checkShots(shots)
	if err != nil {
		return usageError{err}
	}
	text, err := buildPrompt(*mode, *briefPath, *brandPath, *promptPath, *goal, files)
	if err != nil {
		return err
	}
	if *dry {
		fmt.Println(text)
		fmt.Println("---")
		for _, f := range files {
			fmt.Println("attach:", f)
		}
		switch {
		case sess == nil:
			fmt.Println("session: new, chat: new")
		case sess.ChatURL == "":
			fmt.Printf("session: %s, chat: new\n", sess.ID)
		default:
			fmt.Printf("session: %s, chat: %s\n", sess.ID, sess.ChatURL)
		}
		return nil
	}

	out := sess.NextRoundDir()
	tab, closeTab, err := openProfile(ctx, false)
	if err != nil {
		return err
	}
	defer closeTab()
	res, err := browser.Ask(tab, browser.Request{
		Prompt:     text,
		ChatURL:    sess.ChatURL,
		Files:      files,
		WantImages: *mode == "redesign" || *mode == "logo",
		Timeout:    *timeout,
		Log:        logf,
	})
	if err != nil {
		if mkErr := os.MkdirAll(out, 0o755); mkErr == nil {
			shot := filepath.Join(out, "error.png")
			if browser.Screenshot(tab, shot) == nil {
				logf("page at failure: %s", shot)
			}
		}
		return err
	}
	meta, err := output.Save(out, text, res, output.Meta{Mode: *mode, Shots: files})
	if err != nil {
		return err
	}
	if err := sess.AddRound(roundOf(sess.Dir(), out, meta), chatURL(res.URL, sess.ChatURL)); err != nil {
		return err
	}
	for _, w := range meta.Warnings {
		logf("warning: %s", w)
	}
	fmt.Println("session:", sess.ID)
	fmt.Println("response:", meta.Response)
	for _, im := range meta.Images {
		fmt.Println("image:", im)
	}
	fmt.Println("chat:", meta.URL)
	return nil
}

// chatURL keeps the session on the conversation it already has, and adopts the one ChatGPT opened otherwise.
func chatURL(got, had string) string {
	if had != "" || !strings.Contains(got, "/c/") {
		return had
	}
	return got
}

func roundOf(sessDir, out string, m output.Meta) memory.Round {
	rel := func(p string) string {
		if r, err := filepath.Rel(sessDir, p); err == nil {
			return filepath.ToSlash(r)
		}
		return p
	}
	r := memory.Round{Mode: m.Mode, At: m.Created, Dir: rel(out), Response: rel(m.Response), Warnings: m.Warnings}
	for _, im := range m.Images {
		r.Images = append(r.Images, rel(im))
	}
	for _, sh := range m.Shots {
		r.Shots = append(r.Shots, rel(sh))
	}
	return r
}

func session(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("session", flag.ContinueOnError)
	project := fs.String("project", ".", "project directory; memory lives in its design/chatgpt/")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: chatgpt-design session new TOPIC | show [ID] | list | use ID")
		fs.PrintDefaults()
	}
	if err := parse(fs, args); err != nil {
		return err
	}
	p, err := memory.Open(*project)
	if err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		rest = []string{"show"}
	}
	switch verb, rest := rest[0], rest[1:]; verb {
	case "new":
		if len(rest) == 0 {
			return usageError{errors.New("session new needs a topic")}
		}
		s, err := p.New(strings.Join(rest, " "), time.Now())
		if err != nil {
			return usageError{err}
		}
		fmt.Println("session:", s.ID)
		fmt.Println("dir:", s.Dir())
		return nil
	case "show":
		var s *memory.Session
		if len(rest) > 0 {
			s, err = p.Session(rest[0])
		} else {
			s, err = p.Current()
		}
		if err != nil {
			return err
		}
		printSession(s)
		return nil
	case "list":
		all, err := p.List()
		if err != nil {
			return err
		}
		cur, _ := p.Current()
		for _, s := range all {
			mark := " "
			if cur != nil && cur.ID == s.ID {
				mark = "*"
			}
			fmt.Printf("%s %s  %d round(s)  %s\n", mark, s.ID, len(s.Rounds), s.Topic)
		}
		return nil
	case "use":
		if len(rest) != 1 {
			return usageError{errors.New("session use needs one session id")}
		}
		if err := p.Use(rest[0]); err != nil {
			return usageError{err}
		}
		fmt.Println("session:", rest[0])
		return nil
	default:
		return usageError{fmt.Errorf("unknown session command %q, want new, show, list or use", verb)}
	}
}

func printSession(s *memory.Session) {
	fmt.Println("session:", s.ID)
	fmt.Println("topic:", s.Topic)
	fmt.Println("dir:", s.Dir())
	chat := s.ChatURL
	if chat == "" {
		chat = "none yet (the next ask starts it)"
	}
	fmt.Println("chat:", chat)
	for _, r := range s.Rounds {
		fmt.Printf("round %02d  %s  %s  %s", r.N, r.At.Local().Format("2006-01-02 15:04"), r.Mode, r.Response)
		if len(r.Images) > 0 {
			fmt.Printf("  %d image(s)", len(r.Images))
		}
		fmt.Println()
	}
}

func buildPrompt(mode, briefPath, brandPath, promptPath, goal string, files []string) (string, error) {
	if mode != "chat" && !slices.Contains(prompt.Modes, mode) {
		return "", usageError{fmt.Errorf("unknown mode %q, want one of %s, chat", mode, strings.Join(prompt.Modes, ", "))}
	}
	if mode == "chat" {
		if promptPath == "" {
			return "", usageError{errors.New("chat mode needs --prompt FILE")}
		}
		text, err := readText(promptPath)
		if err != nil {
			return "", usageError{err}
		}
		if strings.TrimSpace(text) == "" {
			return "", usageError{errors.New("the prompt is empty")}
		}
		return text, nil
	}
	if promptPath != "" {
		return "", usageError{errors.New("--prompt works only with --mode chat")}
	}
	if briefPath == "" {
		return "", usageError{errors.New("no brief: write SESSION/brief.md or pass --brief")}
	}
	brief, err := readText(briefPath)
	if err != nil {
		return "", usageError{err}
	}
	brand := ""
	if brandPath != "" {
		if brand, err = readText(brandPath); err != nil {
			return "", usageError{err}
		}
	}
	text, err := prompt.Build(mode, prompt.Input{Brief: brief, Brand: brand, Shots: files, Goal: goal})
	if err != nil {
		return "", usageError{err}
	}
	return text, nil
}

func readText(path string) (string, error) {
	var r io.Reader = os.Stdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, maxTextBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxTextBytes {
		return "", fmt.Errorf("%s: larger than %d KB", path, maxTextBytes>>10)
	}
	return string(b), nil
}

var imageExts = []string{".png", ".jpg", ".jpeg", ".webp", ".gif"}

func findShots(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && slices.Contains(imageExts, strings.ToLower(filepath.Ext(e.Name()))) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out, nil
}

// checkShots resolves the screenshots to absolute paths of regular image files within the limits.
func checkShots(shots []string) ([]string, error) {
	if len(shots) > maxShots {
		return nil, fmt.Errorf("%d screenshots: ChatGPT takes at most %d per message", len(shots), maxShots)
	}
	var out []string
	for _, s := range shots {
		if !slices.Contains(imageExts, strings.ToLower(filepath.Ext(s))) {
			return nil, fmt.Errorf("%s: only %s images", s, strings.Join(imageExts, " "))
		}
		abs, err := filepath.Abs(s)
		if err != nil {
			return nil, err
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return nil, err
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a regular file", s)
		}
		if fi.Size() > maxShotBytes {
			return nil, fmt.Errorf("%s is over 20 MB", s)
		}
		out = append(out, abs)
	}
	return out, nil
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func installSkill(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("install-skill", flag.ContinueOnError)
	var dirs multi
	fs.Var(&dirs, "dir", "skills directory (repeatable; default ~/.claude/skills, ~/.codex/skills, ~/.agents/skills)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if len(dirs) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		for _, d := range []string{".claude/skills", ".codex/skills", ".agents/skills"} {
			dirs = append(dirs, filepath.Join(home, d))
		}
	}
	for _, d := range dirs {
		target := filepath.Join(d, "chatgpt-design")
		if err := os.MkdirAll(target, 0o755); err != nil {
			return err
		}
		path := filepath.Join(target, "SKILL.md")
		if err := os.WriteFile(path, skill.Markdown, 0o644); err != nil {
			return err
		}
		fmt.Println(path)
	}
	return nil
}
