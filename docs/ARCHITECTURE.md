# Architecture

A short guide for contributors. No prior knowledge assumed.

## Pieces

| Name | What it is |
|---|---|
| **prism** | Bash launcher. Starts Docker services, picks a free port, mounts the project folder, and execs the agent container. |
| **router container** | Node.js HTTP server (`router/server.mjs`). Accepts OpenAI-compatible `/v1/chat/completions` requests and races up to 3 providers in parallel, returning the first good stream. |
| **agent container** | Runs the Go CLI binary. Mounts the project folder read-write. Talks to the router over the Docker network. |
| **CLI Go app** (`cli/`) | The Go binary. Drives the TUI, the agent loop, tools, memory, skills, MCP, and the optional web server. |
| **web front end** (`web/`) | Static HTML/CSS/JS served by the Go binary on demand. Talks to the same agent via a JSON HTTP API. |

## Message flow

```mermaid
sequenceDiagram
    participant TUI as TUI (bubbletea)
    participant Loop as agent loop
    participant Router as router (Node.js)
    participant Provider as AI provider
    participant Tools as tools / MCP
    participant Memory as memory (files)

    TUI->>Loop: user prompt
    Loop->>Router: POST /v1/chat/completions (SSE)
    Router->>Provider: race up to 3 providers
    Provider-->>Router: first-byte wins, others aborted
    Router-->>Loop: stream chunks (evText / evReasoning)
    Loop->>Tools: tool calls (bash, read, write, edit, skill, MCP)
    Tools-->>Loop: tool results
    Loop->>Router: next completion with results
    Loop-->>TUI: evToolStart / evToolDone / evDone
    Loop->>Memory: saveSession + background memoryJob
```

Steps in `agent.go`:
1. `Run` appends the user message and calls `loop`.
2. `loop` calls `completeRetry` (up to 3 retries on dropped replies, avoiding the last failing route each time).
3. `complete` posts to the router and reads the SSE stream via `readStream`, emitting `evText`/`evReasoning`/`evRoute` events.
4. Tool calls are dispatched: built-in tools via `runTool`, skills via `useSkill`, MCP tools via `mcp.run`. `bash`, `write`, and `edit` require approval in fix/system mode.
5. When context exceeds 70% of the window, `compact` summarises the conversation in place.
6. After `Run` returns, a background `memoryJob` asks the model to update the persistent memory summary.

## Files map

| File | Purpose |
|---|---|
| `prism` | Bash launcher: Docker orchestration, port allocation, fix mode binary extraction |
| `router/server.mjs` | Provider race, cooldowns, speed stats, `/status` and `/health` endpoints |
| `router/providers.json` | List of providers and models; env-var keys; `quick` overrides for fast paths |
| `cli/main.go` | Entry point, TUI model (bubbletea), `runPlain` for headless `run` subcommand |
| `cli/agent.go` | Agent struct, `Run`/`loop`/`complete`/`compact`, SSE stream reader, approval logic |
| `cli/tools.go` | Built-in tool specs and `runTool` dispatcher (list, grep, read, edit, write, bash, skill plus MCP tools) |
| `cli/memory.go` | File store (JSON/markdown under `~/.config/prism/projects/`): sessions, memory summary, usage counters, `writeAtomic` |
| `cli/skills.go` | Load and parse skill markdown files (built-ins embedded, user and project overrides) |
| `cli/mcp.go` | MCP client: stdio and HTTP/SSE transports, tool discovery, `mcpHub` |
| `cli/commands.go` | Slash-command parser and handlers (`/model`, `/skills`, `/resume`, `/web`, ...) |
| `cli/serve.go` | HTTP server for the web front end: JSON API, SSE event stream |
| `cli/handoff.go` | `/web` command: starts the HTTP server and hands the session to the browser |
| `cli/chat.go` | TUI chat rendering: message list, tool groups, scroll, highlight |
| `cli/canvas.go` | Low-level terminal canvas: wrap, visible lines, mouse hit-testing |
| `cli/splash.go` | Splash screen animation |
| `cli/buddy.go` | Buddy character animation |
| `cli/shard.go` | Shard/beam visual effect |
| `cli/theme.go` | Color theme definitions |
| `web/index.html` | Single-page web UI shell |
| `web/app.js` | Web UI logic: SSE consumer, message rendering, send |
| `docker-compose.yml` | Defines `router` and `agent` services |
| `install.sh` | Clones the repo to `~/.prism`, then runs `prism install` which symlinks `prism` into `~/.local/bin` |

## /web handoff

Typing `/web` in the TUI calls `startWeb` (`handoff.go`). It finds the embedded web assets via `webDir()`, binds a TCP listener on `PRISM_WEB_LISTEN` (default `127.0.0.1:4849`), and starts an HTTP server backed by the same `Agent` instance. The TUI stays alive but idles; the browser tab takes over the conversation. The server sets `handoff: true` so it shuts down automatically after 8 seconds of inactivity once the browser disconnects.

`prism web <folder>` skips the TUI entirely and runs `agent serve` directly.

## Fix mode (prism fix)

`prism fix` extracts the native binary from the agent Docker image (`/opt/prism/hosts/prism-<os>-<arch>`), copies it to `.bin/`, and runs it directly on the host with `PRISM_PROJECT="this computer"` and `-system`. The binary runs outside Docker so it can see the real filesystem and run `sudo`. Every `bash`, `write`, and `edit` call is gated behind an approval prompt.

## MCP

`startMCP` (`mcp.go`) reads `~/.config/prism/mcp.json` and `.prism/mcp.json` in the project directory. Each entry is either a stdio process (`command`/`args`) or an HTTP/SSE URL. Tools are discovered via the MCP `tools/list` call and exposed to the agent with names in the form `server__toolname`. The `__` separator is how `isMCP` identifies them for approval routing.

## Skills

Skills are markdown files with YAML front matter (`name`, `description`) followed by a prompt body. Built-ins are embedded at compile time from `cli/skills/`. User skills live in `~/.config/prism/skills/`; project skills in `<project>/.prism/skills/`. Project skills override user skills, which override built-ins, by name. The agent calls the `skill` tool with a name; `useSkill` injects the body as a user message.

## Tests

| File | What it covers |
|---|---|
| `cli/agent_test.go` | Agent loop, compact, stream parsing |
| `cli/tools_test.go` | Built-in tool execution |
| `cli/mcp_test.go` | MCP stdio and HTTP transports |
| `cli/skills_test.go` | Skill parsing and loading |
| `cli/commands_test.go` | Slash-command matching and handlers |
| `cli/handoff_test.go` | Web handoff HTTP API |
| `cli/serve_test.go` | HTTP server endpoints |

Run all tests:

```sh
cd cli && go test ./...
```

With race detector:

```sh
cd cli && go test -race ./...
```

The router has tests in `router/server.test.mjs` (run `node --test router/`); you can also verify it live with `prism status`.
