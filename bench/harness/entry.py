#!/usr/bin/env python3
"""Entry point inside the prism-harness image: run ONE third-party harness
headless on /workspace against an OpenAI-compatible base URL.

usage: entry.py <aider|mini|opencode> <prompt-file>
env:   BASE_URL  e.g. http://127.0.0.1:PORT/v1  (the per-run recording proxy)
       MODEL     model id sent to the router (default: auto)

Each harness gets the same prompt text and the same model id. Settings are the
harness defaults plus only what is needed to run unattended (auto-approve, no
git, no update checks, no telemetry). The 6-minute budget is enforced outside.
"""
import json
import os
import subprocess
import sys
from pathlib import Path

harness, prompt_file = sys.argv[1], sys.argv[2]
prompt = Path(prompt_file).read_text().strip()
base = os.environ["BASE_URL"].rstrip("/")
model = os.environ.get("MODEL", "auto")
home = Path(os.environ.get("HOME", "/tmp/harness-home"))
home.mkdir(parents=True, exist_ok=True)
env = dict(os.environ, OPENAI_API_KEY="free", OPENAI_API_BASE=base, OPENAI_BASE_URL=base,
           LITELLM_LOCAL_MODEL_COST_MAP="True", PYTHONUNBUFFERED="1")

if harness == "aider":
    # Aider edits files that are "in the chat"; headless, that means listing the
    # folder's starting files on the command line (its normal usage). New files
    # (index.html, calc/...) it may create itself.
    files = sorted(str(p) for p in Path(".").rglob("*") if p.is_file())
    cmd = ["/opt/aider/bin/aider", "--model", f"openai/{model}", "--openai-api-base", base,
           "--openai-api-key", "free", "--yes-always", "--no-git", "--no-auto-commits",
           "--no-check-update", "--analytics-disable", "--no-show-model-warnings",
           "--no-pretty", "--no-fancy-input", "--no-detect-urls", "--message", prompt, *files]
elif harness == "mini":
    env.update(MSWEA_CONFIGURED="true", MSWEA_COST_TRACKING="ignore_errors", MSWEA_SILENT_STARTUP="1")
    cmd = ["/opt/mini/bin/mini", "-y", "--exit-immediately", "-l", "0", "-m", f"openai/{model}",
           "-c", "mini.yaml", "-c", f"model.model_kwargs.api_base={base}",
           "-c", "model.model_kwargs.api_key=free",
           "-o", str(home / "traj.json"), "-t", prompt]
elif harness == "opencode":
    config = {
        "$schema": "https://opencode.ai/config.json",
        "provider": {"router": {"npm": "@ai-sdk/openai-compatible", "name": "Prism router",
                                "options": {"baseURL": base, "apiKey": "free"},
                                "models": {model: {"name": model}}}},
        "model": f"router/{model}",
        "permission": {"edit": "allow", "bash": "allow", "webfetch": "allow"},
        "autoupdate": False,
        "share": "disabled",
    }
    env.update(OPENCODE_CONFIG_CONTENT=json.dumps(config), OPENCODE_DISABLE_AUTOUPDATE="1",
               OPENCODE_DISABLE_LSP_DOWNLOAD="1")
    cmd = ["opencode", "run", "--model", f"router/{model}", prompt]
else:
    sys.exit(f"unknown harness {harness}")

print("+ " + " ".join(cmd[:6]) + " ...", flush=True)
sys.exit(subprocess.call(cmd, env=env, stdin=subprocess.DEVNULL))
