<p align="center"><img src="docs/splash.png" alt="Prism start screen" width="820"></p>

<h3 align="center">Free AI that codes, beautifully.</h3>
<p align="center">No subscription. No credit card. No account needed.<br>Already pay for ChatGPT, Claude, Grok, Gemini or Copilot? Sign in and use your plan too.</p>

<p align="center"><img src="docs/prism-demo.gif" alt="Prism builds a todo app with free AI, then the app is used in Firefox" width="820"></p>

Prism is a coding assistant for your terminal that runs on **free AI**, or on the AI plan you already pay for. It reads your code, edits files, runs commands and checks its own work. When it builds a web page, it opens it in a headless browser and clicks through it like a person (add, tick, filter, reload) before calling it done.

It runs inside Docker, so it can only touch the one folder you give it.

> **Try it in 2 minutes**
> 1. Install Docker ([guide](docs/SETUP.md)), then run the install command below.
> 2. Open a new terminal, `cd` into any folder, and type `prism`.
> 3. Press any key and type: `make a snake game I can play in the browser`
>
> Something broken? [Tell us here](https://github.com/sjdsjadjajdjsa/prism/issues/new/choose).

## Quick start

> **New to Docker?** Follow the [step-by-step setup guide](docs/SETUP.md) for Linux, Mac or Windows. It takes about 10 minutes on Linux, a bit longer on Mac or Windows, and each step shows what you should see.

You only need **Docker** (free). Then one command. It runs [install.sh](install.sh), a short script you can read first:

```bash
curl -fsSL https://raw.githubusercontent.com/sjdsjadjajdjsa/prism/main/install.sh | bash
```

Windows: open the Ubuntu app first (see the Windows steps in [docs/SETUP.md](docs/SETUP.md)) and paste the command there. Mac or Linux: paste it into Terminal.

It checks Docker, downloads Prism to `~/.prism` and adds the `prism` command. Then open a new terminal:

```bash
cd ~/my-project
prism
```

The first start takes a few minutes while Docker builds Prism; later starts take a few seconds.

| System | Status |
| --- | --- |
| Linux (Ubuntu and Ubuntu-based) | tested on Linux Mint 22, which is built on Ubuntu 24.04 |
| Linux (Arch-based: CachyOS, Omarchy, Manjaro) | builds and runs on Omarchy; the installer shows `pacman` steps and sets up fish |
| macOS | should work with Docker Desktop, not tested yet |
| Windows | tested on Windows 11 inside WSL (Debian) with Docker Desktop |

New to Docker on Windows? See the [Windows beginner guide](docs/WINDOWS.md).

Prefer to watch? The [demo video](docs/prism-demo.mp4) (51s, no sound) is a real screen recording on Linux: start Prism, ask for an app, then use it in the browser. The two waits are sped up.

[![Prism demo video](docs/prism-demo.png)](docs/prism-demo.mp4)

Tried it on a Mac? Please open an issue with how it went.

<details><summary>Install by hand</summary>

```bash
git clone https://github.com/sjdsjadjajdjsa/prism.git
cd prism
./prism install
```
</details>

Then just type what you want: `fix the bug in app.py`, `make me a to-do list website`, `explain this code`.

## Free AI, no setup

A **router** is a small helper that picks a free AI for each step, and a **key** is a secret code you get free from an AI company's website, pasted in with `prism setup`; you don't need a key to start.

Prism works with **no keys at all**. Out of the box it uses free models from Kilo Gateway, BlockRun and LLM7; the router below hides most of the slowness.

Want it faster and smarter? `prism setup` walks you through free keys, about a minute each. None of them ask for a card.

| Provider | What you get | Key |
| --- | --- | --- |
| Kilo Gateway | Nemotron Ultra, Nemotron Lightning, Laguna | none |
| BlockRun | Nemotron Ultra, Nemotron Lightning | none |
| LLM7 | Codestral | none |
| NVIDIA | Nemotron Ultra and Lightning (fastest), GLM-5.3, GLM-5.3 Flash | free |
| Groq | GPT-OSS 120B, very fast | free |
| Google | Gemini Flash | free |
| Mistral | Codestral | free |
| OpenRouter | free MiniMax and others (50 requests a day) | free |
| Z.ai | GLM-4.7 Flash | free |
| Ollama | any model on your own computer | none |

## Use the plan you already pay for

Prism works with every major AI subscription. Type `/login`, pick your plan with the arrow keys or the mouse, press Enter and sign in in your browser. Your plan's real models appear in `/models`, and `/model` picks one for the chat (`/model auto` goes back to free models). They only answer when you pick them, so free `auto` never spends your plan.

| Plan | Models you get | How you sign in |
| --- | --- | --- |
| ChatGPT (Plus, Pro) | your plan's GPT models | browser |
| Claude (Pro, Max) | Claude Sonnet, Opus, Haiku | browser |
| Grok (SuperGrok) | Grok | browser, or a device code |
| Google Antigravity | Gemini, plus Claude on paid plans | browser with your Google account |
| GitHub Copilot | the models your Copilot plan includes | device code |

If the sign-in page shows a code instead of sending you back, paste the code (or the whole link) into Prism and press Enter. `/login` shows who you're signed in to; `/login rm <name>` signs out. Sign-ins are saved on your computer only (`subscriptions.json`, readable only by you) and renew themselves.

Pay per use instead? `/key <name> <secret>` adds a key for 22 services (OpenAI, Anthropic, DeepSeek, xAI, Mistral, Gemini, OpenRouter and more), and `prism keys add` connects any OpenAI-compatible service. Like plans, they only answer when you pick them.

> These sign-ins use the same app IDs as each company's own tools, so a company can change or block them at any time, and using them may go against its terms.

### How it stays fast

A small router picks the model for every step:

- **Races free models.** If the first one hasn't started answering in a few seconds, the next joins the race, and the first to answer wins.
- **Learns which models are quick** and remembers it between runs.
- **Skips models that are busy or down**, and tries them again later.
- **Fast lane:** on NVIDIA's Nemotron models, short questions skip the slow thinking step.
- **Gets out of loops.** A model that keeps writing the same lines is stopped early, its text is taken back, and another model answers. A model that keeps making the same call with the same result is told so and the next step goes to another model; after two switches Prism stops and says why, instead of using up the free models.

### Benchmarks

Each row is one run of the same tasks on the same computer. A task only counts if a script confirms the result works.

<p align="center"><img src="docs/bench-coding.png" alt="Coding benchmark: 465s at first, 115s now with a free NVIDIA key, 168s with no keys" width="820"></p>
<p align="center"><img src="docs/bench-chat.png" alt="Simple questions: 10.7s to 0.4s" width="820"></p>
<p align="center"><img src="docs/bench-website.png" alt="One website task: 1171s to 24s" width="820"></p>
<p align="center"><img src="docs/bench-light.png" alt="prism starts in 0.27s instead of 11s, idles at 0.2% CPU and 0 KB/s, and uses 9.1 MB per extra session instead of 10.9" width="820"></p>

### Against other coding agents

Same five tasks, same free models, only the agent changes. Every agent went through the same router, three runs per task (60 runs in all).

<p align="center"><img src="docs/bench-harness.png" alt="Prism, opencode, Aider and mini-swe-agent on five tasks: Prism passes 15 of 15 but opencode and Aider are faster on most tasks" width="820"></p>

Prism passed every run, but it is not the fastest: opencode was quicker on 4 of 5 tasks and Aider on 3. Prism runs and checks its work, which costs time. Method, versions and every run: [bench/harness](bench/harness/README.md).

With one model pinned (no router), Prism against Aider:

<p align="center"><img src="docs/bench-pinned.png" alt="Prism 5 of 5 against Aider 4 of 5 with one pinned model; Aider faster on four tasks, one of them a fail" width="820"></p>

Free models are shared, so the same run can be 30% faster or slower on another day. Compare rows loosely. The raw numbers are in [bench/history.json](bench/history.json), `python3 bench/chart.py` redraws the charts, and `bench/run.sh` and `bench/chat.sh` measure your own setup.

## Skills

Skills are short playbooks Prism follows for common jobs. It picks the right one by itself. `/skills new <name>` creates a new one.

| Skill | For |
| --- | --- |
| `website` | a polished site, checked before it says done |
| `fix-bug` | reproduce, find the cause, smallest fix, prove it |
| `python-project` | a new Python project with a venv and tests |
| `web-app-node` | a small Node app with npm scripts |
| `explain-code` | a plain-words tour of a codebase, no changes |
| `git-basics` | safe git: .gitignore, small commits, no secrets |
| `refactor` | restructure without changing behaviour |
| `computer-help` | troubleshooting a Linux computer, asking before changes |
| `browser-game` | a small canvas game with a game loop and input |
| `discord-bot` | a Python discord.py bot skeleton with a slash command |
| `automate-task` | a Python script to automate file chores with dry-run |
| `portfolio-site` | a personal resume or portfolio page with print CSS |
| `learn-mode` | teach by building in small steps with explanations |
| `prove-it` | check the result for real before saying done |
| `retry-ladder` | when a fix fails, change the approach instead of repeating it |
| `handoff-note` | leave a short resume note when work stops unfinished |
| `test-my-app` | use your app like a real user and list what breaks |
| `security-review` | find leaked keys, injection and risky settings |
| `accessibility-review` | check keyboard use, labels and contrast on a web page |
| `release-check` | a go/no-go list before you publish |
| `docker-help` | explain and fix Docker problems, cleaning up safely |
| `use-an-api` | call a web API from a script, keys kept out of the code |
| `clean-data` | clean a CSV, JSON or Excel file into a new file |
| `game-rewrite` | plan and check a game port or rewrite you are allowed to make |

`/skills` lists them. Add your own as `.prism/skills/<name>.md` in a project or `~/.config/prism/skills/<name>.md`. See [cli/skills/README.md](cli/skills/README.md) for the format.

## Crew mode (helper agents)

**Subagents? Why didn't you say so?**

There's no command for them, and you don't need one. Just tell Prism what you want done and that you'd like helpers:

```
use a few helpers: one builds the page, one writes the tests
```
```
do these in parallel: fix the login bug and add a dark theme
```

Prism does the rest:

- **Splits the work.** It hands out the job to 2 or 3 helpers and gives each one its own files. A helper can only change its own files, so two helpers never edit the same thing.
- **Watches them for you.** A live panel shows what each helper is doing, step by step, in the terminal and in the browser.
- **Keeps them going.** If a free model is busy, the helper waits for one to free up and carries on. Helpers spread across different models, so they don't all wait on the same one. A helper that runs too long is stopped instead of hanging forever.
- **Checks the result.** If you ask for a review, a separate read-only helper looks over everything they changed. Any problems it finds go back for one round of fixes.
- **Tests it at the end.** When the helpers finish, Prism runs your project's checks itself before telling you it's done.

Press `esc` at any time to stop the whole crew.

## Plan first

Type `/plan` (or `/plan add a login page`) and Prism looks around before it touches anything. It can read files and search the web, but it can't edit, write or run commands. It answers with a short plan: the goal, the steps and files, and how it will check the work.

When the plan is ready you pick:

- **run this plan**: plan mode turns off and Prism starts
- **keep planning**: type what to change
- **leave plan mode**: nothing runs

The status bar says `plan mode` while it's on. `/plan` again (or `/plan off`) turns it off. Works the same in the terminal and the browser.

## Asks before risky commands

Prism runs most commands on its own, inside the Docker box. It stops and asks first when a command could do real damage or reach other people:

- deleting folders (`rm -rf src`; clearing `node_modules`, `dist`, `build` and other cache folders doesn't ask)
- throwing away changes (`git reset --hard`, `git checkout -- .`)
- `git push`, and force pushes
- publishing or deploying (`npm publish`, `docker push`, `gh pr create`, `vercel --prod`, …)
- running a script straight from the internet (`curl … | sh`)
- connecting to another computer (`ssh`, `scp`)
- deleting database data (`DROP TABLE`, `dropdb`, …)
- commands that can break the system (`mkfs`, `shutdown`, …)

You answer **yes, once**, **yes for this chat** (the same kind of command won't ask again until `/new`) or **no** (Prism is told not to try another way). In the terminal press `y`, `a` or `n`; in the browser click or press the same keys. `prism fix` still asks before every command, and `prism run` without a terminal answers no.

**Your own rules.** Put them in `.prism/rules.json` for one project, or `~/.config/prism/rules.json` for every project:

```json
{
  "allow": ["git push origin main", "npm publish*"],
  "ask":   ["docker *"],
  "block": ["git push --force*"]
}
```

`*` matches anything. `block` stops the command, `ask` always asks, `allow` runs it without asking. A project's `allow` rules only count after `/rules trust`, and any change to the file turns them off again, so a project you download can't let itself run risky commands. `/rules` lists everything, `/rules off` turns a project's allow rules off.

## Hooks (your own checks)

Hooks are your own commands that Prism runs at set moments. Put them in `.prism/hooks.json`:

```json
{
  "before_bash": [{ "run": "./scripts/guard.sh" }],
  "after_edit":  [{ "run": "gofmt -w \"$PRISM_FILE\"", "match": "*.go" }],
  "turn_end":    [{ "run": "go test ./...", "must_pass": true }, "notify-send 'prism is done'"]
}
```

| When | What happens |
| --- | --- |
| `before_bash` | runs before each command the AI runs (`$PRISM_COMMAND` holds it). If your hook fails, the command doesn't run and the AI is told why. |
| `after_edit` | runs after the AI writes a file (`$PRISM_FILE`), for example a formatter. `match` picks files by name, like `*.go`. |
| `turn_end` | runs when the AI is about to finish a turn that changed files or ran commands. If a `must_pass` hook fails, the AI gets its output and keeps working, up to 3 tries. |

A hook is a command, or `{ "run", "match", "must_pass", "timeout" }` (timeout in seconds: 60 by default, 300 for `turn_end`, 600 at most). Hooks run in the project folder, inside the Docker box (on your computer in `prism fix`).

**Hooks stay off until you trust them.** `/hooks` lists them, `/hooks trust` turns them on and `/hooks off` turns them off. If the file changes, even when the AI edits it, they're off again until you trust them again. A folder you cloned can't run anything on its own.

## Tools and plugins (MCP)

Prism can connect MCP servers to give the AI extra tools such as file access, web pages, library docs, git or GitHub. Add a `.prism/mcp.json` like this to connect the filesystem server:

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
    }
  }
}
```

Type `/mcp` to see what is connected. Put the same file in `~/.config/prism/mcp.json` to use a server in every project. See [docs/MCP.md](docs/MCP.md) for more.

## Computer help

```bash
prism fix
```

Prism normally lives in a Docker box and can't see your computer. `prism fix` runs it on your computer instead, so it can find out why the Wi-Fi drops, the disk is full or an app won't start. It looks around with read-only commands, explains what it finds in plain words, and **asks you before every command or file change** (`y` allow, `n` no). Tested on Linux; Mac should work but hasn't been tried yet.

<p align="center"><img src="docs/fix.png" alt="Prism asking before running a command" width="820"></p>

## In your browser

```bash
prism web
```

The same Prism, Shard and beam included, in your browser. It opens by itself; the address is printed in the terminal (usually http://127.0.0.1:4849; if that port is taken it uses the next free one up to 4899, and the terminal shows the exact address). It only listens on your own computer.

<p align="center"><img src="docs/web.png" alt="Prism in the browser, firing the Last Prism beam" width="820"></p>

Inside the terminal app, `/web` moves the current chat to your browser (same history and memory). Close the tab or press enter in the terminal to come back, and everything done in the browser is there. The browser version understands the same slash commands, with the same suggestion list. Commands that need a choice (`/model`, `/login`, `/theme`, `/resume`, `/mcp add`, `/tabs`) open a list you can click or drive with the arrow keys, Enter and Esc, and it fits a phone screen too.

## Project tree

<p align="center"><img src="docs/tree.png" alt="The project tree in the browser: files with change marks on the left, the selected file's functions and importers on the right" width="820"></p>

`/tree` shows every file with what changed: `M` changed, `?` new, `●` Prism edited it, `◦` Prism read it, `★` many files use it. `/tree changed` or `/tree <text>` narrows it. In the browser, the **Files** button opens a live, searchable tree that updates as files change, with each file's functions and which files it imports or is imported by. `prism tree --html` writes the same view as a page you can open anywhere.

It respects `.gitignore`, and watching for changes costs nothing while nothing changes. Prism also gets a short version of the map (about 600 tokens), so easy tasks start in the right file.

## Web search

Prism can search the web and read pages when it needs facts it doesn't have, like current docs, versions or an error message. It uses DuckDuckGo, so no key is needed; if `TAVILY_API_KEY` or `BRAVE_API_KEY` is set, it uses that first. It only reads public pages (never your computer or local network), and it treats what it finds as information, not instructions.

## Memory

- **`PRISM.md`**: rules for your project that Prism always follows. `/init` writes one for you. It also reads `AGENTS.md` and `CLAUDE.md`.
- **Automatic memory**: after each task Prism saves what it learned about the project, so a new chat already knows how your project works. `/memory` lists numbered memories, `/memory forget <n>` deletes one (or several: `forget 2 5`), `/memory forget all` clears everything.
- **Every chat is saved.** `/resume` reopens one, `prism continue` picks up the last.
- **Saves free usage**: old file reads are trimmed and long chats are summarized, keeping your latest request word for word. `/usage` shows what you used today.

## Commands

| In the terminal | |
| --- | --- |
| `prism` | start in the current folder |
| `prism <folder>` | start in that folder |
| `prism continue` | reopen the last chat here |
| `prism web [folder]` | open Prism in your browser |
| `prism fix` | computer help mode, asks before every change |
| `prism setup` | add free keys (optional) |
| `prism status` | which free models are ready |
| `prism logs` | watch which model answers each step |
| `prism stop` | stop the background router |
| `prism install` | add the `prism` command to your terminal |
| `prism update` | get the newest version |

| Inside Prism | |
| --- | --- |
| `enter` / `shift+enter` | send / new line |
| `esc` | stop the AI |
| `ctrl+c` or `/quit` | close Prism |
| `ctrl+t` or `/theme` | switch theme (prism, tokyo-night, catppuccin, rose-pine, matte-black, gruvbox, nord, everforest) |
| `pgup` `pgdown`, mouse wheel | scroll (smooth, with a scrollbar) |
| click a `▸ N steps` line | open folded steps from an earlier turn (`ctrl+o` opens all of them) |
| `/` | list every command, `tab` completes |
| `/model` `/models` | pick the model for this chat · see every model |
| `/login` `/key` | sign in to a plan you pay for · add a provider key |
| `/new` `/clear` `/compact` `/undo` `/resume` | fresh chat · clear · shorten the chat · undo the last changes · reopen a chat |
| `/skills` `/init` `/memory` `/mcp` | playbooks · write PRISM.md · what Prism remembers · connected tools |
| `/plan` | plan first, nothing changes until you pick run (see [Plan first](#plan-first)) |
| `/rules` | which commands Prism asks about first (see [Asks before risky commands](#asks-before-risky-commands)) |
| `/commands` `/hooks` | your saved prompts, run as `/<name>` (see below) · your own checks (see [Hooks](#hooks-your-own-checks)) |
| `/files` `/open` `/tree` | browse and read project files |
| `/tabs` `/rename` `/ask` | several chats at once |
| `/web` `/usage` `/theme` `/beam` `/quiet` `/help` `/quit` | |

**Saved commands.** Save a prompt you type often as `.prism/commands/<name>.md` in a project (or `~/.config/prism/commands/<name>.md` for every project; the project's wins) and it becomes `/<name>`, in the terminal and the browser:

```markdown
---
description: review my change before I commit
---
Review the uncommitted changes. Focus on $ARGUMENTS. List real bugs first, then small things.
```

`/review error handling` sends that text with `$ARGUMENTS` replaced by `error handling` (`$1`, `$2` are single words). With no placeholder, the words are added at the end. `/commands` lists them, `/commands new <name>` writes a starter file, and edits take effect right away. Names use lowercase letters, numbers and dashes; built-in commands always win.

## Meet Shard

<p align="center"><img src="docs/shard.png" alt="Shard's moods" width="820"></p>

Shard is Prism's mascot. He reacts to what the AI is doing: thinking, working, done, or waiting when every free model is busy. When a task with real steps finishes, he fires the Last Prism beam:

<p align="center"><img src="docs/beam.png" alt="The Last Prism beam" width="820"></p>

`/beam` turns the beam on or off, and `/quiet` hides Shard's one-line comments.

## Safety and privacy

- Prism only sees the folder you start it in, and won't start in your home folder or `/`. The one exception is `prism fix`, which you start on purpose: it runs on your computer and asks before every change.
- Commands run inside the Docker box, not on your computer. Files it creates belong to you.
- Keys live in `keys.conf`, readable only by you and ignored by git.
- The router only listens on `127.0.0.1`, so other devices on your network can't use it, and it refuses requests from web pages. Changing keys or sign-ins needs a secret (`.router-token`) that the AI's own commands can't read.
- **Free AI services may keep what you send them.** Don't paste passwords or private data.

## How it's built

| Path | What it is |
| --- | --- |
| `prism` | the command you run |
| `install.sh` | the one-line installer |
| `cli/` | the Prism app, in Go with Charm's Bubble Tea (`cli/skills/` holds the built-in skills) |
| `web/` | the browser version (`prism web`) |
| `router/` | the free-model router (`providers.json` is the model list) |
| `agent/` | the Docker image Prism runs in |
| `bench/` | the speed and correctness tests |

**Networking:** on first run Prism checks whether Docker's normal network can reach the internet. Some Linux firewalls block it; then Prism shares the computer's network instead (`docker-compose.host.yml`). The choice is saved in `.network`; delete it to check again.

## Thanks

Prism was built with [Empryo](https://empryo.com), whose live map of the code inspired Prism's project tree. It borrows ideas from [OpenCode](https://opencode.ai), [Crush](https://github.com/charmbracelet/crush), [jcode](https://jcode.sh), [Claude Code](https://www.anthropic.com/claude-code) and [Codex](https://github.com/openai/codex), and is measured against [Aider](https://aider.chat) and [mini-swe-agent](https://github.com/SWE-agent/mini-swe-agent).

Its look is built on [Charm](https://charm.land)'s [Bubble Tea](https://github.com/charmbracelet/bubbletea) and inspired by [Omarchy](https://omarchy.org). It runs in [Docker](https://www.docker.com), searches with [DuckDuckGo](https://duckduckgo.com) and checks web pages in [Chromium](https://www.chromium.org).

It is free because of the free plans from [NVIDIA](https://build.nvidia.com), [Groq](https://groq.com), [Mistral](https://mistral.ai), [Google Gemini](https://ai.google.dev), [OpenRouter](https://openrouter.ai), [Kilo](https://kilo.ai), [LLM7](https://llm7.io) and [Z.ai](https://z.ai), and local models through [Ollama](https://ollama.com).

The beam is named after the [Last Prism](https://terraria.wiki.gg/wiki/Last_Prism) from [Terraria](https://terraria.org); the art and lines are original.

## License

MIT. See [LICENSE](LICENSE). What changed in each version: [CHANGELOG.md](CHANGELOG.md).
