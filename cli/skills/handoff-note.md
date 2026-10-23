---
name: handoff-note
description: Leave a safe resume note when substantial work is unfinished or a session must stop.
---

# Handoff note

Use for substantial unfinished work when stopping or approaching a session limit.
Do not create a state file for a quick completed task just because this skill exists.

## Decide where to write
- Use `.prism/STATE.md` for a project-specific resume note.
- Inspect an existing file before writing; preserve unrelated user content.
- If that path contains unrelated content, ask or use a distinct agreed note instead.
- Do not overwrite root `STATE.md` or other user files as a fallback.
- If file access is unavailable, give the resume note in your final response.

## Record the minimum useful context
1. Goal: the user's request and the scope still open.
2. Status: what is complete, incomplete, and blocked; distinguish them clearly.
3. Files changed: paths and short purpose, including uncommitted work.
4. Next action: a concrete command or edit and the condition for success.
5. Verification: exact checks run with results, plus checks not run and why.
6. Failures: concise evidence and attempts worth avoiding, not a long transcript.
7. Decisions needed: the specific question for the user, if any.

## Keep it safe
- Never include secrets, tokens, passwords, private input, or full sensitive logs.
- Do not assume a checked-out branch, tool, or dependency will be available later.
- Label uncertain assumptions rather than presenting them as completed facts.
- Keep the note short enough to scan, but include enough to resume independently.

## Finish the handoff
- Verify the note says where to resume and what remains to prove.
- Tell the user where the note lives; do not call unfinished work complete.
