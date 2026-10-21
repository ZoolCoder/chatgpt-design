package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessions(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "web", "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := Open(sub)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "design", "chatgpt"); p.Root != want {
		t.Fatalf("root = %s, want %s", p.Root, want)
	}

	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	s, err := p.Session("b03c93e0-40e2", now)
	if err != nil {
		t.Fatal(err)
	}
	if s.Saved() || s.Dir() != filepath.Join(p.Root, "b03c93e0-40e2") {
		t.Fatalf("fresh session: saved %v, dir %s", s.Saved(), s.Dir())
	}
	if err := s.Record("https://chatgpt.com/c/abc", now); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	again, err := p.Session("b03c93e0-40e2", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if again.ChatURL != "https://chatgpt.com/c/abc" || again.Asks != 2 || !again.Created.Equal(now) {
		t.Fatalf("reloaded session = %+v", again)
	}
	again.NewChat()
	if again.ChatURL != "" {
		t.Fatal("NewChat kept the chat")
	}
	all, err := p.List()
	if err != nil || len(all) != 1 {
		t.Fatalf("list = %d, %v", len(all), err)
	}
}

func TestSessionID(t *testing.T) {
	env := map[string]string{"CLAUDE_CODE_SESSION_ID": "B03C93E0-40E2"}
	get := func(k string) string { return env[k] }
	if id, err := SessionID("", get); err != nil || id != "b03c93e0-40e2" {
		t.Errorf("from Claude Code: %q, %v", id, err)
	}
	env["CHATGPT_DESIGN_SESSION"] = "logo"
	if id, _ := SessionID("", get); id != "logo" {
		t.Errorf("override: %q", id)
	}
	if id, _ := SessionID("mine", get); id != "mine" {
		t.Errorf("flag: %q", id)
	}
	if _, err := SessionID("", func(string) string { return "" }); err != ErrNoSession {
		t.Errorf("none: %v", err)
	}
	for _, bad := range []string{"../x", "a/b", "-a"} {
		if _, err := SessionID(bad, get); err == nil {
			t.Errorf("SessionID(%q) worked", bad)
		}
	}
}

func TestAdoptSavesChatBeforeAnyAsk(t *testing.T) {
	p := &Project{Root: t.TempDir()}
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	s, err := p.Session("broken-run", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Adopt("https://chatgpt.com/c/xyz", now); err != nil {
		t.Fatal(err)
	}
	again, err := p.Session("broken-run", now)
	if err != nil {
		t.Fatal(err)
	}
	if again.ChatURL != "https://chatgpt.com/c/xyz" || again.Asks != 0 {
		t.Fatalf("after adopt: chat %q, asks %d", again.ChatURL, again.Asks)
	}
}

func TestSessionLock(t *testing.T) {
	p := &Project{Root: t.TempDir()}
	s, err := p.Session("abc", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := s.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lock(t.Context()); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("second lock: %v", err)
	}
	unlock()
	unlock, err = s.Lock(t.Context())
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	unlock()
}
