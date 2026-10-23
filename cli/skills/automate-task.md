---
name: automate-task
description: Write a script to do a boring chore: rename, sort, or convert files.
---

# Skill: automate a chore

Use this when the user wants a script to do a boring job: rename files, sort folders, clean CSVs, merge logs.

## Plan
- Restate the chore in one sentence and confirm the exact rule (what counts as a match, what the output is).
- One file, standard library only: `pathlib`, `csv`, `shutil`, `argparse`, `re`. No pip installs.
- The script must be safe to run repeatedly. Same input gives the same result.

## Build the script
- Use `argparse` with an input path argument and a `--dry-run` flag. Default is dry-run ON: real changes need `--apply`.
- Split the work into small functions: one to find items, one to decide the change, one to apply it.
- Never overwrite an existing file. If the target exists, skip it and say so.
- Print one line per item: what would change (dry-run) or what changed (apply). Use a prefix like `would:` or `done:`.
- End with a summary line: counts of changed, skipped, and errors.
- Exit nonzero on bad input (missing folder, unreadable file) with a clear message on stderr.

## Test on sample data
- Make a temp folder: `tmp=$(mktemp -d)`.
- Fill it with small sample files that match the user's real case, including one edge case (weird name, empty row, missing column).
- Run the script in dry-run mode first: `python3 script.py "$tmp"`. Read the output and check it matches the plan.
- Run again with `--apply`, then list the folder and confirm the changes happened.
- Run `--apply` a second time to prove it is idempotent: nothing should change or break.
- Run once with a bad path to confirm the error message is clear.
- Clean up: `rm -rf "$tmp"`.

## Verify before you finish
- Run `python3 -m py_compile script.py`.
- Confirm dry-run is the default by reading the argparse setup, not just the help text.
- Show the user the real command for their data, with `--apply` explained in one line.
