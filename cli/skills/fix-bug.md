---
name: fix-bug
description: Fix a bug or crash: reproduce it first, then fix it.
---

# Skill: fix a bug

Use this when something is broken: a crash, wrong output, or failing test.

## Step 1: Reproduce it
- Ask the user for the exact error message or wrong behaviour if they did not paste it.
- Run the thing with `bash`: the script, the test suite, or the command the user gave.
- Capture the full error. Read the traceback from the bottom up; the last line names the actual failure.
- If you cannot reproduce it, say so and ask for the missing command or input. Do not guess a fix blind.

## Step 2: Locate the root cause
- From the traceback line numbers, `read` the failing file around those lines.
- If the traceback is long, use `grep` to find the function names it mentions across the repo.
- Trace the bad value backwards: where does it come from? Check the caller, not just the crash site.
- Add nothing yet. If the cause is unclear, run the program with a small `print` or `python3 -c` probe to inspect values, then remove the probe later.

## Step 3: Smallest fix
- Fix the cause, not the symptom. Do not wrap code in try/except to hide the error.
- Change the fewest lines possible. One bug, one fix.
- Keep the existing style. Match indentation and naming around your change.

## Step 4: Prove it
- Rerun the exact same command from step 1. The error must be gone and the output correct.
- Run the project's test suite if one exists (look for `pytest`, `npm test`, or a tests folder). Everything that passed before must still pass.
- Try the edge cases near the bug: empty input, zero, None, the value just past the boundary.

## Step 5: Regression test
- If the project has a test setup, add one small test that fails on the old code and passes now.
- Name it after the bug, e.g. `test_empty_list_does_not_crash`.
- If there is no test setup, skip this and tell the user. Do not install a framework just for this.

## Report
- Say what was wrong in one sentence (the cause), what you changed (file and lines), and the command output proving it works now.
