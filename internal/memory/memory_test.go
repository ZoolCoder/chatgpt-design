package memory

import (
	"errors"
	"os"
	"path/filepath"
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
	if _, err := p.Current(); !errors.Is(err, ErrNoSession) {
		t.Fatalf("current before any session: %v", err)
	}

	day := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	a, err := p.New("Simple onboarding!", day)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "2026-10-12-simple-onboarding" {
		t.Fatalf("id = %s", a.ID)
	}
	if err := a.AddRound(Round{Mode: "spec", Response: "rounds/01/response.md"}, "https://chatgpt.com/c/abc"); err != nil {
		t.Fatal(err)
	}
	if err := a.AddRound(Round{Mode: "redesign"}, ""); err != nil {
		t.Fatal(err)
	}

	b, err := p.New("simple onboarding", day)
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != "2026-10-12-simple-onboarding-2" {
		t.Fatalf("second id = %s", b.ID)
	}
	if cur, _ := p.Current(); cur.ID != b.ID {
		t.Fatalf("new session is not current: %s", cur.ID)
	}

	if err := p.Use(a.ID); err != nil {
		t.Fatal(err)
	}
	cur, err := p.Current()
	if err != nil {
		t.Fatal(err)
	}
	if cur.ChatURL != "https://chatgpt.com/c/abc" || len(cur.Rounds) != 2 || cur.Rounds[1].N != 2 {
		t.Fatalf("reloaded session = %+v", cur)
	}
	if got := cur.NextRoundDir(); got != filepath.Join(a.Dir(), "rounds", "03") {
		t.Fatalf("next round dir = %s", got)
	}
	all, err := p.List()
	if err != nil || len(all) != 2 {
		t.Fatalf("list = %d, %v", len(all), err)
	}
}

func TestRejectsBadIDs(t *testing.T) {
	p := &Project{Root: t.TempDir()}
	for _, id := range []string{"../x", "", "A", "a/b"} {
		if err := p.Use(id); err == nil {
			t.Errorf("Use(%q) worked", id)
		}
	}
	if _, err := p.New("!!!", time.Now()); err == nil {
		t.Error("New with an empty slug worked")
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Dashboard redesign": "dashboard-redesign",
		"  --Logo v2--  ":    "logo-v2",
		"Ünïcode only":       "n-code-only",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}
