// Command chatgpt-design sends screenshots and a design prompt to your own ChatGPT and saves the answer.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/zoolcoder/chatgpt-design/internal/browser"
	"github.com/zoolcoder/chatgpt-design/internal/export"
	"github.com/zoolcoder/chatgpt-design/internal/imgdiff"
	"github.com/zoolcoder/chatgpt-design/internal/memory"
	"github.com/zoolcoder/chatgpt-design/internal/output"
	"github.com/zoolcoder/chatgpt-design/internal/prompt"
	"github.com/zoolcoder/chatgpt-design/skill"
)

var version = "dev"

const (
	exitErr   = 1
	exitUsage = 2
	exitLogin = 3
	exitLimit = 4
	// exitInterrupted is the shell's code for a run ended by Ctrl-C.
	exitInterrupted = 130
	maxShots        = 10
	maxShotBytes    = 20 << 20
	maxTextBytes    = 256 << 10
)

const usage = `chatgpt-design: send screenshots and a design prompt to your own ChatGPT, save the answer.

Usage:
  chatgpt-design doctor                     check Chrome, the profile, the sign-in and the skill, with a fix for each
  chatgpt-design login                      sign in once, in a dedicated Chrome profile
  chatgpt-design status                     say whether that profile is signed in
  chatgpt-design session [show|list]        see this session or all of the project's sessions
  chatgpt-design session export             write the session as one HTML file to open or share
  chatgpt-design shoot --base URL name=/path ...
                                            screenshot pages of a running app into the session
                                            (--sizes 390,768,1440 for each width, --diff to see what changed)
  chatgpt-design ask --mode MODE            send to the session's chat, save the round in the session
                                            (--stream prints the answer as it arrives)
  chatgpt-design browser stop               quit the background Chrome that keeps you signed in
  chatgpt-design install-skill              install the agent skill for Claude Code, Codex and others
  chatgpt-design version

A session is your agent session (Claude Code's id, or --session / CHATGPT_DESIGN_SESSION).
Its files live in design/chatgpt/<session>/, and every ask continues its ChatGPT chat until --new.
Chrome starts on the first command and keeps running between runs; each run opens and closes only its own tab.
Modes: spec (written critique and tokens), redesign (mockup images), logo, chat (--prompt FILE).
Add --json to doctor, status, session, shoot or ask for machine-readable output on stdout.
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
		"doctor": doctor, "login": login, "status": status, "browser": browserCmd, "session": session, "shoot": shoot, "ask": ask, "install-skill": installSkill,
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
		if ctx.Err() != nil {
			fmt.Fprintln(os.Stderr, "chatgpt-design: interrupted")
			os.Exit(exitInterrupted)
		}
		code := exitCode(err)
		var q quietError
		if !errors.As(err, &q) {
			fmt.Fprintln(os.Stderr, "chatgpt-design:", err)
			if jsonOut {
				_ = emit(struct {
					OK    bool   `json:"ok"`
					Error string `json:"error"`
					Exit  int    `json:"exit"`
				}{false, err.Error(), code}, nil)
			}
		}
		os.Exit(code)
	}
}

// jsonOut is set by a command's --json flag.
var jsonOut bool

// emit prints v as JSON under --json, and runs text otherwise.
func emit(v any, text func()) error {
	if !jsonOut {
		text()
		return nil
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := os.Stdout.Write(b.Bytes())
	return err
}

type usageError struct{ error }

// quietError exits with code after the command has already reported why.
type quietError struct{ code int }

func (q quietError) Error() string { return fmt.Sprintf("exit %d", q.code) }

func exitCode(err error) int {
	var ue usageError
	var q quietError
	switch {
	case errors.As(err, &q):
		return q.code
	case errors.As(err, &ue), errors.Is(err, memory.ErrNoSession):
		return exitUsage
	case errors.Is(err, browser.ErrNotLoggedIn):
		return exitLogin
	case errors.Is(err, browser.ErrUsageLimit):
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
	tab, closeTab, err := openProfile(ctx)
	if err != nil {
		return err
	}
	defer closeTab()
	return browser.Login(tab, *wait, logf)
}

func status(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	if err := parse(fs, args); err != nil {
		return err
	}
	tab, closeTab, err := openProfile(ctx)
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
	return emit(map[string]bool{"logged_in": true}, func() { fmt.Println("logged in") })
}

func openProfile(ctx context.Context) (context.Context, context.CancelFunc, error) {
	dir, err := browser.ProfileDir()
	if err != nil {
		return nil, nil, err
	}
	tab, closeTab, err := browser.Open(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	startWatch()
	return tab, closeTab, nil
}

// defaultIdle is how long the background Chrome waits for another run before it quits.
const defaultIdle = 15 * time.Minute

// idleTime reads CHATGPT_DESIGN_IDLE; 0 keeps Chrome running until browser stop.
func idleTime() (time.Duration, error) {
	v := os.Getenv("CHATGPT_DESIGN_IDLE")
	if v == "" {
		return defaultIdle, nil
	}
	if v == "0" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < time.Minute {
		return 0, fmt.Errorf("CHATGPT_DESIGN_IDLE=%q: want a duration of 1m or more, like 30m, or 0 to never quit", v)
	}
	return d, nil
}

// startWatch starts a background watcher that quits Chrome once it sits idle. It exits at once
// when one already runs, so every run can start it.
func startWatch() {
	if d, err := idleTime(); err != nil || d == 0 {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "browser", "watch")
	browser.Detach(cmd)
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

func browserCmd(ctx context.Context, args []string) error {
	const use = "usage: chatgpt-design browser stop"
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(os.Stderr, use)
		return flag.ErrHelp
	}
	if len(args) != 1 || args[0] != "stop" && args[0] != "watch" {
		return usageError{errors.New(use)}
	}
	dir, err := browser.ProfileDir()
	if err != nil {
		return err
	}
	if args[0] == "watch" {
		// Started by startWatch, detached; not meant to be run by hand.
		idle, err := idleTime()
		if err != nil || idle == 0 {
			return err
		}
		return browser.Watch(ctx, dir, idle)
	}
	was, err := browser.Stop(ctx, dir)
	if err != nil {
		return err
	}
	if was {
		fmt.Println("Chrome stopped")
	} else {
		fmt.Println("Chrome was not running")
	}
	return nil
}

func shoot(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("shoot", flag.ContinueOnError)
	base := fs.String("base", "", "app URL, e.g. http://localhost:3000")
	out := fs.String("out", "", "directory for NN-name.png files (default: the current session's shots/)")
	project := fs.String("project", ".", "project directory; memory lives in its design/chatgpt/")
	sessionID := fs.String("session", "", "session id (default: the agent's session)")
	width := fs.Int("width", 1440, "viewport width, 320 to 1600 (390 with --mobile)")
	height := fs.Int("height", 900, "viewport height, 320 to 4000 (844 with --mobile)")
	sizes := fs.String("sizes", "", "shoot every page at each of these widths, e.g. 390,768,1440, as NN-name@WIDTH.png")
	settle := fs.Duration("wait", 3*time.Second, "the longest to wait for each page to stop changing once it and its fonts load")
	full := fs.Bool("full", false, "capture the whole page, not just the viewport")
	mobile := fs.Bool("mobile", false, "shoot as a phone: touch, 3x pixels, mobile user agent")
	dark := fs.Bool("dark", false, "prefer the dark color scheme")
	diff := fs.Bool("diff", false, "compare each page with its last shot and save the changes to shots/diff/")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: chatgpt-design shoot --base URL [--out DIR] name=/path [name=/path ...]")
		fs.PrintDefaults()
	}
	if err := parse(fs, args); err != nil {
		return err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if *mobile {
		if !set["width"] {
			*width = 390
		}
		if !set["height"] {
			*height = 844
		}
	}
	widths := []int{*width}
	if *sizes != "" {
		if set["width"] {
			return usageError{errors.New("--sizes sets the widths: it cannot go with --width")}
		}
		var err error
		if widths, err = parseSizes(*sizes); err != nil {
			return usageError{err}
		}
	}
	for _, w := range widths {
		if w < 320 || w > 1600 {
			return usageError{fmt.Errorf("width %d: widths must be 320 to 1600", w)}
		}
	}
	if *height < 320 || *height > 4000 {
		return usageError{errors.New("--height must be 320 to 4000")}
	}
	if *settle < 0 {
		return usageError{errors.New("--wait must not be negative")}
	}
	pages, err := shotTargets(*base, fs.Args())
	if err != nil {
		return usageError{err}
	}
	if *sizes != "" {
		pages = atSizes(pages, widths)
	} else {
		for i := range pages {
			pages[i].width = *width
		}
	}
	if *out == "" {
		p, err := memory.Open(*project)
		if err != nil {
			return err
		}
		id, err := memory.SessionID(*sessionID, os.Getenv)
		if err != nil {
			return err
		}
		cur, err := p.Session(id, time.Now())
		if err != nil {
			return err
		}
		// A run that asks is reading these shots; another shoot would race for the numbers.
		unlock, err := cur.Lock(ctx)
		if err != nil {
			return err
		}
		defer unlock()
		*out = filepath.Join(cur.Dir(), "shots")
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	names := make([]string, len(pages))
	for i, p := range pages {
		names[i] = p.name
	}
	paths, err := shotPaths(*out, names)
	if err != nil {
		return err
	}
	opts := browser.Options{Headless: true, Mobile: *mobile, Width: widths[0], Height: *height}
	tab, closeTab, err := browser.Start(ctx, opts)
	if err != nil {
		return err
	}
	defer closeTab()
	shots, err := shootAll(tab, opts, pages, browser.ShotOptions{Height: *height, Settle: *settle, Full: *full, Mobile: *mobile, Dark: *dark})
	if err != nil {
		return err
	}
	var diffs []shotDiff
	for i, buf := range shots {
		if *diff {
			d, err := diffShot(paths[i], pages[i].name, buf)
			if err != nil {
				return err
			}
			if d != nil {
				diffs = append(diffs, *d)
			}
		}
		if err := writeAtomic(paths[i], buf); err != nil {
			return err
		}
	}
	return emit(struct {
		Shots []string   `json:"shots"`
		Diffs []shotDiff `json:"diffs,omitempty"`
	}{paths, diffs}, func() {
		for _, p := range paths {
			fmt.Println(p)
		}
	})
}

// shotTabs is how many pages load at once: enough to hide each page's settle time, few enough
// that a dev server does not slow down under the load.
const shotTabs = 4

// shootAll captures pages side by side in a few tabs of one browser and returns the images in
// page order. It stops at the first page that fails.
func shootAll(first context.Context, opts browser.Options, pages []target, o browser.ShotOptions) ([][]byte, error) {
	ctx, cancel := context.WithCancel(first)
	defer cancel()
	tabs := []context.Context{first}
	for range min(shotTabs, len(pages)) - 1 {
		tab, closeTab, err := browser.Tab(ctx, opts)
		if err != nil {
			return nil, err
		}
		defer closeTab()
		tabs = append(tabs, tab)
	}
	out := make([][]byte, len(pages))
	// The first failure cancels the rest, so keep it, not the errors the cancel causes.
	var once sync.Once
	var failed error
	next := make(chan int)
	done := make(chan struct{})
	for _, tab := range tabs {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := range next {
				o := o
				o.Width = pages[i].width
				buf, err := browser.Shot(tab, pages[i].url, o)
				if err != nil {
					once.Do(func() { failed = shotErr(pages[i].url, err); cancel() })
					continue
				}
				out[i] = buf
			}
		}()
	}
	for i := range pages {
		if ctx.Err() != nil {
			break
		}
		next <- i
	}
	close(next)
	for range tabs {
		<-done
	}
	if failed != nil {
		return nil, failed
	}
	if err := first.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func shotErr(url string, err error) error {
	if strings.Contains(err.Error(), "ERR_CONNECTION_REFUSED") {
		return fmt.Errorf("%s: nothing answers there; start the app or fix --base", url)
	}
	if strings.HasPrefix(err.Error(), "HTTP ") {
		return fmt.Errorf("%s: %w: check --base and the path", url, err)
	}
	return fmt.Errorf("%s: %w", url, err)
}

type shotDiff struct {
	Page        string  `json:"page"`
	Diff        string  `json:"diff"`
	Changed     float64 `json:"changed_percent"`
	SizeChanged bool    `json:"size_changed,omitempty"`
}

// diffShot compares a new shot with the file it replaces and saves the changes to diff/ beside it,
// where ask never picks them up. A page without an earlier shot has nothing to compare.
func diffShot(path, page string, after []byte) (*shotDiff, error) {
	before, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		logf("%s: new page, nothing to compare", page)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r, err := imgdiff.Compare(before, after)
	if err != nil {
		return nil, fmt.Errorf("compare %s: %w", path, err)
	}
	dir := filepath.Join(filepath.Dir(path), "diff")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	d := &shotDiff{Page: page, Diff: filepath.Join(dir, filepath.Base(path)), Changed: math.Round(r.Changed*100) / 100, SizeChanged: r.SizeChanged}
	if err := writeAtomic(d.Diff, r.PNG); err != nil {
		return nil, err
	}
	note := ""
	if d.SizeChanged {
		note = ", size changed"
	}
	logf("%s: %.2f%% changed%s, diff %s", page, d.Changed, note, d.Diff)
	return d, nil
}

// parseSizes reads a list of widths such as 390,768,1440.
func parseSizes(list string) ([]int, error) {
	var out []int
	for f := range strings.SplitSeq(list, ",") {
		w, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			return nil, fmt.Errorf("--sizes %q: want widths like 390,768,1440", list)
		}
		if slices.Contains(out, w) {
			return nil, fmt.Errorf("--sizes lists %d twice", w)
		}
		out = append(out, w)
	}
	return out, nil
}

// atSizes turns each page into one target per width, named name@width.
func atSizes(pages []target, widths []int) []target {
	var out []target
	for _, p := range pages {
		for _, w := range widths {
			out = append(out, target{name: fmt.Sprintf("%s@%d", p.name, w), url: p.url, width: w})
		}
	}
	return out
}

var shotName = regexp.MustCompile(`^(\d+)-(.+)\.png$`)

// shotPaths names each page's file: a page shot before keeps its file and number, so a re-shoot
// replaces it, and a new page takes the next number after those in dir.
func shotPaths(dir string, names []string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	had := map[string]string{}
	last := 0
	for _, e := range entries {
		m := shotName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		last = max(last, n)
		had[m[2]] = e.Name()
	}
	out := make([]string, len(names))
	for i, name := range names {
		file, ok := had[name]
		if !ok {
			last++
			file = fmt.Sprintf("%02d-%s.png", last, name)
			had[name] = file
		}
		out[i] = filepath.Join(dir, file)
	}
	return out, nil
}

// writeAtomic replaces path in one step, so a failed run never leaves half an image to be sent.
// The temp file has a name of its own, so two runs never write into the same one.
func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

type target struct {
	name, url string
	width     int
}

func shotTargets(base string, args []string) ([]target, error) {
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return nil, errors.New("--base must be an http(s) URL")
	}
	if len(args) == 0 {
		return nil, errors.New("name at least one page as name=/path")
	}
	base = strings.TrimRight(base, "/")
	var out []target
	seen := map[string]bool{}
	for _, a := range args {
		name, path, ok := strings.Cut(a, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("%q is not name=/path", a)
		}
		if strings.ContainsAny(name, `/\. @`) {
			return nil, fmt.Errorf("page name %q must be a plain word like inbox-connect", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("page name %q is used twice", name)
		}
		seen[name] = true
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		out = append(out, target{name: name, url: base + path})
	}
	return out, nil
}

func ask(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	mode := fs.String("mode", "spec", "spec, redesign, logo or chat")
	project := fs.String("project", ".", "project directory; memory lives in its design/chatgpt/")
	newChat := fs.Bool("new", false, "start a new ChatGPT chat in this session")
	sessionID := fs.String("session", "", "session id (default: the agent's session)")
	briefPath := fs.String("brief", "", "brief file (default SESSION/brief.md)")
	brandPath := fs.String("brand", "", "brand sheet, e.g. design/brand.md")
	promptPath := fs.String("prompt", "", "your own prompt file, - for stdin (required for chat)")
	goal := fs.String("goal", "", "one line on what this ask should optimise for")
	timeout := fs.Duration("timeout", 20*time.Minute, "give up after this long")
	dry := fs.Bool("dry-run", false, "print the prompt and files, send nothing")
	chat := fs.String("chat", "", "continue this ChatGPT conversation URL, or \"latest\" for the newest in the sidebar (the session adopts it)")
	collect := fs.Bool("collect", false, "send nothing; save the answer to the chat's last message (rerun a failed ask with the same flags)")
	stream := fs.Bool("stream", false, "print the answer's text to stdout as ChatGPT writes it")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	var shots multi
	fs.Var(&shots, "shot", "screenshot to attach (repeatable; a new chat defaults to SESSION/shots/*)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageError{fmt.Errorf("unexpected argument %q", fs.Arg(0))}
	}
	if *timeout <= 0 {
		return usageError{errors.New("--timeout must be positive")}
	}
	if *chat != "" && *chat != browser.Latest && !browser.IsChatURL(*chat) {
		return usageError{fmt.Errorf("--chat %q is not a ChatGPT conversation URL (https://chatgpt.com/c/...) or latest", *chat)}
	}
	if *stream && jsonOut {
		return usageError{errors.New("--stream prints text: it cannot go with --json")}
	}
	if *newChat && (*chat != "" || *collect) {
		return usageError{errors.New("--new starts an empty chat: it cannot go with --chat or --collect")}
	}
	p, err := memory.Open(*project)
	if err != nil {
		return err
	}
	id, err := memory.SessionID(*sessionID, os.Getenv)
	if err != nil {
		return err
	}
	sess, err := p.Session(id, time.Now())
	if err != nil {
		return usageError{err}
	}
	if !*dry {
		unlock, err := sess.Lock(ctx)
		if err != nil {
			return err
		}
		defer unlock()
		// Read the session again under the lock: a run that just ended may have moved it on.
		if sess, err = p.Session(id, time.Now()); err != nil {
			return err
		}
	}
	if *newChat {
		sess.NewChat()
	}
	if *chat != "" {
		sess.ChatURL = *chat
	}
	if *briefPath == "" && *mode != "chat" {
		*briefPath = filepath.Join(sess.Dir(), "brief.md")
	}
	// --collect rebuilds the prompt of the ask it recovers, so it lists the same shots without sending them.
	if len(shots) == 0 && (sess.ChatURL == "" || *collect) && *mode != "logo" && *mode != "chat" {
		found, err := findShots(filepath.Join(sess.Dir(), "shots"))
		if err != nil {
			return err
		}
		shots = found
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
		chat := sess.ChatURL
		if chat == "" {
			chat = "new"
		}
		return emit(struct {
			Prompt  string   `json:"prompt"`
			Attach  []string `json:"attach"`
			Session string   `json:"session"`
			Chat    string   `json:"chat"`
		}{text, nonNil(files), sess.ID, chat}, func() {
			fmt.Println(text)
			fmt.Println("---")
			for _, f := range files {
				fmt.Println("attach:", f)
			}
			fmt.Printf("session: %s, chat: %s\n", sess.ID, chat)
		})
	}

	tab, closeTab, err := openProfile(ctx)
	if err != nil {
		return err
	}
	defer closeTab()
	var onText func(string)
	var live streamer
	if *stream {
		live.w = os.Stdout
		onText = live.update
	}
	res, err := browser.Ask(tab, browser.Request{
		Prompt:     text,
		ChatURL:    sess.ChatURL,
		Files:      files,
		WantImages: *mode == "redesign" || *mode == "logo",
		Collect:    *collect,
		Timeout:    *timeout,
		Log:        logf,
		OnText:     onText,
		OnChat: func(u string) {
			if err := sess.Adopt(u, time.Now()); err != nil {
				logf("warning: could not save the chat URL: %v", err)
			}
		},
	})
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() == nil {
			err = errors.New("the ChatGPT tab closed during the run (closed by hand, or Chrome dropped it); if the message was sent, rerun with --collect")
		}
		if mkErr := os.MkdirAll(sess.Dir(), 0o755); mkErr == nil {
			shot := filepath.Join(sess.Dir(), "error.png")
			if browser.Screenshot(tab, shot) == nil {
				logf("page at failure: %s", shot)
			}
		}
		return err
	}
	live.end()
	now := time.Now()
	e, err := output.Save(sess.Dir(), text, res, output.Entry{Mode: *mode, At: now, Shots: relAll(sess.Dir(), files)})
	if err != nil {
		return err
	}
	if err := sess.Record(chatURL(res.URL, sess.ChatURL), now); err != nil {
		return err
	}
	for _, w := range e.Warnings {
		logf("warning: %s", w)
	}
	images := make([]string, len(e.Images))
	for i, im := range e.Images {
		images[i] = filepath.Join(sess.Dir(), im)
	}
	return emit(struct {
		Session  string   `json:"session"`
		ChatLog  string   `json:"chat_log"`
		Images   []string `json:"images"`
		Chat     string   `json:"chat"`
		Warnings []string `json:"warnings"`
		Answer   string   `json:"answer"`
	}{sess.ID, filepath.Join(sess.Dir(), "chat.md"), images, sess.ChatURL, nonNil(e.Warnings), e.Answer}, func() {
		fmt.Println("session:", sess.ID)
		fmt.Println("chat log:", filepath.Join(sess.Dir(), "chat.md"))
		for _, im := range images {
			fmt.Println("image:", im)
		}
		fmt.Println("chat:", sess.ChatURL)
		// --stream has already printed it.
		if !*stream && e.Answer != "" {
			fmt.Printf("\n%s\n", e.Answer)
		}
	})
}

// streamer prints the answer as it grows. It prints only what is new, so if ChatGPT rewrites text
// it has already shown, the screen keeps the old wording; chat.md always holds the final answer.
type streamer struct {
	w     io.Writer
	shown string
}

func (s *streamer) update(text string) {
	if len(text) <= len(s.shown) {
		return
	}
	i := len(s.shown)
	if !strings.HasPrefix(text, s.shown) {
		// Start the tail on a whole character.
		for i < len(text) && !utf8.RuneStart(text[i]) {
			i++
		}
	}
	fmt.Fprint(s.w, text[i:])
	s.shown = text
}

// end closes the streamed text with a blank line, so the run's summary stands apart.
func (s *streamer) end() {
	if s.shown != "" {
		fmt.Fprint(s.w, "\n\n")
	}
}

// nonNil keeps an empty list as [] in JSON, not null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// chatURL keeps the session on the conversation it already has, and adopts the one ChatGPT opened otherwise.
func chatURL(got, had string) string {
	if had != "" || !strings.Contains(got, "/c/") || strings.Contains(got, "/c/local-") {
		return had
	}
	return got
}

// relAll writes paths relative to dir where it can, so the chat log reads the same on any machine.
func relAll(dir string, paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = p
		if abs, err := filepath.Abs(p); err == nil {
			if r, err := filepath.Rel(dir, abs); err == nil {
				out[i] = filepath.ToSlash(r)
			}
		}
	}
	return out
}

func session(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("session", flag.ContinueOnError)
	project := fs.String("project", ".", "project directory; memory lives in its design/chatgpt/")
	sessionID := fs.String("session", "", "session id (default: the agent's session)")
	out := fs.String("out", "", "export: the HTML file to write (default: session.html in the session folder)")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: chatgpt-design session [show|list|export [--out FILE]]")
		fs.PrintDefaults()
	}
	if err := parse(fs, args); err != nil {
		return err
	}
	verb := "show"
	if fs.NArg() > 0 {
		verb = fs.Arg(0)
		// Flags may follow the command too: session export --out x.html.
		if err := parse(fs, fs.Args()[1:]); err != nil {
			return err
		}
		if fs.NArg() > 0 {
			return usageError{fmt.Errorf("unexpected %q", fs.Arg(0))}
		}
	}
	if *out != "" && verb != "export" {
		return usageError{errors.New("--out goes with session export")}
	}
	p, err := memory.Open(*project)
	if err != nil {
		return err
	}
	switch verb {
	case "show":
		id, err := memory.SessionID(*sessionID, os.Getenv)
		if err != nil {
			return err
		}
		s, err := p.Session(id, time.Now())
		if err != nil {
			return err
		}
		chat := s.ChatURL
		if chat == "" {
			chat = "none yet (the next ask starts it)"
		}
		return emit(sessionJSON(s, true), func() {
			fmt.Println("session:", s.ID)
			fmt.Println("dir:", s.Dir())
			fmt.Println("chat:", chat)
			fmt.Println("asks:", s.Asks)
		})
	case "list":
		all, err := p.List()
		if err != nil {
			return err
		}
		cur, _ := memory.SessionID(*sessionID, os.Getenv)
		list := make([]sessionOut, len(all))
		for i, s := range all {
			list[i] = sessionJSON(s, s.ID == cur)
		}
		return emit(list, func() {
			for _, s := range all {
				mark := " "
				if s.ID == cur {
					mark = "*"
				}
				fmt.Printf("%s %s  %s  %d ask(s)  %s\n", mark, s.ID, s.Updated.Local().Format("2006-01-02 15:04"), s.Asks, s.ChatURL)
			}
		})
	case "export":
		id, err := memory.SessionID(*sessionID, os.Getenv)
		if err != nil {
			return err
		}
		s, err := p.Session(id, time.Now())
		if err != nil {
			return err
		}
		page, err := export.HTML(export.Session{ID: s.ID, Dir: s.Dir(), ChatURL: s.ChatURL, Asks: s.Asks, Created: s.Created, Updated: s.Updated})
		if err != nil {
			return err
		}
		if *out == "" {
			*out = filepath.Join(s.Dir(), "session.html")
		}
		if err := writeAtomic(*out, page); err != nil {
			return err
		}
		return emit(struct {
			File string `json:"file"`
		}{*out}, func() { fmt.Println(*out) })
	default:
		return usageError{fmt.Errorf("unknown session command %q, want show, list or export", verb)}
	}
}

type sessionOut struct {
	ID      string    `json:"id"`
	Dir     string    `json:"dir"`
	Chat    string    `json:"chat"`
	Asks    int       `json:"asks"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	Current bool      `json:"current"`
}

func sessionJSON(s *memory.Session, current bool) sessionOut {
	return sessionOut{s.ID, s.Dir(), s.ChatURL, s.Asks, s.Created, s.Updated, current}
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
	if errors.Is(err, os.ErrNotExist) {
		return "", usageError{fmt.Errorf("no brief at %s: write it, or pass --brief FILE", briefPath)}
	}
	if err != nil {
		return "", usageError{err}
	}
	brand := ""
	if brandPath != "" {
		if brand, err = readText(brandPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				err = fmt.Errorf("--brand %s: no such file", brandPath)
			}
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
		names := make([]string, len(shots))
		for i, s := range shots {
			names[i] = filepath.Base(s)
		}
		return nil, fmt.Errorf("%d screenshots (%s): ChatGPT takes at most %d per message; pick some with --shot, or delete old ones from the shots folder",
			len(shots), strings.Join(names, ", "), maxShots)
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
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("--shot %s: no such file", s)
		}
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
	fs.Var(&dirs, "dir", "skills directory (repeatable; default: the skills folder of each agent found, ~/.claude, ~/.codex, ~/.agents)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if len(dirs) == 0 {
		found, err := skillDirs()
		if err != nil {
			return err
		}
		dirs = found
	}
	for _, d := range dirs {
		target := filepath.Join(d, "chatgpt-design")
		if err := os.MkdirAll(target, 0o755); err != nil {
			return err
		}
		path := filepath.Join(target, "SKILL.md")
		if err := writeAtomic(path, skill.Markdown); err != nil {
			return err
		}
		fmt.Println(path)
	}
	return nil
}

// skillDirs lists the skills folder of each agent installed here, or Claude Code's when none is.
func skillDirs() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, agent := range []string{".claude", ".codex", ".agents"} {
		if fi, err := os.Stat(filepath.Join(home, agent)); err == nil && fi.IsDir() {
			out = append(out, filepath.Join(home, agent, "skills"))
		}
	}
	if len(out) == 0 {
		out = []string{filepath.Join(home, ".claude", "skills")}
	}
	return out, nil
}
