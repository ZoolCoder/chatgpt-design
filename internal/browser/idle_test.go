package browser

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIdleFor(t *testing.T) {
	tabs := `[{"type":"page","title":"chatgpt-design","url":"data:text/html,x"},{"type":"page","url":"about:blank"},{"type":"service_worker","url":"chrome-extension://x"}]`
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot listen on localhost:", err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/list" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(tabs))
	}))
	srv.Listener = ln
	srv.Start()
	defer srv.Close()
	ws := "ws://" + strings.TrimPrefix(srv.URL, "http://") + "/devtools/browser/x"
	profile := t.TempDir()

	if !idleFor(ws, profile, time.Minute) {
		t.Error("only anchors open and never used: want idle")
	}
	touch(profile)
	if idleFor(ws, profile, time.Minute) {
		t.Error("just used: want busy")
	}
	old := time.Now().Add(-2 * time.Minute)
	os.Chtimes(filepath.Join(profile, lastUsedFile), old, old)
	if !idleFor(ws, profile, time.Minute) {
		t.Error("used two minutes ago: want idle")
	}
	tabs = strings.Replace(tabs, `"url":"about:blank"`, `"url":"https://chatgpt.com/c/1"`, 1)
	if idleFor(ws, profile, time.Minute) {
		t.Error("a run's tab is open: want busy")
	}
	if idleFor("ws://127.0.0.1:1/x", profile, time.Minute) {
		t.Error("DevTools does not answer: want busy, never a blind stop")
	}
}
