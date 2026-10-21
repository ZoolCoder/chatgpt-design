# chatgpt-design

Get ChatGPT's design opinion from the terminal or from your coding agent. `chatgpt-design` sends screenshots of your app and a design brief to **your own ChatGPT account**, then saves what comes back:

- the real generated mockup images, not screen captures of them
- the answer as markdown
- a link to the chat, so you can follow up there

A session is your agent session: Claude Code's session id, or one you name. Follow-ups go to the same ChatGPT chat for the whole session, so ChatGPT remembers your screenshots and its earlier answers. Everything is kept in one folder per session under `design/chatgpt/`.

There is no API key and no per-image billing. It is one Go binary that drives a dedicated Chrome profile, which you sign in to once. It ships with an agent skill, so Claude Code, Codex and other agents use it when you say "redesign this", "give me mockups" or "ask ChatGPT".

```
export CHATGPT_DESIGN_SESSION=settings   # not needed inside Claude Code
chatgpt-design shoot --base http://localhost:3000 overview=/ settings=/settings
chatgpt-design ask --mode redesign
# design/chatgpt/settings/chat.md, images/01.png ...
chatgpt-design ask --mode chat --prompt - <<< "Make the settings mockup denser"
# same chat, appended to chat.md, images/02.png
```

## Install

You need Google Chrome or Chromium, and a ChatGPT account. Image generation works best on a paid plan.

```sh
# Homebrew (macOS, Linux)
brew tap zoolcoder/chatgpt-design https://github.com/zoolcoder/chatgpt-design
brew install --cask chatgpt-design

# or Go 1.25+ (any OS)
go install github.com/zoolcoder/chatgpt-design/cmd/chatgpt-design@latest
```

Prebuilt binaries for macOS, Linux and Windows are on the [releases page](https://github.com/zoolcoder/chatgpt-design/releases).

## Set up once

```sh
chatgpt-design login           # opens Chrome, you sign in to chatgpt.com; Chrome stays open for later runs
chatgpt-design doctor          # checks Chrome, the profile, the sign-in and the skill
chatgpt-design install-skill   # adds the skill for Claude Code, Codex and other agents
```

`doctor` prints one line per check and a fix under each failure. `--offline` skips the checks that open Chrome. Its exit code is the first failure's, so `3` means sign in.

`install-skill` writes `SKILL.md` to `skills/chatgpt-design/` in each of `~/.claude`, `~/.codex` and `~/.agents` that exists, or to `~/.claude` when none does. To install into a project or another agent, pass one or more `--dir` flags.

## Use it from an agent

After `install-skill`, ask in plain words:

> redesign the settings and billing pages, mockups from ChatGPT

> ask chatgpt for a design review of the dashboard

> logo ideas for this project

The skill takes the agent through each step:

1. write a short brief
2. screenshot the running app
3. show you the exact prompt and files, then **wait for your yes**
4. send them
5. review what came back

The skill never writes application code. Implementing the chosen direction is a separate step.

In Claude Code, run the login with `! chatgpt-design login`, so it runs in your session. Run the CLI outside the agent's command sandbox, because it starts Chrome and talks to it on 127.0.0.1. Inside a sandbox it stops with `a command sandbox blocks localhost` and never starts a second Chrome.

## Commands

### `session`: one ChatGPT chat per agent session

The session id comes from, in order: `--session ID`, `CHATGPT_DESIGN_SESSION`, then `CLAUDE_CODE_SESSION_ID`, which Claude Code sets by itself. In Codex or a plain terminal, set `CHATGPT_DESIGN_SESSION` or pass `--session`.

```sh
chatgpt-design session        # this session: folder, chat URL, number of asks
chatgpt-design session list   # every session in the project, * marks this one
chatgpt-design session export # the whole session as one HTML file, images inside
```

`session export` writes `session.html` in the session folder (or `--out FILE`): every prompt and answer with its images built in, so it opens offline and can be shared as one file. It inlines only images inside the session folder and drops any raw HTML in the answers.

Each project keeps its memory in `design/chatgpt/`, at the root of the git repository you run from (or `--project DIR`):

```
design/chatgpt/<session>/
  session.json                     chat URL, when it started, how many asks
  chat.md                          every prompt and answer, images inline
  images/                          01.png, 02.png ... numbered across the session
  shots/                           screenshots to send
  shots/diff/                      what changed since the last shot (shoot --diff), never sent
  session.html                     session export
  brief.md                         optional brief
```

Commit it to keep the design history with the code, or add it to `.gitignore`.

### `shoot`: screenshot a running app

```sh
chatgpt-design shoot --base http://localhost:5173 home=/ 'billing=/settings?tab=billing'
```

Each `name=path` becomes `NN-name.png` at 1440×900, in the current session's `shots/` (or `--out DIR`). Shooting a name again replaces its file and keeps its number; a new name takes the next number. Quote arguments that contain `?` or `&`. A page that answers with HTTP 400 or more fails the run. Flags:

| Flag | Default | |
|---|---|---|
| `--width`, `--height` | 1440, 900 | viewport, width 320–1600 |
| `--wait` | 1.5s | extra settle time once the page and its fonts load |
| `--full` | off | whole page instead of the viewport |
| `--mobile` | off | a phone: 390×844 at 3x, touch, mobile user agent |
| `--dark` | off | `prefers-color-scheme: dark` |
| `--sizes` | | shoot each page at these widths, e.g. `390,768,1440`, as `NN-name@WIDTH.png`; not with `--width` |
| `--diff` | off | compare each page with the shot it replaces; prints the share of pixels that changed and saves the changes in red to `shots/diff/` |

Pages load four at a time, in tabs of one Chrome.
It uses a throwaway headless profile and never your ChatGPT one.

### `ask`: send to ChatGPT

```sh
chatgpt-design ask --mode spec --goal "fewer setup steps for non-developers"
```

| Mode | Gets back |
|---|---|
| `spec` | a markdown critique with these sections: Verdict, Keep, Findings, Tokens, Sequence |
| `redesign` | one mockup image per screenshot, plus tokens |
| `logo` | three logo concepts and an app icon; needs no screenshots |
| `chat` | the answer to any prompt: `--prompt FILE` or `--prompt -` for stdin |

`ask` sends to the session's chat and appends the prompt and answer to `chat.md`. The first ask of a chat reads `brief.md` and attaches every image in `shots/`. Later asks attach only the screenshots you pass with `--shot`, because the chat already has the others.

| Flag | |
|---|---|
| `--new` | start a new ChatGPT chat in this session |
| `--stream` | print the answer on stdout as ChatGPT writes it; not with `--json` |
| `--collect` | send nothing; save the answer to the chat's last message. Rerun a failed or timed-out ask with the same flags plus `--collect` |
| `--chat URL\|latest` | continue that conversation and save it in the session; `latest` is the newest chat under Recents in the sidebar (pinned chats are skipped) |
| `--session ID` | use this session instead of the agent's |
| `--brief` | a brief file other than `SESSION/brief.md` |
| `--shot` | one screenshot; repeat it for more, up to 10 |
| `--brand` | a brand sheet |
| `--goal` | one line on what this ask should optimise for |

`--dry-run` prints the exact prompt, file list and target chat without opening Chrome or creating anything.

The session folder holds:

| File | Contents |
|---|---|
| `chat.md` | every prompt (folded) and answer, tables included, images inline |
| `images/NN.png` | generated images, at full resolution, numbered across the session |
| `session.json` | chat URL, start time, number of asks |
| `error.png` | a screenshot of the page when a run fails |

`redesign` and `logo` can take 5 to 15 minutes. The default `--timeout` is 20m. While it waits, `ask` prints its progress to stderr every 30 seconds. When ChatGPT shows "Something went wrong" with a retry button, `ask` clicks it once before it gives up.

One ask runs per session at a time: a second one fails at once with `session ... is busy`.

### `browser stop`: quit the background Chrome

The first command starts Chrome on the tool's profile and leaves it running. Every later run attaches to it, opens one tab and closes only that tab, so you stay signed in, nothing reloads from cold, and Chrome never sees an unclean shutdown. Commands started together share that one Chrome: only one of them starts it. Leave the window alone during a run. `chatgpt-design browser stop` quits it cleanly. On macOS and Linux it also ends a Chrome that holds the port but no longer answers; it only ever ends a process that is Chrome.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | done |
| 1 | error, such as Chrome failing to start, the profile being in use, ChatGPT's page having changed, or a time-out |
| 2 | bad flags or arguments, or no current session |
| 3 | not logged in: run `chatgpt-design login` |
| 4 | ChatGPT usage limit reached |

### `--json`

`doctor`, `status`, `session`, `shoot` and `ask` (also with `--dry-run`) print their result as JSON on stdout with `--json`. Logs stay on stderr. On failure they print `{"ok": false, "error": "...", "exit": N}` and exit with `N`.

## Privacy and safety

- **Look at your screenshots before you send them.** Screenshots of a logged-in app often show real customer or personal data. Everything you send goes to your ChatGPT account and follows your ChatGPT data settings.
- Nothing is sent before `ask` runs. Use `--dry-run` to check first.
- The tool never asks for, types or stores your password. You sign in yourself, in its own Chrome profile.
  - On macOS the profile is in `~/Library/Application Support/chatgpt-design/chrome-profile`. On Linux it is in `~/.config/chatgpt-design/chrome-profile`.
  - The profile directory has mode 0700.
  - Set `CHATGPT_DESIGN_PROFILE` to move the profile.
  - Your everyday Chrome profile is never touched.
- Images are downloaded only from ChatGPT's own HTTPS hosts (`chatgpt.com`, `*.oaiusercontent.com`, `*.openai.com`). Each image must be at most 50 MB and must decode as an image.
- Inputs are checked:
  - at most 10 screenshots
  - each screenshot is PNG, JPEG, WebP or GIF
  - each screenshot is at most 20 MB
  - prompts are at most 256 KB

## Limits

- This tool automates the chatgpt.com web page. When OpenAI changes that page, a step can break. The tool fails loudly with `error.png` rather than guessing. Please open an issue with the message.
- Automating a consumer web app may conflict with its terms of use. It is your account: use the tool for your own work, at a human pace.
- Runs at the same time share one Chrome, each in its own tab. Only one ask runs per session at a time.
- Set `CHATGPT_DESIGN_CHROME` to the browser binary if Chrome is not found.

## Develop

```sh
make          # lint, test, build to bin/chatgpt-design
make race
```

Prompt templates are in `internal/prompt/templates/`, the ChatGPT page driver is in `internal/browser/chatgpt.go`, and sessions are in `internal/memory/`.

## License

MIT
