# Changelog

## Unreleased

New
- `/model <name>` pins the model for one chat (`/model auto` lets Prism choose again); the name can be the short one the list shows. `/models <text>` filters the list, so a provider with hundreds of models stays readable (the list caps at 60 rows and says how many are hidden).
- ChatGPT sign-in now lists your plan's real models: the Codex backend gates its model list on a `client_version` query parameter the official CLI sends, which Prism now sends too (on the model list and every request). Codex requests also carry the reasoning block the backend requires — without it it returns 200 with an empty stream.
- `/login` signs in to plans you already pay for, the same way each vendor's own tools do it. Accounts: ChatGPT, Claude, Grok, Gemini and GitHub Copilot. ChatGPT, Claude, Grok and Gemini sign in with your browser (Prism opens it for you; ChatGPT and Grok can also use a device code), Copilot uses a device code. Prism keeps checking, asks the service which models your plan can use, and they appear in `/models` under the account's name, answering only when picked by name. Tokens live in `subscriptions.json` (git-ignored, mode 600) and refresh themselves. `/login rm <name>` signs out. ChatGPT and Claude requests are translated to and from the Codex Responses and Anthropic Messages formats, Gemini to the Code Assist format, so the agent keeps speaking plain OpenAI chat.
- Device-code sign-in for GitHub Copilot: run `/login copilot`, open the link, type the code.
- `/key` inside Prism (terminal and browser) adds or removes a provider key: `/key` lists every free and paid provider with a masked key, `/key <name> <secret>` saves one, `/key rm <name>` removes it. The router watches `keys.conf`, so a new key works without a restart. Paid presets grew to 22 (OpenAI, Anthropic, DeepSeek, xAI, Mistral, Gemini, Together, Fireworks, Cerebras, SambaNova, OpenRouter, Hugging Face, Perplexity, Cohere, Moonshot, Qwen, Z.AI, DeepInfra, Novita, Lambda, Ollama, LM Studio); when a key is saved the router asks that service which models it serves and lists them automatically, falling back to known model names when it does not answer. Paid models still answer only when picked by name (or with `PRISM_USE_PAID=1`).
- Web search: a built-in `web` tool searches DuckDuckGo (no key needed; Tavily or Brave first when `TAVILY_API_KEY` or `BRAVE_API_KEY` is set) and reads public pages as clean text. It never reaches your computer, local network or the router, follows at most 5 redirects, and marks everything it returns as untrusted.
- Project tree: `/tree` (terminal and browser) and `prism tree` show every project file with git status, what Prism read or changed in this chat, and which files many others depend on; `/tree changed` or `/tree <text>` narrows it. `prism tree --html` writes an interactive page (search, filters, imports and imported-by), and the browser version has a live Files panel that updates as files change. It respects .gitignore and uses inotify, so it costs nothing while nothing changes.
- The AI gets a compact project map in its instructions: folders, file sizes, the most depended-on files and their main functions, within about 600 tokens.
- Browse tool: after building or changing a web page, Prism uses it like a person in a headless browser. It finds buttons, fields and checkboxes by their role and accessible name (worked out in the page, not from pixels), clicks and types with real input events, reloads, checks what should and should not be on the page, and reports page errors. It keeps fixing until the steps pass, and saves a screenshot of the end state under `.prism/checks`.
- Crew: when you ask for several agents or parallel work, Prism runs 2 or 3 helper agents at once, each editing its own files, then a read-only reviewer checks the result. A live panel shows each worker in the terminal and in the browser.
- Screenshot tool: Prism can look at a page it built. The headless browser (about 126 MB) is downloaded once on first use, and a free model that can see images describes the picture. Public URLs need your approval.
- `prism keys`: lists your keys; `prism keys add` connects a model you pay for (OpenAI, Anthropic, DeepSeek, OpenRouter, Ollama on this computer, or any OpenAI-compatible URL); `prism keys remove <id>` removes it. These models only answer when you pick them by name, never for the free `auto`.
- `prism update` pulls the newest version and rebuilds.
- MCP setup: `/mcp add` picks a server from a built-in list (fetch, context7, deepwiki, github, tavily, git, memory, thinking, files) and asks for any key it needs; `/mcp remove <name>` and `/mcp test <name>` manage them. Missing keys and failed servers now say what to fix. Docs updated in docs/MCP.md.
- 11 new built-in skills (24 in total): prove-it, retry-ladder and handoff-note shape how Prism works by default; plus test-my-app, security-review, accessibility-review, release-check, docker-help, use-an-api, clean-data and game-rewrite.
- Delete single memories: `/memory` lists numbered memories, `/memory forget <n>` deletes one (or several: `forget 2 5`), `/memory forget all` clears everything. A numbered forget is refused if the memory changed since the list was shown.
- Router: `auto:code`, `auto:fast`, `auto:review` and `auto:vision` pick models by role, and at most 2 requests run per provider at a time.
- Weekly free-model check: `router/check.mjs` tests every free model and a weekly workflow opens an issue when some stop working. Kilo dropped free MiniMax M3.1 Flash; free Nemotron Lightning replaces it.
- Benchmarks: `bench/compare.sh` and `bench/harness/` run the same tasks through Prism, Aider and other open-source agents on the same free models, with new charts.

Windows
- Tested on Windows 11 with WSL (Debian). New step-by-step guide in docs/WINDOWS.md.
- `/web` opens your normal browser on Windows.
- Clearer Docker errors: a broken Windows link in WSL, broken Docker login settings, and Docker Hub download limits each say how to fix them. The installer's Docker message in WSL points to Docker Desktop's WSL integration.

Faster
- `prism` opens in about a quarter of a second instead of 11: it no longer rebuilds the Docker images on every start (a cached build still cost ~10s) and only checks the router with one quick request. Images rebuild by themselves when Prism's own files change.
- An idle Prism costs nothing: the screen stops redrawing 3 seconds after the last key or event (before, the chat screen sent ~47 KB a second to the terminal forever) and hands unused memory back. Each extra session needs about 9 MB of private memory (was 11).
- The project map is built in the background, so the first screen does not wait for it.
- The crew tool and its instructions (~450 tokens) are only sent once a chat asks for several agents.
- `bench/perf.py` measures first-screen time, idle CPU, idle terminal output and memory per session.
- The start of every request (tools, instructions, project files) stays byte-for-byte the same for the whole chat, so providers can reuse their cached copy: automatic memory updates and coming back from the browser no longer rewrite the instructions mid-chat (only commands you run, like `/memory forget`, do), and old tool output is only trimmed in one batch once a chat passes a quarter of the context window.
- Prism asks the router for the model that answered last (`prism_prefer`), whose cache still holds the conversation; slow answers are still raced as before. It only does this when the router says it understands the hint, so an older router keeps working.
- The project's file list (taken once when a chat starts) is part of the instructions, so easy tasks start with read or edit instead of a list call.
- The AI sends all the edits it already knows in one reply and stops once the real check passes, without re-reading files.
- `prism run` and `/usage` show how many prompt tokens the provider served from its cache.

Fixed
- When free models are rate-limited (HTTP 429), Prism waits 5, 15 then 30 seconds and asks again instead of ending the task. The note says "free models are busy, trying again in Ns…".
- Retry notes say what happened: "the model stopped mid-reply, trying another…" or "that model failed, trying another…" instead of "reply dropped, asking again…".
- `prism` prints "starting prism…" while Docker starts, instead of a blank terminal.
- Tool lines no longer run into their argument (`screenshotindex.html` → `screenshot index.html`).
- `bench/compare.sh` re-runs a task the provider rate-limited instead of counting it as a fail.

Changed
- New demo video and GIF: a real screen recording of Prism building an app on a Linux desktop, replacing the old setup video and GIF.
- Benchmarks in the README now compare Prism with opencode, Aider and mini-swe-agent (60 runs) and with Aider on one pinned model.
- The AI is told to run the real check once, skip re-reading what a command already showed, and batch independent tool calls; it no longer opens a browser to check its work.
- The browser handoff screen can open the page again (`o`) or copy the link (`c`), and the link is clickable in supporting terminals.
- When several approvals arrive at once they queue instead of replacing each other.
- `prism run` prints the answer first, then waits up to 15 seconds for the memory update.
- The terminal size is passed into the container so the layout fits at start.
- Hints like "Run: prism status" show a command that works even before `prism` is on your PATH.
- Browser: `/web` explains you're already there, unknown commands point to `/help`, closing the terminal says how to pick the chat back up, and the input box focus shows as the rainbow border.
- `/help` and error messages link to the GitHub issues page.
- README: "Try it in 2 minutes" box, setup video, Ubuntu in the platform table, new benchmark charts and skill list.

Fixes
- Memory updates from two Prism windows can no longer overwrite each other or bring back a forgotten memory.
- `web/app.js.bak` removed from the repo.

## 0.2.0

- 3 new themes: gruvbox, nord, everforest.
- Fixes: stopping a task no longer makes the next message resume it, tools stay inside the project folder, two Prism windows no longer clash, old chats are trimmed to the newest 50.
- Tests for the tools, the AI loop, MCP and the router run on every push.

- MCP support: connect MCP servers for extra tools like file access, web pages, git, a browser or GitHub. `/mcp` shows what is connected; see docs/MCP.md.
- `prism web`: Prism in your browser, with the same look, Shard and the beam.
- `prism fix`: computer help mode. Runs on your computer and asks before every command or file change.
- Skills: 13 built-in playbooks (website, browser game, Discord bot, learn mode and more), plus your own in `.prism/skills/` or `~/.config/prism/skills/`. `/skills` lists them, `/skills new <name>` starts a new one.
- Code in replies and diffs is shown in colour.
- Steps from earlier turns fold into one line; click to open, or `ctrl+o` for all.
- Smooth scrolling with a scrollbar.
- Commands that leave something running in the background no longer hang.
- Benchmark charts in the README. Five coding tasks: 115 s with a free NVIDIA key (465 s at first), 168 s with no keys.
- /web: move a chat to the browser and back
- browser version has the same slash commands
- Shard and the beam reworked: no stray pixels, terminal and browser match

## 0.1.0

First release.

- Terminal coding assistant that runs on free AI, with no keys needed.
- Router that races free models and remembers which ones are fast.
- Memory: `PRISM.md` rules, automatic project memory, saved chats, `/usage`.
- Shard, the Last Prism beam and five themes.
- Runs in Docker and only sees the folder you choose.
