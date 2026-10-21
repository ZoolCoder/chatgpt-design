// Package export turns a session's chat log into one HTML file that opens anywhere.
package export

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Session is what the page header shows.
type Session struct {
	ID, Dir, ChatURL string
	Asks             int
	Created, Updated time.Time
}

// ErrEmpty means the session has no asks to export.
var ErrEmpty = errors.New("nothing to export: the session has no asks yet")

const maxImage = 50 << 20

var imageExts = []string{".png", ".jpg", ".jpeg", ".webp", ".gif"}

// The chat log wraps each prompt in <details>. Raw HTML never reaches the page, since answers
// could carry any, so these exact lines pass through as markers instead.
const (
	detailsOpen  = "<details><summary>Prompt</summary>"
	detailsClose = "</details>"
	markOpen     = "CGDEXPORTDETAILSOPEN"
	markClose    = "CGDEXPORTDETAILSCLOSE"
)

// HTML renders dir/chat.md with its images inlined. Only images inside the session folder are
// inlined, so a link in an answer can never pull another file into a page meant for sharing.
func HTML(s Session) ([]byte, error) {
	md, err := os.ReadFile(filepath.Join(s.Dir, "chat.md"))
	if errors.Is(err, os.ErrNotExist) || err == nil && len(bytes.TrimSpace(md)) == 0 {
		return nil, ErrEmpty
	}
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(md), "\n")
	for i, l := range lines {
		switch l {
		case detailsOpen:
			lines[i] = markOpen
		case detailsClose:
			lines[i] = markClose
		}
	}
	gm := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(inliner{s.Dir}, 100))),
	)
	var body bytes.Buffer
	if err := gm.Convert([]byte(strings.Join(lines, "\n")), &body); err != nil {
		return nil, err
	}
	page := strings.NewReplacer(
		"<p>"+markOpen+"</p>", detailsOpen,
		"<p>"+markClose+"</p>", detailsClose,
	).Replace(body.String())

	var b bytes.Buffer
	title := "Design session " + s.ID
	fmt.Fprintf(&b, head, html.EscapeString(title))
	fmt.Fprintf(&b, "<header><h1>%s</h1><p>", html.EscapeString(title))
	if strings.HasPrefix(s.ChatURL, "https://chatgpt.com/") {
		fmt.Fprintf(&b, `<a href="%s">ChatGPT chat</a> · `, html.EscapeString(s.ChatURL))
	}
	fmt.Fprintf(&b, "%d ask(s) · %s to %s</p></header>\n<main>\n%s</main>\n</body>\n</html>\n",
		s.Asks, s.Created.Local().Format("2006-01-02 15:04"), s.Updated.Local().Format("2006-01-02 15:04"), page)
	return b.Bytes(), nil
}

// inliner swaps each image link for the image itself, as a data URL.
type inliner struct{ dir string }

func (in inliner) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if img, ok := n.(*ast.Image); ok && entering {
			if data, ok := in.dataURL(string(img.Destination)); ok {
				img.Destination = []byte(data)
			}
		}
		return ast.WalkContinue, nil
	})
}

func (in inliner) dataURL(link string) (string, bool) {
	clean := path.Clean(link)
	if strings.Contains(link, ":") || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	if !slices.Contains(imageExts, strings.ToLower(path.Ext(clean))) {
		return "", false
	}
	// Resolve links on the way: a link planted in the folder must not lead out of it.
	root, err := filepath.EvalSymlinks(in.dir)
	if err != nil {
		return "", false
	}
	file, err := filepath.EvalSymlinks(filepath.Join(in.dir, filepath.FromSlash(clean)))
	if err != nil {
		return "", false
	}
	if rel, err := filepath.Rel(root, file); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	fi, err := os.Stat(file)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxImage {
		return "", false
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", false
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return "", false
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), true
}

const head = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
:root { --bg: #fbfbfa; --fg: #1d1d1f; --muted: #6e6e73; --line: #e3e3e0; --card: #ffffff; --accent: #0a66c2; }
@media (prefers-color-scheme: dark) {
  :root { --bg: #161617; --fg: #ececec; --muted: #a1a1a6; --line: #333336; --card: #1f1f21; --accent: #6cb0ff; }
}
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--fg); font: 16px/1.6 system-ui, -apple-system, "Segoe UI", sans-serif; }
header, main { max-width: 880px; margin: 0 auto; padding: 0 16px; }
header { padding-top: 40px; }
header h1 { margin: 0 0 4px; font-size: 1.6rem; }
header p { margin: 0; color: var(--muted); }
a { color: var(--accent); }
hr { border: 0; border-top: 1px solid var(--line); margin: 48px 0 24px; }
h2 { font-size: 1.25rem; }
img { max-width: 100%%; height: auto; border-radius: 8px; border: 1px solid var(--line); }
details { background: var(--card); border: 1px solid var(--line); border-radius: 8px; padding: 8px 16px; margin: 16px 0; }
summary { cursor: pointer; color: var(--muted); }
pre { overflow-x: auto; background: var(--card); border: 1px solid var(--line); border-radius: 8px; padding: 12px; }
code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.9em; }
table { border-collapse: collapse; display: block; overflow-x: auto; }
th, td { border: 1px solid var(--line); padding: 6px 10px; text-align: left; }
main { padding-bottom: 64px; }
</style>
</head>
<body>
`
