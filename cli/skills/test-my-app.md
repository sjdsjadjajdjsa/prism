---
name: test-my-app
description: Exercise a user's running app like a real user and report reproducible failures without changing code.
---

# Test my app

Use this when the user asks whether an app actually works, not just whether it builds.

## Find the real entry point
- Read `README.md`, `package.json` scripts, and `Makefile` if present; identify setup, start, and test commands.
- Check the required environment and dependencies. Do not invent credentials, install packages, or run migrations with unclear effects without permission.
- Note the advertised main flows and the port, base URL, or CLI invocation. Prefer the documented command over guesses.
- Record the commands and inputs you use so someone else can repeat them.

## Run it safely
- Start the app with its documented command in the project directory. Keep its output and startup errors.
- Prism's `bash` tool stops any process still running when a command ends, and commands cannot read keyboard input. A server cannot stay up between tool calls: start it, probe it, and stop it inside ONE `bash` call.
- For a long-running server, start it in the background, record its PID (`app_pid=$!`), and use `trap 'kill "$app_pid" 2>/dev/null || true' EXIT` in that same command. Poll the documented local URL with `curl -i` in a bounded loop until it answers, and run all your probes before the command ends.
- Do not mistake a listening port from another process for this app. Stop every server you started, even after a failed check. Do not kill unrelated processes.

## Act like a user
- For a CLI, invoke its help and main commands with realistic arguments and actual input files; inspect exit codes, output, and resulting files.
- For a web app, request the landing page and main routes with `curl -i http://127.0.0.1:PORT/`; follow links and exercise forms or actions with representative requests.
- For web pages, use the `browse` tool to do the main user actions (add, toggle, filter, submit, reload) and expect the results; it also reports page errors. Use `screenshot` only to judge layout and looks. If neither tool is available, say the page was not checked in a browser.
- For an API, call documented endpoints with `curl -i`, including a valid JSON request using `-H 'Content-Type: application/json' -d '{"example":"value"}'` when applicable.
- Check responses and side effects against the README or UI promises, not merely HTTP status codes or process survival.
- Try an empty input, malformed or unsupported input, and a reasonably large input for each important flow; avoid destructive data and excessive load.
- Check useful error messages, validation, and recovery after bad input. Keep sample data temporary when possible.

## Check and report
- Run the repository's existing test command (for example, `npm test`, `make test`, or `python3 -m unittest` only when documented or configured).
- Record test output, exit status, skips, and missing prerequisites; do not call an unrun check a pass.
- Do not change code or tests unless the user asks for a fix.
- Report a plain `PASS`/`FAIL` list for each flow and the existing tests. For each failure, give the exact directory, startup command, request or input, expected result, and actual result (including status/error).
- Mark blocked or untested flows explicitly with reasons; distinguish those from failures. State whether visual testing was possible and confirm started servers were stopped.
