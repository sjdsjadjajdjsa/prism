#!/usr/bin/env bash
# usage: bench/run.sh <label> [router-api-url]
set -u
LABEL="$1"; API="${2:-}"; HERE="$(cd "$(dirname "$0")/.." && pwd)"
FILES=(-f "$HERE/docker-compose.yml")
[[ "$(cat "$HERE/.network" 2>/dev/null)" == host ]] && FILES+=(-f "$HERE/docker-compose.host.yml")
OUT="$HERE/bench/results-$LABEL.txt"; : > "$OUT"
task() {
  local name="$1" prompt="$2" setup="$3" check="$4" dir
  dir="$(mktemp -d /tmp/bench-$LABEL-$name-XXXX)"
  (cd "$dir" && eval "$setup")
  local start=$(date +%s)
  PROJECT_DIR="$dir" HOST_UID=$(id -u) HOST_GID=$(id -g) timeout 900 docker compose --project-directory "$HERE" "${FILES[@]}" run --rm --no-deps -T -e PRISM_PROJECT="$dir" -e PRISM_MEMORY="${PRISM_MEMORY:-}" -e PRISM_THINK="${PRISM_THINK:-}" agent ${API:+-api "$API"} run "$prompt" > "$dir.log" 2>&1
  local secs=$(( $(date +%s) - start ))
  local ok=fail; (cd "$dir" && eval "$check") >/dev/null 2>&1 && ok=pass
  local models=$(grep -o '^\[[^]]*\]' "$dir.log" | sort -u | tr -d '[]' | tr '\n' ' ')
  local sent=$(grep -o '[0-9]* tokens sent' "$dir.log" | tail -1 | cut -d' ' -f1)
  printf '%-10s %5ss  %s  %6s tok  %s\n' "$name" "$secs" "$ok" "${sent:-?}" "$models" | tee -a "$OUT"
}
task bugfix "shop.py should print 3.83 but prints something wrong. Find and fix every bug and run it to prove it." \
  "printf 'def total(items):\n    t = 0\n    for name, price, qty in items:\n        t += price\n    return t\n\ndef discount(amount, percent):\n    return amount - amount * percent\n\nprint(round(discount(total([(\"apple\", 0.5, 4), (\"bread\", 2.25, 1)]), 10), 2))\n' > shop.py" \
  "[ \"\$(python3 shop.py)\" = 3.83 ]"
task website "Make a to-do list website in index.html: add, mark done, delete, saved in localStorage. One file." \
  "true" \
  "grep -qi localStorage index.html && python3 -c \"import html.parser;html.parser.HTMLParser().feed(open('index.html').read())\""
task refactor "In report.py the three functions repeat the same formatting code. Move it into one helper function without changing the output, then run python3 report.py to check." \
  "printf 'def a(x):\n    return \"[\" + str(x).upper().strip() + \"]\"\n\ndef b(x):\n    return (\"[\" + str(x).upper().strip() + \"]\") * 2\n\ndef c(x):\n    return \"[\" + str(x).upper().strip() + \"]\" + \"!\"\n\nprint(a(\" hi \"), b(\"yo\"), c(\"ok\"))\n' > report.py; python3 report.py > expected.txt" \
  "[ \"\$(python3 report.py)\" = \"\$(cat expected.txt)\" ] && [ \$(grep -c 'upper()' report.py) -eq 1 ]"
task explain "How many functions are defined across all .py files in this folder? Write just the number into answer.txt." \
  "for i in 1 2 3; do for j in \$(seq 1 \$((i*3))); do echo \"def f\${i}_\${j}(): pass\" >> m\$i.py; done; done" \
  "[ \"\$(tr -dc 0-9 < answer.txt)\" = 18 ]"
task multifile "Create a package calc/ with add.py (add(a,b)) and sub.py (sub(a,b)) and an __init__.py exporting both, plus test_calc.py with plain asserts. Run the test." \
  "true" \
  "python3 -c 'from calc import add, sub; assert add(2,3)==5 and sub(5,3)==2' && python3 test_calc.py"
echo "total: $(awk '{s+=$2; t+=$4} END {print s "s  " t " tokens"}' "$OUT")  passed: $(grep -c ' pass ' "$OUT")/5" | tee -a "$OUT"
