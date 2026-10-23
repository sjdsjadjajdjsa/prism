---
name: explain-code
description: Explain how code works, in plain words. Nothing is changed.
---

# Skill: explain code

Use this when the user asks "what does this do", "explain this project", or "walk me through this file".

## Golden rule
- Explain only. Do not edit, rename, reformat, or "improve" anything. Read tools only: `list`, `grep`, `read`.
- Only state what you read in the files; quote file:line; never invent config or fields; define each technical word in plain words; end with how to run it.

## Step 1: Map the project
- Use `list` on the project root to see the shape: folders, entry files, config.
- Look for entry points: `main.py`, `index.js`, `package.json` scripts, a `cmd/` folder, or the file the user opened.
- Use `grep` to find where the entry point calls into, e.g. `grep` for `def main` or `require(` to follow imports.

## Step 2: Read before you speak
- `read` every file you will explain. Never describe code you have not read.
- Read in dependency order: entry point first, then the files it imports.
- For big files, read the top (imports, constants) and the main functions. Skip repetitive middle parts but say you did.

## Step 3: Explain in plain words
- Assume the reader is a beginner. Replace jargon with everyday words, or explain the jargon in one clause: "a regex (a pattern for matching text)".
- Start with one sentence answering: what is this project for?
- Then give a file-by-file tour. For each file:
  - One sentence: what this file's job is.
  - The most important function and what it does, in plain words.
  - How it connects to the other files ("main.py calls this to ...").
- Trace one real example through the code: "when you run X, first this happens, then that".
- Use short sentences. Use the actual names of files and functions so the user can find them.

## Step 4: Check understanding
- Point out the one or two places a beginner should read first to learn more.
- If something in the code is confusing or looks like a bug, mention it honestly, but do not fix it.
- End by offering to go deeper on any one file or function.

## Format
- Keep the whole explanation under a screenful where possible. Structure it with a short intro, a file list, and the example trace.
