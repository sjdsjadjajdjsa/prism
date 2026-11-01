<p align="center">
  <picture>
    <source media="(prefers-color-scheme: light)" srcset="docs/hero-light.png">
    <img src="docs/hero-dark.png" alt="Prism: free AI that codes, beautifully" width="820">
  </picture>
</p>

<p align="center">A coding assistant for your terminal and browser that runs on free AI models,<br>or on the ChatGPT, Claude, Grok, Gemini or Copilot plan you already pay for.</p>

<p align="center">
  <a href="https://github.com/sjdsjadjajdjsa/prism/releases/latest"><img src="https://img.shields.io/github/v/release/sjdsjadjajdjsa/prism?style=flat&color=c69bff&label=release" alt="Latest release"></a>
  <a href="https://github.com/sjdsjadjajdjsa/prism/actions/workflows/check.yml"><img src="https://img.shields.io/github/actions/workflow/status/sjdsjadjajdjsa/prism/check.yml?branch=main&style=flat&logo=github&label=ci" alt="Checks"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/sjdsjadjajdjsa/prism?style=flat&color=7ee2a8" alt="MIT license"></a>
  <img src="https://img.shields.io/badge/runs%20on-Linux%20%C2%B7%20macOS%20%C2%B7%20Windows%20(WSL)-6fd3ff?style=flat" alt="Runs on Linux, macOS and Windows (WSL)">
</p>

<p align="center">
  <a href="#get-started">Get started</a> ·
  <a href="#features">Features</a> ·
  <a href="#models">Models</a> ·
  <a href="#benchmarks">Benchmarks</a> ·
  <a href="docs/GUIDE.md">Guide</a> ·
  <a href="CHANGELOG.md">Changelog</a> ·
  <a href="CONTRIBUTING.md">Contributing</a>
</p>

<p align="center"><img src="docs/prism-demo.gif" alt="Prism builds a todo app with free AI, then the app is used in the browser" width="820"></p>

Prism reads your code, edits files, runs commands and checks its own work. When it builds a web page, it opens it in a hidden browser and clicks through it like a person before it calls it done. It runs inside Docker, so it can only touch the one folder you give it.

No subscription, no credit card, no account.

## Get started

You need [Docker](https://docs.docker.com/get-docker/). New to it? The [setup guide](docs/SETUP.md) walks you through Linux, macOS and Windows.

```bash
curl -fsSL https://raw.githubusercontent.com/sjdsjadjajdjsa/prism/main/install.sh | bash
```

The [installer](install.sh) checks Docker, downloads Prism to `~/.prism` and adds the `prism` command. Open a new terminal, then:

```bash
cd ~/my-project
prism
```

The first start builds Prism, which takes a few minutes. After that it opens in seconds. Ask for anything: *make a snake game I can play in the browser*, *fix the bug in app.py*, *explain this code*.

| Platform | Status |
| --- | --- |
| Linux (Ubuntu, Mint, Debian) | tested |
| Linux (Arch, CachyOS, Omarchy) | tested |
| Windows 11 (WSL with Docker Desktop) | tested · [beginner guide](docs/WINDOWS.md) |
| macOS (Docker Desktop) | should work, not tested yet |

<details>
<summary>Install from source</summary>

```bash
git clone https://github.com/sjdsjadjajdjsa/prism.git
cd prism
./prism install
```
</details>

## Features

**Free by default.** A small router picks a free model for every step, races them so you aren't stuck waiting, and skips the ones that are busy. No key needed to start; `prism setup` adds free keys for more speed. [Providers →](docs/GUIDE.md#free-ai-no-setup)

**Your own plan, if you have one.** `/login` signs in to ChatGPT, Claude, Grok, Gemini or Copilot. Plan models only answer when you pick them, so free mode never spends your plan. [Plans →](docs/GUIDE.md#use-the-plan-you-already-pay-for)

**Checks its own work.** Runs your tests, opens web pages it builds and clicks through them, and asks before risky commands like deleting folders or pushing. [Safety →](docs/GUIDE.md#asks-before-risky-commands)

**Talk to it while it works.** Enter sends a correction it picks up before its next step, alt+enter queues the next message, esc stops. `/plan` makes it plan before it touches anything.

**Files, editor and map.** A file tree beside the chat (ctrl+b), an editor that works like a notes app (ctrl+e) or Neovim, pictures and videos right in the terminal, and a constellation map of how your files link. [Files →](docs/GUIDE.md#files-and-the-editor)

**Helpers, skills and tools.** Ask for helpers and Prism splits the work between them. 24 built-in skills, your own saved commands and hooks, and tool servers (MCP). [Crew mode →](docs/GUIDE.md#crew-mode-helper-agents)

**Terminal or browser.** `prism web` (or `/web` mid-chat) opens the same chat in your browser, with the same history.

**Remembers your project.** `PRISM.md` rules, automatic memory between chats, and every chat saved to pick up later.

<table>
  <tr>
    <td width="50%"><img src="docs/editor-and-tree.png" alt="The terminal: file tree, chat and editor side by side"></td>
    <td width="50%"><img src="docs/map.png" alt="The constellation map of how files link"></td>
  </tr>
  <tr>
    <td align="center"><sub>Tree, chat and editor in the terminal</sub></td>
    <td align="center"><sub>The constellation map</sub></td>
  </tr>
  <tr>
    <td width="50%"><img src="docs/web.png" alt="Prism in the browser, fixing a bug"></td>
    <td width="50%"><img src="docs/fix.png" alt="Prism asking before running a command"></td>
  </tr>
  <tr>
    <td align="center"><sub>The same chat in your browser</sub></td>
    <td align="center"><sub>It asks before risky commands</sub></td>
  </tr>
</table>

## Models

### Free, no key

| Service | Models |
| --- | --- |
| Kilo Gateway | Nemotron 3 Ultra, Nemotron 3.5 Lightning |
| BlockRun | Nemotron 3 Ultra, Nemotron 3.5 Lightning |
| LLM7 | Codestral |

### Free, with a free key

`prism setup` asks for these. None need a card.

| Service | Models |
| --- | --- |
| NVIDIA | Nemotron 3 Ultra, Nemotron 3.5 Lightning, GLM-5.3 Flash |
| Groq | GPT-OSS 120B |
| Google AI Studio | Gemini Flash (latest) |
| Mistral | Codestral (latest) |
| OpenRouter | Gemma 4 31B, OpenRouter's free pick (50 requests a day) |
| Z.ai | GLM-4.7 Flash |
| Ollama | any model on your own computer |

### Sign in with a plan

`/login` signs in through your browser. Plan models only answer when you pick them with `/model`.

| Plan | Models |
| --- | --- |
| ChatGPT (Plus, Pro) | GPT-6.1 Sol, GPT-6 Luna and your plan's other models |
| Claude (Pro, Max) | Claude Sonnet 5.5, Opus 5.5, Haiku 5.5 |
| Grok (SuperGrok) | Grok 4.7, 4.7 Build Fast, 4.6, 4.5, 3 Mini and more |
| Google Antigravity | Gemini 3.8 Flash, Gemini 4 Argon, Claude on paid plans |
| GitHub Copilot | the models your Copilot plan includes |

Pay per use instead? `/key` adds a key for 23 services, including OpenAI, Anthropic, DeepSeek, xAI, Mistral, Gemini, OpenRouter, Cerebras, SambaNova, Moonshot (Kimi), Alibaba (Qwen), Together, Fireworks, DeepInfra, Venice and LM Studio. [More →](docs/GUIDE.md#use-the-plan-you-already-pay-for)

## Benchmarks

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: light)" srcset="docs/bench-summary-light.png">
    <img src="docs/bench-summary-dark.png" alt="Prism, opencode and mini-swe-agent passed 15 of 15 runs, Aider 14. Prism opens in 0.27s instead of 11s, answers hi in 0.4s instead of 10.7s, and builds a website in 34s instead of 928s." width="820">
  </picture>
</p>

Four coding agents ran the same five tasks on the same free models, three times each. opencode and Aider finished most tasks sooner, because Prism also runs and checks its own work. Free models are shared, so times vary from day to day. Method, every run and per-task times: [docs/BENCHMARKS.md](docs/BENCHMARKS.md).

## Documentation

| | |
| --- | --- |
| [Guide](docs/GUIDE.md) | every feature and command in detail |
| [Setup](docs/SETUP.md) · [Windows](docs/WINDOWS.md) | installing Docker and Prism, step by step |
| [Tool servers](docs/MCP.md) | connecting MCP servers |
| [Architecture](docs/ARCHITECTURE.md) | how the code fits together |
| [Benchmarks](docs/BENCHMARKS.md) | speed and correctness, with method |
| [Changelog](CHANGELOG.md) | what changed in each version |

## Privacy

Prism only sees the folder you start it in, and its commands run inside Docker. Keys and sign-ins stay on your computer, and the router only listens on `127.0.0.1`. Free AI services may keep what you send them, so don't paste passwords or private data. [More →](docs/GUIDE.md#safety-and-privacy) · [Report a security problem](SECURITY.md)

## Contributing

Bug reports, platform reports and new free providers are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md), and please follow the [code of conduct](CODE_OF_CONDUCT.md).

## Thanks

Built with [Empryo](https://empryo.com). Ideas from [OpenCode](https://opencode.ai), [Crush](https://github.com/charmbracelet/crush), [jcode](https://jcode.sh), [Claude Code](https://www.anthropic.com/claude-code) and [Codex](https://github.com/openai/codex); measured against [Aider](https://aider.chat) and [mini-swe-agent](https://github.com/SWE-agent/mini-swe-agent). The look is built on [Charm](https://charm.land)'s [Bubble Tea](https://github.com/charmbracelet/bubbletea) and inspired by [Omarchy](https://omarchy.org).

Free thanks to [NVIDIA](https://build.nvidia.com), [Groq](https://groq.com), [Mistral](https://mistral.ai), [Google Gemini](https://ai.google.dev), [OpenRouter](https://openrouter.ai), [Kilo](https://kilo.ai), [BlockRun](https://blockrun.ai), [LLM7](https://llm7.io), [Z.ai](https://z.ai) and [Ollama](https://ollama.com). The beam is named after the [Last Prism](https://terraria.wiki.gg/wiki/Last_Prism) from [Terraria](https://terraria.org); the art is original.

## License

[MIT](LICENSE)
