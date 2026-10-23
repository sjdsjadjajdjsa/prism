---
name: release-check
description: Run a full go/no-go checklist before publishing or releasing a project.
---

# Skill: release check

Use before tagging, publishing, or shipping a release. Read-only verification: never run
`git push`, `git tag`, `npm publish`, or any upload without the user explicitly asking.

## Clean-clone proof
- Clone fresh into a temp dir: `d=$(mktemp -d) && git clone . "$d/clone" && cd "$d/clone"`.
- Follow the README install/setup steps exactly as written, copy-pasting commands. Note every
  step that fails, is missing, or only works with extra knowledge.
- From the clean clone, run the project's tests and build (`npm test`, `npm run build`,
  `python3 -m pytest`, `go test ./...` — whatever the README says). Record exit codes.

## Version and changelog
- Confirm version numbers agree everywhere (package.json, pyproject.toml, tags, docs).
- Confirm CHANGELOG (or release notes) has an entry for this release with today's date.

## Repo hygiene
- List tracked files: `git ls-files`. Look for junk: `.env`, `*.pem`, `*.key`,
  `node_modules/`, `.venv/`, `__pycache__/`, build output, `*.log`, `.DS_Store`.
- Find oversized tracked files: `git ls-files -z | xargs -0 du -b 2>/dev/null | sort -n | tail -20`.
  Flag anything over ~5 MB; suggest git history stays lean and big assets live elsewhere.
- Scan for secrets in tracked content: `grep -rInE '(api[_-]?key|secret|token|password)' $(git ls-files) | head -20`.
  If a real secret is tracked, stop and tell the user it must be rotated; do not just delete it.
- Verify a LICENSE file exists: `ls LICENSE* LICENCE* COPYING* 2>/dev/null`. Flag it if missing.

## README and docs
- Every command block in the README must be copy-pasteable and work; test them in the clean clone.
- Check links: extract URLs and probe them, e.g.
  `grep -oE 'https?://[^) ]+' README.md | sort -u | while read u; do curl -sI -o /dev/null -w "%{http_code} $u\n" --max-time 10 "$u"; done`.
  Report any non-2xx/3xx link.
- Confirm screenshots/images referenced in the README exist and still show the current UI;
  retake them if the app changed. If a `screenshot` tool is available, use it.

## Go / no-go report
- End with a plain checklist: each item above marked GO or NO-GO, with file:line or command
  output as evidence for every NO-GO.
- Offer to fix NO-GO items. Only push, tag, or publish when the user asks after seeing the list.
