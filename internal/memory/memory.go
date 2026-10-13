// Package memory keeps a project's ChatGPT sessions on disk. A session is the agent session that
// runs the tool (Claude Code's session id, or one you name), so follow-ups continue the same ChatGPT
// chat until the agent session ends or you ask for a new chat.
//
//	<repo>/design/chatgpt/<session>/
//	  session.json   chat URL, when it started, how many asks
//	  chat.md        every prompt and answer, newest last
//	  images/        every image ChatGPT made, numbered across the session
//	  shots/         screenshots to send
//	  brief.md       optional brief
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

// SessionEnv names the variables that carry a session id, most specific first.
var SessionEnv = []string{"CHATGPT_DESIGN_SESSION", "CLAUDE_CODE_SESSION_ID"}

// ErrNoSession means no session id was given and the agent did not provide one.
var ErrNoSession = errors.New("no session: pass --session ID or set CHATGPT_DESIGN_SESSION (Claude Code sets one by itself)")

// Project is one repository's memory.
type Project struct{ Root string }

// Session is one agent session's ChatGPT chat and files.
type Session struct {
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	ChatURL string    `json:"chat_url,omitempty"`
	Asks    int       `json:"asks"`

	dir string
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

// SessionID picks the session: the flag, else the first set variable of SessionEnv.
func SessionID(flag string, getenv func(string) string) (string, error) {
	id := flag
	for _, k := range SessionEnv {
		if id != "" {
			break
		}
		id = getenv(k)
	}
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return "", ErrNoSession
	}
	if !validID.MatchString(id) {
		return "", fmt.Errorf("%q is not a session id: use letters, digits and dashes", id)
	}
	return id, nil
}

// Session loads a session, or returns a new unsaved one when it has no files yet.
func (p *Project) Session(id string, now time.Time) (*Session, error) {
	if !validID.MatchString(id) {
		return nil, fmt.Errorf("%q is not a session id", id)
	}
	s := &Session{dir: filepath.Join(p.Root, id)}
	err := readJSON(filepath.Join(s.dir, "session.json"), s)
	if errors.Is(err, os.ErrNotExist) {
		t := now.UTC().Truncate(time.Second)
		return &Session{ID: id, Created: t, Updated: t, dir: s.dir}, nil
	}
	return s, err
}

// List returns every saved session, oldest first.
func (p *Project) List() ([]*Session, error) {
	entries, err := os.ReadDir(p.Root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Session
	for _, e := range entries {
		if !e.IsDir() || !validID.MatchString(e.Name()) {
			continue
		}
		s := &Session{dir: filepath.Join(p.Root, e.Name())}
		if readJSON(filepath.Join(s.dir, "session.json"), s) == nil {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b *Session) int { return a.Created.Compare(b.Created) })
	return out, nil
}

// Dir is the session's directory.
func (s *Session) Dir() string { return s.dir }

// Saved reports whether the session has been written to disk.
func (s *Session) Saved() bool {
	_, err := os.Stat(filepath.Join(s.dir, "session.json"))
	return err == nil
}

// NewChat makes the next ask start a fresh ChatGPT chat in the same session.
func (s *Session) NewChat() { s.ChatURL = "" }

// Adopt saves the chat the session continues, before any ask has finished, so a broken run can be collected.
func (s *Session) Adopt(chatURL string, now time.Time) error {
	if chatURL == "" || chatURL == s.ChatURL {
		return nil
	}
	s.ChatURL = chatURL
	s.Updated = now.UTC().Truncate(time.Second)
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return writeJSON(filepath.Join(s.dir, "session.json"), s)
}

// Record counts a finished ask and keeps the chat it continued or started.
func (s *Session) Record(chatURL string, now time.Time) error {
	s.Asks++
	s.Updated = now.UTC().Truncate(time.Second)
	if chatURL != "" {
		s.ChatURL = chatURL
	}
	return writeJSON(filepath.Join(s.dir, "session.json"), s)
}

var validID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

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
