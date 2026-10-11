package browser

import "testing"

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
