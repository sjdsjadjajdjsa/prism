# Changelog

## Unreleased

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
