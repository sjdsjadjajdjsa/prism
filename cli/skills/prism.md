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
- `/quit` closes Prism.

Models and accounts
- `/models` shows which free models are ready; `/models <text>` narrows the list.
- `/model` opens a picker; `/model <name>` pins one; `/model auto` returns to automatic.
- `/key` lists provider keys; `/key <name> <secret>` adds one; `/key rm <name>` removes it.
- `/login` lists accounts; `/login <name>` signs in; `/login rm <name>` signs out.
- `/usage` free AI used today.

Project
- `/init` writes PRISM.md, rules Prism always follows in this folder.
- `/memory` shows what Prism remembers; `/memory forget <n>` removes one, `/memory forget all` clears.
- `/skills` lists playbooks; `/skills new <name>` writes your own (kept in ~/.config/prism/skills/ or .prism/skills/).
- `/mcp` shows connected tools; `/mcp add`, `/mcp remove <name>`, `/mcp test <name>`.
- `/tree` project files; `/tree changed`; `/tree <text>` filters.
- `/files` browse the project and open a file read-only (arrows, enter, type to filter, esc); `/open <path>` opens one file. In the browser, click a file in Files to view it, then Edit and Save.
- `/web` opens this chat in the browser; press enter in the terminal to come back (in web mode `o` reopens, `c` copies the link).

Settings
- `/theme` or `/theme <name>`, `/beam` finish beam on/off, `/quiet` Shard's lines on/off.

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

Paid or own models: `/key <name> <secret>` with a preset name: openai, anthropic, deepseek, xai, mistral-paid, gemini-paid, together, fireworks, cerebras, sambanova, openrouter-paid, huggingface, perplexity, cohere, moonshot, dashscope, zhipu, deepinfra, novita, lambda. Local ones need no key: ollama-local, lmstudio. `/key` shows the full list and which are set. Important: models from your own keys answer only when you pick them by name (`/model` picker or `/model <name>`). Auto mode never spends them, so adding a key costs nothing until you choose it.

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
