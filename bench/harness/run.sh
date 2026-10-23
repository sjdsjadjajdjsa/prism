#!/usr/bin/env bash
# Harness comparison: same tasks, same router, same model id, same time budget;
# only the coding harness differs.
#
# usage: bench/harness/run.sh [N]          (N repetitions, default 3)
# env:   HARNESSES="prism aider mini opencode"  TASKS="bugfix website refactor explain multifile"
#        BUDGET=360 (seconds per run)  MODEL=auto  ROUTER=http://127.0.0.1:4848
#        OUT=bench/harness/out/<stamp>  (raw logs; results.json is rebuilt from all of out/)
set -u
N="${1:-3}"
H="$(cd "$(dirname "$0")" && pwd)"
HARNESSES="${HARNESSES:-prism aider mini opencode}"
TASKS="${TASKS:-bugfix website refactor explain multifile}"
BUDGET="${BUDGET:-360}"
MODEL="${MODEL:-auto}"
ROUTER="${ROUTER:-http://127.0.0.1:4848}"
IMAGE="${IMAGE:-prism-harness:20261022}"
PRISM_IMAGE="${PRISM_IMAGE:-prism-agent:latest}"
OUT="${OUT:-$H/out/$(date +%Y%m%d-%H%M%S)}"
mkdir -p "$OUT"

curl -sf -m 5 "$ROUTER/v1/models" >/dev/null || { echo "router not reachable at $ROUTER (is prism-router-1 running?)"; exit 1; }
docker image inspect "$IMAGE" >/dev/null 2>&1 || docker build --network host -f "$H/Dockerfile" -t "$IMAGE" "$H" || exit 1
curl -s -m 5 "$ROUTER/status" > "$OUT/router-status-start.json"
{
  echo "prism: image $PRISM_IMAGE $(docker image inspect "$PRISM_IMAGE" --format '{{.Id}} built {{.Created}}' | cut -c8-19,72-) (repo HEAD $(git -C "$H" log -1 --format='%h %cs'))"
  echo "router: $(docker inspect prism-router-1 --format '{{.Image}}' 2>/dev/null | cut -c8-19) (container prism-router-1)"
  docker run --rm --entrypoint bash "$IMAGE" -c 'echo "aider: aider-chat $(/opt/aider/bin/aider --version 2>/dev/null | tail -1 | cut -d" " -f2)"; echo "mini: mini-swe-agent $(/opt/mini/bin/pip show mini-swe-agent | sed -n "s/^Version: //p")"; echo "opencode: opencode-ai $(HOME=/tmp/v opencode --version 2>/dev/null)"'
} > "$OUT/versions.txt"

one() { # harness task rep
  local h="$1" t="$2" rep="$3" id="$1-$2-r$3" dir port pid rc start secs ok timed_out=false
  local run="$OUT/$id"; mkdir -p "$run"
  dir="$(mktemp -d "/tmp/hbench-$id-XXXX")"
  (cd "$dir" && bash "$H/tasks/$t/setup.sh")
  python3 "$H/proxy.py" 0 "$ROUTER" "$run/requests.jsonl" > "$run/port" 2> "$run/proxy.err" & pid=$!
  for _ in $(seq 50); do [ -s "$run/port" ] && break; sleep 0.1; done
  port="$(head -1 "$run/port")"
  local base="http://127.0.0.1:$port/v1" name="hbench-$id-$$"
  local common=(--rm --name "$name" --network host -u "$(id -u):$(id -g)" -v "$dir:/workspace" -w /workspace)
  start=$(date +%s.%N)
  if [ "$h" = prism ]; then
    # Same image and command line as bench/run.sh (`prism -api <url> run "<prompt>"`),
    # but a fresh container without the shared prism-config volume (no memory carry-over).
    timeout "$BUDGET" docker run "${common[@]}" -e PRISM_API="$base" -e PRISM_PROJECT="$dir" -e TERM=xterm-256color \
      "$PRISM_IMAGE" -api "$base" run "$(cat "$H/tasks/$t/prompt.txt")" > "$run/log.txt" 2>&1
  else
    timeout "$BUDGET" docker run "${common[@]}" -e HOME=/tmp/run-home -e BASE_URL="$base" -e MODEL="$MODEL" \
      -v "$H:/harness:ro" "$IMAGE" "$h" "/harness/tasks/$t/prompt.txt" > "$run/log.txt" 2>&1
  fi
  rc=$?
  if [ "$rc" = 124 ]; then timed_out=true; docker kill "$name" >/dev/null 2>&1; fi
  for _ in $(seq 30); do docker inspect "$name" >/dev/null 2>&1 || break; sleep 1; done
  secs=$(python3 -c "import time;print(round(time.time()-$start,1))")
  ok=false; (cd "$dir" && timeout 30 bash "$H/tasks/$t/check.sh") > "$run/check.txt" 2>&1 && ok=true
  sleep 1; kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null
  cp -r "$dir" "$run/workspace" 2>/dev/null
  printf '{"harness":"%s","task":"%s","rep":%s,"pass":%s,"secs":%s,"exit_code":%s,"timed_out":%s,"started":"%s","dir":"%s"}\n' \
    "$h" "$t" "$rep" "$ok" "$secs" "$rc" "$timed_out" "$(date -d @"${start%.*}" -Is)" "$dir" > "$run/meta.json"
  printf '%-9s %-10s r%s  %-4s %6ss  exit=%s%s  requests=%s\n' "$h" "$t" "$rep" "$($ok && echo pass || echo fail)" "$secs" "$rc" \
    "$($timed_out && echo ' TIMEOUT')" "$(wc -l < "$run/requests.jsonl" 2>/dev/null || echo 0)" | tee -a "$OUT/progress.txt"
}

hs=($HARNESSES)
for rep in $(seq 1 "$N"); do
  for t in $TASKS; do
    # rotate harness order each task/rep so no harness always runs first (router warm-up, rate limits)
    k=$(( (rep + $(echo "$TASKS" | tr ' ' '\n' | grep -nx "$t" | cut -d: -f1)) % ${#hs[@]} ))
    for i in $(seq 0 $(( ${#hs[@]} - 1 ))); do one "${hs[$(( (i + k) % ${#hs[@]} ))]}" "$t" "$rep"; done
  done
done
curl -s -m 5 "$ROUTER/status" > "$OUT/router-status-end.json"
python3 "$H/summarize.py" "$H/out" "$H/results.json"
