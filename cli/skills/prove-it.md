---
name: prove-it
description: Verify meaningful changes with real checks before reporting results.
---

# Prove it before saying done

Use at the verification stage of work, not as a reason to load a playbook for every trivial reply.

## Choose evidence
- Identify what changed and which existing check exercises it.
- Prefer the repository's test, lint, typecheck, build, or documented validation command.
- For a running app or UI, exercise the real behavior when a usable environment exists.
- For a web page, the proof is the `browse` tool: steps that click and type like a user, with `expect` checks on visible results, all passing. A screenshot or grep of the HTML is not proof.
- Include a focused regression check when a bug needs one and the project supports it.
- Match the check to the claim: a successful build does not prove runtime behavior.

## Run and inspect
1. Run the relevant check after the change, not only before it.
2. Read its exit status and output, including skipped tests and warnings.
3. If it fails, diagnose the failure; fix the implementation or report the blocker.
4. Re-run affected checks after fixing them; do not cite an earlier pass.
5. If a real-app check is possible, try the changed path with representative input.
6. Avoid destructive or costly checks without permission when their effects are unclear.

## Preserve honest proof
- Never edit, disable, or weaken a test just to make a change appear to pass.
- Update tests only to reflect a justified requirement change, with the reason stated.
- Do not invent a check or claim a check passed when it was not run.
- If dependencies, credentials, or an environment are missing, say what prevented running it.
- Keep unverified behavior distinct from observed behavior.
- If the UI was not exercised with `browse` (no Chromium, or not a web page), say so as untested.

## Report
- Give the exact checks run and outcomes, including failures or partial coverage.
- State what remains untested and why, without saying the whole task is proven.
- For a simple non-code answer, evidence may be a source or calculation rather than a command.
