package prompt

import (
	"strings"
	"testing"
)

func TestBuildSpec(t *testing.T) {
	got, err := Build("spec", Input{Brief: "A job tracker.\n", Shots: []string{"/x/01-overview.png", "/x/02-inbox_connect.jpg"}, Goal: "simpler setup"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"attached\n2 screenshots", "A job tracker.", NoBrand, "1. 01-overview.png: overview", "2. 02-inbox_connect.jpg: inbox connect", "## Verdict", "GOAL FOR THIS ROUND\nsimpler setup"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if strings.Contains(got, "{{") {
		t.Error("a token was left in the prompt")
	}
}

func TestBuildRules(t *testing.T) {
	if _, err := Build("redesign", Input{Brief: "b"}); err == nil {
		t.Error("redesign without screenshots should fail")
	}
	if _, err := Build("logo", Input{Brief: "b", Brand: "teal"}); err != nil {
		t.Errorf("logo needs no screenshots: %v", err)
	}
	if _, err := Build("poster", Input{}); err == nil || !strings.Contains(err.Error(), "spec, redesign, logo") {
		t.Errorf("unknown mode: %v", err)
	}
}

func TestLabel(t *testing.T) {
	for in, want := range map[string]string{"03-job-apply.png": "job apply", "dashboard.png": "dashboard", "v2-home.png": "v2 home"} {
		if got := Label(in); got != want {
			t.Errorf("Label(%s) = %q, want %q", in, got, want)
		}
	}
}
