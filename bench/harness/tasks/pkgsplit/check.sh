# behaviour of BOTH entry points must be byte-identical to the original script, and the code really split up
[ "$(sha256sum sales.csv | cut -d' ' -f1)" = "5d2ed430d72fbccf9bbbf195e05ed8112b18e3b1dd312b59f8006d349c367191" ] || { echo "sales.csv changed"; exit 1; }
[ -f salesrep/__init__.py ] && [ -f salesrep/__main__.py ] || { echo "package salesrep/ with __main__.py missing"; exit 1; }
n=$(ls salesrep/*.py | grep -v -e __init__ -e __main__ | wc -l)
[ "$n" -ge 4 ] || { echo "need >=4 modules besides __init__/__main__, got $n"; exit 1; }
for f in salesrep/*.py; do [ "$(wc -l < "$f")" -le 150 ] || { echo "$f longer than 150 lines"; exit 1; }; done
[ -f sales_report.py ] && [ "$(grep -c '[^[:space:]]' sales_report.py)" -le 15 ] || { echo "sales_report.py must be a thin wrapper (<=15 non-blank lines)"; exit 1; }
sig() { # entry-point args...: sha256 of stdout, stderr and exit code
  local o e rc; o=$(mktemp); e=$(mktemp)
  "$@" >"$o" 2>"$e"; rc=$?
  { cat "$o"; printf '\n---\n'; cat "$e"; printf '\n---\n%s' "$rc"; } | sha256sum | cut -d' ' -f1
  rm -f "$o" "$e"
}
bad=0
while IFS='|' read -r want args; do
  for entry in "python3 sales_report.py" "python3 -m salesrep"; do
    got=$(sig $entry $args)
    [ "$got" = "$want" ] || { echo "MISMATCH: $entry $args"; bad=1; }
  done
done <<'GOLDEN'
c77539aedf3946d45d4fe81e1cb03a7fddc316b55c85c9b96fc002701b57ed48|
5fdf1a1ae0c1119bea82dea37cd08b140f484a3fc558a0782ef0234c996fb280|--top 5
932541112780418cc0e4ae1f0c321744429232cb064be9fba444666880219565|--region north --format csv
6a079aaf01a0fac474ae0d04e134d42169d294e301f6cdbe8836c33c9bce252c|--format json
54bbf8df21a4998af4c1060b9a9bc235629eb7796ca85fa10b312a56698fc4bf|--month 2024-03 --no-trend
036ff77d2332e5748edc83aa2f11675793dd249e252efdf3a1db3dd0663fa563|--region WEST --top 2 --format json --no-trend
3aec2b1f14fd0b138aa876c7b36eb2eb5a148d7ce9470161ecb05748f34512ad|--bogus
44769b0d0a8701fbec84d2d7da525c4d859dea1ebc95dd9b4c74a56b5e5f7086|--top 0
e7b04ae9d9fec255e5cc8584e8c64a723fa54088b1602d093103779caad31787|--file nope.csv
ecbcd8f6709566e377bdd2e1ee6d6455394d436d9b4a6b3f80db378c1aefad09|--month 2024-13
f8a16aac15cf3ae581727d0228307fa606f7500bcff2dd215ad792152f4bd196|--region east --month 2024-07
49cb908254160ed6690c09b9af16752855fbf662034db0a484a0cc7410a11ded|--format
7004fa8a20e3f2b993a44ed64925f5782a3b9585d6f9fed90097032227d3f0d1|--file sales.csv --format csv --top 1
GOLDEN
exit $bad
