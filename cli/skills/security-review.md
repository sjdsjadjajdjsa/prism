---
name: security-review
description: Review a small project for leaked secrets, injection, XSS, risky CORS and vulnerable dependencies.
---

# Skill: security review

Use this when the user asks if a project is safe, before sharing or deploying it. Stay read-only until the user agrees to fixes.

## Rules
- Never print a full secret. Mask it: first 4 characters plus `****` (e.g. `sk-a****`).
- Report only what you saw in a file or command output. Say "not checked" for anything you skipped.

## Step 1: Map the project
- `git ls-files | head -100` and `ls -a` to see languages, entry points and config files.
- Note web pages, servers, scripts that run commands, and any `Dockerfile`.

## Step 2: Secrets
- `git ls-files | rg -i '(^|/)\.env|\.pem$|id_rsa|\.key$|credentials'` for tracked secret files.
- `rg -n -i '(api[_-]?key|secret|token|passw(or)?d)\s*[:=]\s*["'\'']?[A-Za-z0-9_\-]{12,}' --glob '!node_modules' --glob '!*.lock'`
- `rg -n 'AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{30,}|sk-[A-Za-z0-9]{20,}|BEGIN (RSA |EC )?PRIVATE KEY'`
- History: `git log --all --oneline -- .env '*.pem' | head`. A secret in history stays leaked until rotated.
- Check `.gitignore` lists `.env` and key files.

## Step 3: Injection and unsafe code
- Shell: `rg -n 'child_process|exec\(|execSync|shell=True|os\.system|subprocess' ` and check whether user input reaches the command string.
- SQL: `rg -n -i '(select|insert|update|delete).*(\+|\$\{|%s|f")'` for string-built queries. Fix is parameters.
- Paths: `rg -n 'readFile|sendFile|open\(|path\.join'` fed by request input without a check that the result stays inside the base directory.
- Eval and deserialization: `rg -n '\beval\(|new Function|pickle\.loads?|yaml\.load\(|marshal\.loads'`.

## Step 4: Web pages and servers
- XSS: `rg -n 'innerHTML|outerHTML|document\.write|dangerouslySetInnerHTML|v-html'` with non-constant data.
- CORS: `rg -n -i "access-control-allow-origin|cors\("` and flag `*` combined with credentials or private data.
- Insecure defaults: servers bound to `0.0.0.0`, `debug=True`, default passwords, disabled TLS checks (`rejectUnauthorized: false`, `verify=False`), containers running as root.

## Step 5: Dependencies
- Node: `npm audit --omit=dev` (needs `package-lock.json` and network).
- Python: `pip-audit -r requirements.txt`; if `pip-audit` is missing, say so and offer `pip install pip-audit` instead of skipping silently.
- If a tool or network is missing, report "dependencies not checked" and why.

## Report
- List findings as `file:line - severity (high/medium/low) - plain-words problem`, highest first. Mask secrets.
- For each, propose the smallest fix in one line. Leaked secrets: rotate first, then remove.
- Ask which fixes the user wants. Edit only after they agree, then rerun the matching search to prove it is gone.
