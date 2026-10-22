<p align="center"><img src="docs/splash.png" alt="Prism start screen" width="820"></p>

<h3 align="center">Free AI that codes, beautifully.</h3>
<p align="center">No subscription. No credit card. No account needed.<br>Inspired by the best, built to be the best.</p>

<p align="center"><img src="docs/demo.gif" alt="Prism fixing a bug with free AI" width="820"></p>

Prism is a coding assistant for your terminal that runs entirely on **free AI**. It reads your code, edits files, runs commands and checks its own work, like the paid tools do, without costing anything.

It runs inside Docker, so it can only touch the one folder you give it.

## Quick start

You need Linux and Docker (`docker run hello-world` should work).

```bash
git clone https://github.com/sjdsjadjajdjsa/prism.git
cd prism
./prism install      # adds the prism command
cd ~/my-project
prism
```

The first start takes a few minutes while Docker builds Prism. After that it opens instantly.

Then just type what you want: `fix the bug in app.py`, `make me a to-do list website`, `explain this code`.

## Free AI, no setup

Prism works with **no keys at all**. Out of the box it uses free models from Kilo Gateway, BlockRun and LLM7.

Want it faster and smarter? `prism setup` walks you through free keys, about a minute each. **None of them ever ask for a card.**

| Provider | What you get | Key |
| --- | --- | --- |
| Kilo Gateway | MiniMax M3.1 Flash, Nemotron Ultra, Laguna | none |
| BlockRun | Nemotron Ultra, Nemotron Lightning | none |
| LLM7 | Codestral | none |
| NVIDIA | Nemotron Ultra and Lightning (fastest), GLM-5.3, GLM-5.3 Flash | free |
| Groq | GPT-OSS 120B, very fast | free |
| Google | Gemini Flash | free |
| Mistral | Codestral | free |
| OpenRouter | free MiniMax and others (50 a day) | free |
| Z.ai | GLM-4.7 Flash | free |
| Ollama | any model on your own computer | none |

### How it stays fast

A small router picks the model for every step:

- **Races free models.** If the first one hasn't started answering in a few seconds, the next joins the race, and the first to answer wins.
- **Learns which models are quick** and remembers it between runs.
- **Skips models that are busy or down**, and tries them again later.
- **Fast lane:** short questions skip the model's slow thinking step.

Measured on five real coding tasks (fix a bug, build a website, refactor, count functions, make a package), all checked automatically:

| | Time | Tasks passed |
| --- | --- | --- |
| First version | 465 s | 5/5 |
| Now, with a free NVIDIA key | 100–131 s | 5/5 |
| Now, with no keys at all | 278 s | 5/5 |

Simple questions like "hi" or "explain a Python list" answer in about a second. Run `bench/run.sh` and `bench/chat.sh` to measure on your machine.

## Meet Shard

<p align="center"><img src="docs/shard.png" alt="Shard's moods" width="820"></p>

Shard is Prism's little crystal. He waves hello, charges up while the AI works, gets sleepy when every free model is busy, and when a task is done he fires the **Last Prism beam**:

<p align="center"><img src="docs/beam.png" alt="The Last Prism beam" width="820"></p>

`/beam` turns the beam off, `/quiet` keeps Shard from talking.

## Memory

- **`PRISM.md`**: rules for your project that Prism always follows. `/init` writes one for you. It also reads `AGENTS.md` and `CLAUDE.md`.
- **Automatic memory**: after each task Prism saves what it learned about the project, so a new chat already knows how your project works. `/memory` shows it, `/memory forget` clears it.
- **Every chat is saved.** `/resume` reopens one, `prism continue` picks up the last.
- **Saves free usage**: old file reads are trimmed and long chats are summarized, keeping your latest request word for word. `/usage` shows what you used today.

## Commands

| In the terminal | |
| --- | --- |
| `prism` | start in the current folder |
| `prism <folder>` | start in that folder |
| `prism continue` | reopen the last chat here |
| `prism setup` | add free keys (optional) |
| `prism status` | which free models are ready |
| `prism logs` | watch which model answers each step |
| `prism stop` | stop the background router |

| Inside Prism | |
| --- | --- |
| `enter` / `shift+enter` | send / new line |
| `esc` | stop the AI |
| `ctrl+t` or `/theme` | switch theme (prism, tokyo-night, catppuccin, rose-pine, matte-black) |
| `pgup` `pgdown`, mouse wheel | scroll |
| `/` | list every command, `tab` completes |
| `/models` `/new` `/resume` `/init` `/memory` `/usage` `/beam` `/quiet` `/help` | |

## Safety and privacy

- Prism only sees the folder you start it in. It refuses to run in your home folder or `/`.
- Commands run inside the Docker box, not on your computer. Files it creates belong to you.
- Keys live in `keys.conf`, readable only by you and ignored by git.
- The router only listens on `127.0.0.1`.
- **Free AI services may keep what you send them.** Don't paste passwords or private data.

## How it's built

| Path | What it is |
| --- | --- |
| `prism` | the command you run |
| `cli/` | the Prism app, in Go with Charm's Bubble Tea |
| `router/` | the free-model router (`providers.json` is the model list) |
| `agent/` | the Docker image Prism runs in |
| `bench/` | the speed and correctness tests |

`prism run "message"` inside the container runs without the screen, for scripts and tests.

## Thanks

Prism takes ideas from the tools that showed the way: OpenCode, Crush, jcode, Claude Code and Codex. Its look is built on [Charm](https://charm.land)'s open-source libraries and inspired by [Omarchy](https://omarchy.org). The beam is a nod to a certain rainbow weapon from Terraria; all art and lines are original.

## License

MIT. See [LICENSE](LICENSE).
