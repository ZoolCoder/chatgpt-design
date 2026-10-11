package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShotTargets(t *testing.T) {
	got, err := shotTargets("http://localhost:3000/", []string{"home=/", "inbox=inbox?x=1"})
	if err != nil || len(got) != 2 || got[0].url != "http://localhost:3000/" || got[1].url != "http://localhost:3000/inbox?x=1" {
		t.Fatalf("%+v %v", got, err)
	}
	for _, bad := range [][]string{{"../x=/"}, {"=/"}, {"home"}} {
		if _, err := shotTargets("http://x", bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if _, err := shotTargets("file:///etc", []string{"a=/"}); err == nil {
		t.Error("non-http base accepted")
	}
}

func TestCheckShots(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "01-a.png")
	os.WriteFile(ok, []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	found, _ := findShots(dir)
	if len(found) != 1 || found[0] != ok {
		t.Errorf("findShots = %v", found)
	}
	if _, err := checkShots([]string{filepath.Join(dir, "notes.txt")}); err == nil {
		t.Error("text file accepted")
	}
	if _, err := checkShots(make([]string, maxShots+1)); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("too many: %v", err)
	}
}

func TestBuildPromptChat(t *testing.T) {
	p := filepath.Join(t.TempDir(), "p.txt")
	os.WriteFile(p, []byte("hello"), 0o644)
	if got, err := buildPrompt("chat", "", "", p, "", nil); err != nil || got != "hello" {
		t.Errorf("%q %v", got, err)
	}
	if _, err := buildPrompt("spec", "b.md", "", p, "", nil); err == nil {
		t.Error("--prompt with spec accepted")
	}
}

func TestBuildPromptRejects(t *testing.T) {
	big := filepath.Join(t.TempDir(), "big.txt")
	os.WriteFile(big, make([]byte, maxTextBytes+1), 0o644)
	for name, err := range map[string]error{
		"too big":      func() error { _, err := buildPrompt("chat", "", "", big, "", nil); return err }(),
		"missing file": func() error { _, err := buildPrompt("chat", "", "", "/nonexistent", "", nil); return err }(),
		"unknown mode": func() error { _, err := buildPrompt("poster", "b.md", "", "", "", nil); return err }(),
	} {
		var u usageError
		if !errors.As(err, &u) {
			t.Errorf("%s: want a usage error, got %v", name, err)
		}
	}
}

func TestChatURL(t *testing.T) {
	for _, c := range []struct{ got, had, want string }{
		{"https://chatgpt.com/c/new", "", "https://chatgpt.com/c/new"},
		{"https://chatgpt.com/", "", ""},
		{"https://chatgpt.com/c/other", "https://chatgpt.com/c/old", "https://chatgpt.com/c/old"},
	} {
		if got := chatURL(c.got, c.had); got != c.want {
			t.Errorf("chatURL(%q, %q) = %q, want %q", c.got, c.had, got, c.want)
		}
	}
}
