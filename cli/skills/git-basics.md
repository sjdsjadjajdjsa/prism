---
name: git-basics
description: Set up git safely — init, .gitignore, small clear commits, no force pushes, never commit secrets.
---

# Skill: git basics

Use this for git setup, commits, and teaching beginners safe git habits.

## Starting a repo
- Check first: run `git status`. If it is already a repo, do not run `git init` again.
- For a new project: `git init` in the project root.

## .gitignore before the first commit
- Always create or update `.gitignore` before staging anything. Typical entries:
  - Python: `.venv/`, `__pycache__/`, `*.pyc`
  - Node: `node_modules/`
  - Always: `.env`, `*.log`, `.DS_Store`
- Check what would be committed with `git status --short` and add anything sensitive or generated to `.gitignore`.

## Never commit secrets
- Before every commit, scan staged files: `git diff --cached` and look for API keys, passwords, tokens.
- Files that must never be committed: `.env`, `*.pem`, `*.key`, `credentials.json`, anything with a live token.
- If you find a secret already committed, tell the user plainly: the key must be rotated (changed) because removing it from git does not make it safe again. Ask before rewriting anything.

## Commits
- Commit small and often. One logical change per commit — one feature, one fix.
- Stage deliberately: `git add <file>` for the files that belong to this change. Avoid `git add -A` until you have checked `git status`.
- Message style: short imperative line under 60 chars, e.g. `fix crash when input file is empty` or `add dark mode toggle`. Not "updates" or "stuff".

## Safety rules (never break these)
- Never run `git push --force`, `git rebase`, `git reset --hard`, or `git commit --amend` unless the user explicitly asks. These rewrite or destroy history.
- Never run `git clean -f` or delete branches without asking.
- If a merge conflict appears, show the user the conflicted files and explain the markers (`<<<<<<<`, `=======`, `>>>>>>>`) in plain words before resolving.

## Verify and report
- After committing, run `git log --oneline -5` and `git status` to show a clean state.
- Report what was committed and the message used.
