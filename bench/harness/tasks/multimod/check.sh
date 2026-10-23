# the tests must be untouched and must all pass
echo "3337bc9a629a7d2f638ffa743850f067d9b4bcf9222a73f8ee61bcbbf9854d2b  test_shop.py" | sha256sum -c --quiet - || exit 1
python3 -m unittest >/dev/null 2>&1
