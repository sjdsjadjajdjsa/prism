---
name: computer-help
description: Fix a Linux problem in plain words, asking before any big change.
---

# Skill: Linux computer help

Use this when the user has a problem with their Linux machine: slow, no network, disk full, something will not start.

## Talk plainly
- The user is a beginner. One short sentence per idea. Explain any term you must use.
- Note up front when relevant: Prism may run in a sandbox, so some host info may not be visible. If a command shows nothing useful, say that might be why.

## Step 1: Gather info (read-only commands only)
- Run whichever of these fit the problem. They change nothing:

  ```bash
  uname -a                          # kernel and system
  lsb_release -a 2>/dev/null        # distro version
  df -h                             # disk space
  free -h                           # memory
  ip a                              # network interfaces
  systemctl --failed                # broken services
  journalctl -p 3 -b --no-pager | tail   # recent serious errors
  ```

- Match commands to the symptom:
  - "disk full" → `df -h`, then `du -sh ~/.* ~/* 2>/dev/null | sort -h | tail` to find big folders.
  - "slow" → `free -h` and `uptime` (load average).
  - "no internet" → `ip a`, then `ping -c 2 1.1.1.1`.
  - "app won't start" → `journalctl -p 3 -b --no-pager | tail` and `systemctl --failed`.
- If a command is missing or fails, move on. Do not install tools during diagnosis.

## Step 2: Explain what you found
- Translate the output into plain words: "Your disk is 96% full. The biggest folder is Downloads at 40 GB."
- Say what is normal and what is not. Beginners cannot tell.
- If the info is inconclusive, say so and pick the next read-only check.

## Step 3: Propose a fix, then ASK
- State the fix in plain words, then show the exact command.
- ALWAYS ask the user before running any command that changes the system or uses `sudo`. Wait for a clear yes. Examples: `apt`, `systemctl restart`, deleting files, editing config.
- Warn about risk honestly: "this deletes files permanently" or "this restarts the network, you may disconnect briefly".
- Prefer the least destructive fix first.

## Step 4: Verify
- After an approved fix, rerun the matching read-only command from step 1 to show the change worked.
- Close with one sentence: what was wrong, what fixed it.
