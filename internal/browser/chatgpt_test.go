package browser

import (
	"strings"
	"testing"
)

func TestAllowedImageHost(t *testing.T) {
	for u, want := range map[string]bool{
		"https://chatgpt.com/backend-api/estuary/content?id=1": true,
		"https://files.oaiusercontent.com/file-1?se=x":         true,
		"https://cdn.openai.com/a.png":                         true,
		"http://chatgpt.com/a.png":                             false,
		"https://evilchatgpt.com/a.png":                        false,
		"https://chatgpt.com.evil.test/a.png":                  false,
		"file:///etc/passwd":                                   false,
	} {
		if got := allowedImageHost(u); got != want {
			t.Errorf("allowedImageHost(%s) = %v", u, got)
		}
	}
}

func TestDecodeDataURL(t *testing.T) {
	b, err := decodeDataURL("data:image/png;base64,aGk=")
	if err != nil || string(b) != "hi" {
		t.Errorf("%q %v", b, err)
	}
	if _, err := decodeDataURL("data:text/plain,hi"); err == nil {
		t.Error("non-base64 data URL accepted")
	}
}

func TestScriptsHaveNoPlaceholders(t *testing.T) {
	for name, src := range map[string]string{"state": stateJS, "answer": answerJS, "baseline": baselineJS, "turns": js(`ASSISTANT_TURNS.map(t => BODIES(t))`)} {
		for _, p := range []string{"COMPOSER", "SEND", "STOP", "USER_TURNS", "ASSISTANT_TURNS", "BODIES(", "GENERATED"} {
			if strings.Contains(src, p) {
				t.Errorf("%s script still has %s", name, p)
			}
		}
	}
}

func TestIsChatURL(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://chatgpt.com/c/6acc070e":       true,
		"https://chatgpt.com/":                 false,
		"http://chatgpt.com/c/x":               false,
		"https://evil.example/c/x":             false,
		"https://chatgpt.com.evil.example/c/x": false,
	} {
		if got := isChatURL(raw); got != want {
			t.Errorf("isChatURL(%q) = %v", raw, got)
		}
	}
}
