---
name: refactor
description: Restructure code without changing behaviour: capture current output first, small steps, verify each.
---

# Skill: refactor safely

Use this when the user wants code cleaned up, renamed, split, or restructured, without changing what it does.

## Rule zero
- Behaviour must stay identical. Same inputs, same outputs, same errors. If the user wants behaviour changed too, do that as a separate step before or after, never mixed in.

## Step 1: Capture the current behaviour
- Before touching anything, get a baseline:
  - Run the tests if they exist: `pytest`, `npm test`, or the project's command. Record the result.
  - No tests? Run the program on a few representative inputs and save the outputs:

    ```bash
    python3 main.py < sample1.txt > /tmp/before1.txt
    ```

  - Pick at least one normal case and one edge case.
- This baseline is your proof. Do not skip it.

## Step 2: Plan the steps
- Break the refactor into small independent moves, each safe on its own: rename one function, extract one helper, move one block to a new file.
- Order them so the code runs after every single step.

## Step 3: Small steps, verify each
- Do ONE move at a time.
- After each move:
  - Check syntax: `node --check file.js` or `python3 -m py_compile file.py`.
  - Rerun the tests or the baseline commands.
  - Compare output with the baseline: `diff /tmp/before1.txt <(python3 main.py < sample1.txt)`: the diff must be empty.
- If a check fails, undo just that step and redo it smaller. Do not pile more changes on a broken state.

## Step 4: Final sweep
- When all steps are done, run the full test suite one more time.
- Grep for the old names to catch stragglers: `grep -rn "oldFunctionName" .`
- Remove now-dead code, but only code your refactor made dead.

## Report
- Summarize each move in one line ("extracted parse_args from main", "moved helpers to utils.py").
- State the proof: "all tests pass, and output is byte-identical to before on N sample runs".
