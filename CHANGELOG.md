# Changelog

## 0.4.0 (2026-10-25)

### New

- The map is now a constellation: stars for files, galaxies for folders, glowing beams for links. Hover lights up a file's links, click flies to it, double-click opens it, `/` finds a file. Time-lapse replays how the project grew from git history, and the trail shows the path Prism took this chat.
- Talk to Prism while it works: Enter sends a correction it picks up before its next step, alt+enter (or ctrl+q) queues a message for after, esc still stops. `/notify on|off` for a desktop notification when it finishes or needs your yes.
- The browser editor shows a live preview for Markdown, and you can paste or drag a picture or video straight in; it's saved next to the note in `attachments/`.
- Commands show their choices as you type: `/editor ` lists simple and nvim, `/theme ` the themes, `/beam ` on and off, and so on, in the terminal and the browser.
- A simple editor is now the default beside the chat: type to write, the mouse selects, ctrl+s saves, ctrl+z undoes, ctrl+c/x/v, ctrl+a, ctrl+f find. Neovim is still there with `/editor nvim`.
- The file tree starts closed; ctrl+b opens it. Without Neovim installed, text files open in the viewer instead of failing.
- A file tree that stays open on the left, in the terminal (ctrl+b or `/tree`) and the browser. Click to open, files Prism is working on light up, the open file is highlighted. New file, new folder, rename and delete from the tree; mention a file in the chat with `@` or by dragging it.
- The map (browser, `/map`): how files link through imports and Markdown links, including `[[wiki links]]`, like Obsidian's graph.
- File paths in the browser chat open the file when clicked. Markdown previews show HTML pictures and follow links to other files.
- Pictures and videos open from `/tree`. In Ghostty, kitty and WezTerm pictures are real pictures; other terminals get coloured blocks. Videos play in the terminal (space pauses, arrows skip 5 seconds) and `o` opens them in the browser.
- Browser Files panel: pictures show, videos play, and Markdown files show their pictures with a Preview button.
- ffmpeg (a 76 MB static build, checked against its SHA-256) is in Prism's box for videos.

### Changed

- The benchmark charts and the GitHub preview picture are redrawn in one style and show results without ranking the tools.
- Shard's idle face no longer has the two mouth dots; they stay for his working phase.

## 0.3.0 (2026-10-25)

### New

- Plan mode: `/plan` lets Prism look around and write a plan. Nothing changes until you run it.
- Hooks: your own checks in `.prism/hooks.json` run before commands, after edits and at the end of a turn. `/hooks` shows them.
- Prism asks before risky commands in every mode: deleting folders, throwing away changes, `git push`, publishing, scripts piped from the internet. `/rules` shows the list and lets you add your own.
- Loop detector: when a model keeps repeating itself or the same step, Prism moves to another model, and stops after a few tries.
- Neovim editor beside the chat in the terminal: `/editor` or ctrl+e opens it, `/editor <file>` opens a file, and `/editor` again hides it.
- In the editor, ctrl+e or a click moves the keyboard between chat and editor, and you drag the line between them to resize.
- Files Prism changes reload in the editor by themselves. Neovim comes in the Docker image, so there is nothing to install.
- `/tree` on its own opens a file tree you move through with the arrows: Enter reads a file, ctrl+e edits it. In the browser it opens the Files panel.
- Saved commands: a prompt in `.prism/commands/<name>.md` (or in your config folder) runs as `/<name>`. `/commands` lists them and `/commands new <name>` starts one.
- `/models` shows three groups: free models, your plans, your own keys.
- A chat pinned to a plan model (ChatGPT, Claude, Grok, Gemini) also uses that model to look at screenshots.
- `/login` signs in to plans you already pay for: ChatGPT, Claude, Grok, Antigravity (Google) and GitHub Copilot. Their models show up in `/models` and only answer when you pick them.
- `/login antigravity` signs in with your Google account and lists your plan's models (Gemini, plus Claude on paid plans). It replaces the old Gemini CLI sign-in.
- If a sign-in page shows a code instead of sending you back, paste the code or the whole link into Prism, or type `/login grok <code>`.
- `/login rm <name>` signs out. Sign-ins are saved in `subscriptions.json`, which only you can read, and renew by themselves.
- `/key` adds or removes a provider key from inside Prism, and a new key works without a restart. 22 paid services are ready, and Prism asks each one which models it has.
- `prism keys` lists your keys, `prism keys add` connects a model you pay for (or one on your computer), and `prism keys remove <id>` removes it.
- `prism update` gets the newest version and rebuilds.
- Pick lists: `/model`, `/login`, `/key`, `/resume`, `/tabs`, `/theme` and `/mcp add` open a menu. Use the arrows, Enter and Esc, or the mouse. Typing filters it.
- `/model <name>` picks the model for one chat, and `/model auto` lets Prism choose again. `/models <text>` filters the list.
- Tabs: several chats in one window (`/tabs`, `/rename`, `/ask`). Also new: `/clear`, `/compact` and `/undo`.
- `/files` browses the project and `/open <path>` shows a file. In the browser you can edit and save it.
- Project tree: `/tree` and `prism tree` show every file with its git changes, what Prism read or changed, and which files many others use.
- `/tree changed` or `/tree <text>` narrows the list, and `prism tree --html` writes a page you can search.
- Prism gets a short map of the project (folders, sizes, the most used files) at the start of each chat.
- Web search: a `web` tool searches DuckDuckGo without a key (Tavily or Brave first if you set their key) and reads public pages.
- Browse tool: after building a web page, Prism clicks and types in it like a person, checks the result and keeps fixing until it works.
- Screenshot tool: Prism can look at a page it built. The browser it needs (about 126 MB) downloads the first time.
- Crew: ask for several agents and Prism runs 2 or 3 helpers at once, then a reviewer checks the work. A panel shows each one.
- MCP setup: `/mcp add` picks a tool server from a built-in list and asks for any key it needs. `/mcp remove` and `/mcp test` manage them.
- 11 new built-in skills (24 in total), plus a `prism` skill so Prism can answer questions about itself.
- `/memory forget <n>` deletes one memory and `/memory forget all` clears them all.
- You can ask for a model by job: `auto:code`, `auto:fast`, `auto:review` or `auto:vision`.
- A weekly check tests every free model and opens an issue when some stop working.
- Benchmarks: `bench/compare.sh` and `bench/harness/` run the same tasks through Prism and other open-source agents. `bench/perf.py` measures start time, idle use and memory.
- Windows: tested on Windows 11 with WSL. There is a step-by-step guide in docs/WINDOWS.md.

### Changed

- Each answer's line and the status bar say who answered: free, plan or yours. `/usage` counts plan and key requests apart.
- The chat follows the answer as it streams. If you scroll up, it stays where you are.
- Typing commands: the exact match comes first, close spellings work (`/mdl` finds `/model`), and what you typed is kept while Prism is busy.
- The browser version matches the terminal: the same commands and pick lists. It fits a phone screen and long links wrap.
- `prism` opens in about a quarter of a second instead of 11 seconds. It only rebuilds when Prism's own files change.
- An idle Prism uses no CPU, and each extra session needs about 9 MB of memory.
- Replies are quicker and cheaper: each request starts the same way during a chat, so providers can reuse their saved copy. Prism also asks for the model that answered last.
- `prism run` and `/usage` show how many tokens the provider had saved.
- Prism makes all the edits it already knows at once and runs the real check once.
- Plans only list models worth picking. Old and internal ones are hidden.
- `/skills` is a short grouped list, `/skills <name>` shows one skill, and `/help` is grouped too.
- When a plan's usage runs out, Prism says so in plain words.
- Retry notes say what happened, like "the model stopped mid-reply, trying another".
- Several questions at once wait in line instead of replacing each other.
- The browser handoff screen can open the page again (`o`) or copy the link (`c`).
- `prism run` prints the answer first, then waits up to 15 seconds for the memory update.
- Clearer Docker errors, each with the fix. On Windows, `/web` opens your normal browser.
- The router only answers this computer, and changing keys or sign-ins needs a secret the AI's commands can't read.
- Grok sign-in uses a device code by default, and the terminal shows numbered steps.
- Free Nemotron Lightning replaces MiniMax M3.1 Flash, which Kilo stopped offering for free.
- New demo video and GIF, and README benchmarks against opencode, Aider and mini-swe-agent.

### Fixed

- Prism works from a folder with a space in its name.
- Phones show "tap to start" instead of "press any key".
- Screen readers now name the files, tabs and pick lists.
- In the browser, `/beam` and `/quiet` are saved when you reload.
- Images now reach the plan models.
- ChatGPT sign-in lists your plan's real models, and its replies no longer come back empty.
- Grok plans can chat again, and old Grok sign-ins are asked to sign in once more.
- When free models are busy, Prism waits 5, 15 then 30 seconds and tries again instead of stopping.
- Sign-ins: Copilot no longer shows an error after it worked, signing out really signs out, reloading the page doesn't cancel it, and one left open over 10 minutes says it timed out.
- Launcher: no more `loginwatch: unbound variable`, a wrong folder name stops instead of building, and builds work when Docker's own network has no internet.
- Install: Arch-based systems (CachyOS, Omarchy, Manjaro) get `pacman` steps, and the fish shell finds `prism`.
- `prism` says "starting prism…" while Docker starts, instead of a blank screen.
- The layout fits the terminal from the start.
- Hints like "Run: prism status" work even before `prism` is on your PATH.
- Tool lines no longer run into their file name.
- Two Prism windows can't overwrite each other's memory or bring back a forgotten one.
- `bench/compare.sh` runs a rate-limited task again instead of counting it as failed.

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
