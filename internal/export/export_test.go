package export

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTML(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "images"), 0o755)
	var img bytes.Buffer
	png.Encode(&img, image.NewGray(image.Rect(0, 0, 2, 2)))
	os.WriteFile(filepath.Join(dir, "images", "01.png"), img.Bytes(), 0o644)
	secret := filepath.Join(t.TempDir(), "secret.png")
	os.WriteFile(secret, img.Bytes(), 0o644)
	os.Symlink(secret, filepath.Join(dir, "images", "02.png"))
	os.Symlink(filepath.Dir(secret), filepath.Join(dir, "linked"))
	md := "\n---\n\n## 2026-10-21 10:00 · redesign\n\n" + detailsOpen + "\n\nMake it **calm**.\n\n" + detailsClose + "\n\n" +
		"The answer <script>alert(1)</script> with a [bad link](javascript:alert(1)).\n\n" +
		"![01.png](images/01.png)\n\n![02.png](images/02.png)\n\n![x](../" + filepath.Base(filepath.Dir(secret)) + "/secret.png)\n\n![y](linked/secret.png)\n"
	os.WriteFile(filepath.Join(dir, "chat.md"), []byte(md), 0o644)

	out, err := HTML(Session{ID: "s1", Dir: dir, ChatURL: "https://chatgpt.com/c/abc", Asks: 1})
	if err != nil {
		t.Fatal(err)
	}
	page := string(out)
	for _, want := range []string{"<details><summary>Prompt</summary>", "<strong>calm</strong>", `src="data:image/png;base64,`, `href="https://chatgpt.com/c/abc"`, "<title>Design session s1</title>"} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{"<script", "javascript:", `src="images/01.png"`} {
		if strings.Contains(page, bad) {
			t.Errorf("page has %q", bad)
		}
	}
	if n := strings.Count(page, "data:image/png"); n != 1 {
		t.Errorf("%d images inlined, want only the one inside the session (no symlink, no ../, no linked folder)", n)
	}
	if _, err := HTML(Session{Dir: t.TempDir()}); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty session: %v", err)
	}
}
