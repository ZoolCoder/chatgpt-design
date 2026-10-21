package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// Home is where every run starts a new chat.
const Home = "https://chatgpt.com/"

// Latest stands for the newest conversation in the sidebar, for a chat whose URL was never saved.
const Latest = "latest"

const (
	poll         = time.Second
	heartbeat    = 30 * time.Second
	retryGrace   = 20 * time.Second // how long ChatGPT's retry may take to clear its error
	nothingYet   = 15 * time.Second // how long --collect waits for an answer to start
	maxImageSize = 50 << 20
)

// ErrUsageLimit means ChatGPT refused the message because the account hit a usage limit.
var ErrUsageLimit = errors.New("ChatGPT usage limit")

// Request is one message to ChatGPT.
type Request struct {
	Prompt     string
	ChatURL    string            // continue this conversation; empty starts a new one
	Files      []string          // absolute paths of images to attach
	WantImages bool              // the answer is not done until it carries an image
	Collect    bool              // send nothing; wait for and read the answer to the chat's last message
	OnChat     func(url string)  // called once the conversation URL is known, before the answer is in
	OnText     func(text string) // called with the answer's text so far, on every poll while it grows
	Timeout    time.Duration
	Log        func(format string, args ...any)
}

// Image is one generated image.
type Image struct {
	Data []byte
	Type string // MIME type, sniffed from the bytes
}

// Result is ChatGPT's answer.
type Result struct {
	HTML   string // the assistant turn, to convert to markdown
	Text   string
	Images []Image
	URL    string // the conversation, for the user to open
}

type pageState struct {
	Login    bool `json:"login"`
	Composer bool `json:"composer"`
}

// Page selectors, newest layout first. ChatGPT renames these often; keep the old ones as fallbacks.
const (
	selComposer = "#prompt-textarea, div.ProseMirror[contenteditable=true], div[role=textbox][contenteditable=true], form textarea"
	selSend     = "[data-testid=send-button], #composer-submit-button, form button[type=submit]"
	selStop     = "[data-testid=stop-button], button[aria-label^=Stop]"
)

// Turn and answer selectors, one per layout; the first that matches wins, so layouts never mix.
var (
	selUserTurns      = []string{`[data-message-author-role="user"]`, `[data-chatgpt-search-unit-key$=":user"]`}
	selAssistantTurns = []string{`[data-message-author-role="assistant"]`, `[data-chatgpt-search-unit-key$=":assistant"]`}
	selAnswerBody     = []string{`.markdown`, `[data-markdown-text-style="assistant-message"]`}
)

// Generated images can render in a gallery outside the assistant turn.
const selGenerated = "[data-testid=generated-image-gallery] img, [data-testid=generated-image-preview] img"

// js fills a script's placeholders: COMPOSER, SEND and STOP become selector strings; USER_TURNS and
// ASSISTANT_TURNS become arrays of turn elements; NEW_TURNS and NEW_GENERATED keep only what follows
// the last user turn; LAST_USER is that turn; BODIES(t) becomes the answer blocks inside turn t;
// NOT_ANSWER matches user turns and the composer, ANSWER_BODY the answer blocks.
func js(src string) string {
	lit := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	first := func(root string, sels []string) string {
		return `((root, sels) => { for (const s of sels) { const n = root.querySelectorAll(s); if (n.length) return [...n]; } return []; })(` + root + `, ` + lit(sels) + `)`
	}
	// ChatGPT unmounts old turns in a long chat, so counts are no anchor; the last user turn is.
	after := func(nodes string) string {
		return `((nodes) => { const u = ` + first("document", selUserTurns) + `.pop(); return u ? nodes.filter(n => u.compareDocumentPosition(n) & Node.DOCUMENT_POSITION_FOLLOWING) : nodes; })(` + nodes + `)`
	}
	return strings.NewReplacer(
		"NEW_TURNS", after(first("document", selAssistantTurns)),
		"NEW_GENERATED", after(`[...document.querySelectorAll(`+lit(selGenerated)+`)]`),
		"NOT_ANSWER", lit(strings.Join(append(slices.Clone(selUserTurns), selComposer), ", ")),
		"ANSWER_BODY", lit(strings.Join(selAnswerBody, ", ")),
		"LAST_USER", first("document", selUserTurns)+".pop()",
		"MAX_IMAGE", strconv.Itoa(maxImageSize),
		"COMPOSER", lit(selComposer), "SEND", lit(selSend), "STOP", lit(selStop), "GENERATED", lit(selGenerated),
		"USER_TURNS", first("document", selUserTurns),
		"ASSISTANT_TURNS", first("document", selAssistantTurns),
		"BODIES(t)", `((t) => { const b = `+first("t", selAnswerBody)+`; return b.length ? b : [t]; })(t)`,
	).Replace(src)
}

var stateJS = js(`(() => ({
  login: !!document.querySelector('[data-testid="login-button"], a[href*="/auth/login"]'),
  composer: !!document.querySelector(COMPOSER),
}))()`)

// sessionJS asks chatgpt.com whether its cookies hold a session: true, false, or null when the
// tab is on another site or the check failed. Only the presence of a user is read, never a token.
const sessionJS = `(async () => {
  if (location.hostname !== 'chatgpt.com') return null;
  try {
    const r = await fetch('/api/auth/session', {credentials: 'include'});
    if (!r.ok) return null;
    const j = await r.json();
    return !!(j && j.user);
  } catch { return null; }
})()`

func session(ctx context.Context) (*bool, error) {
	return eval[*bool](ctx, sessionJS, chromedp.EvalAwaitPromise)
}

// Login opens ChatGPT in the dedicated profile and waits until the user has signed in.
func Login(ctx context.Context, timeout time.Duration, log func(string, ...any)) error {
	if err := chromedp.Do(ctx, chromedp.Navigate(Home)); err != nil {
		return err
	}
	log("Sign in to ChatGPT in the Chrome window that just opened. Waiting up to %s.", timeout)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// The sign-in can finish in a popup or another tab, so ask the server, not this tab's page.
		if ok, err := session(ctx); err == nil && ok != nil && *ok {
			log("Signed in. The session is kept in the chatgpt-design Chrome profile.")
			return nil
		}
		if err := sleep(ctx, poll); err != nil {
			return err
		}
	}
	return errors.New("timed out waiting for the ChatGPT sign-in")
}

// Ask sends one message in a new chat and waits for the full answer.
func Ask(ctx context.Context, r Request) (*Result, error) {
	if r.Timeout <= 0 {
		return nil, errors.New("the timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	start := Home
	if r.ChatURL != "" && r.ChatURL != Latest {
		if !IsChatURL(r.ChatURL) {
			return nil, fmt.Errorf("%q is not a ChatGPT conversation URL", r.ChatURL)
		}
		start = r.ChatURL
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(start)); err != nil {
		return nil, err
	}
	if err := waitFor(ctx, time.Minute, func() (bool, error) {
		st, err := eval[pageState](ctx, stateJS)
		if err != nil {
			return false, nil
		}
		if st.Login {
			return false, ErrNotLoggedIn
		}
		return st.Composer, nil
	}); err != nil {
		return nil, fmt.Errorf("open ChatGPT: %w", err)
	}
	if r.ChatURL == Latest {
		u, err := latestChat(ctx)
		if err != nil {
			return nil, err
		}
		r.ChatURL = u
		if err := chromedp.Do(ctx, chromedp.Navigate(u)); err != nil {
			return nil, err
		}
	}
	if r.ChatURL != "" {
		r.Log("continuing %s", r.ChatURL)
		if err := waitHistory(ctx); err != nil {
			return nil, err
		}
	}

	if r.Collect {
		if r.ChatURL == "" {
			return nil, errors.New("nothing to collect: the session has no ChatGPT chat yet (pass --chat URL, or --chat latest for the newest one)")
		}
		r.notify(r.ChatURL)
		r.Log("collecting the answer to the last message")
		if err := waitAnswer(ctx, r); err != nil {
			return nil, err
		}
		return harvest(ctx)
	}
	if len(r.Files) > 0 {
		r.Log("attaching %d file(s)", len(r.Files))
		if err := attach(ctx, r.Files); err != nil {
			return nil, err
		}
	}

	if err := typePrompt(ctx, r.Prompt); err != nil {
		return nil, err
	}
	r.Log("sending")
	if err := send(ctx); err != nil {
		return nil, err
	}
	r.notify(chatOpened(ctx, r.ChatURL))
	r.Log("waiting for the answer")
	if err := waitAnswer(ctx, r); err != nil {
		return nil, err
	}
	return harvest(ctx)
}

func (r Request) notify(url string) {
	if r.OnChat != nil && url != "" {
		r.OnChat(url)
	}
}

// chatOpened returns the conversation URL once a new chat has one; ChatGPT assigns it shortly after the send.
func chatOpened(ctx context.Context, had string) string {
	if had != "" {
		return had
	}
	var u string
	_ = waitFor(ctx, 2*time.Minute, func() (bool, error) {
		u, _ = eval[string](ctx, `location.href`)
		return IsChatURL(u), nil
	})
	if !IsChatURL(u) {
		return ""
	}
	return u
}

// latestJS takes the first link under the sidebar's "Recents" heading: pinned chats sit above it.
const latestJS = `(() => {
  const links = [...document.querySelectorAll('a[href^="/c/"]')];
  const head = [...document.querySelectorAll('h2, h3, div, span')].find(e => e.children.length === 0 && /^recents$/i.test(e.textContent.trim()));
  if (!head) return '';
  const a = links.find(l => head.compareDocumentPosition(l) & Node.DOCUMENT_POSITION_FOLLOWING);
  return a ? a.href : '';
})()`

// latestChat reads the newest conversation link from the sidebar.
func latestChat(ctx context.Context) (string, error) {
	var u string
	err := waitFor(ctx, 20*time.Second, func() (bool, error) {
		u, _ = eval[string](ctx, latestJS)
		return IsChatURL(u), nil
	})
	if err != nil {
		return "", errors.New("no conversation in the ChatGPT sidebar")
	}
	return u, nil
}

func attach(ctx context.Context, files []string) error {
	const pick = `(() => {
  const ins = [...document.querySelectorAll('input[type="file"]')];
  const el = ins.find(i => i.id === 'upload-files')
    || ins.find(i => /attach (files|photos)$/i.test(i.getAttribute('aria-label') || ''))
    || ins.find(i => !i.hasAttribute('capture') && (!i.accept || /image/.test(i.accept)))
    || ins[0];
  if (!el) return false;
  el.setAttribute('data-cgd-upload', '1');
  return true;
})()`
	uploads := js(`(() => {
  const form = document.querySelector(COMPOSER)?.closest('form') || document.body;
  const imgs = [...form.querySelectorAll('img')].filter(i => i.complete && i.naturalWidth > 0).length;
  const busy = !!form.querySelector('[role="progressbar"], .animate-spin, circle[stroke-dasharray]');
  return { imgs, busy };
})()`)
	type state struct {
		Imgs int  `json:"imgs"`
		Busy bool `json:"busy"`
	}
	// ChatGPT re-renders the composer while the page settles, and files set on the input it dropped
	// never show up. No preview at all after a short wait means that happened, so pick the input again.
	for try := 1; ; try++ {
		if ok, err := eval[bool](ctx, pick); err != nil || !ok {
			return errors.New("ChatGPT has no file input here: the page layout changed or uploads are off for this account")
		}
		if err := chromedp.Do(ctx, chromedp.SetUploadFiles(chromedp.CSS(`input[data-cgd-upload]`), files)); err != nil {
			return fmt.Errorf("attach files: %w", err)
		}
		landed := waitFor(ctx, 20*time.Second, func() (bool, error) {
			st, err := eval[state](ctx, uploads)
			return err == nil && (st.Imgs > 0 || st.Busy), nil
		})
		if landed == nil {
			break
		}
		if try == 3 {
			return errors.New("the attached files never showed in the composer")
		}
	}
	return waitFor(ctx, 3*time.Minute, func() (bool, error) {
		st, err := eval[state](ctx, uploads)
		if err != nil {
			return false, nil
		}
		return st.Imgs >= len(files) && !st.Busy, nil
	})
}

func typePrompt(ctx context.Context, prompt string) error {
	lit, _ := json.Marshal(prompt)
	src := js(`((p) => {
  const el = document.querySelector(COMPOSER);
  if (!el) return -1;
  el.focus();
  if (el.tagName === 'TEXTAREA') {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(el, p);
    el.dispatchEvent(new Event('input', { bubbles: true }));
    return el.value.length;
  }
  document.execCommand('selectAll', false);
  document.execCommand('insertText', false, p);
  el.dispatchEvent(new InputEvent('input', { bubbles: true }));
  return el.innerText.length;
})(`) + string(lit) + `)`
	n, err := eval[int](ctx, src)
	if err != nil {
		return fmt.Errorf("type the prompt: %w", err)
	}
	// ProseMirror turns blank lines into paragraphs, so the count drifts a little.
	if n < len([]rune(prompt))*9/10 {
		return fmt.Errorf("the composer took %d of %d characters", n, len([]rune(prompt)))
	}
	return nil
}

func send(ctx context.Context) error {
	ready := js(`(() => { const b = document.querySelector(SEND); return !!b && !b.disabled; })()`)
	if err := waitFor(ctx, time.Minute, func() (bool, error) {
		ok, _ := eval[bool](ctx, ready)
		return ok, nil
	}); err != nil {
		return errors.New("the send button never became ready")
	}
	// The last user turn changes when the message lands; its count alone does not grow in a long chat.
	lastUser := func() string {
		s, _ := eval[string](ctx, js(`(() => { const u = USER_TURNS; const l = u[u.length - 1]; return l ? u.length + ':' + (l.getAttribute('data-message-id') || l.innerText.slice(0, 200)) : ''; })()`))
		return s
	}
	before := lastUser()
	sent := func() (bool, error) { return lastUser() != before, nil }
	if err := chromedp.Do(ctx, chromedp.Click(chromedp.CSS(selSend))); err != nil {
		return fmt.Errorf("click send: %w", err)
	}
	if waitFor(ctx, 15*time.Second, sent) == nil {
		return nil
	}
	// A missed click leaves the prompt in the composer; a DOM click is the fallback.
	_, _ = eval[chromedp.Void](ctx, js(`document.querySelector(SEND)?.click()`))
	if waitFor(ctx, 15*time.Second, sent) != nil {
		return errors.New("the message was not sent")
	}
	return nil
}

// IsChatURL reports whether raw is a ChatGPT conversation another tab can open.
func IsChatURL(raw string) bool {
	u, err := url.Parse(raw)
	// A new chat first shows a client-side id (/c/local-chatgpt:…) that no other tab can open.
	return err == nil && u.Scheme == "https" && (u.Host == "chatgpt.com" || u.Host == "chat.openai.com") &&
		strings.HasPrefix(u.Path, "/c/") && !strings.HasPrefix(u.Path, "/c/local-")
}

// waitHistory waits until a continued conversation shows its earlier turns and stops growing.
func waitHistory(ctx context.Context) error {
	gone := errors.New("the session's ChatGPT conversation did not open (deleted, or another account): start a new session")
	prev, stable := -1, 0
	err := waitFor(ctx, 45*time.Second, func() (bool, error) {
		st, err := eval[struct {
			Turns int  `json:"turns"`
			Chat  bool `json:"chat"`
		}](ctx, js(`({ turns: USER_TURNS.length + ASSISTANT_TURNS.length, chat: location.pathname.startsWith('/c/') })`))
		if err != nil {
			return false, nil
		}
		if !st.Chat {
			return false, gone
		}
		if st.Turns > 0 && st.Turns == prev {
			stable++
		} else {
			stable = 0
		}
		prev = st.Turns
		return stable >= 4, nil
	})
	if err != nil && prev <= 0 {
		return gone
	}
	return err
}

type answerState struct {
	N      int    `json:"n"`
	Stop   bool   `json:"stop"`
	Len    int    `json:"len"`
	Imgs   int    `json:"imgs"`
	Busy   bool   `json:"busy"`
	Limit  string `json:"limit"`
	ErrMsg string `json:"err"`
	Retry  bool   `json:"retry"`
	Done   bool   `json:"finished"`
	Text   string `json:"text"`
}

// answerJS reads only what follows the last user turn, so text in earlier turns never reads as a
// limit or an error. Limits, errors and progress notes must also sit outside the answer, which may
// well quote them; only a near-empty answer counts as an image-progress placeholder.
var answerJS = js(`((wantText) => {
  const turns = NEW_TURNS;
  const last = turns[turns.length - 1];
  const stop = !!document.querySelector(STOP);
  const u = LAST_USER;
  const isNew = n => !u || (u.compareDocumentPosition(n) & Node.DOCUMENT_POSITION_FOLLOWING);
  let body = '', outside = '';
  const w = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  // Start at the last user turn: in a long chat the earlier turns are most of the page.
  if (u) w.currentNode = u;
  for (let n; (n = w.nextNode());) {
    const p = n.parentElement;
    // Hidden nodes hold templates such as "{active_limit}", never a message on screen.
    if (!p || p.closest(NOT_ANSWER) || !p.checkVisibility()) continue;
    if (p.closest(ANSWER_BODY)) body += n.nodeValue + '\n';
    else outside += n.nodeValue + '\n';
  }
  const limit = (outside.match(/You[’']ve (?:hit|reached) [^.\n{]*limit[^.\n{]*(?![^.\n]*\{)/i) || [''])[0];
  const err = (outside.match(/Something went wrong[^\n]*|There was an error generating a response/i) || [''])[0];
  const buttons = [...document.querySelectorAll('button')].filter(isNew);
  const retry = buttons.some(b => /^(retry|try again|regenerate)$/i.test(b.innerText.trim()));
  // ChatGPT adds the answer's action bar, with Read aloud, only once the whole turn is done.
  const finished = buttons.some(b => /^read aloud$/i.test(b.getAttribute('aria-label') || ''));
  const big = i => i.complete && (i.naturalWidth || 0) >= 256;
  const gen = NEW_GENERATED.filter(big).length;
  const progress = /creating image|generating image|image is being/i;
  const busy = progress.test(outside) || (body.trim().length < 80 && progress.test(body));
  if (!turns.length) return { n: 0, stop, len: 0, imgs: gen, busy, limit, err, retry, finished, text: '' };
  const scope = last.closest('article') || last;
  const imgs = gen + [...scope.querySelectorAll('img')].filter(big).length;
  const text = wantText ? turns.flatMap(t => BODIES(t)).map(b => b.innerText).join('\n\n') : '';
  return { n: turns.length, stop, len: scope.innerText.length, imgs, busy, limit, err, retry, finished, text };
})`)

// retryJS clicks ChatGPT's own retry button under the failed answer.
var retryJS = js(`(() => {
  const u = LAST_USER;
  const b = [...document.querySelectorAll('button')].find(b => (!u || (u.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)) && /^(retry|try again|regenerate)$/i.test(b.innerText.trim()));
  if (b) b.click();
  return !!b;
})()`)

func waitAnswer(ctx context.Context, r Request) error {
	check := "(" + answerJS + ")(" + strconv.FormatBool(r.OnText != nil) + ")"
	// The answer must hold still this long: images keep loading after the text has stopped. Once
	// ChatGPT marks the turn finished, a short wait only catches the last image swap.
	settle, quick := 4*time.Second, time.Second
	if r.WantImages {
		settle, quick = 8*time.Second, 2*time.Second
	}
	var prev answerState
	var still time.Time
	start := time.Now()
	beat := start.Add(heartbeat)
	var retried time.Time
	for {
		st, err := eval[answerState](ctx, check)
		if err != nil {
			return waitErr(ctx, err)
		}
		if st.Limit != "" {
			return fmt.Errorf("%w: %s", ErrUsageLimit, st.Limit)
		}
		switch {
		case st.ErrMsg == "" || st.Stop:
		case !retried.IsZero() && time.Since(retried) < retryGrace:
			// The old error stays on screen until the retry gets going.
		case !retried.IsZero() || !st.Retry:
			return fmt.Errorf("ChatGPT: %s", st.ErrMsg)
		default:
			r.Log("ChatGPT failed (%s): retrying once", st.ErrMsg)
			if ok, err := eval[bool](ctx, retryJS); err != nil || !ok {
				return fmt.Errorf("ChatGPT: %s", st.ErrMsg)
			}
			retried, prev, still = time.Now(), answerState{}, time.Time{}
		}
		if r.Collect && st.N == 0 && st.Imgs == 0 && !st.Stop && !st.Busy && time.Since(start) > nothingYet {
			return errors.New("nothing to collect: the chat's last message has no answer; send it again without --collect")
		}
		// An answer can be images alone with no text, in any mode, so new images complete it too.
		answered := st.N > 0 && st.Len > 0 || st.Imgs > 0
		if r.WantImages {
			answered = st.Imgs > 0
		}
		// An error still on screen is never a finished answer, even while a retry starts.
		done := answered && !st.Stop && !st.Busy && st.ErrMsg == ""
		switch {
		case !done || st.Len != prev.Len || st.Imgs != prev.Imgs:
			still = time.Time{}
		case still.IsZero():
			still = time.Now()
		case time.Since(still) >= settle, st.Done && time.Since(still) >= quick:
			return nil
		}
		if r.OnText != nil && st.Text != "" {
			r.OnText(st.Text)
		}
		prev = st
		if now := time.Now(); now.After(beat) {
			r.Log("still waiting (%s): %d chars, %d image(s)%s", now.Sub(start).Round(time.Second), st.Len, st.Imgs, activity(st))
			beat = now.Add(heartbeat)
		}
		if err := sleep(ctx, poll); err != nil {
			if r.WantImages && st.N > 0 && st.Imgs == 0 && !st.Stop {
				return errors.New("timed out: ChatGPT answered without an image (it may have asked a question instead; open the chat)")
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("timed out waiting for ChatGPT's answer (last seen: %d turn(s), %d chars, %d image(s), stop=%t, busy=%t)", st.N, st.Len, st.Imgs, st.Stop, st.Busy)
			}
			return err
		}
	}
}

func activity(st answerState) string {
	switch {
	case st.Busy:
		return ", making an image"
	case st.Stop:
		return ", writing"
	}
	return ""
}

func waitErr(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errors.New("timed out waiting for ChatGPT's answer")
	}
	return err
}

func harvest(ctx context.Context) (*Result, error) {
	res := &Result{}
	src := js(`(() => {
  const turns = NEW_TURNS;
  return {
    html: turns.map(t => BODIES(t).map(b => b.innerHTML).join('\n')).join('\n'),
    text: turns.map(t => t.innerText).join('\n\n'),
    url: location.href,
  };
})()`)
	out, err := eval[struct {
		HTML string `json:"html"`
		Text string `json:"text"`
		URL  string `json:"url"`
	}](ctx, src)
	if err != nil {
		return nil, fmt.Errorf("read the answer: %w", err)
	}
	res.HTML, res.Text, res.URL = out.HTML, out.Text, out.URL

	imgJS := js(`(async () => {
  const turns = NEW_TURNS;
  const imgs = [...turns.flatMap(t => [...(t.closest('article') || t).querySelectorAll('img')]), ...NEW_GENERATED];
  const srcs = [...new Set(imgs.filter(i => (i.naturalWidth || 0) >= 256).map(i => i.currentSrc || i.src).filter(Boolean))];
  // Fetch in parallel, keep the page's order.
  return Promise.all(srcs.map(async src => {
    try {
      const r = await fetch(src, { credentials: 'include' });
      if (!r.ok) throw new Error(String(r.status));
      const b = await r.blob();
      if (b.size > MAX_IMAGE) throw new Error('too big');
      return { data: await new Promise((ok, no) => { const f = new FileReader(); f.onload = () => ok(f.result); f.onerror = no; f.readAsDataURL(b); }) };
    } catch (e) { return { src }; }
  }));
})()`)
	imgs, err := eval[[]struct {
		Data string `json:"data"`
		Src  string `json:"src"`
	}](ctx, imgJS, chromedp.EvalAwaitPromise)
	if err != nil {
		return nil, fmt.Errorf("read the images: %w", err)
	}
	for _, im := range imgs {
		var data []byte
		var err error
		if im.Data != "" {
			data, err = decodeDataURL(im.Data)
		} else {
			data, err = download(ctx, im.Src)
		}
		if err != nil {
			return nil, fmt.Errorf("save an image: %w", err)
		}
		typ := http.DetectContentType(data)
		if !strings.HasPrefix(typ, "image/") {
			return nil, fmt.Errorf("an image came back as %s", typ)
		}
		res.Images = append(res.Images, Image{Data: data, Type: typ})
	}
	return res, nil
}

// eval runs a script in the tab and decodes its result.
func eval[T any](ctx context.Context, js string, opts ...chromedp.EvaluateOption) (T, error) {
	return chromedp.Run(ctx, chromedp.Evaluate[T](js, opts...))
}

func decodeDataURL(s string) ([]byte, error) {
	_, b64, ok := strings.Cut(s, ";base64,")
	if !ok {
		return nil, errors.New("not a base64 data URL")
	}
	if base64.StdEncoding.DecodedLen(len(b64)) > maxImageSize {
		return nil, errors.New("image larger than 50 MB")
	}
	return base64.StdEncoding.DecodeString(b64)
}

// allowedImageHost keeps the fallback download on OpenAI's own hosts.
func allowedImageHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	h := u.Hostname()
	for _, d := range []string{"chatgpt.com", "oaiusercontent.com", "openai.com"} {
		if h == d || strings.HasSuffix(h, "."+d) {
			return true
		}
	}
	return false
}

// download fetches an image the page could not read itself, with the browser's cookies for that URL.
func download(ctx context.Context, src string) ([]byte, error) {
	if !allowedImageHost(src) {
		return nil, fmt.Errorf("refusing to fetch an image from %q", hostOf(src))
	}
	got, err := chromedp.Run(ctx, func(ctx context.Context, t *chromedp.Target) (*network.GetCookiesResult, error) {
		r, err := cdp.Call(ctx, t, network.GetCookies, network.GetCookiesParams{URLs: []string{src}})
		return &r, err
	})
	if err != nil {
		return nil, err
	}
	cookies := got.Cookies
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, err
	}
	for _, c := range cookies {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", hostOf(src), resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageSize+1))
	if len(data) > maxImageSize {
		return nil, errors.New("image larger than 50 MB")
	}
	return data, err
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return "?"
}

// Screenshot saves what the tab shows, for a failed run.
func Screenshot(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	buf, err := chromedp.Run(ctx, chromedp.CaptureScreenshot())
	if err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0o644)
}

// ShotOptions sets up one screenshot.
type ShotOptions struct {
	Width, Height int
	Settle        time.Duration // extra wait once the page and its fonts have loaded
	Full          bool          // the whole page, not just the viewport
	Mobile        bool          // a phone: touch, 3x pixels, mobile user agent
	Dark          bool          // prefers-color-scheme: dark
}

// Shot captures a page. It fails on an HTTP error status, since a picture of an error page is no
// use to a design review.
func Shot(ctx context.Context, url string, o ShotOptions) ([]byte, error) {
	scheme := "light"
	if o.Dark {
		scheme = "dark"
	}
	var vp []chromedp.EmulateViewportOption
	if o.Mobile {
		vp = append(vp, chromedp.EmulateScale(3), chromedp.EmulateMobile, chromedp.EmulateTouch)
	}
	if err := chromedp.Do(ctx,
		chromedp.EmulateViewport(int64(o.Width), int64(o.Height), vp...),
		chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			_, err := cdp.Call(ctx, t, emulation.SetEmulatedMedia, emulation.SetEmulatedMediaParams{
				Features: []*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: scheme}},
			})
			return err
		}),
		chromedp.Navigate(url),
	); err != nil {
		return nil, err
	}
	status, err := eval[int](ctx, `performance.getEntriesByType('navigation')[0]?.responseStatus || 0`)
	if err == nil && status >= 400 {
		return nil, fmt.Errorf("HTTP %d", status)
	}
	fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, _ = eval[bool](fctx, `document.fonts.ready.then(() => true)`, chromedp.EvalAwaitPromise)
	cancel()
	if err := sleep(ctx, o.Settle); err != nil {
		return nil, err
	}
	if o.Full {
		return chromedp.Run(ctx, chromedp.FullScreenshot(100))
	}
	return chromedp.Run(ctx, chromedp.CaptureScreenshot())
}

func waitFor(ctx context.Context, limit time.Duration, check func() (bool, error)) error {
	deadline := time.Now().Add(limit)
	for {
		ok, err := check()
		if err != nil || ok {
			return err
		}
		if time.Now().After(deadline) {
			return errors.New("timed out")
		}
		if err := sleep(ctx, 500*time.Millisecond); err != nil {
			return err
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// LoggedIn opens ChatGPT and reports whether the profile has a session.
func LoggedIn(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := chromedp.Do(ctx, chromedp.Navigate(Home)); err != nil {
		return false, err
	}
	var in bool
	err := waitFor(ctx, 45*time.Second, func() (bool, error) {
		ok, err := session(ctx)
		if err != nil || ok == nil {
			return false, nil
		}
		in = *ok
		return true, nil
	})
	if err != nil {
		return false, errors.New("ChatGPT did not load (a bot check or no network)")
	}
	return in, nil
}
