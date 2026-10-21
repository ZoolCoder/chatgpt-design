// Package output writes a run's answer to disk.
package output

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"

	"github.com/zoolcoder/chatgpt-design/internal/browser"
)

// Entry is one ask: what was sent and what came back.
type Entry struct {
	Mode     string
	At       time.Time
	Shots    []string
	Images   []string
	Warnings []string
	Answer   string // the answer as markdown, without images
}

var conv = converter.NewConverter(converter.WithPlugins(
	base.NewBasePlugin(),
	commonmark.NewCommonmarkPlugin(),
	strikethrough.NewStrikethroughPlugin(),
	table.NewTablePlugin(),
))

// escapedNumber matches "1\." at a line start: ChatGPT writes numbered steps as paragraphs, and the
// converter escapes them so they do not become a list. They read as a list, so drop the escape.
var escapedNumber = regexp.MustCompile(`(?m)^(\d+)\\\.`)

// Markdown converts the answer's HTML, falling back to its plain text.
func Markdown(r *browser.Result) string {
	if md, err := conv.ConvertString(r.HTML); err == nil && strings.TrimSpace(md) != "" {
		return escapedNumber.ReplaceAllString(strings.TrimSpace(md), "$1.") + "\n"
	}
	return strings.TrimSpace(r.Text) + "\n"
}

var verdict = regexp.MustCompile(`(?m)^#{1,6}\s*Verdict\b`)

// HasVerdict reports whether a spec answer kept the required first section.
func HasVerdict(md string) bool { return verdict.MatchString(md) }

// Save appends the prompt and the answer to dir/chat.md and writes the images to dir/images,
// numbered after the ones already there. Paths in the entry are relative to dir.
func Save(dir, prompt string, r *browser.Result, e Entry) (Entry, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return e, err
	}
	md := Markdown(r)
	e.Answer = strings.TrimSpace(md)
	if e.Mode == "spec" && !HasVerdict(md) {
		e.Warnings = append(e.Warnings, "the answer has no Verdict heading: ChatGPT did not follow the spec format")
	}
	if len(r.Images) > 0 && strings.TrimSpace(md) == "" {
		e.Warnings = append(e.Warnings, "the answer has images but no text")
	}
	if len(r.Images) > 0 {
		imgDir := filepath.Join(dir, "images")
		if err := os.MkdirAll(imgDir, 0o755); err != nil {
			return e, err
		}
		n, err := lastNumber(imgDir)
		if err != nil {
			return e, err
		}
		for _, im := range r.Images {
			name, err := writeNew(imgDir, &n, ext(im.Type), im.Data)
			if err != nil {
				return e, err
			}
			e.Images = append(e.Images, "images/"+name)
		}
	}
	f, err := os.OpenFile(filepath.Join(dir, "chat.md"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return e, err
	}
	defer f.Close()
	_, err = f.WriteString(entryMarkdown(prompt, md, e))
	return e, err
}

func entryMarkdown(prompt, answer string, e Entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n---\n\n## %s · %s\n\n", e.At.Local().Format("2006-01-02 15:04"), e.Mode)
	if len(e.Shots) > 0 {
		fmt.Fprintf(&b, "Sent: %s\n\n", strings.Join(e.Shots, ", "))
	}
	fmt.Fprintf(&b, "<details><summary>Prompt</summary>\n\n%s\n\n</details>\n\n", strings.TrimSpace(prompt))
	if a := strings.TrimSpace(answer); a != "" {
		b.WriteString(a + "\n\n")
	}
	for _, im := range e.Images {
		fmt.Fprintf(&b, "![%s](%s)\n\n", filepath.Base(im), im)
	}
	return b.String()
}

// lastNumber is the highest NN among dir's NN.ext files, so a deleted image never makes the next
// one reuse a number that is still taken.
func lastNumber(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		base := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if v, err := strconv.Atoi(base); err == nil && v > n {
			n = v
		}
	}
	return n, nil
}

// writeNew writes data to the next free NN.ext after *n and never replaces a file.
func writeNew(dir string, n *int, ext string, data []byte) (string, error) {
	for {
		*n++
		name := fmt.Sprintf("%02d%s", *n, ext)
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return name, err
	}
}

func ext(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/jpeg"):
		return ".jpg"
	case strings.HasPrefix(mime, "image/webp"):
		return ".webp"
	case strings.HasPrefix(mime, "image/gif"):
		return ".gif"
	}
	return ".png"
}
