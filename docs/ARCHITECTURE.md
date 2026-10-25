# Architecture

A short guide for contributors. No prior knowledge assumed.

## Pieces

| Name | What it is |
|---|---|
| **prism** | Bash launcher. Starts the router with Docker Compose, picks a free port, mounts the project folder, and runs the agent container (`compose run --rm agent`). |
| **router container** | Node.js HTTP server (`router/server.mjs`) on port 4848. Accepts OpenAI-compatible `POST /v1/chat/completions` requests and races free providers (up to 3 at once), streaming back the first one that answers. |
| **agent container** | Runs the Go CLI binary (`agent/Dockerfile`). Mounts the project folder read-write at `/workspace`. Talks to the router at `PRISM_API` (`http://router:4848/v1`). |
| **CLI Go app** (`cli/`) | The Go binary. Drives the TUI, the agent loop, tools, memory, skills, MCP, crew, and the web server. |
| **web front end** (`web/`) | Static HTML/CSS/JS (no build step) served by the Go binary. Talks to the same agent through a JSON HTTP API with an SSE chat stream. |

## Message flow

```mermaid
sequenceDiagram
    participant UI as TUI (bubbletea) / web / run
    participant Loop as agent loop
    participant Router as router (Node.js)
    participant Provider as AI provider
    participant Tools as tools / skills / crew / MCP
    participant Memory as memory (files)

    UI->>Loop: user prompt (Agent.Run)
    Loop->>Router: POST /v1/chat/completions (stream)
    Router->>Provider: race providers (hedged)
    Provider-->>Router: first byte wins, others aborted
    Router-->>Loop: SSE chunks + x-free-route and x-prism-source headers
    Loop-->>UI: evRoute / evThinking / evReasoning / evText / evDraft
    Loop->>Tools: tool calls
    Tools-->>Loop: tool results
    Loop-->>UI: evToolStart / evToolDone / evUsage / evNote
    Loop->>Router: next completion with results
    Loop->>Memory: saveSession
    Loop-->>UI: evDone
    UI->>Memory: background memoryJob
```

Steps in `cli/agent.go`:
1. `Run` appends the user message, calls `loop`, then `saveSession` and emits `evDone`. If the run was cancelled, `stopped` closes any pending tool calls first.
2. `loop` runs up to `maxSteps` (80) steps. Each step calls `prune` (trims old tool output), compacts if needed, then `completeRetry`.
3. `completeRetry` calls `complete` up to 3 times when a reply is dropped, sending the routes that already failed as `prism_avoid`. A `routerError` (the router's own 503) is not retried.
4. `complete` posts to `<api>/chat/completions` with `model` (`auto` unless a crew role sets one), the tool specs from `tools()`, and optional `prism_quick` / `prism_exclude_families`. It reads the route from the `x-free-route` header and who pays for it from `x-prism-source`, then parses the body with `readStream` (SSE) or `readWhole` (plain JSON).
5. Tool calls are dispatched in `loop`: `skill` (or a skill name) → `useSkill`; `crew` → `runCrew`; MCP tools → `mcp.run`; `screenshot` → `(*Agent).screenshot`; `browse` → `(*Agent).browse`; `web` → `webTool`; everything else → `runTool`. Before a call runs, `refused` (`cli/approve.go`) checks it. In every mode, a risky `bash` command (see `cli/risk.go` and the README section "Asks before risky commands") or one matched by your own rules asks first. In `prism fix`, every `bash`, `write`, `edit` and MCP call asks. Plan mode (`cli/plan.go`) refuses anything that would change files, and hooks (`cli/hooks.go`) can stop a command before it runs.
6. When the token count passes 70% of the window (`PRISM_CONTEXT`, default 128000), `compact` asks the model for a summary and replaces the history with it.
7. After the run, the caller (TUI `handle`, `runPlain`, or the web `chat` handler) runs `memoryJob` in the background to update the project memory. It is skipped when `PRISM_MEMORY=off` or the store can't persist (`store.persists`).

## Router

`router/server.mjs`:
- `routes()` builds the route list from `router/providers.json` plus the user's own providers (`customProviders()` from `router/custom.mjs`, read from `custom-providers.json`). Providers with `requires` are used only when that env key is set (from `keys.conf`).
- `complete` orders routes with `ordered` (rank + learned first-byte speed, skipping cooldowns), then `launch` starts one route and hedges another after `HEDGE_MS` (6 s) or `COLD_HEDGE_MS` (2 s), up to 3 live at once and at most `PRISM_PROVIDER_INFLIGHT` per provider. The first route to send a byte wins (`pump`); the rest are aborted.
- Request fields: `prism_quick` merges the provider's `quick` params, `prism_avoid` moves routes to the back, `prism_exclude_families` drops families. `model` may be `auto`, `auto:code|fast|review` (role-ranked), `auto:vision`, or an exact route id. Requests with `x-prism-crew: 1` don't hedge.
- Failures set cooldowns via `pauseFor` (429 → retry-after, 401/402/403 → 15 min, 404 → 1 h, 5xx/network → 30 s). Speed stats persist to `STATS_FILE` (`/data/speed.json` in Docker).
- Every answer carries `x-free-route` (the route that answered) and, for a plan or your own key, `x-prism-source` (`subscription` or `yours`). The CLI uses it for the free, plan or yours label and to count plan and key requests apart in `/usage`.
- Endpoints: `POST /v1/chat/completions`, `GET /v1/models`, `GET /status` (JSON, or text with `?format=text`), `GET /health`, `GET|POST /keys` (provider keys), `GET|POST /logins` (subscription sign-ins: start, poll, `{id, code}` for a pasted code, `signOut`), `GET /logins/pending`, and the OAuth callbacks.
- Guard: `localOnly` (`router/subs.mjs`) refuses any Host other than loopback, `router` or `host.docker.internal` on the router's port, and any foreign Origin. Writes to `/keys` and `/logins` need `x-prism-token` when `PRISM_ROUTER_TOKEN` is set; the launcher creates `.router-token` once and passes it to both containers. The CLI moves the token out of its environment at start (`cli/routerauth.go`), so tool commands can't read it.
- Subscriptions (`router/subs.mjs`): ChatGPT, Claude, Grok, Antigravity (browser sign-in, PKCE, local callback or pasted code) and Copilot (device code). After sign-in the router asks the service for the plan's models (`probeSubModels`, filtered by `visibleModels`), and `router/wire.mjs` translates chat requests to each vendor's format.
- Screenshots: the CLI tries the chat's pinned model first (so a plan model looks at them), then `auto:vision`. `router/wire.mjs` passes the image on in each plan's format.
- `router/check.mjs` sends a tiny request to every model in `providers.json` and prints a report.

## Speed and prompt caching

Providers that cache prompts reuse work only for a byte-identical prefix, so each request is built static-first:
- The tool specs from `tools()` come first, then `messages[0]`, the system prompt from `systemPrompt` (`cli/agent.go`): base rules, the skill index (`skillIndex`), crew guidance, project rules (`projectRules`: PRISM.md / AGENTS.md / CLAUDE.md), the project file list (`projectMap`, top-level agent only, capped at `mapLimit`), then the project memory. The conversation follows.
- The system prompt is built once per chat. Mid-chat it is rebuilt only by explicit user actions: `/memory` or `/forget` when they change memory (TUI and web), `/skills new` (reloads skills), and returning from `/web` (`endWeb`). `compact` also rebuilds it, but compaction replaces the history anyway.
- `prune` (`cli/memory.go`) does nothing until the chat passes a quarter of the window (`window/4`); then it trims every old tool output in one batch, so short tasks keep their cache and long ones lose it only now and then. `PRISM_PRUNE=off` disables it.
- After each reply `complete` stores the serving route (`x-free-route`) and sends it as `prism_prefer` on the next request (not in light mode). The router sorts that route first, after `prism_avoid` and role ranking; slow answers are still hedged.
- Cached tokens come from `usage.prompt_tokens_details.cached_tokens`. `prism run` prints them in its stats line (`… tokens sent (N cached)`); `/usage` shows `this chat … sent · … cached`.

## Files map

| File | Purpose |
|---|---|
| `prism` | Bash launcher: `start_router`, `run_agent`, `web`, `fix`, `install`, `setup`, `keys`, `tree`, `status`, `logs`, `stop`, `update`; port allocation (`free_port`), host-network fallback (`network_mode`) |
| `docker-compose.yml` | Defines the `router` and `agent` services and the `router-data` / `prism-config` volumes |
| `docker-compose.host.yml` | Override used when Docker's bridge network can't reach the internet: both services use host networking |
| `agent/Dockerfile` | Builds the CLI, cross-compiles host binaries into `/opt/prism/hosts/`, copies `web/` to `/usr/share/prism/web`, installs Neovim for the editor panel |
| `router/Dockerfile` | Image for the router container |
| `install.sh` | Clones the repo to `~/.prism` (or `PRISM_HOME`), then runs `prism install`, which symlinks `prism` into `~/.local/bin` |
| `router/server.mjs` | Provider race, hedging, cooldowns, speed stats, `/v1/models`, `/status`, `/health`, `/keys`, `/logins`; sets `x-free-route` and `x-prism-source` on each answer |
| `router/subs.mjs` | Plan sign-ins (ChatGPT, Claude, Grok, Antigravity, Copilot), token refresh, plan model lists, the `localOnly` guard |
| `router/wire.mjs` | Turns a chat request into each plan's own format (ChatGPT, Claude, Gemini) and the reply back, images included |
| `router/custom.mjs` | User-added providers (`prism keys add`): presets, validation, `customProviders`, `eligible` (paid routes only with `PRISM_USE_PAID=1` or an exact pin) |
| `router/providers.json` | Built-in providers and models; env-var keys (`requires`); per-model `family`, `roles`, `vision`; provider `quick` overrides |
| `router/check.mjs` | One-shot health check of every configured model |
| `cli/main.go` | Entry point and flags (`-system`, `-continue`, `-theme`, `-api`), `serve`, `run` and `tree` subcommands, TUI model (`Update`, `handle`, `View`), `runPlain` |
| `cli/agent.go` | `Agent` struct, `Run`/`loop`/`completeRetry`/`complete`/`compact`, `readStream`/`readWhole`, event types, approval text |
| `cli/approve.go` | `refused`: decides when a tool call needs a yes and asks it (TUI, web or `askOnTerminal` for `prism run`) |
| `cli/risk.go` | Splits a shell command into its parts and names the risky ones (deleting folders, `git push`, publishing, …); your own rules and `/rules` |
| `cli/plan.go` | Plan mode (`/plan`): look around, write a plan, change nothing until you run it |
| `cli/hooks.go` | Your own checks from `.prism/hooks.json` (`before_bash`, `after_edit`, `turn_end`), trust on first use, `/hooks` |
| `cli/loop.go` | Spots repeating text and repeated tool calls (`repeatingTail`, `callLog`) |
| `cli/loopguard.go` | Loop detector: drops a reply that goes in circles and asks another model; stops the turn after a few switches |
| `cli/usercmds.go` | Saved commands: `.prism/commands/<name>.md` or your config folder becomes `/<name>`; `/commands` |
| `cli/tools.go` | Built-in tool specs (`toolSpecs`: browse, screenshot, list, grep, read, edit, write, bash) and the `runTool` dispatcher |
| `cli/web.go` | `web` tool: search (Tavily, Brave, then DuckDuckGo) and reading public pages as text |
| `cli/browse.go` | `browse` tool: drives headless Chromium over the DevTools pipe, finds elements by role and accessible name, text or ref, clicks and types with real input events, checks expectations, reports page errors and an outline of the page, saves an end-state screenshot under `.prism/checks` |
| `cli/screenshot.go` | `screenshot` tool: headless Chromium (downloaded on first use), local file server and URL policy, desktop capture in system mode, sends the image to the chat's pinned model first, then `auto:vision` |
| `cli/crew.go` | `crew` tool: `runCrew` runs 2–3 worker agents in parallel on disjoint files, then an optional read-only review (`runReview`) and `repair` round |
| `cli/crewui.go` | Crew panel state (`crewBlock`) for the TUI and its JSON form for the web (`crewWire`) |
| `cli/tabs.go` | Tabs: several chats in one window, each with its own agent and transcript; `/tabs`, `/rename`, `/ask` |
| `cli/crosstab.go` | `tabHub`: the open tabs, their status and which files each one is changing, so tabs don't edit the same file |
| `cli/memory.go` | File store under `<config>/prism/projects/<name>-<hash>/`: sessions, memory summary, daily usage, `writeAtomic`, `projectRules` (PRISM.md / AGENTS.md), `memoryJob` |
| `cli/compact.go` | `/compact`: swaps the history for a summary; a one-time hint when the chat gets long |
| `cli/undo.go` | `/undo`: keeps each file as it was before a turn changed it, and puts it back |
| `cli/skills.go` | Parse and load skill markdown files (`loadSkills`, `useSkill`), the grouped `/skills` list |
| `cli/skills/` | Built-in skill files, embedded at compile time |
| `cli/mcp.go` | MCP client: `startMCP`, stdio and HTTP transports, tool discovery, `mcpHub` |
| `cli/mcp_setup.go` | `/mcp` command: catalog, add/remove/test servers, secrets, restart |
| `cli/mcp_catalog.json` | The built-in list of tool servers `/mcp add` offers |
| `cli/commands.go` | The slash command list (`commands`, also sent to the web) and what each one does, plus error wording (`friendly`) |
| `cli/cmdview.go` | Draws the grouped text blocks of `/skills`, `/models`, `/memory`, `/usage` and others |
| `cli/menu.go` | The pick-one menu used by `/model`, `/login`, `/key`, `/resume`, `/theme`, `/tabs`, `/mcp add` |
| `cli/picker.go` | The `/model` picker on that menu |
| `cli/model.go` | `/models` in three groups (free models, your plans, your own keys), filtering, `/model <name>` |
| `cli/keys.go` | `/key`: list, add and remove provider keys through the router |
| `cli/login.go` | `/login`: plan sign-ins, polling, pasted codes, sign-out |
| `cli/routerauth.go` | Moves the router token out of the environment at start so tool commands can't read it (Linux and macOS) |
| `cli/viewer.go` | `/files` browser and `/open` file viewer; `projectPath` keeps paths inside the project |
| `cli/tree.go` | Live project tree: files, git status, main functions, imports, rank; the short project map for the prompt |
| `cli/tree_watch_linux.go` | Keeps the tree current with inotify on Linux |
| `cli/tree_watch_other.go` | Keeps the tree current by polling on other systems |
| `cli/treeview.go` | `prism tree`: prints the tree, or writes it as a page (`--html`) or JSON |
| `cli/treeview/` | The tree page's HTML, CSS and JS, embedded at compile time |
| `cli/editor.go` | Embedded Neovim: runs `nvim --embed`, reads its screen updates and draws its grid next to the chat |
| `cli/editorpanel.go` | Editor panel layout, keys, mouse, focus and the divider you drag to resize |
| `cli/nvim/init.lua` | Built-in Neovim settings Prism starts the editor with |
| `cli/serve.go` | HTTP server for the web front end: JSON API, SSE chat stream, `localOnly` host/origin checks, `webDir` |
| `cli/handoff.go` | `/web` from the TUI: `startWeb`, `checkWeb`, `endWeb`, handoff screen |
| `cli/chat.go` | TUI chat rendering: transcript, markdown, tool groups, input, status bar, highlight |
| `cli/canvas.go` | Low-level terminal canvas: cells, text, links, gradients |
| `cli/splash.go` | Splash screen |
| `cli/buddy.go` | Buddy (Shard) moods, poses and speech |
| `cli/shard.go` | Shard character and finish-beam drawing |
| `cli/theme.go` | Color themes |
| `web/index.html` | Single-page web UI shell |
| `web/app.js` | Web UI logic: `/api/chat` SSE consumer, message rendering, history, stop/new, tabs |
| `web/commands.js` | Slash-command list and the pick-one overlay for the web; sends `/api/command` |
| `web/files.js` | Files panel: the live project tree from `/api/tree` |
| `web/beam.js` | Finish-beam animation |
| `web/style.css`, `web/commands.css` | Styles |
| `bench/run.sh` | Five coding tasks run through `prism run` in the agent container; writes `bench/results-<label>.txt` |
| `bench/chat.sh` | Simple questions; time to first word and total |
| `bench/compare.sh` | Runs the `run.sh` tasks through another harness (aider, claude-code, …) via a pinning proxy |
| `bench/charts.py` | Builds each chart and the social preview as an HTML page in `docs/graphics/`, then screenshots it to a PNG in `docs/` |
| `bench/harness/` | Harness comparison (Prism vs aider, mini-swe-agent, opencode): `run.sh`, `entry.py`, `proxy.py`, `summarize.py`, `tasks/`; see its `README.md` |

## Run modes

- `prism [folder]` / `prism continue`: `run_agent` starts the TUI in the agent container with `PRISM_WEB_LISTEN` set to a free port so `/web` works.
- `prism web [folder]`: runs `agent serve`, which calls `serve` (`serve.go`) on `PRISM_LISTEN` (default `127.0.0.1:4849`) without the TUI.
- `prism run <message>` (binary subcommand, used by the benches): `runPlain` prints the reply and tool steps to stdout, then a stats line and a bounded memory update.

## Web API

`server.handler` (`serve.go`) serves the files from `webDir()` (`PRISM_WEB`, `/usr/share/prism/web`, `web`, `../web`) and:

| Route | Purpose |
|---|---|
| `GET /api/info` | Project name, theme, number of ready models, handoff flag |
| `GET /api/history` | Conversation entries |
| `POST /api/chat` | Runs `Agent.Run` and streams events as SSE (`route`, `text`, `reasoning`, `tool_start`, `tool_done`, `usage`, `done`, `error`, crew events) |
| `POST /api/stop` | Cancels the running chat |
| `POST /api/new` | Starts a fresh session |
| `POST /api/command` | Runs a slash command; choices come back as a `picker` for the web list |
| `GET /api/commands` | The command list from `cli/commands.go`, so web and terminal match |
| `POST /api/login` | Polls a sign-in, or finishes it with a pasted `{id, code}` |
| `GET /api/ping` | Keep-alive; updates last-seen |

`localOnly` rejects any Host other than loopback on the public port and any POST without a loopback Origin.

## /web handoff

Typing `/web` in the TUI calls `startWeb` (`handoff.go`). It finds the web files via `webDir()`, listens on `PRISM_WEB_LISTEN` (default `127.0.0.1:4849`), and serves a `server` (from `newServer`) backed by the same `Agent`, with `handoff = true` and an extra `/api/handoff-open` endpoint. The TUI shows the handoff screen while the browser takes over. `checkWeb` calls `endWeb` once the browser has been silent for 8 seconds (`handoffIdle`) and no chat is running; `endWeb` stops the server and rebuilds the TUI transcript from the agent's messages.

## Fix mode (prism fix)

`prism fix` copies the native binary out of the `prism-agent` image (`/opt/prism/hosts/prism-<os>-<arch>`) into `.bin/` and runs it on the host from `$HOME` with `PRISM_PROJECT="this computer"` and `-system`. Outside Docker it can see the real filesystem. In system mode `main` sets `agent.approve`, so every `bash`, `write`, `edit` and MCP call asks for approval (in the TUI via `evApprove`, in `run` via `askOnTerminal`). This is stricter than the other modes, where only risky commands and your own rules ask first.

## MCP

`startMCP` (`mcp.go`) merges `<config>/prism/mcp.json` and `<project>/.prism/mcp.json` (key `mcpServers`; the project wins on name clashes). Each entry is a stdio process (`command`/`args`/`env`) or an HTTP `url` (with `headers`); `disabled: true` turns one off. Servers connect in the background; `tools/list` results are exposed with wire names `server__tool` (`mcpWireName`, de-duplicated by `dedupWireNames`). The loop checks `mcp.has` to route calls; `isMCP` (the `__` separator) is used to block MCP tools for crew workers and to format approval text. `/mcp` (`mcp_setup.go`) adds servers from the built-in catalog into the project file.

## Skills

Skills are markdown files with front matter (`name`, `description`) followed by a prompt body. Built-ins are embedded from `cli/skills/*.md`. User skills live in `<config>/prism/skills/`; project skills in `<project>/.prism/skills/`. Project skills override user skills, which override built-ins, by name. `skillIndex` lists them in the system prompt; the model calls the `skill` tool (or a skill's name directly) and `useSkill` returns the body as the tool result.

`<config>` is `os.UserConfigDir()`: `~/.config` on Linux; in the agent container it is the `prism-config` volume.

## Tests

| File | What it covers |
|---|---|
| `cli/agent_test.go` | Stream parsing, split tool calls, cut-off retry with avoid, router 503, full loop |
| `cli/approve_test.go` | Risky commands ask and plain ones don't, rules block or ask, nobody to ask means no, `prism fix` asks before every command |
| `cli/risk_test.go` | Which commands count as risky, rule parsing, priority and trust |
| `cli/plan_test.go` | Plan mode refuses changes, terminal and web flow, survives `/compact` |
| `cli/hooks_test.go` | Reading `.prism/hooks.json`, trust, matching |
| `cli/hooks_agent_test.go` | Hooks inside the agent loop: block a command, keep a turn going until a check passes |
| `cli/loop_test.go` | Spotting repeated text and repeated tool calls |
| `cli/loopguard_test.go` | Loop detector drops looping replies, switches models, stops an endless turn |
| `cli/usercmds_test.go` | Saved commands: parsing, filling in words, project vs your own, `/commands` |
| `cli/tools_test.go` | Built-in tools, edit diffs, bash timeouts/background, symlink escape |
| `cli/web_test.go` | Web search order, DuckDuckGo results, reading pages, refusing local addresses |
| `cli/browse_test.go` | Browse tool against real Chromium: catches a broken filter, passes the fixed page, reports missing targets and page errors |
| `cli/screenshot_test.go` | Screenshot tool: URL policy, file server, desktop capture, browser download, vision |
| `cli/crew_test.go` | Crew parallel workers, ownership, review/repair, concurrency, retries |
| `cli/crewui_test.go` | Crew panel rendering and wire format |
| `cli/tabs_test.go` | Switching, closing and naming tabs, background tab events |
| `cli/crosstab_test.go` | `tabHub`: tabs list, status, file claims |
| `cli/memory_test.go` | Store, sessions, usage, project rules, prune, memory job |
| `cli/compact_test.go` | `/compact` and the hint when a chat gets long |
| `cli/undo_test.go` | `/undo` for new, changed and deleted files; refuses files you changed since |
| `cli/mcp_test.go` | MCP stdio and HTTP transports, wire names, catalog, `/mcp` command |
| `cli/skills_test.go` | Skill parsing, loading and overrides |
| `cli/commands_test.go` | Slash-command matching, `/memory`, `/skills new`, helpers |
| `cli/cmdview_test.go` | `/skills`, `/resume` and `/usage` views |
| `cli/menu_test.go` | Pick-one menu keys, filter, mouse, narrow screens |
| `cli/model_test.go` | `/models` filtering and `/model` |
| `cli/keys_test.go` | `/key` list, save and errors |
| `cli/login_test.go` | `/login` flows, pasted codes, sign-out during a sign-in |
| `cli/routerauth_test.go` | Router token moved out of the environment, admin headers |
| `cli/viewer_test.go` | Path guard, binary and large files, file browser rows, viewer scrolling |
| `cli/tree_test.go` | Tree scan, .gitignore, git status, watching for changes, prompt map, JSON |
| `cli/editor_test.go` | Keys sent to Neovim, grid updates, a run against real Neovim |
| `cli/handoff_test.go` | Web handoff flow and handoff screen layout |
| `cli/serve_test.go` | Web server host/origin checks, static files, `/api/command` |
| `cli/shard_test.go` | Shard drawing |
| `router/server.test.mjs` | Router race, roles, vision, pins, avoid/exclude, hedging, limits |
| `router/custom.test.mjs` | Custom provider validation and loading |
| `router/subs.test.mjs` | Subscription sign-in flows, model lists, tokens |
| `router/wire.test.mjs` | Request and reply translation for subscription wires |

Run the Go tests:

```sh
cd cli && go test ./...
```

With race detector:

```sh
cd cli && go test -race ./...
```

Router tests: `cd router && node --test`. You can also check a live router with `prism status`.
