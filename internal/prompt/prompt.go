// Package prompt builds the message sent to ChatGPT for each mode.
package prompt

import (
	"embed"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed templates/*.txt
var templates embed.FS

// NoBrand stands in for a missing brand sheet.
const NoBrand = "No brand sheet provided — propose one."

// Modes lists the built-in templates.
var Modes = []string{"spec", "redesign", "logo"}

// Input fills a template.
type Input struct {
	Brief string
	Brand string
	Shots []string // file paths, in upload order
	Goal  string   // optional extra instruction, appended last
}

// Build returns the prompt for mode.
func Build(mode string, in Input) (string, error) {
	raw, err := templates.ReadFile("templates/" + mode + ".txt")
	if err != nil {
		return "", fmt.Errorf("unknown mode %q, want one of %s", mode, strings.Join(Modes, ", "))
	}
	if mode != "logo" && len(in.Shots) == 0 {
		return "", fmt.Errorf("mode %s needs at least one screenshot", mode)
	}
	brand := strings.TrimSpace(in.Brand)
	if brand == "" {
		brand = NoBrand
	}
	out := strings.NewReplacer(
		"{{BRIEF}}", strings.TrimSpace(in.Brief),
		"{{BRAND}}", brand,
		"{{SHOT_LIST}}", ShotList(in.Shots),
		"{{SCREEN_COUNT}}", strconv.Itoa(len(in.Shots)),
	).Replace(string(raw))
	if g := strings.TrimSpace(in.Goal); g != "" {
		out = strings.TrimRight(out, "\n") + "\n\nGOAL FOR THIS ROUND\n" + g + "\n"
	}
	return out, nil
}

// ShotList is one line per screenshot: its file name and the screen it shows.
func ShotList(shots []string) string {
	lines := make([]string, len(shots))
	for i, s := range shots {
		lines[i] = fmt.Sprintf("%d. %s: %s", i+1, filepath.Base(s), Label(s))
	}
	return strings.Join(lines, "\n")
}

// Label turns "02-inbox-connect.png" into "inbox connect".
func Label(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if i := strings.IndexByte(name, '-'); i > 0 && strings.Trim(name[:i], "0123456789") == "" {
		name = name[i+1:]
	}
	return strings.NewReplacer("-", " ", "_", " ").Replace(name)
}
