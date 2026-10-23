# tests must be untouched and must all pass
echo "080c96454b2084e4b8e6711c3fe782655685f54c3520a8c48bc494d85405c132  test_dates.py" | sha256sum -c --quiet - || exit 1
python3 -m unittest >/dev/null 2>&1
