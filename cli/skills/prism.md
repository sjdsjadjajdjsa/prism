---
name: prism
description: Answer questions about Prism itself: commands, keys, logins, models, tabs.
---

# Skill: Prism guide

Use this when the user asks how Prism works, what a command does, how to sign in, add a key, pick a model, or use tabs. Answer in plain words, short, no jargon. Always suggest the exact command to type, like `/login chatgpt`. If unsure, say so and point to `/help`.

## Commands (type in the chat box; tab completes)

Chat
- `/new` fresh chat. `/clear` clears the screen and starts fresh (saves first).
- `/resume` lists earlier chats in this folder; `/resume <number>` reopens one.
- `/compact` summarises the conversation to free context; `/compact <focus>` guides the summary. Prism hints at it near 70% full.
- `/undo` puts back the files Prism changed in its last turn (files you edited yourself afterwards are left alone).
- `/plan` or `/plan <task>` plans first: Prism may read files and search the web but can't edit, write or run commands. It answers with the goal, steps, files and how it will check the work, then asks: run this plan, keep planning, or leave plan mode. The status bar says `plan mode`; `/plan` again or `/plan off` turns it off.
- `/quit` closes Prism.

Models and accounts
- `/models` lists every model in three groups: free models, your plans (from `/login`) and your own keys (from `/key`), each with ready or "back in Ns"; `/models <text>` narrows the list.
- `/model` opens a picker; `/model <name>` pins one; `/model auto` returns to automatic.
- `/key` lists provider keys; `/key <name> <secret>` adds one; `/key rm <name>` removes it.
- `/login` lists accounts; `/login <name>` signs in; `/login rm <name>` signs out.
- `/usage` requests and tokens used today and in this chat, and how many went to your plans or keys instead of free models. Each answer's line and the status bar say `free`, `plan` or `yours`.

Project
- `/init` writes PRISM.md, rules Prism always follows in this folder.
- `/memory` shows what Prism remembers; `/memory forget <n>` removes one, `/memory forget all` clears.
- `/skills` lists playbooks; `/skills new <name>` writes your own (kept in ~/.config/prism/skills/ or .prism/skills/).
- `/commands` lists saved commands: a prompt saved as `.prism/commands/<name>.md` (or ~/.config/prism/commands/) becomes `/<name>`. `/<name> some words` sends the file's text with `$ARGUMENTS` replaced by the words (`$1`, `$2` single words; with no placeholder the words go at the end). Optional front matter `description:` is the one-line help. `/commands new <name>` writes a starter file. Built-in names always win; edits take effect right away.
- `/mcp` shows connected tools; `/mcp add`, `/mcp remove <name>`, `/mcp test <name>`.
- `/tree` (or `/files`) browses the project: arrows move, enter reads a file, ctrl+e opens it in the editor, typing filters, esc closes. `/tree changed` and `/tree <text>` print a list instead. `/open <path>` reads one file. In the browser, `/tree` opens the Files panel: click a file to view it, then Edit and Save.
- `/editor` or ctrl+e opens Neovim beside the chat; `/editor <file>` opens a file in it. ctrl+e switches the keyboard between the chat and the editor, a click does too, `/editor` again hides it (open files stay), `:q` closes it. Drag the line between them to resize. Files Prism changes reload in the editor by themselves. Neovim comes with Prism; nothing to install.
- `/web` opens this chat in the browser; press enter in the terminal to come back (in web mode `o` reopens, `c` copies the link).

Settings
- `/theme` or `/theme <name>`, `/beam` finish beam on/off, `/quiet` Shard's lines on/off.

Safety and checks
- Prism asks before risky commands (deleting folders, `git reset --hard`, `git push`, publishing or deploying, `curl … | sh`, `ssh`, dropping database data, `mkfs`, `shutdown`). Answer y (yes once), a (yes for this chat) or n (no).
- `/rules` lists the rules for which commands ask first. Own rules go in `.prism/rules.json` (this project) or `~/.config/prism/rules.json` (every project) with `allow`, `ask` and `block` lists; `*` matches anything. A project's `allow` rules only count after `/rules trust` and turn off again when the file changes; `/rules off` turns them off.
- `/hooks` lists your own checks from `.prism/hooks.json`: `before_bash` (runs before each command; if it fails the command doesn't run), `after_edit` (after a file is written, e.g. a formatter, `match` picks files) and `turn_end` (at the end of a turn that changed things; a `must_pass` hook that fails sends Prism back to work, up to 3 tries). Hooks stay off until `/hooks trust`, turn off again when the file changes; `/hooks off` turns them off.
- If a free model goes in circles, Prism notices and switches to another one by itself.

Tabs
- `/tabs` lists open chats and their status; `/rename <name>` names this tab; `/ask <n> <question>` asks about another tab's chat without disturbing it.

## Keys

- ctrl+t new tab, ctrl+w close tab (asks first if busy, answer y or n).
- ctrl+pgup / ctrl+pgdown (or ctrl+shift+tab / ctrl+tab) switch tabs; alt+1..9 jump to a tab.
- esc stops the current turn (or says no to a permission question). enter sends; shift+enter or ctrl+j adds a new line; ctrl+u clears the input.
- ctrl+o opens or folds all steps. ctrl+y changes theme. pgup/pgdown scroll, end jumps to the latest. ctrl+c quits.
- When Prism asks to run something: y or enter allows, n or esc refuses.

## Free vs paid models

Prism works with no keys: a built-in router picks among free models. `prism setup` (in a normal terminal, not in chat) adds free keys for more speed. `prism status` shows which are ready, `prism update` upgrades.

Paid or own models: `/key <name> <secret>` with a preset name: openai, anthropic, deepseek, xai, mistral-paid, gemini-paid, together, fireworks, cerebras, sambanova, openrouter-paid, huggingface, perplexity, cohere, moonshot, dashscope, zhipu, deepinfra, novita, lambda. Local ones need no key: ollama-local, lmstudio. `/key` shows the full list and which are set. Important: paid models from your own keys answer only when you pick them by name (`/model` picker or `/model <name>`). Auto mode never spends them, so adding a paid key costs nothing until you choose it. Models that are free to you (local ones like ollama-local and lmstudio) can also answer for auto.

## Logins (plans you already pay for)

Names: chatgpt, claude, grok, antigravity (Google: Gemini, plus Claude on paid plans), copilot (`/login` shows the current list, and which are signed in). `/login <name>` starts sign-in. Copilot gives a short code and a web address: open it and type the code. The others open a browser page: sign in there, then come back; Prism notices by itself. If the browser does not open, copy the link shown. If the sign-in page shows a code instead of sending you back (Grok does), paste the code or the whole link into Prism and press Enter, or type `/login <name> <code>`. Signed-in plan models show up in `/model` and, like paid keys, answer only when picked. `/login rm <name>` signs out. If sign-in fails, the message says why; try `/login <name>` again.

## The /model picker

`/model` opens a list: up/down move, enter pins the highlighted model, esc closes, typing narrows the list (backspace deletes, ctrl+u clears the filter). The first row is auto. Dot shows ready or "back in Ns" (rate-limited, will return).

## Auto vs pinned

Auto (default): Prism chooses a ready free model for each step and switches if one is busy or limited. Pinned (`/model <name>` or picker): every request in this chat uses that model, and a pin is the only way paid models or plan logins are used. `/model auto` unpins. The choice belongs to the tab.

## Tips

- Long chat getting slow or full: `/compact`. Made a mess: `/undo`. Want a clean slate: `/new`.
- Two jobs at once: ctrl+t for a new tab, `/rename` to label it, `/ask 1 what did you decide?` to peek.
- Teach Prism a habit for this folder: `/init`, then edit PRISM.md.
