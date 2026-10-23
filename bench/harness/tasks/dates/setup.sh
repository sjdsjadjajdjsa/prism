cat > dates.py <<'EOF'
"""Small date helpers (stdlib only)."""
import re
from datetime import date, datetime, timedelta, timezone

_ISO = re.compile(
    r"^(\d{4})-(\d{2})-(\d{2})"
    r"(?:[T ](\d{2}):(\d{2})(?::(\d{2}))?"
    r"(?:(Z)|([+-])(\d{2}):?(\d{2}))?)?$"
)


def parse_iso(text):
    """Parse 'YYYY-MM-DD' or 'YYYY-MM-DD[T ]HH:MM[:SS]' with an optional
    'Z', '+HH:MM', '-HH:MM', '+HHMM' or '-HHMM' suffix.

    Returns a timezone-aware datetime converted to UTC. Values without a
    date-time part or without an offset are taken as UTC already.
    Raises ValueError for anything that is not a real date/time."""
    m = _ISO.match(text.strip())
    if not m:
        raise ValueError("not an ISO date: %r" % text)
    y, mo, d, hh, mi, ss, _z, sign, oh, om = m.groups()
    local = datetime(int(y), int(mo), int(d), int(hh or 0), int(mi or 0), int(ss or 0),
                     tzinfo=timezone.utc)
    if sign:
        offset = timedelta(hours=int(sign + oh), minutes=int(om))
        return local - offset
    return local


def days_in_month(year, month):
    if month == 2:
        return 29 if year % 4 == 0 and year % 100 != 0 else 28
    return 30 if month in (4, 6, 9, 11) else 31


def add_months(d, n):
    """Shift date d by n months (n may be negative). The day is clamped to the
    length of the target month: Jan 31 + 1 month -> Feb 28/29."""
    month = (d.month + n) % 12 or 12
    year = d.year + (d.month + n) // 12
    return date(year, month, min(d.day, days_in_month(year, month)))


def weekdays_between(start, end):
    """Number of Monday-Friday days in the half-open range [start, end)."""
    if end < start:
        raise ValueError("end before start")
    count = 0
    cur = start
    while cur <= end:
        if cur.weekday() < 5:
            count += 1
        cur += timedelta(days=1)
    return count


def age_on(born, on):
    """Completed years of age on the date `on`. A person born on Feb 29 has
    their birthday on Mar 1 in non-leap years."""
    years = on.year - born.year
    if (on.month, on.day) <= (born.month, born.day):
        years -= 1
    return years
EOF
cat > test_dates.py <<'EOF'
import unittest
from datetime import date, datetime, timezone

from dates import add_months, age_on, days_in_month, parse_iso, weekdays_between


def utc(*a):
    return datetime(*a, tzinfo=timezone.utc)


class ParseIsoTests(unittest.TestCase):
    def test_utc_and_naive(self):
        self.assertEqual(parse_iso("2024-03-10T12:00:00Z"), utc(2024, 3, 10, 12))
        self.assertEqual(parse_iso("2024-03-10T12:00:05"), utc(2024, 3, 10, 12, 0, 5))
        self.assertEqual(parse_iso("2024-03-10 08:15"), utc(2024, 3, 10, 8, 15))
        self.assertEqual(parse_iso("  2024-03-10 "), utc(2024, 3, 10))

    def test_negative_offset_whole_hours(self):
        self.assertEqual(parse_iso("2024-03-10T23:30:00-05:00"), utc(2024, 3, 11, 4, 30))

    def test_positive_offset_with_minutes(self):
        self.assertEqual(parse_iso("2024-03-10T23:30:00+05:30"), utc(2024, 3, 10, 18, 0))

    def test_negative_offset_with_minutes(self):
        self.assertEqual(parse_iso("2024-03-10T23:30-03:30"), utc(2024, 3, 11, 3, 0))
        self.assertEqual(parse_iso("2024-01-01T00:00:00-09:30"), utc(2024, 1, 1, 9, 30))

    def test_compact_offset_and_zero_offset(self):
        self.assertEqual(parse_iso("2024-03-10T01:00:00+0100"), utc(2024, 3, 10, 0, 0))
        self.assertEqual(parse_iso("2024-03-10T01:00:00-0000"), utc(2024, 3, 10, 1, 0))
        self.assertEqual(parse_iso("2024-03-10T01:00:00+00:00"), utc(2024, 3, 10, 1, 0))

    def test_offset_crossing_year_boundary(self):
        self.assertEqual(parse_iso("2023-12-31T22:00:00-02:00"), utc(2024, 1, 1, 0, 0))
        self.assertEqual(parse_iso("2024-01-01T01:00:00+02:00"), utc(2023, 12, 31, 23, 0))

    def test_result_is_aware_utc(self):
        r = parse_iso("2024-03-10T12:00:00+02:00")
        self.assertEqual(r.utcoffset().total_seconds(), 0)

    def test_invalid(self):
        for bad in ["garbage", "2024-02-30", "2023-02-29", "2024-13-01", "2024-03-10T25:00",
                    "2024-03-10T10:61", "24-03-10", "2024-03-10T10"]:
            with self.assertRaises(ValueError, msg=bad):
                parse_iso(bad)


class CalendarTests(unittest.TestCase):
    def test_days_in_month(self):
        self.assertEqual([days_in_month(2023, m) for m in range(1, 13)],
                         [31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31])
        self.assertEqual(days_in_month(2024, 2), 29)
        self.assertEqual(days_in_month(1900, 2), 28)
        self.assertEqual(days_in_month(2000, 2), 29)
        self.assertEqual(days_in_month(2100, 2), 28)

    def test_add_months_basic(self):
        self.assertEqual(add_months(date(2024, 3, 15), 0), date(2024, 3, 15))
        self.assertEqual(add_months(date(2024, 3, 15), 2), date(2024, 5, 15))
        self.assertEqual(add_months(date(2024, 12, 15), 1), date(2025, 1, 15))

    def test_add_months_year_boundaries(self):
        self.assertEqual(add_months(date(2024, 1, 15), 11), date(2024, 12, 15))
        self.assertEqual(add_months(date(2024, 12, 15), 12), date(2025, 12, 15))
        self.assertEqual(add_months(date(2024, 6, 1), 24), date(2026, 6, 1))
        self.assertEqual(add_months(date(2024, 12, 31), 24), date(2026, 12, 31))

    def test_add_months_negative(self):
        self.assertEqual(add_months(date(2024, 1, 15), -1), date(2023, 12, 15))
        self.assertEqual(add_months(date(2024, 1, 15), -13), date(2022, 12, 15))
        self.assertEqual(add_months(date(2024, 12, 15), -12), date(2023, 12, 15))
        self.assertEqual(add_months(date(2024, 3, 31), -1), date(2024, 2, 29))

    def test_add_months_clamps_day(self):
        self.assertEqual(add_months(date(2024, 1, 31), 1), date(2024, 2, 29))
        self.assertEqual(add_months(date(2023, 1, 31), 1), date(2023, 2, 28))
        self.assertEqual(add_months(date(2000, 1, 31), 1), date(2000, 2, 29))
        self.assertEqual(add_months(date(1900, 1, 31), 1), date(1900, 2, 28))
        self.assertEqual(add_months(date(2024, 8, 31), 1), date(2024, 9, 30))


class CountingTests(unittest.TestCase):
    def test_weekdays_between(self):
        mon = date(2024, 3, 4)
        self.assertEqual(weekdays_between(mon, date(2024, 3, 11)), 5)
        self.assertEqual(weekdays_between(mon, mon), 0)
        self.assertEqual(weekdays_between(mon, date(2024, 3, 5)), 1)
        self.assertEqual(weekdays_between(date(2024, 3, 8), date(2024, 3, 11)), 1)   # Fri -> Mon
        self.assertEqual(weekdays_between(date(2024, 3, 9), date(2024, 3, 11)), 0)   # Sat -> Mon
        self.assertEqual(weekdays_between(date(2024, 3, 1), date(2024, 4, 1)), 21)
        with self.assertRaises(ValueError):
            weekdays_between(date(2024, 3, 5), mon)

    def test_age_on(self):
        born = date(2000, 5, 17)
        self.assertEqual(age_on(born, date(2024, 5, 16)), 23)
        self.assertEqual(age_on(born, date(2024, 5, 17)), 24)
        self.assertEqual(age_on(born, date(2024, 5, 18)), 24)
        self.assertEqual(age_on(born, born), 0)

    def test_age_on_leap_day_birthday(self):
        born = date(2004, 2, 29)
        self.assertEqual(age_on(born, date(2023, 2, 28)), 18)
        self.assertEqual(age_on(born, date(2023, 3, 1)), 19)
        self.assertEqual(age_on(born, date(2024, 2, 28)), 19)
        self.assertEqual(age_on(born, date(2024, 2, 29)), 20)


if __name__ == "__main__":
    unittest.main()
EOF
