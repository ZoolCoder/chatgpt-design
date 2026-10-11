// Package output writes a run's answer to disk.
package output

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"

	"github.com/zoolcoder/chatgpt-design/internal/browser"
)

// Meta records where an answer came from.
type Meta struct {
	Mode     string    `json:"mode"`
	URL      string    `json:"url"`
	Created  time.Time `json:"created"`
	Shots    []string  `json:"shots,omitempty"`
	Images   []string  `json:"images,omitempty"`
	Response string    `json:"response"`
	Warnings []string  `json:"warnings,omitempty"`
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

// Save writes prompt.txt, response.md, images/NN.ext and meta.json under dir.
func Save(dir, prompt string, r *browser.Result, m Meta) (Meta, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return m, err
	}
	if err := os.WriteFile(filepath.Join(dir, "prompt.txt"), []byte(prompt), 0o644); err != nil {
		return m, err
	}
	md := Markdown(r)
	if m.Mode == "spec" && !HasVerdict(md) {
		m.Warnings = append(m.Warnings, "the answer has no Verdict heading: ChatGPT did not follow the spec format")
	}
	m.Response = filepath.Join(dir, "response.md")
	if err := os.WriteFile(m.Response, []byte(md), 0o644); err != nil {
		return m, err
	}
	if len(r.Images) > 0 {
		imgDir := filepath.Join(dir, "images")
		if err := os.MkdirAll(imgDir, 0o755); err != nil {
			return m, err
		}
		for i, im := range r.Images {
			p := filepath.Join(imgDir, fmt.Sprintf("%02d%s", i+1, ext(im.Type)))
			if err := os.WriteFile(p, im.Data, 0o644); err != nil {
				return m, err
			}
			m.Images = append(m.Images, p)
		}
	}
	m.URL, m.Created = r.URL, time.Now().UTC().Truncate(time.Second)
	b, _ := json.MarshalIndent(m, "", "  ")
	return m, os.WriteFile(filepath.Join(dir, "meta.json"), append(b, '\n'), 0o644)
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
