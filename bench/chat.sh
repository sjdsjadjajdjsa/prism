#!/usr/bin/env bash
# usage: bench/chat.sh <label>  — simple questions, time to first word and total
set -u
HERE="$(cd "$(dirname "$0")/.." && pwd)"; OUT="$HERE/bench/chat-$1.txt"; : > "$OUT"
D=/tmp/chatbench; mkdir -p $D; printf 'def add(a, b):\n    return a + b\n' > $D/math.py
for q in "what do you remember about this project?" "what is 17 times 23?" "give me three quick tips for staying focused, one sentence each" "hi" "explain what a python list is in two sentences"; do
  line=$(PROJECT_DIR=$D HOST_UID=$(id -u) HOST_GID=$(id -g) timeout 300 docker compose -f "$HERE/docker-compose.yml" run --rm --no-deps -T -e PRISM_PROJECT=$D -e PRISM_MEMORY=off -e PRISM_THINK="${PRISM_THINK:-}" agent run "$q" 2>&1)
  tools=$(grep -c '^◆' <<<"$line"); stat=$(grep -o 'first word.*' <<<"$line" | tail -1); model=$(grep -o '^\[[^]]*\]' <<<"$line" | head -1)
  printf '%-48s tools=%s  %s  %s\n' "${q:0:46}" "$tools" "$stat" "$model" | tee -a "$OUT"
done
