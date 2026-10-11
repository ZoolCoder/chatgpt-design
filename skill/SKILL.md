---
name: chatgpt-design
description: Use when the user wants ChatGPT in the loop on a design. That covers redesign mockups as images, a written UI critique with design tokens, logo concepts, or any prompt sent to their ChatGPT. Trigger on "ask ChatGPT", "use chatgpt", "redesign", "mockups", "design review", "logo ideas". Drives the user's own chatgpt.com login through the chatgpt-design CLI. No API key needed.
---

# chatgpt-design

The `chatgpt-design` CLI sends screenshots and a prompt to the user's own ChatGPT account and saves what comes back. It drives a dedicated Chrome profile and does not need an API key. It saves the real generated image files, the answer as markdown, and a link to the chat. This works the same from Claude Code, Codex, or any agent with a shell.

This skill never writes application code. It produces images and prose, then hands off.

## Preflight

1. Run `command -v chatgpt-design`. If the CLI is missing, ask the user before installing it with `go install github.com/zoolcoder/chatgpt-design/cmd/chatgpt-design@latest`.
2. Run `chatgpt-design status`.
   - If it prints `not logged in`, the user signs in once. The login is interactive and the user does it, never you. In Claude Code, tell them to type `! chatgpt-design login`. In Codex or a plain terminal, tell them to run `chatgpt-design login`.
3. Run the CLI outside any command sandbox. It starts Chrome and needs network access to chatgpt.com.

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

   - Each `name=path` argument becomes `<session>/shots/NN-name.png` at 1440×900. Quote any argument with `?` or `&`, because zsh expands them. Add `--full` for whole pages.
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
| 1 | `no DevTools port` | Another Chrome uses the tool's profile. Run `chatgpt-design browser stop`, or close that window. |
| 1 | `no file input` / `send button` / `composer took` | ChatGPT changed its page. Report it with `<session>/error.png`. Do not retry more than twice. |
| 1 | `conversation did not open` | The session's chat was deleted or belongs to another account. Ask the user, then send with `--new`. |
| 2 | `no session` | Pass `--session <name>` or set `CHATGPT_DESIGN_SESSION`. |
| 1 | `timed out` | Open the chat URL from `chatgpt-design session`. ChatGPT may have asked a question. |

Never type the user's ChatGPT password and never handle their cookies. The CLI keeps the session in its own Chrome profile and never prints it.
