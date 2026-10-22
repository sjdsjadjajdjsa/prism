# Prism plan

## Goal
Free AI coding for people with no money for subscriptions. The best-looking coding tool in the terminal. Safe to run, easy to start.

## Layout
- prism: launcher (install, setup, status, logs, stop, continue). Was "free" (clashed with the Linux free command).
- cli/: the Go app. router/: model router. agent/: Debian image with python, node, git, ripgrep (982 MB, was 1.88 GB with OpenCode).
- OpenCode and the "web" command were removed; a Prism web page is future work.

## Tested
- No keys: fixed a 2-bug Python file and wrote passing tests (36 s).
- Compaction: forced a 6k window, it compacted mid-task and finished with correct counts.
- Parallel tool calls without an index field: split correctly (mock server).
- Real TUI screenshots: prism/preview/index.html.

## Review (Kimi K3)
- Fixed: parallel tool calls merging when a provider omits index; wide characters (emoji, CJK) shifting the layout; input over 5 lines hidden without a hint.
- Rejected: "agent goroutine deadlocks on quit" (the process exits; esc keeps the channel drained).
- Ideas kept for later: /model to pin a model, save and resume chats, real terminal cursor.

## Roadmap
Rule: the Omarchy-style look stays (pixel logo, rainbow beam, twinkling squares, flowing border, themes). Every visual change gets before/after screenshots.

1. Polish (done): /models, /help, /theme, /new, slash suggestions with tab, friendly busy and key errors. Router retries network failures once; Kimi K2.6 removed (404 on free NVIDIA accounts).
2a. Speed (done): router races free models (hedge after 6s, max 3), learns which start fastest, cuts stalled replies; Prism retries cut-off replies and shows live thinking plus queue time. Same to-do task A/B: old 686s and 1171s, new 44s and 24s. Cause: GLM-5.3 Flash on NVIDIA's free tier queues ~55s before every reply.
2b. Shard (done): style A "faceted" picked. Big on the splash above the logo, small beside the input box (only when the side margin is 17+ columns; narrow terminals show his line in the header). Moods: idle, thinking, working, happy, sleepy (all models busy). Lines only at key moments. Last Prism beam fires leftward when a turn with tool steps finishes. /beam and /quiet toggle, saved in ~/.config/prism.
2c. Providers + fast-first (done): order is NVIDIA Nemotron Ultra/Lightning, Groq, Kilo (no key), Gemini, Mistral, OpenRouter, Ollama, GLM-5.3 / GLM-5.3 Flash, Z.ai, BlockRun, LLM7 Codestral. Rule: free only, never a card. Cerebras never added (needs a card since July 2026). Pollinations and OpenCode Zen rejected (need key / app-locked). Router saves learned speeds to /data/speed.json (survives restarts) and races after 2s when it has no data.
   Bench (bench/run.sh, 5 tasks): old order 465s 5/5; new order 100s 5/5; no keys at all 278s 5/5. Nemotron Lightning first failed the website task (deleted its own file), so Ultra goes first and the system prompt forbids servers and deleting user files.
2d. Memory (done): PRISM.md / AGENTS.md / CLAUDE.md loaded as project rules; automatic memory per project (~/.config/prism/projects/<name-hash>/memory.md) rewritten in the background after any task with tool steps; every chat saved and reopenable (/resume, prism -continue); old tool output trimmed beyond the last 10 messages (16% fewer tokens on a 6-file read task, same answers); compaction keeps the latest request word for word; /usage shows requests and tokens today. Commands: /init, /memory [forget], /resume [n], /usage. Bench with memory on 162s 5/5, off 184s 5/5 (run-to-run noise is larger than the memory cost; in the app the memory save runs in the background).
2e. Speed pass 2 (done): first request of a short prompt goes out with thinking off (router 'quick' params per provider, NVIDIA Nemotron only); prompt tells the model to answer general questions without tools. Simple questions now 0.6-1.8s total (were up to 10.6s); coding bench 115s 5/5. bench/chat.sh measures first word and total.
2f. Shard polish (done): waves hello, charges up while busy (orbiting dots, rising glow), looks at streaming text, surprised on a failed step ('hm. a crack in the lens.'), proud when tests pass. Beam: converging glow point, white flash, pulsing core, drifting sparkles, 240ms screen shake.
2g. Live progress fix: NVIDIA sends a whole tool call in one chunk at the end, so long file writes looked frozen. Fast lane now only for prompts up to 80 characters (kept thinking visible for real tasks), "writing Ns · big files show up when they're finished" once a model is answering, live "writing <file> N chars" for providers that do stream tool calls. Verified in the real screen with the user's own prompt.
3. GitHub prep (done): README with demo, LICENSE (MIT), CI workflow, docs/ images. Final bench 131s 5/5, chat questions 0.7-2.7s.
2. Memory and token savings (remaining ideas): prune old tool output before summarizing, smarter compaction, PRISM.md project rules, automatic memory saved at the end of a chat, save and resume chats, daily usage display.
3. Visuals: mouse click and hover, sticky prompt, smooth scrolling, no flicker, code colours, collapsible steps, more themes.
4. Skills and plugins: instruction files for common jobs, shareable extra tools.
5. Computer troubleshooting: separate system mode, off by default, asks before every command.
6. GitHub and launch: repo, install command, README with the animated demo, Prism web page.

Each phase: tested in the real app, reviewed by Kimi K3, every finding checked before acting.
