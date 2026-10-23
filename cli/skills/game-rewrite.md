---
name: game-rewrite
description: Plan and verify a game decompilation, recompilation, port, or authorized mashup.
---

# Game rewrite

Load this specialist skill only when the user requests game reconstruction or reuse.
Confirm the intended outcome before choosing tools or promising a result.

## Classify the goal
- Matching decompilation targets byte-identical output from a specified build.
- Static recompilation translates existing executable behavior to another runtime.
- Port or rewrite targets documented behavior, not identical machine code.
- Mashup combines authorized material; agree on gameplay and asset boundaries.
- Record platform, target version, input artifacts, and the available baseline.

## Discover first (read-only)
1. Inspect the repository, documentation, licenses, and supplied build instructions.
2. Identify available binaries, source, tools, and checks without modifying them.
3. Determine whether the user supplied or is authorized to use required materials.
4. Flag copyright and redistribution limits; do not obtain or publish protected assets.
5. Ask for missing target details or permissions when they determine the next step.
6. Do not assume any particular decompiler, compiler, SDK, or container is installed.

## Work in reviewable steps
- For a byte match, compare reproducible builds and hashes only when a baseline exists.
- For a recompilation, test representative control flow and runtime behavior.
- For a port, compare observable gameplay with a lawful, runnable reference if available.
- For a mashup, track the origin and permissions of reused code and assets.
- Use existing project tools where possible; propose installations separately if needed.
- Never automatically install toolchains or run destructive cleanup commands.
- Ask before replacing builds, removing assets, changing baselines, or publishing artifacts.

## Report evidence
- Say which goal was pursued and which checks were actually run.
- Separate local matches from whole-game results and untested behavior.
- Record relevant versions only when observed, not guessed.
- Explain limitations from missing references, rights, or toolchains without claiming success.
