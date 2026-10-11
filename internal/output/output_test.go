package output

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/chatgpt-design/internal/browser"
)

var png = []byte("\x89PNG\r\n\x1a\n0000")

func TestSave(t *testing.T) {
	dir := t.TempDir()
	r := &browser.Result{
		HTML:   `<h2>Verdict</h2><p>Works.</p><table><thead><tr><th>Screen</th><th>Fix</th></tr></thead><tbody><tr><td>Inbox</td><td>Fewer options</td></tr></tbody></table>`,
		Images: []browser.Image{{Data: png, Type: "image/png"}},
		URL:    "https://chatgpt.com/c/abc",
	}
	e, err := Save(dir, "the prompt", r, Entry{Mode: "spec", At: time.Now(), Shots: []string{"shots/01-home.png"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Warnings) != 0 || len(e.Images) != 1 || e.Images[0] != "images/01.png" {
		t.Errorf("entry: %+v", e)
	}
	e, err = Save(dir, "second", &browser.Result{HTML: "<p>More.</p>", Images: []browser.Image{{Data: png, Type: "image/jpeg"}}}, Entry{Mode: "redesign", At: time.Now()})
	if err != nil || len(e.Images) != 1 || e.Images[0] != "images/02.jpg" {
		t.Fatalf("second entry: %+v, %v", e, err)
	}
	md, _ := os.ReadFile(filepath.Join(dir, "chat.md"))
	for _, want := range []string{"## Verdict", "| Inbox  | Fewer options |", "Sent: shots/01-home.png", "the prompt", "![01.png](images/01.png)", "More.", "![02.jpg](images/02.jpg)"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("chat.md lacks %q:\n%s", want, md)
		}
	}
}

func TestSaveWarns(t *testing.T) {
	e, err := Save(t.TempDir(), "p", &browser.Result{HTML: "<p>Looks fine to me.</p>"}, Entry{Mode: "spec"})
	if err != nil || len(e.Warnings) != 1 {
		t.Errorf("no verdict: warnings %v, err %v", e.Warnings, err)
	}
	e, err = Save(t.TempDir(), "p", &browser.Result{Images: []browser.Image{{Data: png}}}, Entry{Mode: "redesign"})
	if err != nil || len(e.Warnings) != 1 {
		t.Errorf("image only: warnings %v, err %v", e.Warnings, err)
	}
}

func TestMarkdownNumberedParagraphs(t *testing.T) {
	got := Markdown(&browser.Result{HTML: "<p>1. First step.</p><p>2. Second step.</p>"})
	if got != "1. First step.\n\n2. Second step.\n" {
		t.Errorf("%q", got)
	}
}
