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

Everything lives in the project, under `design/chatgpt/`:

```
design/chatgpt/
  project.json                     the current session
  sessions/<date>-<topic>/
    session.json                   topic, ChatGPT chat URL, every round
    brief.md, shots/
    rounds/NN/                     prompt.txt, response.md, images/, meta.json
```

One session is one ChatGPT chat about one topic. **Every `ask` continues the current session's chat**, so ChatGPT keeps the earlier screenshots and answers in mind. Start a new session only when the user asks for a new session or a new chat, or moves to an unrelated topic. If you are unsure, ask.

- `chatgpt-design session show` prints the current session, its chat and its rounds. Read it before starting, and read earlier `rounds/*/response.md` for context.
- `chatgpt-design session list` and `chatgpt-design session use ID` switch to an older session.

## Steps

1. **Session.** Run `chatgpt-design session show`.
   - If it is on this topic, continue in it and skip to step 4 for a follow-up.
   - Otherwise, or if the user asked for a new session, run `chatgpt-design session new <topic>`. It prints the session directory, `<session>` below.
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
   - The first round attaches every shot in `shots/`. Follow-up rounds attach only the `--shot` files you pass, because the chat already has the rest.
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

   - The command opens a Chrome window. Tell the user not to touch it.
   - `redesign` and `logo` can take 5 to 15 minutes. Use a long timeout or run it in the background.
   - Output goes to `<session>/rounds/NN/`: `response.md`, `images/NN.png`, `prompt.txt`, `meta.json`. The session records the round and the chat URL.
   - `--new --topic TOPIC` starts a new session and sends in one step. `--session ID` sends to another session without switching.
6. **Review.** Read `response.md` and look at every image. Summarise ChatGPT's direction in a few lines, list the image paths, and give the chat URL.
   - If ChatGPT returned fewer mockups than screens, send a follow-up with `--mode chat` asking for the missing ones. It lands in the same chat.
7. **Hand off.** Ask which direction the user wants. Implementing it is a separate task, with the `impeccable` skill if it is installed, or as normal frontend work.

## Failures

| Exit code | Message | Do |
|---|---|---|
| 3 | `not logged in` | The user runs `chatgpt-design login`. |
| 4 | `usage limit` | Report it. Retry later or switch the ChatGPT model in the browser. |
| 1 | `the profile is in use` | Another run or the login window is still open. Close it. |
| 1 | `no file input` / `send button` / `composer took` | ChatGPT changed its page. Report it with `<session>/rounds/NN/error.png`. Do not retry more than twice. |
| 1 | `conversation did not open` | The session's chat was deleted or belongs to another account. Ask the user, then start a new session. |
| 2 | `no current session` | Run `chatgpt-design session new <topic>` first. |
| 1 | `timed out` | Open the chat URL in `error.png` or `meta.json`. ChatGPT may have asked a question. |

Never type the user's ChatGPT password and never handle their cookies. The CLI keeps the session in its own Chrome profile and never prints it.
