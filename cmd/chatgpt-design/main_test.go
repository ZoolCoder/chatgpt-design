package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zoolcoder/chatgpt-design/internal/browser"
)

func TestShotTargets(t *testing.T) {
	got, err := shotTargets("http://localhost:3000/", []string{"home=/", "inbox=inbox?x=1"})
	if err != nil || len(got) != 2 || got[0].url != "http://localhost:3000/" || got[1].url != "http://localhost:3000/inbox?x=1" {
		t.Fatalf("%+v %v", got, err)
	}
	for _, bad := range [][]string{{"../x=/"}, {"=/"}, {"home"}, {"a=/", "a=/b"}} {
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

func TestShotPaths(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"01-home.png", "03-pricing.png", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := shotPaths(dir, []string{"pricing", "about", "home", "faq"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"03-pricing.png", "04-about.png", "01-home.png", "05-faq.png"}
	for i, w := range want {
		if filepath.Base(got[i]) != w {
			t.Errorf("shotPaths[%d] = %s, want %s", i, filepath.Base(got[i]), w)
		}
	}
	if got, _ := shotPaths(filepath.Join(dir, "missing"), []string{"home"}); filepath.Base(got[0]) != "01-home.png" {
		t.Errorf("empty dir: %v", got)
	}
}

func TestExitCode(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{fmt.Errorf("ask: %w", browser.ErrUsageLimit), exitLimit},
		{fmt.Errorf("x: %w", browser.ErrNotLoggedIn), exitLogin},
		{usageError{errors.New("bad flag")}, exitUsage},
		{quietError{exitLogin}, exitLogin},
		{errors.New("usage limit in some text"), exitErr},
	} {
		if got := exitCode(tc.err); got != tc.want {
			t.Errorf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestStreamer(t *testing.T) {
	var b strings.Builder
	s := streamer{w: &b}
	for _, text := range []string{"Hel", "Hello", "Hello", "Hello wo", "Hullo world", "Hullo"} {
		s.update(text)
	}
	s.end()
	if got := b.String(); got != "Hello wo"+"rld"+"\n\n" {
		t.Errorf("streamed %q", got)
	}
	var quiet strings.Builder
	(&streamer{w: &quiet}).end()
	if quiet.Len() != 0 {
		t.Errorf("empty stream printed %q", quiet.String())
	}
}

func TestSizes(t *testing.T) {
	got, err := parseSizes("390, 1440")
	if err != nil || !slices.Equal(got, []int{390, 1440}) {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{"", "390,x", "390,390"} {
		if _, err := parseSizes(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	pages := atSizes([]target{{name: "home", url: "u"}}, got)
	if len(pages) != 2 || pages[0].name != "home@390" || pages[1].width != 1440 {
		t.Errorf("%+v", pages)
	}
	if _, err := shotTargets("http://x", []string{"a@1=/"}); err == nil {
		t.Error("@ in a page name accepted")
	}
}
