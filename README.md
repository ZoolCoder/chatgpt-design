# chatgpt-design

Get ChatGPT's design opinion from the terminal or from your coding agent. `chatgpt-design` sends screenshots of your app and a design brief to **your own ChatGPT account**, then saves what comes back:

- the real generated mockup images, not screen captures of them
- the answer as markdown
- a link to the chat, so you can follow up there

There is no API key and no per-image billing. It is one Go binary that drives a dedicated Chrome profile, which you sign in to once. It ships with an agent skill, so Claude Code, Codex and other agents use it when you say "redesign this", "give me mockups" or "ask ChatGPT".

```
chatgpt-design shoot --base http://localhost:3000 --out jam/shots overview=/ settings=/settings
chatgpt-design ask --mode redesign --dir jam
# jam/chatgpt/response.md, jam/chatgpt/images/01.png ..., jam/chatgpt/meta.json
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
chatgpt-design login           # opens Chrome, you sign in to chatgpt.com, the window closes by itself
chatgpt-design status          # prints: logged in
chatgpt-design install-skill   # adds the skill for Claude Code, Codex and other agents
```

`install-skill` writes `SKILL.md` to `~/.claude/skills/chatgpt-design/`, `~/.codex/skills/chatgpt-design/` and `~/.agents/skills/chatgpt-design/`. To install into a project or another agent, pass one or more `--dir` flags.

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

In Claude Code, run the login with `! chatgpt-design login`, so it runs in your session. Run the CLI outside the agent's command sandbox, because it starts Chrome and talks to chatgpt.com.

## Commands

### `shoot`: screenshot a running app

```sh
chatgpt-design shoot --base http://localhost:5173 --out jam/shots home=/ 'billing=/settings?tab=billing'
```

Each `name=path` becomes `NN-name.png` at 1440×900. Quote arguments that contain `?` or `&`. Flags:

| Flag | Default | |
|---|---|---|
| `--width`, `--height` | 1440, 900 | viewport, width 320–1600 |
| `--wait` | 1.5s | settle time after load |
| `--full` | off | whole page instead of the viewport |

It uses a throwaway headless profile and never your ChatGPT one.

### `ask`: send to ChatGPT

```sh
chatgpt-design ask --mode spec --dir jam --goal "fewer setup steps for non-developers"
```

| Mode | Gets back |
|---|---|
| `spec` | a markdown critique with these sections: Verdict, Keep, Findings, Tokens, Sequence |
| `redesign` | one mockup image per screenshot, plus tokens |
| `logo` | three logo concepts and an app icon; needs no screenshots |
| `chat` | the answer to any prompt: `--prompt FILE` or `--prompt -` for stdin |

`--dir JAM` reads `JAM/brief.md` and `JAM/shots/*`, and writes to `JAM/chatgpt/`. You can also pass each part yourself:

| Flag | Passes |
|---|---|
| `--brief` | the brief file |
| `--shot` | one screenshot; repeat it for more, up to 10 |
| `--brand` | a brand sheet |
| `--out` | the output directory |

`--dry-run` prints the exact prompt and file list without opening Chrome.

The output directory holds:

| File | Contents |
|---|---|
| `response.md` | the answer, tables included |
| `images/NN.png` | generated images, at full resolution |
| `prompt.txt` | what was sent |
| `meta.json` | mode, chat URL, file list, warnings |
| `error.png` | a screenshot of the page when a run fails |

`redesign` and `logo` can take 5 to 15 minutes. The default `--timeout` is 20m. A Chrome window opens during the run; leave it alone.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | done |
| 1 | error, such as Chrome failing to start, the profile being in use, ChatGPT's page having changed, or a time-out |
| 2 | bad flags or arguments |
| 3 | not logged in: run `chatgpt-design login` |
| 4 | ChatGPT usage limit reached |

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
- One run at a time per profile, because Chrome locks the profile directory.
- Set `CHATGPT_DESIGN_CHROME` to the browser binary if Chrome is not found.

## Develop

```sh
make          # lint, test, build to bin/chatgpt-design
make race
```

Prompt templates are in `internal/prompt/templates/`, and the ChatGPT page driver is in `internal/browser/chatgpt.go`.

## License

MIT
