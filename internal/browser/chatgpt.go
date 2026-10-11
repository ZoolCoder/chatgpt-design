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
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// Home is where every run starts a new chat.
const Home = "https://chatgpt.com/"

const (
	poll         = 2 * time.Second
	maxImageSize = 50 << 20
)

// Request is one message to ChatGPT.
type Request struct {
	Prompt     string
	Files      []string // absolute paths of images to attach
	WantImages bool     // the answer is not done until it carries an image
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

const stateJS = `(() => ({
  login: !!document.querySelector('[data-testid="login-button"], a[href*="/auth/login"]'),
  composer: !!document.querySelector('#prompt-textarea'),
}))()`

// Login opens ChatGPT in the dedicated profile and waits until the user has signed in.
func Login(ctx context.Context, timeout time.Duration, log func(string, ...any)) error {
	if err := chromedp.Do(ctx, chromedp.Navigate(Home)); err != nil {
		return err
	}
	log("Sign in to ChatGPT in the Chrome window that just opened. Waiting up to %s.", timeout)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st, err := eval[pageState](ctx, stateJS); err == nil && st.Composer && !st.Login {
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
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	if err := chromedp.Do(ctx, chromedp.Navigate(Home)); err != nil {
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

	if len(r.Files) > 0 {
		r.Log("attaching %d file(s)", len(r.Files))
		if err := attach(ctx, r.Files); err != nil {
			return nil, err
		}
	}

	before, err := assistantTurns(ctx)
	if err != nil {
		return nil, err
	}
	if err := typePrompt(ctx, r.Prompt); err != nil {
		return nil, err
	}
	r.Log("sending")
	if err := send(ctx); err != nil {
		return nil, err
	}
	r.Log("waiting for the answer")
	if err := waitAnswer(ctx, before, r); err != nil {
		return nil, err
	}
	return harvest(ctx, before)
}

func attach(ctx context.Context, files []string) error {
	const pick = `(() => {
  const ins = [...document.querySelectorAll('input[type="file"]')];
  const el = ins.find(i => i.id === 'upload-files')
    || ins.find(i => !i.hasAttribute('capture') && (!i.accept || /image/.test(i.accept)))
    || ins[0];
  if (!el) return false;
  el.setAttribute('data-cgd-upload', '1');
  return true;
})()`
	if ok, err := eval[bool](ctx, pick); err != nil || !ok {
		return errors.New("ChatGPT has no file input here: the page layout changed or uploads are off for this account")
	}
	if err := chromedp.Do(ctx, chromedp.SetUploadFiles(chromedp.CSS(`input[data-cgd-upload]`), files)); err != nil {
		return fmt.Errorf("attach files: %w", err)
	}
	const uploads = `(() => {
  const form = document.querySelector('#prompt-textarea')?.closest('form') || document.body;
  const imgs = [...form.querySelectorAll('img')].filter(i => i.complete && i.naturalWidth > 0).length;
  const busy = !!form.querySelector('[role="progressbar"], .animate-spin, circle[stroke-dasharray]');
  return { imgs, busy };
})()`
	return waitFor(ctx, 3*time.Minute, func() (bool, error) {
		st, err := eval[struct {
			Imgs int  `json:"imgs"`
			Busy bool `json:"busy"`
		}](ctx, uploads)
		if err != nil {
			return false, nil
		}
		return st.Imgs >= len(files) && !st.Busy, nil
	})
}

func typePrompt(ctx context.Context, prompt string) error {
	lit, _ := json.Marshal(prompt)
	js := `((p) => {
  const el = document.querySelector('#prompt-textarea');
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
})(` + string(lit) + `)`
	n, err := eval[int](ctx, js)
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
	const ready = `(() => { const b = document.querySelector('[data-testid="send-button"]'); return !!b && !b.disabled; })()`
	if err := waitFor(ctx, time.Minute, func() (bool, error) {
		ok, _ := eval[bool](ctx, ready)
		return ok, nil
	}); err != nil {
		return errors.New("the send button never became ready")
	}
	users := func() int {
		n, _ := eval[int](ctx, `document.querySelectorAll('[data-message-author-role="user"]').length`)
		return n
	}
	before := users()
	if err := chromedp.Do(ctx, chromedp.Click(chromedp.CSS(`[data-testid="send-button"]`))); err != nil {
		return fmt.Errorf("click send: %w", err)
	}
	if waitFor(ctx, 15*time.Second, func() (bool, error) { return users() > before, nil }) == nil {
		return nil
	}
	// A missed click leaves the prompt in the composer; a DOM click is the fallback.
	_, _ = eval[chromedp.Void](ctx, `document.querySelector('[data-testid="send-button"]')?.click()`)
	if waitFor(ctx, 15*time.Second, func() (bool, error) { return users() > before, nil }) != nil {
		return errors.New("the message was not sent")
	}
	return nil
}

func assistantTurns(ctx context.Context) (int, error) {
	return eval[int](ctx, `document.querySelectorAll('[data-message-author-role="assistant"]').length`)
}

type answerState struct {
	N      int    `json:"n"`
	Stop   bool   `json:"stop"`
	Len    int    `json:"len"`
	Imgs   int    `json:"imgs"`
	Busy   bool   `json:"busy"`
	Limit  string `json:"limit"`
	ErrMsg string `json:"err"`
}

const answerJS = `(() => {
  const turns = [...document.querySelectorAll('[data-message-author-role="assistant"]')];
  const last = turns[turns.length - 1];
  const stop = !!document.querySelector('[data-testid="stop-button"]');
  const page = document.body.innerText;
  const limit = (page.match(/You[’']ve (?:hit|reached) [^.\n]*limit[^.\n]*/i) || [''])[0];
  const err = (page.match(/Something went wrong[^\n]*|There was an error generating a response/i) || [''])[0];
  if (!last) return { n: 0, stop, len: 0, imgs: 0, busy: false, limit, err };
  const scope = last.closest('article') || last;
  const imgs = [...scope.querySelectorAll('img')].filter(i => i.complete && (i.naturalWidth || 0) >= 256).length;
  const busy = /creating image|generating image|image is being/i.test(scope.innerText);
  return { n: turns.length, stop, len: scope.innerText.length, imgs, busy, limit, err };
})()`

func waitAnswer(ctx context.Context, before int, r Request) error {
	var prev answerState
	stable := 0
	need := 3
	if r.WantImages {
		need = 5
	}
	for {
		st, err := eval[answerState](ctx, answerJS)
		if err != nil {
			return waitErr(ctx, err)
		}
		if st.Limit != "" {
			return fmt.Errorf("ChatGPT usage limit: %s", st.Limit)
		}
		if st.ErrMsg != "" && !st.Stop {
			return fmt.Errorf("ChatGPT: %s", st.ErrMsg)
		}
		done := st.N > before && !st.Stop && !st.Busy && st.Len > 0 && (!r.WantImages || st.Imgs > 0)
		if done && st.Len == prev.Len && st.Imgs == prev.Imgs {
			stable++
		} else {
			stable = 0
		}
		if stable >= need {
			return nil
		}
		prev = st
		if err := sleep(ctx, poll); err != nil {
			if r.WantImages && st.N > before && st.Imgs == 0 && !st.Stop {
				return errors.New("timed out: ChatGPT answered without an image (it may have asked a question instead; open the chat)")
			}
			return waitErr(ctx, err)
		}
	}
}

func waitErr(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errors.New("timed out waiting for ChatGPT's answer")
	}
	return err
}

func harvest(ctx context.Context, before int) (*Result, error) {
	res := &Result{}
	js := fmt.Sprintf(`(() => {
  const turns = [...document.querySelectorAll('[data-message-author-role="assistant"]')].slice(%d);
  return {
    html: turns.map(t => (t.querySelector('.markdown') || t).innerHTML).join('\n'),
    text: turns.map(t => t.innerText).join('\n\n'),
    url: location.href,
  };
})()`, before)
	out, err := eval[struct {
		HTML string `json:"html"`
		Text string `json:"text"`
		URL  string `json:"url"`
	}](ctx, js)
	if err != nil {
		return nil, fmt.Errorf("read the answer: %w", err)
	}
	res.HTML, res.Text, res.URL = out.HTML, out.Text, out.URL

	imgJS := fmt.Sprintf(`(async () => {
  const turns = [...document.querySelectorAll('[data-message-author-role="assistant"]')].slice(%d);
  const seen = new Set(), out = [];
  for (const t of turns) for (const i of (t.closest('article') || t).querySelectorAll('img')) {
    if ((i.naturalWidth || 0) < 256) continue;
    const src = i.currentSrc || i.src;
    if (!src || seen.has(src)) continue;
    seen.add(src);
    try {
      const r = await fetch(src, { credentials: 'include' });
      if (!r.ok) throw new Error(String(r.status));
      const b = await r.blob();
      out.push({ data: await new Promise((ok, no) => { const f = new FileReader(); f.onload = () => ok(f.result); f.onerror = no; f.readAsDataURL(b); }) });
    } catch (e) { out.push({ src }); }
  }
  return out;
})()`, before)
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

// Shot captures a page at width x height: the viewport, or the whole page when full is set.
func Shot(ctx context.Context, url string, width, height int, settle time.Duration, full bool) ([]byte, error) {
	if err := chromedp.Do(ctx,
		chromedp.EmulateViewport(int64(width), int64(height)),
		chromedp.Navigate(url),
		chromedp.WaitReady(chromedp.CSS("body")),
		chromedp.Sleep(settle),
	); err != nil {
		return nil, err
	}
	if full {
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
	var st pageState
	err := waitFor(ctx, 45*time.Second, func() (bool, error) {
		got, err := eval[pageState](ctx, stateJS)
		if err != nil {
			return false, nil
		}
		st = got
		return st.Login || st.Composer, nil
	})
	if err != nil {
		return false, errors.New("ChatGPT did not load (a bot check or no network)")
	}
	return !st.Login, nil
}
