package main

import (
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
