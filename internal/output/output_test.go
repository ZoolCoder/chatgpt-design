package output

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	m, err := Save(dir, "the prompt", r, Meta{Mode: "spec"})
	if err != nil {
		t.Fatal(err)
	}
	md, _ := os.ReadFile(m.Response)
	for _, want := range []string{"## Verdict", "| Inbox  | Fewer options |"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("response.md lacks %q:\n%s", want, md)
		}
	}
	if len(m.Warnings) != 0 || len(m.Images) != 1 || m.Images[0] != filepath.Join(dir, "images", "01.png") {
		t.Errorf("meta: %+v", m)
	}
	for _, f := range []string{"prompt.txt", "meta.json", "images/01.png"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Error(err)
		}
	}
}

func TestSaveWarnsWithoutVerdict(t *testing.T) {
	m, err := Save(t.TempDir(), "p", &browser.Result{HTML: "<p>Looks fine to me.</p>"}, Meta{Mode: "spec"})
	if err != nil || len(m.Warnings) != 1 {
		t.Errorf("warnings %v, err %v", m.Warnings, err)
	}
}
