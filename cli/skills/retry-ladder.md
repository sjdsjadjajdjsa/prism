---
name: retry-ladder
description: When a fix keeps failing: read the error, try a new approach.
---

# Retry ladder

Use when a fix or command fails and another attempt is warranted.
Do not reload this playbook for each retry.

## Diagnose before changing anything
1. Capture the failed command, exit status, error text, and relevant inputs.
2. Check whether the failure is reproducible and whether it is in scope.
3. Identify the assumption the evidence contradicts; inspect the smallest relevant path.
4. Choose a check that measures the intended behavior, not merely a green exit code.
5. Distinguish transient failures from deterministic ones before rerunning a command.

## Make bounded, informative attempts
- Start with the smallest plausible correction supported by the evidence.
- Change one cause at a time where practical; rerun the relevant check.
- If it still fails, compare outputs and revise the hypothesis, not just the wording.
- Narrow to a minimal case or use a meaningfully different approach when needed.
- Preserve production behavior: do not hardcode a value just to pass an example.
- Do not weaken checks or silently widen the requested scope to claim success.
- Keep a brief record of attempts, observations, and what each ruled out.

## Know when to pause
- Bound retries by the available evidence, time, cost, and risk; avoid loops.
- A fixed attempt count alone is not a reason to abandon a tractable task.
- Missing permissions, credentials, critical inputs, or an unsafe operation need a question.
- Ask for the specific missing decision only when further safe investigation cannot help.
- If blocked, report the failing check, observations, and what would unblock it.

## Report
- State the final check and its actual result, including remaining failures.
- Distinguish verified progress from untested hypotheses.
