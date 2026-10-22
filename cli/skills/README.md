# Prism skills

A skill is a small playbook the Prism agent can load for a common kind of task, like
building a website, fixing a bug or setting up a project. Only the one-line description is
shown to the agent until it decides the skill fits, then it reads the full file.

## File format

One Markdown file per skill, `cli/skills/<name>.md`:

```
---
name: website
description: one line saying when to use this skill (under 120 chars)
---

<the playbook: short imperative bullet steps with concrete commands and checks>
```

Rules:

- `name` must match the filename (without `.md`).
- `description` is a single line. It is the only thing the agent sees first, so
  make it say when to use the skill.
- The body is written to the agent: imperative steps, real commands, clear
  checks. Keep it roughly 25 to 60 lines.

## Adding your own skills

Users can drop their own skill files in either place:

- `<project>/.prism/skills/<name>.md`: skills for one project
- `~/.config/prism/skills/<name>.md`: skills for all your projects

Same format as above. Project skills override user skills with the same name. Inside Prism, `/skills new <name>` writes a starter file for you.

## Built-in skills

- `website`: build a polished, accessible single-page site
- `fix-bug`: reproduce, find root cause, smallest fix, prove it
- `python-project`: new Python project with venv and tests
- `web-app-node`: small Node app with npm scripts, tested without a server
- `explain-code`: plain-words tour of a codebase, read-only
- `git-basics`: safe git setup and commits, no history rewrites
- `refactor`: restructure code with behaviour proven identical
- `computer-help`: troubleshoot a Linux machine, ask before changes
- `browser-game`: small canvas game in one index.html, loop, input, score, restart
- `discord-bot`: Python discord.py bot skeleton, token from .env, sample command
- `automate-task`: Python chore script with dry-run default and clear output
- `portfolio-site`: resume page with marked placeholders and print-friendly CSS
- `learn-mode`: teach a beginner in small steps with tiny hands-on changes
