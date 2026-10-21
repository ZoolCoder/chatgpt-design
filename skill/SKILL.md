---
name: chatgpt-design
description: Use when the user wants ChatGPT in the loop on a design. That covers redesign mockups as images, a written UI critique with design tokens, logo concepts, or any prompt sent to their ChatGPT. Trigger on "ask ChatGPT", "use chatgpt", "redesign", "mockups", "design review", "logo ideas". Drives the user's own chatgpt.com login through the chatgpt-design CLI. No API key needed.
---

# chatgpt-design

The `chatgpt-design` CLI sends screenshots and a prompt to the user's own ChatGPT account and saves what comes back. It drives a dedicated Chrome profile and does not need an API key. It saves the real generated image files, the answer as markdown, and a link to the chat. This works the same from Claude Code, Codex, or any agent with a shell.

This skill never writes application code. It produces images and prose, then hands off.

## Preflight

1. Run `command -v chatgpt-design`. If the CLI is missing, ask the user before installing it with `go install github.com/zoolcoder/chatgpt-design/cmd/chatgpt-design@latest`.
2. Run `chatgpt-design doctor`. It checks Chrome, the profile, the browser, the sign-in, the session and the installed skill, and prints a fix under each failure. Add `--json` to parse it.
   - If the `login` check fails, the user signs in once. The login is interactive and the user does it, never you. In Claude Code, tell them to type `! chatgpt-design login`. In Codex or a plain terminal, tell them to run `chatgpt-design login`.
3. Run every `chatgpt-design` command that opens Chrome (`doctor`, `login`, `status`, `shoot`, `ask` without `--dry-run`, `browser stop`) outside any command sandbox, from the first call. In Claude Code, set `dangerouslyDisableSandbox: true` on that Bash call. The CLI talks to its Chrome on 127.0.0.1 and to chatgpt.com, and a sandbox blocks both. Only `session` and `ask --dry-run` can stay in the sandbox.
4. Run one Chrome-opening command at a time and wait for it to finish. Never start a retry while a run is still going.

## Modes

| Mode | When the user says | Gets back |
|---|---|---|
| `redesign` | "redesign", "mockups", "new look" | one mockup image per screen, plus tokens |
| `spec` | "critique", "design review", "what tokens" | a markdown critique: Verdict, Keep, Findings, Tokens, Sequence |
| `logo` | "logo", "app icon", "brand mark" | three logo concepts and an app icon |
| `chat` | anything else for ChatGPT | the answer to `--prompt FILE` |

If the mode is unclear, ask.

## Sessions

A session is your agent session. In Claude Code the CLI reads `CLAUDE_CODE_SESSION_ID` by itself. In Codex or elsewhere, pass `--session <name>` or set `CHATGPT_DESIGN_SESSION`. Everything lives in one folder in the project:

```
design/chatgpt/<session>/
  session.json                     chat URL, when it started, how many asks
  chat.md                          every prompt and answer, images inline
  images/                          01.png, 02.png ... numbered across the session
  shots/                           screenshots to send
  brief.md                         optional brief
```

**Every `ask` continues the session's ChatGPT chat**, so ChatGPT keeps the earlier screenshots and answers in mind. Pass `--new` only when the user asks for a new chat.

- `chatgpt-design session` prints the folder, the chat URL and the number of asks. Read `<session>/chat.md` for earlier answers.
- `chatgpt-design session list` lists every session in the project.
- `chatgpt-design session export` writes `<session>/session.html`: the whole session with its images built in, one file to open or share. Offer it when the user wants to show the work to someone.

## Steps

1. **Session.** Run `chatgpt-design session`. It prints the session folder, `<session>` below.
   - If it already has asks, this is a follow-up: skip to step 4.
2. **Brief.** Write `<session>/brief.md` in under 400 words with these headings:
   - `## Product`
   - `## Users`
   - `## Stack`
   - `## Screens`
   - `## Current tokens`

   Describe the product, not the codebase: ChatGPT has no repo access. If `design/brand.md` exists, pass it with `--brand`.
3. **Screenshots.** Skip this step in `logo` mode.
   - With the app running, capture 4 to 6 key screens into the session:

     ```
     chatgpt-design shoot --base http://localhost:3000 overview=/ 'settings=/settings?tab=team'
     ```

   - Each `name=path` argument becomes `<session>/shots/NN-name.png` at 1440×900. Quote any argument with `?` or `&`, because zsh expands them. Add `--full` for whole pages, `--mobile` for a phone at 390×844, and `--dark` for the dark color scheme. `--sizes 390,768,1440` shoots each page at every width as `NN-name@WIDTH.png`; mind the limit of 10 shots per ask.
   - After applying a redesign, shoot the same names again with `--diff`. It prints how much of each page changed and saves the changes in red to `shots/diff/`, which no ask sends.
   - Shooting a name again replaces its file and keeps its number. A page that answers with HTTP 400 or more fails the run, so fix the path or start the app first.
   - Or drop existing images into `<session>/shots/`.
   - The first ask of a chat attaches every shot in `shots/`. Follow-ups attach only the `--shot` files you pass, because the chat already has the rest.
4. **Confirm.** This step is mandatory. Run `chatgpt-design ask --mode <mode> --dry-run` (same flags as the real run) to print the exact prompt, the file list and the chat it goes to. Show the user these things:
   - the brief, or the follow-up prompt
   - the list of screenshots
   - a warning that screenshots of a logged-in app often show real personal or customer data, so they should look first
   - that everything goes to their ChatGPT account, in the session's chat

   Wait for a clear yes. Nothing leaves the machine before this.
5. **Send.**

   ```
   chatgpt-design ask --mode <mode> [--shot FILE ...] [--brand design/brand.md] [--goal "one line on what to optimise for"]
   chatgpt-design ask --mode chat --prompt FILE      # a follow-up in your own words
   ```

   - Chrome keeps running between runs; each run opens and closes its own tab. Tell the user not to touch it during a run.
   - `redesign` and `logo` can take 5 to 15 minutes. Use a long timeout or run it in the background.
   - The prompt and answer are appended to `<session>/chat.md`, and images go to `<session>/images/NN.png`, numbered across the session. The command prints the new image paths.
   - `--new` starts a new ChatGPT chat in the same session.
6. **Review.** Read the newest entry in `chat.md` and look at every new image. Summarise ChatGPT's direction in a few lines, list the image paths, and give the chat URL.
   - If ChatGPT returned fewer mockups than screens, send a follow-up with `--mode chat` asking for the missing ones. It lands in the same chat.
7. **Hand off.** Ask which direction the user wants. Implementing it is a separate task, with the `impeccable` skill if it is installed, or as normal frontend work.

## Failures

| Exit code | Message | Do |
|---|---|---|
| 3 | `not logged in` | The user runs `chatgpt-design login`. |
| 4 | `usage limit` | Report it. Retry later or switch the ChatGPT model in the browser. |
| 1 | `a command sandbox blocks localhost` | Rerun the same command outside the sandbox. Nothing was started. |
| 1 | `no DevTools port` / `does not answer` / `already uses` | Run `chatgpt-design browser stop`, then rerun once. |
| 1 | `still starting Chrome` / `still shutting down` | Another run is mid-start or mid-stop. Wait a minute, then rerun. |
| 1 | `no file input` / `send button` / `composer took` | ChatGPT changed its page. Report it with `<session>/error.png`. Do not retry more than twice. |
| 1 | `conversation did not open` | The session's chat was deleted or belongs to another account. Ask the user, then send with `--new`. |
| 2 | `no session` | Pass `--session <name>` or set `CHATGPT_DESIGN_SESSION`. |
| 1 | `session ... is busy` | Another ask uses this session. Wait for it to finish. Never pass a second session to the same chat. |
| 1 | `HTTP 404` (or another code) | `shoot` reached an error page. Check `--base` and the path. |
| 1 | `timed out` | Open the chat URL from `chatgpt-design session`. ChatGPT may have asked a question. If it is still answering, rerun the same command with `--collect`. |
| 1 | `the ChatGPT tab closed during the run` | The chat URL is saved as soon as the message lands, so rerun the same command with `--collect`. If the session still has no chat, add `--chat latest`. |
| 1 | `ChatGPT: Something went wrong` | ChatGPT failed twice: the CLI already clicked its retry once. Rerun the same command with `--collect` after a minute. |
| 1 | `nothing to collect` | The chat's last message never got an answer. Rerun the same command without `--collect` to send it. |
| 1 | `the message was not sent` | Check `<session>/error.png`. If the prompt shows in the chat, rerun the same command with `--collect` instead of sending again. |

Long asks print `still waiting (...)` to stderr every 30 seconds. That is progress, not an error.

Add `--json` to `doctor`, `status`, `session`, `shoot` or `ask` to get the result as JSON on stdout. On failure it prints `{"ok": false, "error": ..., "exit": N}`.

## With Claude in Chrome

This section applies only when the `mcp__claude-in-chrome__*` tools are available. They act in the user's everyday Chrome, which is a different browser from the CLI's profile and has its own ChatGPT sign-in. The CLI stays the default for `ask`: it saves full-resolution images and costs no screenshots. Use the extension for these three cases:

- **Fallback ask.** Use it when the CLI is missing or its `login` check fails and the user prefers not to set it up. Ask the user first.
  1. Open a new tab on chatgpt.com.
  2. Attach the shots with `file_upload`.
  3. Paste the prompt: the one `ask --dry-run` prints, or one you write from the brief.
  4. Read the answer with `get_page_text` and append the prompt and answer to `<session>/chat.md`.
  - Generated images stay in the chat: give the chat URL and say they were not saved.
  - Step 4's confirmation applies unchanged.
- **Pages behind the app's login.** `shoot` cannot reach these, but the user's Chrome can. View them there to describe or critique them. Those screenshots reach you only as images, never as files. If one must go to ChatGPT, ask the user to save it into `<session>/shots/`. The personal-data warning applies.
- **Review.** In step 6, open the chat URL, or `file://<session>/session.html` after `session export`, in a new tab, so the user sees the result with you.

If chatgpt.com shows a sign-in page, the user signs in in that tab.

Never type the user's ChatGPT password and never handle their cookies. The CLI keeps the session in its own Chrome profile and never prints it.
