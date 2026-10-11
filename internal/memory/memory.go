// Package memory keeps a project's ChatGPT sessions on disk: one ChatGPT chat per session, every
// round of it saved, so follow-ups continue the same chat until a new session starts.
//
//	<repo>/design/chatgpt/
//	  project.json                current session id
//	  sessions/<id>/session.json  chat URL and rounds
//	  sessions/<id>/brief.md, shots/
//	  sessions/<id>/rounds/NN/    prompt.txt, response.md, images/, meta.json
package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Dir is where a project's memory lives, relative to its root.
const Dir = "design/chatgpt"

// ErrNoSession means the project has no current session yet.
var ErrNoSession = errors.New("no current session: start one with `chatgpt-design session new TOPIC` or `ask --new --topic TOPIC`")

// Project is one repository's memory.
type Project struct{ Root string }

// Round is one message sent in a session and what came back.
type Round struct {
	N        int       `json:"n"`
	Mode     string    `json:"mode"`
	At       time.Time `json:"at"`
	Dir      string    `json:"dir"`
	Response string    `json:"response"`
	Images   []string  `json:"images,omitempty"`
	Shots    []string  `json:"shots,omitempty"`
	Warnings []string  `json:"warnings,omitempty"`
}

// Session is one ChatGPT chat about one topic.
type Session struct {
	ID      string    `json:"id"`
	Topic   string    `json:"topic"`
	Created time.Time `json:"created"`
	ChatURL string    `json:"chat_url,omitempty"`
	Rounds  []Round   `json:"rounds"`

	dir string
}

type state struct {
	Current string `json:"current"`
}

// Open finds the project that holds dir: the nearest ancestor with .git, or dir itself.
func Open(dir string) (*Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	for d := abs; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			abs = d
			break
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	return &Project{Root: filepath.Join(abs, filepath.FromSlash(Dir))}, nil
}

func (p *Project) sessionsDir() string { return filepath.Join(p.Root, "sessions") }

// Current returns the session that follow-ups go to.
func (p *Project) Current() (*Session, error) {
	var st state
	if err := readJSON(filepath.Join(p.Root, "project.json"), &st); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoSession
		}
		return nil, err
	}
	if st.Current == "" {
		return nil, ErrNoSession
	}
	return p.Session(st.Current)
}

// Session loads one session by id.
func (p *Project) Session(id string) (*Session, error) {
	if !validID.MatchString(id) {
		return nil, fmt.Errorf("%q is not a session id", id)
	}
	s := &Session{dir: filepath.Join(p.sessionsDir(), id)}
	if err := readJSON(filepath.Join(s.dir, "session.json"), s); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no session %q", id)
		}
		return nil, err
	}
	return s, nil
}

// New starts a session named after topic and makes it current.
func (p *Project) New(topic string, now time.Time) (*Session, error) {
	slug := Slug(topic)
	if slug == "" {
		return nil, errors.New("the topic needs at least one letter or digit")
	}
	base := now.Format("2006-01-02") + "-" + slug
	id := base
	for i := 2; ; i++ {
		if _, err := os.Stat(filepath.Join(p.sessionsDir(), id)); errors.Is(err, os.ErrNotExist) {
			break
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
	s := &Session{ID: id, Topic: topic, Created: now.UTC().Truncate(time.Second), Rounds: []Round{}, dir: filepath.Join(p.sessionsDir(), id)}
	if err := os.MkdirAll(filepath.Join(s.dir, "shots"), 0o755); err != nil {
		return nil, err
	}
	if err := s.save(); err != nil {
		return nil, err
	}
	return s, p.Use(id)
}

// Use makes an existing session current.
func (p *Project) Use(id string) error {
	if _, err := p.Session(id); err != nil {
		return err
	}
	return writeJSON(filepath.Join(p.Root, "project.json"), state{Current: id})
}

// List returns every session, oldest first.
func (p *Project) List() ([]*Session, error) {
	entries, err := os.ReadDir(p.sessionsDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Session
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if s, err := p.Session(e.Name()); err == nil {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b *Session) int { return a.Created.Compare(b.Created) })
	return out, nil
}

// Dir is the session's directory.
func (s *Session) Dir() string { return s.dir }

// NextRoundDir is where the next round's files go.
func (s *Session) NextRoundDir() string {
	return filepath.Join(s.dir, "rounds", fmt.Sprintf("%02d", len(s.Rounds)+1))
}

// AddRound records a finished round and the chat it continued or started.
func (s *Session) AddRound(r Round, chatURL string) error {
	r.N = len(s.Rounds) + 1
	s.Rounds = append(s.Rounds, r)
	if chatURL != "" {
		s.ChatURL = chatURL
	}
	return s.save()
}

func (s *Session) save() error { return writeJSON(filepath.Join(s.dir, "session.json"), s) }

var (
	validID  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	nonSlug  = regexp.MustCompile(`[^a-z0-9]+`)
	maxSlugN = 48
)

// Slug turns a topic into a short kebab-case name.
func Slug(topic string) string {
	s := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(topic), "-"), "-")
	if len(s) > maxSlugN {
		s = strings.TrimRight(s[:maxSlugN], "-")
	}
	return s
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// writeJSON replaces path atomically, so an interrupted run never leaves half a file.
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
