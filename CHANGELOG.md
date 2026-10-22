# Changelog

## Unreleased

- MCP support: connect MCP servers for extra tools like file access, web pages, git, a browser or GitHub. `/mcp` shows what is connected; see docs/MCP.md.
- `prism web`: Prism in your browser, with the same look, Shard and the beam.
- `prism fix`: computer help mode. Runs on your computer and asks before every command or file change.
- Skills: 13 built-in playbooks (website, browser game, Discord bot, learn mode and more), plus your own in `.prism/skills/` or `~/.config/prism/skills/`. `/skills` lists them, `/skills new <name>` starts a new one.
- Code in replies and diffs is shown in colour.
- Steps from earlier turns fold into one line; click to open, or `ctrl+o` for all.
- Smooth scrolling with a scrollbar.
- Commands that leave something running in the background no longer hang.
- Benchmark charts in the README. Five coding tasks: 96 s at best, about 165 s with skills (more checking), 168 s with no keys.

## 0.1.0

First release.

- Terminal coding assistant that runs on free AI, with no keys needed.
- Router that races free models and remembers which ones are fast.
- Memory: `PRISM.md` rules, automatic project memory, saved chats, `/usage`.
- Shard, the Last Prism beam and five themes.
- Runs in Docker and only sees the folder you choose.
