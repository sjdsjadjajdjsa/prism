# tests must be untouched and pass, and the fix must hold through the public API too
echo "e5423970df4b4d8ed02d3a5ea33f0cdf29362906147fab784e0c07e9b96ef556  test_catalog.py" | sha256sum -c --quiet - || exit 1
python3 -m unittest >/dev/null 2>&1 || exit 1
python3 - <<'PY'
from slugs import slugify
from catalog import Catalog, DuplicateTitle, display
assert slugify("a\t\tb\u00a0c\r\nd  e") == "a-b-c-d-e"
assert slugify("Rock & Roll!") == "rock-roll"
assert display("x\n\ny") == "x y"
c = Catalog(); c.add("Foo Bar")
try:
    c.add("foo\t\t\tbar"); raise SystemExit(1)
except DuplicateTitle:
    pass
PY
