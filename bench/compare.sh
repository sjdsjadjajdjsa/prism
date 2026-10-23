#!/usr/bin/env bash
# usage: bench/compare.sh <harness> [model]
#   harness: prism | aider | claude-code | codex | gemini | openhands
#   model:   for prism and aider, a route id from router/providers.json, written
#            <provider>/<model> like the router's /status (default below).
#            The provider's key comes from the environment or keys.conf.
#            For the others, passed to the harness's own --model flag if given.
#
# Runs the five tasks of bench/run.sh through one harness. The task prompts, setup
# commands and pass/fail checks are read straight out of bench/run.sh, so they are
# byte-for-byte the same. Each task runs in a fresh temp dir; the check runs on the
# host after the harness exits. A harness that is not installed is skipped, never faked.
#
# prism and aider both talk to a small pinning proxy started here, which forwards
# straight to that one provider + model (not through the router's racing), the same
# way the router would: prism_* fields stripped, the provider's "quick" params merged
# when prism_quick is set. It asks the provider to report usage and sums the prompt
# tokens the provider reports (the number Prism prints as "tokens sent"), so both
# harnesses are measured by the same code. The model the provider says served each
# request is logged next to the task dir (<dir>.requests.jsonl).
#
# env: TASKS="bugfix website ..."  BUDGET=900 (s per task)
#      PRISM_IMAGE=prism-agent:latest  HARNESS_IMAGE=prism-harness:20261022 (has aider-chat 0.86.2)
set -u
H="${1:?usage: bench/compare.sh <harness> [model]}"
HERE="$(cd "$(dirname "$0")/.." && pwd)"

BUDGET="${BUDGET:-900}"
PRISM_IMAGE="${PRISM_IMAGE:-prism-agent:latest}"
HARNESS_IMAGE="${HARNESS_IMAGE:-prism-harness:20261022}"
DEFAULT_MODEL="nvidia/nvidia/nemotron-3-ultra-550b-a55b"
skip() { echo "SKIP $H: $1"; exit 0; }

# ---- adapters: one per harness. Each runs the prompt headless in $dir, output to $log. ----
# $base is the pinning proxy (prism, aider only). $cname is a docker container name.
# They run under `timeout` in a child bash, so everything they use is exported.
run_prism() {
  docker run --rm --name "$cname" --network host -u "$(id -u):$(id -g)" -v "$dir:/workspace" -w /workspace \
    -e PRISM_API="$base" -e PRISM_PROJECT="$dir" -e PRISM_MEMORY="${PRISM_MEMORY:-}" -e PRISM_THINK="${PRISM_THINK:-}" -e TERM=xterm-256color \
    "$PRISM_IMAGE" -api "$base" run "$prompt"
}
run_aider() {
  # Aider edits the files that are in the chat, so pass the starting files (its normal usage).
  local files; mapfile -t files < <(cd "$dir" && find . -type f | sed 's|^\./||' | sort)
  local args=(--model "openai/$MODEL" --openai-api-base "$base" --openai-api-key free --yes-always --no-git
    --no-auto-commits --no-check-update --analytics-disable --no-show-model-warnings --no-pretty
    --no-fancy-input --no-detect-urls --message "$prompt" "${files[@]}")
  if [ "$AIDER" = local ]; then
    (cd "$dir" && aider "${args[@]}" < /dev/null)
  else
    docker run --rm --name "$cname" --network host -u "$(id -u):$(id -g)" -v "$dir:/workspace" -w /workspace \
      -e HOME=/tmp/run-home --entrypoint /opt/aider/bin/aider "$HARNESS_IMAGE" "${args[@]}" < /dev/null
  fi
}
run_claude_code() { (cd "$dir" && claude -p "$prompt" --dangerously-skip-permissions ${MODEL:+--model "$MODEL"} < /dev/null); }
run_codex() { (cd "$dir" && codex exec --full-auto --skip-git-repo-check ${MODEL:+--model "$MODEL"} "$prompt" < /dev/null); }
run_gemini() { (cd "$dir" && gemini --yolo ${MODEL:+--model "$MODEL"} -p "$prompt" < /dev/null); }
run_openhands() { (cd "$dir" && openhands --headless ${MODEL:+--model "$MODEL"} -t "$prompt" < /dev/null); }

case "$H" in
  prism)
    command -v docker >/dev/null && docker image inspect "$PRISM_IMAGE" >/dev/null 2>&1 || skip "not installed (no $PRISM_IMAGE image)"
    PROXIED=1; MODEL="${2:-$DEFAULT_MODEL}" ;;
  aider)
    if command -v aider >/dev/null; then AIDER=local
    elif command -v docker >/dev/null && docker image inspect "$HARNESS_IMAGE" >/dev/null 2>&1; then AIDER=docker
    else skip "not installed (no aider on PATH, no $HARNESS_IMAGE image)"; fi
    PROXIED=1; MODEL="${2:-$DEFAULT_MODEL}" ;;
  claude-code) command -v claude >/dev/null || skip "not installed"; PROXIED=0; MODEL="${2:-}" ;;
  codex|gemini|openhands) command -v "$H" >/dev/null || skip "not installed"; PROXIED=0; MODEL="${2:-}" ;;
  *) echo "unknown harness '$H' (prism aider claude-code codex gemini openhands)"; exit 2 ;;
esac
RUN="run_${H//-/_}"
export -f "$RUN"; export MODEL PRISM_IMAGE HARNESS_IMAGE AIDER
PROXY="$(mktemp /tmp/pin-proxy-XXXX.py)"; trap 'rm -f "$PROXY"' EXIT

cat > "$PROXY" <<'PY'
import http.client, json, os, pathlib, sys, threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit
# usage: proxy.py <route-id> <repo-root> <log.jsonl|check>
ROUTE, ROOT, LOG = sys.argv[1], pathlib.Path(sys.argv[2]), sys.argv[3]
HOP = {"connection", "keep-alive", "transfer-encoding", "content-length", "host", "proxy-connection", "te", "upgrade",
       "authorization", "accept-encoding"}
lock = threading.Lock()
provider = spec = None
for p in json.loads((ROOT / "router" / "providers.json").read_text())["providers"]:
    for m in p["models"]:
        if f'{p["name"]}/{m["name"]}' == ROUTE:
            provider, spec = p, m
if not provider:
    sys.exit(f"route {ROUTE} is not in router/providers.json")
KEY = ""
if provider.get("requires") and provider.get("auth", True):
    KEY = os.environ.get(provider["requires"], "")
    conf = ROOT / "keys.conf"
    if not KEY and conf.exists():
        for line in conf.read_text().splitlines():
            if line.startswith(provider["requires"] + "="):
                KEY = line.split("=", 1)[1].strip().strip('"').strip("'")
    if not KEY:
        sys.exit(f"route {ROUTE} needs {provider['requires']} (not in the environment or keys.conf)")
UP = urlsplit(provider["baseURL"])
if LOG == "check":
    sys.exit(0)

def served_by(data, sse):
    """The model name the provider says answered (first "model" field in the reply)."""
    for line in (data.split(b"\n") if sse else [b"data:" + data]):
        if line.startswith(b"data:") and b'"model"' in line:
            try:
                return json.loads(line[5:]).get("model")
            except Exception:
                pass
    return None

def usage_of(data, sse):
    found = None
    if sse:
        for line in data.split(b"\n"):
            if line.startswith(b"data:") and b'"usage"' in line:
                try:
                    found = json.loads(line[5:]).get("usage") or found
                except Exception:
                    pass
    else:
        try:
            found = json.loads(data).get("usage")
        except Exception:
            pass
    return found or {}

class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def log_message(self, *args):
        pass
    def handle_any(self):
        body = self.rfile.read(int(self.headers.get("content-length") or 0))
        chat = self.path.rstrip("/").endswith("/chat/completions")
        if chat and body:
            try:
                p = json.loads(body)
                quick = p.get("prism_quick")
                p = {k: v for k, v in p.items() if not k.startswith("prism_")}
                if quick:
                    p.update(provider.get("quick") or {})
                p["model"] = spec["name"]
                if p.get("stream"):
                    p["stream_options"] = {**(p.get("stream_options") or {}), "include_usage": True}
                body = json.dumps(p).encode()
            except Exception:
                pass
        headers = {k: v for k, v in self.headers.items() if k.lower() not in HOP}
        if KEY:
            headers["Authorization"] = f"Bearer {KEY}"
        path = UP.path.rstrip("/") + (self.path[3:] if self.path.startswith("/v1") else self.path)
        try:
            cls = http.client.HTTPSConnection if UP.scheme == "https" else http.client.HTTPConnection
            conn = cls(UP.hostname, UP.port, timeout=900)
            conn.request(self.command, path, body=body or None, headers=headers)
            resp = conn.getresponse()
        except Exception:
            self.send_error(502, "proxy could not reach the provider")
            return
        self.send_response(resp.status)
        for k, v in resp.getheaders():
            if k.lower() not in HOP:
                self.send_header(k, v)
        self.send_header("Connection", "close")
        self.end_headers()
        data = bytearray()
        try:
            while chunk := resp.read1(65536):
                data += chunk
                self.wfile.write(chunk)
                self.wfile.flush()
        except Exception:
            pass
        self.close_connection = True
        if chat:
            sse = "event-stream" in (resp.getheader("content-type") or "")
            usage = usage_of(bytes(data), sse)
            entry = {"status": resp.status, "route": served_by(bytes(data), sse), "prompt_tokens": usage.get("prompt_tokens"),
                     "cached_tokens": (usage.get("prompt_tokens_details") or {}).get("cached_tokens"), "bytes_sent": len(body)}
            if resp.status != 200:
                entry["error"] = bytes(data[:400]).decode("utf8", "replace")
            with lock, open(LOG, "a") as f:
                f.write(json.dumps(entry) + "\n")
    do_GET = do_POST = handle_any

ThreadingHTTPServer.daemon_threads = True
server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
print(server.server_address[1], flush=True)
server.serve_forever()
PY
[ "$PROXIED" = 1 ] && { python3 "$PROXY" "$MODEL" "$HERE" check || exit 1; }
TAG="$H-$(echo "${MODEL:-default}" | tr '/:' '__')"
OUT="$HERE/bench/results-compare-$TAG.txt"; : > "$OUT"
JSONL="$HERE/bench/results-compare-$TAG.jsonl"; : > "$JSONL"

task() {
  local name="$1" prompt="$2" setup="$3" check="$4" dir pid port base log req
  case " ${TASKS:-bugfix website refactor explain multifile} " in *" $name "*) ;; *) return ;; esac
  local try=1 limited=0
  while :; do
  dir="$(mktemp -d "/tmp/compare-$H-$name-XXXX")"; log="$dir.log"; req="$dir.requests.jsonl"
  export dir prompt base cname="compare-$H-$name-$$"
  (cd "$dir" && eval "$setup")
  if [ "$PROXIED" = 1 ]; then
    python3 "$PROXY" "$MODEL" "$HERE" "$req" > "$dir.port" & pid=$!
    for _ in $(seq 50); do [ -s "$dir.port" ] && break; sleep 0.1; done
    port="$(head -1 "$dir.port")"; base="http://127.0.0.1:$port/v1"
  fi
  local start=$(date +%s)
  timeout "$BUDGET" bash -c "$RUN" > "$log" 2>&1
  [ $? = 124 ] && { docker kill "$cname" >/dev/null 2>&1; echo "TIMEOUT after ${BUDGET}s" >> "$log"; }
  local secs=$(( $(date +%s) - start ))
  [ "$PROXIED" = 1 ] && { kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null; }
  local ok=fail; (cd "$dir" && eval "$check") >/dev/null 2>&1 && ok=pass
  # A provider rate limit (HTTP 429) that ends the run is not the harness failing the task:
  # wait and run the task again from scratch; if it is still limited, record no result.
  limited=0
  if [ $ok = fail ] && { grep -qE 'error 429|Too Many Requests' "$log" || { [ -s "$req" ] && grep -q '"status": 429' "$req"; }; }; then limited=1; fi
  if [ $limited = 1 ] && [ $try -lt "${RETRIES:-3}" ]; then
    echo "$name: provider rate limit (429), try $try of ${RETRIES:-3}, again in ${BACKOFF:-90}s" >&2
    sleep "${BACKOFF:-90}"; try=$((try + 1)); continue
  fi
  break
  done
  [ $limited = 1 ] && ok=limited
  # tokens: sum of provider-reported prompt tokens over all successful requests; "?" if any lacked usage
  local tok="?" routes="${MODEL:-default}"
  if [ "$PROXIED" = 1 ] && [ -s "$req" ]; then
    tok="$(python3 -c 'import json,sys
rows=[json.loads(l) for l in open(sys.argv[1])]
ok=[r for r in rows if r["status"]==200]
print("?" if not ok or any(r["prompt_tokens"] is None for r in ok) else sum(r["prompt_tokens"] for r in ok))' "$req")"
    routes="$(python3 -c 'import json,sys
print(" ".join(sorted({json.loads(l)["route"] or "-" for l in open(sys.argv[1]) if json.loads(l)["status"]==200})) or "none")' "$req")"
  fi
  printf '%-10s %5ss  %s  %6s tok  %s\n' "$name" "$secs" "$ok" "$tok" "$routes" | tee -a "$OUT"
  printf '{"harness":"%s","model":"%s","task":"%s","seconds":%s,"pass":%s,"tokens":%s}\n' "$H" "${MODEL:-default}" "$name" "$secs" \
    "$(case $ok in pass) echo true ;; limited) echo null ;; *) echo false ;; esac)" "$([ "$tok" = "?" ] && echo null || echo "$tok")" >> "$JSONL"
}

# The exact task lines of bench/run.sh (from `task bugfix` up to the total line).
eval "$(sed -n '/^task bugfix /,/^echo "total/{/^echo "total/d;p;}' "$HERE/bench/run.sh")"
# Same total line as bench/run.sh (harness and model are in the file name). The denominator is
# the number of tasks actually run, which is 5 unless TASKS selects a subset.
echo "total: $(awk '{s+=$2; t+=$4} END {print s "s  " t " tokens"}' "$OUT")  passed: $(grep -c ' pass ' "$OUT")/$(grep -c . "$OUT")" | tee -a "$OUT"
