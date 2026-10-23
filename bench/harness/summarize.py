#!/usr/bin/env python3
"""Rebuild results.json from every run under bench/harness/out/.

usage: summarize.py <out-dir> <results.json>
"""
import json
import statistics
import sys
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path

out, dest = Path(sys.argv[1]), Path(sys.argv[2])
runs, batches, versions = [], [], {}
for batch in sorted(p for p in out.iterdir() if p.is_dir()):
    metas = sorted(batch.glob("*/meta.json"))
    if not metas:
        continue
    vfile = batch / "versions.txt"
    bv = {}
    if vfile.exists():
        for line in vfile.read_text().splitlines():
            if ": " in line:
                k, v = line.split(": ", 1)
                bv[k] = v
                versions.setdefault(k, set()).add(v)
    batches.append({"batch": batch.name, "versions": bv, "runs": len(metas)})
    for meta in metas:
        run = json.loads(meta.read_text())
        rdir = meta.parent
        reqs = []
        if (rdir / "requests.jsonl").exists():
            reqs = [json.loads(l) for l in (rdir / "requests.jsonl").read_text().splitlines() if l.strip()]
        chat = [r for r in reqs if r.get("path", "").endswith("/chat/completions")]
        ok = [r for r in chat if r.get("status") == 200]
        bad = [r for r in chat if r.get("status") != 200]
        log = (rdir / "log.txt").read_text(errors="replace") if (rdir / "log.txt").exists() else ""
        run.update(
            batch=batch.name,
            model_requests=len(chat),
            failed_requests=len(bad),
            request_kb_sent=round(sum(r.get("req_bytes", 0) for r in chat) / 1024, 1),
            routes=dict(Counter(r.get("route") or "?" for r in ok)),
            request_errors=sorted({(r.get("error") or "")[:200] for r in bad})[:5],
            log=str(rdir.relative_to(out.parent) / "log.txt"),
        )
        if not run["pass"] or run["exit_code"] != 0:
            run["log_tail"] = [l[:300] for l in log.strip().splitlines()[-12:]]
        if run["timed_out"]:
            run["error"] = "hit the time budget and was killed"
        elif run["exit_code"] != 0:
            run["error"] = f"harness exited with code {run['exit_code']}"
        elif not run["pass"]:
            run["error"] = "harness finished but the check failed"
        runs.append(run)

summary = {}
for h in sorted({r["harness"] for r in runs}):
    rs = [r for r in runs if r["harness"] == h]
    routes = Counter()
    for r in rs:
        routes.update(r["routes"])
    per_task = {}
    for t in sorted({r["task"] for r in rs}):
        tr = [r for r in rs if r["task"] == t]
        per_task[t] = f"{sum(r['pass'] for r in tr)}/{len(tr)}"
    summary[h] = {
        "passed": sum(r["pass"] for r in rs),
        "runs": len(rs),
        "pass_rate": round(sum(r["pass"] for r in rs) / len(rs), 3),
        "per_task": per_task,
        "median_secs": round(statistics.median(r["secs"] for r in rs), 1),
        "median_secs_passing": round(statistics.median([r["secs"] for r in rs if r["pass"]]), 1) if any(r["pass"] for r in rs) else None,
        "timeouts": sum(r["timed_out"] for r in rs),
        "nonzero_exits": sum(r["exit_code"] != 0 for r in rs),
        "model_requests": sum(r["model_requests"] for r in rs),
        "failed_requests": sum(r["failed_requests"] for r in rs),
        "request_kb_sent": round(sum(r["request_kb_sent"] for r in rs), 1),
        "mean_secs": round(statistics.mean(r["secs"] for r in rs), 1),
        "routes_used": dict(routes.most_common()),
    }

dest.write_text(json.dumps({
    "generated": datetime.now(timezone.utc).isoformat(timespec="seconds"),
    "method": "same 5 tasks (from bench/run.sh), same router (prism-router-1), same model id 'auto', "
              "same 360 s budget, one fresh container per run, --network host; only the harness differs. "
              "Each run talks to its own recording proxy (bench/harness/proxy.py) so every request's upstream "
              "route (router header x-free-route) is attributed to that run.",
    "model": "auto",
    "budget_secs": 360,
    "versions": {k: sorted(v) for k, v in versions.items()},
    "batches": batches,
    "summary": summary,
    "failures": [{k: r[k] for k in ("harness", "task", "rep", "batch", "error", "exit_code", "secs") if k in r}
                 for r in runs if not r["pass"]],
    "runs": runs,
}, indent=2) + "\n")
print(f"{'harness':10} {'pass':>7} {'median s':>9} {'mean s':>7} {'timeouts':>9} {'requests':>9} {'KB sent':>8}")
for h, s in summary.items():
    print(f"{h:10} {s['passed']:>3}/{s['runs']:<3} {s['median_secs']:>9} {s['mean_secs']:>7} {s['timeouts']:>9} {s['model_requests']:>9} {s['request_kb_sent']:>8}")
