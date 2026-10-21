package browser

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// page starts a headless Chrome for tests that run the page scripts against a fixture.
func page(t *testing.T) context.Context {
	t.Helper()
	if testing.Short() {
		t.Skip("starts Chrome")
	}
	if _, err := ChromePath(); err != nil {
		t.Skip(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	tab, closeTab, err := Start(ctx, Options{Headless: true})
	if errors.Is(err, ErrSandboxed) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeTab)
	return tab
}

func setBody(t *testing.T, ctx context.Context, html string) {
	t.Helper()
	if err := chromedp.Do(ctx, chromedp.Navigate("about:blank")); err != nil {
		t.Fatal(err)
	}
	if _, err := eval[bool](ctx, `(document.body.innerHTML = `+jsString(html)+`, true)`); err != nil {
		t.Fatal(err)
	}
}

func jsString(s string) string { return "`" + s + "`" }

const (
	oldTurns = `<div data-message-author-role="user">first</div>
<div data-message-author-role="assistant"><div class="markdown">Something went wrong earlier. You've reached your limit for GPT-5.</div></div>`
	composer = `<form><div id="prompt-textarea" contenteditable="true">Something went wrong</div></form>`
)

func TestAnswerJS(t *testing.T) {
	ctx := page(t)
	answer := func(body string) string {
		return `<article><div data-message-author-role="assistant"><div class="markdown">` + body + `</div></div></article>`
	}
	for _, tc := range []struct {
		name, html  string
		n, imgs     int
		limit, err  string
		retry, busy bool
		done        bool
	}{
		{name: "clean answer, noisy history", n: 1,
			html: oldTurns + `<div data-message-author-role="user">why did it say Something went wrong?</div>` + answer("All good.") + composer},
		{name: "answer quotes an error", n: 1,
			html: oldTurns + `<div data-message-author-role="user">q</div>` + answer(`The toast says "Something went wrong" too often.`)},
		{name: "error banner with retry", n: 0, err: "Something went wrong while generating the response.", retry: true,
			html: oldTurns + `<div data-message-author-role="user">q</div><div>Something went wrong while generating the response.</div><button>Retry</button>`},
		{name: "hidden limit template", n: 1,
			html: `<div data-message-author-role="user">q</div>` + answer("ok") + `<div style="display:none">You've reached your limit of {active_limit} active tasks</div>`},
		{name: "visible limit", n: 0, limit: "You’ve hit your limit for GPT-5",
			html: oldTurns + `<div data-message-author-role="user">q</div><div>You’ve hit your limit for GPT-5. Try again later.</div>`},
		{name: "making an image", n: 1, busy: true,
			html: `<div data-message-author-role="user">q</div>` + answer("Creating image")},
		{name: "answer quotes progress and limit", n: 1,
			html: `<div data-message-author-role="user">q</div>` + answer(`Rename the "Generating image…" label, and soften "You've reached the limit of 3 projects" in the paywall copy. `+strings.Repeat("More critique. ", 10))},
		{name: "finished turn", n: 1, done: true,
			html: `<div data-message-author-role="user">q</div><button aria-label="Copy message"></button>` + answer("Done.") + `<button aria-label="Read aloud"></button>`},
		{name: "user actions only", n: 1,
			html: `<div data-message-author-role="user">q</div><button aria-label="Copy message"></button>` + answer("Still writing")},
		{name: "progress outside the answer", n: 1, busy: true,
			html: `<div data-message-author-role="user">q</div>` + answer("Here is the mockup with a long description of what changed in it.") + `<div>Creating image</div>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setBody(t, ctx, tc.html)
			st, err := eval[answerState](ctx, "("+answerJS+")()")
			if err != nil {
				t.Fatal(err)
			}
			if st.N != tc.n || st.Limit != tc.limit || st.ErrMsg != tc.err || st.Retry != tc.retry || st.Busy != tc.busy || st.Done != tc.done {
				t.Errorf("got %+v", st)
			}
		})
	}
	t.Run("text for streaming", func(t *testing.T) {
		setBody(t, ctx, oldTurns+`<div data-message-author-role="user">q</div>`+answer("Part one")+`<p>noise</p>`)
		st, err := eval[answerState](ctx, "("+answerJS+")(true)")
		if err != nil || st.Text != "Part one" {
			t.Errorf("text = %q, %v", st.Text, err)
		}
	})
}
