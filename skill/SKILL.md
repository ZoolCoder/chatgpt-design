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

## Steps

1. **Jam directory.** Create `design/jams/YYYY-MM-DD-<topic>/` at the repo root.
2. **Brief.** Write `<jam>/brief.md` in under 400 words with these headings:
   - `## Product`
   - `## Users`
   - `## Stack`
   - `## Screens`
   - `## Current tokens`

   Describe the product, not the codebase: ChatGPT has no repo access. If `design/brand.md` exists, pass it with `--brand`.
3. **Screenshots.** Skip this step in `logo` mode.
   - With the app running, capture 4 to 6 key screens:

     ```
     chatgpt-design shoot --base http://localhost:3000 --out <jam>/shots overview=/ 'settings=/settings?tab=team'
     ```

   - Each `name=path` argument becomes `NN-name.png` at 1440×900. Quote any argument with `?` or `&`, because zsh expands them. Add `--full` for whole pages.
   - Or drop existing images into `<jam>/shots/`.
4. **Confirm.** This step is mandatory. Run `chatgpt-design ask --mode <mode> --dir <jam> --dry-run` to print the exact prompt and file list. Show the user these four things:
   - the brief
   - the list of screenshots
   - a warning that screenshots of a logged-in app often show real personal or customer data, so they should look first
   - that everything goes to their ChatGPT account

   Wait for a clear yes. Nothing leaves the machine before this.
5. **Send.**

   ```
   chatgpt-design ask --mode <mode> --dir <jam> [--brand design/brand.md] [--goal "one line on what to optimise for"]
   ```

   - The command opens a Chrome window. Tell the user not to touch it.
   - `redesign` and `logo` can take 5 to 15 minutes. Use a long timeout or run it in the background.
   - Output goes to `<jam>/chatgpt/`: `response.md`, `images/NN.png`, `prompt.txt`, and `meta.json` with the chat URL.
6. **Review.** Read `response.md` and look at every image. Summarise ChatGPT's direction in a few lines, list the image paths, and give the chat URL for follow-ups.
   - If ChatGPT returned fewer mockups than screens, run the command again with `--mode chat` and a prompt asking for the missing ones.
7. **Hand off.** Ask which direction the user wants. Implementing it is a separate task, with the `impeccable` skill if it is installed, or as normal frontend work.

## Failures

| Exit code | Message | Do |
|---|---|---|
| 3 | `not logged in` | The user runs `chatgpt-design login`. |
| 4 | `usage limit` | Report it. Retry later or switch the ChatGPT model in the browser. |
| 1 | `the profile is in use` | Another run or the login window is still open. Close it. |
| 1 | `no file input` / `send button` / `composer took` | ChatGPT changed its page. Report it with `<jam>/chatgpt/error.png`. Do not retry more than twice. |
| 1 | `timed out` | Open the chat URL in `error.png` or `meta.json`. ChatGPT may have asked a question. |

Never type the user's ChatGPT password and never handle their cookies. The CLI keeps the session in its own Chrome profile and never prints it.
