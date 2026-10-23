cat > sales_report.py <<'EOF'
#!/usr/bin/env python3
"""sales_report - summarise sales.csv.

usage: sales_report.py [--file PATH] [--top N] [--region NAME] [--month YYYY-MM]
                       [--format text|csv|json] [--no-trend]
"""
import csv
import datetime
import json
import sys

PROG = "sales_report"
DEFAULT_FILE = "sales.csv"
KNOWN_REGIONS = ("north", "south", "east", "west")
FORMATS = ("text", "csv", "json")
SPARK = " .:-=+*#%@"

# problems found while loading, reported at the end of the text report
WARNINGS = []


# ---------------------------------------------------------------- loading

def warn(line_no, message):
    WARNINGS.append("line %d: %s" % (line_no, message))


def parse_date(text):
    try:
        return datetime.datetime.strptime(text.strip(), "%Y-%m-%d").date()
    except ValueError:
        return None


def parse_int(text):
    try:
        return int(text.strip())
    except ValueError:
        return None


def parse_money(text):
    try:
        return round(float(text.strip()), 2)
    except ValueError:
        return None


def clean_name(text):
    return " ".join(text.strip().lower().split())


def parse_row(line_no, row):
    """Return a dict for a valid row, or None (after recording a warning)."""
    if len(row) != 6:
        warn(line_no, "expected 6 fields, got %d" % len(row))
        return None
    date = parse_date(row[0])
    if date is None:
        warn(line_no, "bad date %r" % row[0])
        return None
    region = clean_name(row[1])
    if region not in KNOWN_REGIONS:
        warn(line_no, "unknown region %r" % row[1].strip())
        return None
    qty = parse_int(row[3])
    if qty is None:
        warn(line_no, "bad quantity %r" % row[3])
        return None
    if qty <= 0:
        warn(line_no, "quantity must be positive, got %d" % qty)
        return None
    price = parse_money(row[4])
    if price is None or price < 0:
        warn(line_no, "bad unit price %r" % row[4])
        return None
    return {
        "date": date,
        "month": "%04d-%02d" % (date.year, date.month),
        "region": region,
        "product": clean_name(row[2]),
        "qty": qty,
        "price": price,
        "rep": clean_name(row[5]),
        "revenue": round(qty * price, 2),
    }


def load_rows(path):
    rows = []
    with open(path, newline="") as handle:
        reader = csv.reader(handle)
        header = next(reader, None)
        if header is None:
            return rows
        for index, raw in enumerate(reader, start=2):
            if not raw:
                continue
            parsed = parse_row(index, raw)
            if parsed is not None:
                rows.append(parsed)
    return rows


# ---------------------------------------------------------------- filtering

def filter_rows(rows, region=None, month=None):
    out = []
    for row in rows:
        if region is not None and row["region"] != region:
            continue
        if month is not None and row["month"] != month:
            continue
        out.append(row)
    return out


# ---------------------------------------------------------------- statistics

def group_sum(rows, key):
    """key -> {'orders': n, 'units': n, 'revenue': x} ordered by key."""
    groups = {}
    for row in rows:
        g = groups.setdefault(row[key], {"orders": 0, "units": 0, "revenue": 0.0})
        g["orders"] += 1
        g["units"] += row["qty"]
        g["revenue"] = round(g["revenue"] + row["revenue"], 2)
    return dict(sorted(groups.items()))


def top_n(groups, n):
    """Best n groups by revenue, ties broken by name."""
    ranked = sorted(groups.items(), key=lambda kv: (-kv[1]["revenue"], kv[0]))
    return ranked[:n]


def median(values):
    ordered = sorted(values)
    if not ordered:
        return 0.0
    mid = len(ordered) // 2
    if len(ordered) % 2:
        return float(ordered[mid])
    return (ordered[mid - 1] + ordered[mid]) / 2.0


def totals(rows):
    revenues = [r["revenue"] for r in rows]
    total = round(sum(revenues), 2)
    return {
        "orders": len(rows),
        "units": sum(r["qty"] for r in rows),
        "revenue": total,
        "average_order": round(total / len(rows), 2) if rows else 0.0,
        "median_order": round(median(revenues), 2),
        "largest_order": max(revenues) if revenues else 0.0,
    }


def monthly_trend(rows):
    months = group_sum(rows, "month")
    result = []
    previous = None
    for month, g in months.items():
        if previous is None or previous == 0:
            change = None
        else:
            change = round((g["revenue"] - previous) * 100.0 / previous, 1)
        result.append((month, g["revenue"], change))
        previous = g["revenue"]
    return result


def best_rep_per_region(rows):
    per = {}
    for row in rows:
        reps = per.setdefault(row["region"], {})
        reps[row["rep"]] = round(reps.get(row["rep"], 0.0) + row["revenue"], 2)
    best = {}
    for region in sorted(per):
        name, amount = sorted(per[region].items(), key=lambda kv: (-kv[1], kv[0]))[0]
        best[region] = (name, amount)
    return best


# ---------------------------------------------------------------- text helpers

def money(value):
    return "{:,.2f}".format(value)


def pad(text, width, right=False):
    text = str(text)
    return text.rjust(width) if right else text.ljust(width)


def table(headers, body, right_cols=()):
    """Plain ASCII table; right_cols are right-aligned column indexes."""
    widths = [len(h) for h in headers]
    for line in body:
        for i, cell in enumerate(line):
            widths[i] = max(widths[i], len(str(cell)))
    def fmt(line):
        return "  ".join(pad(c, widths[i], i in right_cols) for i, c in enumerate(line)).rstrip()
    out = [fmt(headers), "  ".join("-" * w for w in widths)]
    out.extend(fmt(line) for line in body)
    return out


def bar(value, maximum, width=24):
    if maximum <= 0:
        return ""
    filled = int(round(width * value / maximum))
    return "#" * filled


def sparkline(values):
    if not values:
        return ""
    low, high = min(values), max(values)
    span = high - low
    chars = []
    for v in values:
        idx = 0 if span == 0 else int((v - low) / span * (len(SPARK) - 1))
        chars.append(SPARK[idx])
    return "".join(chars)


def section(title):
    return ["", title, "=" * len(title)]


# ---------------------------------------------------------------- renderers

def render_text(rows, opts):
    lines = ["Sales report for %s" % opts["file"]]
    scope = []
    if opts["region"]:
        scope.append("region=%s" % opts["region"])
    if opts["month"]:
        scope.append("month=%s" % opts["month"])
    lines.append("Scope: %s" % (", ".join(scope) if scope else "all sales"))
    t = totals(rows)
    lines += section("Totals")
    lines += table(["metric", "value"], [
        ["orders", t["orders"]],
        ["units", t["units"]],
        ["revenue", money(t["revenue"])],
        ["average order", money(t["average_order"])],
        ["median order", money(t["median_order"])],
        ["largest order", money(t["largest_order"])],
    ], right_cols=(1,))
    by_region = group_sum(rows, "region")
    lines += section("By region")
    peak = max([g["revenue"] for g in by_region.values()] or [0])
    lines += table(["region", "orders", "units", "revenue", ""], [
        [name, g["orders"], g["units"], money(g["revenue"]), bar(g["revenue"], peak)]
        for name, g in by_region.items()
    ], right_cols=(1, 2, 3))
    lines += section("Top %d products" % opts["top"])
    ranked = top_n(group_sum(rows, "product"), opts["top"])
    lines += table(["#", "product", "units", "revenue"], [
        [i, name, g["units"], money(g["revenue"])] for i, (name, g) in enumerate(ranked, start=1)
    ], right_cols=(0, 2, 3))
    lines += section("Best rep per region")
    lines += table(["region", "rep", "revenue"], [
        [region, rep, money(amount)] for region, (rep, amount) in best_rep_per_region(rows).items()
    ], right_cols=(2,))
    if opts["trend"]:
        trend = monthly_trend(rows)
        lines += section("Monthly trend")
        lines += table(["month", "revenue", "change"], [
            [m, money(rev), "n/a" if ch is None else "%+.1f%%" % ch] for m, rev, ch in trend
        ], right_cols=(1, 2))
        lines.append("")
        lines.append("sparkline: [%s]" % sparkline([rev for _m, rev, _c in trend]))
    if WARNINGS:
        lines += section("Warnings (%d)" % len(WARNINGS))
        lines += ["  " + w for w in WARNINGS]
    return "\n".join(lines) + "\n"


def render_csv(rows, opts):
    out = ["region,orders,units,revenue"]
    for name, g in group_sum(rows, "region").items():
        out.append("%s,%d,%d,%.2f" % (name, g["orders"], g["units"], g["revenue"]))
    out.append("")
    out.append("product,units,revenue")
    for name, g in top_n(group_sum(rows, "product"), opts["top"]):
        out.append("%s,%d,%.2f" % (name, g["units"], g["revenue"]))
    return "\n".join(out) + "\n"


def render_json(rows, opts):
    data = {
        "file": opts["file"],
        "region": opts["region"],
        "month": opts["month"],
        "totals": totals(rows),
        "regions": group_sum(rows, "region"),
        "top_products": [{"product": n, **g} for n, g in top_n(group_sum(rows, "product"), opts["top"])],
        "warnings": WARNINGS,
    }
    if opts["trend"]:
        data["trend"] = [{"month": m, "revenue": r, "change_pct": c} for m, r, c in monthly_trend(rows)]
    return json.dumps(data, indent=2, sort_keys=True) + "\n"


RENDERERS = {"text": render_text, "csv": render_csv, "json": render_json}


# ---------------------------------------------------------------- command line

class UsageError(Exception):
    pass


def parse_args(argv):
    opts = {"file": DEFAULT_FILE, "top": 3, "region": None, "month": None, "format": "text", "trend": True}
    i = 0
    while i < len(argv):
        arg = argv[i]
        if arg == "--no-trend":
            opts["trend"] = False
            i += 1
            continue
        if arg in ("--file", "--top", "--region", "--month", "--format"):
            if i + 1 >= len(argv):
                raise UsageError("%s needs a value" % arg)
            value = argv[i + 1]
            i += 2
            if arg == "--top":
                if not value.isdigit() or int(value) < 1:
                    raise UsageError("--top must be a positive integer")
                opts["top"] = int(value)
            elif arg == "--region":
                opts["region"] = clean_name(value)
                if opts["region"] not in KNOWN_REGIONS:
                    raise UsageError("unknown region %r" % value)
            elif arg == "--month":
                if parse_date(value + "-01") is None:
                    raise UsageError("--month must look like YYYY-MM")
                opts["month"] = value
            elif arg == "--format":
                if value not in FORMATS:
                    raise UsageError("--format must be one of %s" % ", ".join(FORMATS))
                opts["format"] = value
            else:
                opts["file"] = value
            continue
        raise UsageError("unrecognised argument %r" % arg)
    return opts


def main(argv=None):
    argv = sys.argv[1:] if argv is None else argv
    try:
        opts = parse_args(argv)
    except UsageError as exc:
        sys.stderr.write("%s: error: %s\n" % (PROG, exc))
        sys.stderr.write("usage: %s [--file PATH] [--top N] [--region NAME] [--month YYYY-MM] "
                         "[--format text|csv|json] [--no-trend]\n" % PROG)
        return 2
    try:
        rows = load_rows(opts["file"])
    except OSError as exc:
        sys.stderr.write("%s: cannot read %s: %s\n" % (PROG, opts["file"], exc.strerror))
        return 1
    rows = filter_rows(rows, opts["region"], opts["month"])
    sys.stdout.write(RENDERERS[opts["format"]](rows, opts))
    return 0


if __name__ == "__main__":
    sys.exit(main())
EOF
cat > sales.csv <<'EOF'
date,region,product,qty,unit_price,rep
2024-04-28,west,doohickey,33,1.06,bob
2024-02-26,west,thingamajig,12,28.94,cyd
2024-02-03,north,thingamajig,26,34.43,eli
2024-06-06,north,thingamajig,5,28.73,bob
2024-02-20,north,doohickey,21,0.98,bob
2024-05-08,east,doohickey,1,1.02,dee
2024-06-09,west,thingamajig,6,32.81,cyd
2024-02-17,east,widget,5,2.53,amy
2024-04-04,east,doohickey,5,1.08,amy
2024-02-07,north,doohickey,25,1.09,dee
2024-04-03,south,gizmo,22,11.01,cyd
2024-01-14,north,gadget,16,7.55,amy
2024-01-15,west,gadget,36,6.8,eli
2024-02-24,south,doohickey,25,0.91,dee
2024-02-01,east,thingamajig,20,34.64,amy
2024-02-06,west,thingamajig,37,28.98,bob
2024-02-30,north,widget,3,2.5,amy
2024-02-15,east,widget,40,2.41,cyd
2024-04-03,north,widget,14,2.54,bob
2024-01-20,east,gizmo,40,11.89,eli
2024-04-27,south,doohickey,12,1.02,cyd
2024-02-27,south,gadget,11,7.6,eli
2024-02-22,west,doohickey,39,0.91,amy
2024-01-04,north,thingamajig,17,29.85,dee
2024-03-14,west,gizmo,34,11.22,amy
2024-02-08,west,thingamajig,40,32.22,cyd
2024-02-07,north,widget,18,2.46,bob
2024-01-02,south,gizmo,24,12.07,bob
2024-01-12,south,doohickey,22,1.02,eli
2024-05-05,north,widget,31,2.71,cyd
2024-01-01,north,doohickey,5,1.04,cyd
2024-02-03,north,doohickey,35,0.96,amy
2024-06-24,south,gizmo,23,11.0,dee
2024-01-28,west,widget,32,2.54,eli
2024-06-13,west,thingamajig,1,32.19,amy
2024-01-21,north,gizmo,27,12.55,dee
2024-06-23,west,doohickey,30,1.06,amy
2024-05-25,north,gizmo,39,11.01,amy
2024-02-23,north,doohickey,40,1.02,dee
2024-03-05,east,gizmo,abc,12.0,bob
2024-03-01,east,gizmo,10,12.43,bob
2024-05-06,east,doohickey,32,1.07,cyd
2024-04-22,east,gadget,28,7.69,bob
2024-02-13,south,thingamajig,21,29.67,bob
2024-04-12,north,widget,18,2.66,amy
2024-04-16,east,gadget,27,7.08,eli
2024-04-22,east,thingamajig,29,30.37,amy
2024-03-28,north,gizmo,37,11.65,eli
2024-01-21,south,doohickey,30,0.93,cyd
2024-02-25,south,widget,8,2.47,eli
2024-06-21,east,widget,13,2.35,dee
2024-03-06,north,doohickey,35,1.03,bob
2024-02-09,east,thingamajig,34,31.5,bob
2024-04-28,south,widget,27,2.72,dee
2024-02-15,west,gadget,1,7.07,eli
2024-06-17,east,doohickey,21,1.02,bob
2024-01-24,north,gadget,16,7.83,amy
2024-03-18,east,gizmo,2,11.64,amy
2024-01-15,east,thingamajig,27,33.19,dee
2024-01-07,north,doohickey,3,0.93,cyd
2024-06-26,south,doohickey,10,1.09,eli
2024-06-23,west,doohickey,38,1.08,amy
2024-02-15,east,thingamajig,11,31.64,eli
2024-03-10,west,thingamajig,14,30.27,bob
2024-05-17,east,thingamajig,32,29.62,eli
2024-03-09,west,gadget,-4,7.25,cyd
2024-01-17,north,thingamajig,25,28.52,amy
2024-05-13,north,doohickey,6,1.03,amy
2024-05-15,west,doohickey,18,0.94,dee
2024-02-11,west,doohickey,34,0.95,bob
2024-04-20,north,gizmo,9,12.49,amy
2024-01-07,south,gadget,1,7.52,cyd
2024-06-12,south,thingamajig,32,29.01,eli
2024-01-28,east,gadget,34,7.8,amy
2024-04-21,west,thingamajig,40,29.36,bob
2024-06-18,south,thingamajig,14,33.71,eli
2024-05-28,south,gadget,23,7.83,cyd
2024-05-11,south,gadget,13,7.81,bob
2024-02-05,north,gizmo,25,11.03,dee
2024-05-26,south,gadget,26,7.44,amy
2024-01-07,east,gizmo,8,12.49,cyd
2024-05-22,south,widget,31,2.3,amy
2024-05-20,west,gadget,13,6.79,bob
2024-02-27,south,gizmo,7,12.19,bob
2024-06-15,north,widget,21,2.45,dee
2024-05-12,west,gadget,39,7.07,amy
2024-02-06,west,doohickey,24,1.04,dee
2024-02-20,south,widget,33,2.65,cyd
2024-01-26,west,thingamajig,39,29.55,eli
2024-04-01,south,widget,5
2024-03-26,east,gizmo,8,12.59,bob
2024-04-05,east,thingamajig,24,33.18,bob
2024-04-07,south,widget,22,2.4,amy
2024-01-12,north,gadget,18,7.89,cyd
2024-03-07,west,thingamajig,12,31.71,dee
2024-05-16,south,widget,26,2.74,amy
2024-01-20,north,gadget,17,7.17,eli
2024-01-25,south,doohickey,1,0.91,cyd
2024-03-11,west,thingamajig,37,33.68,amy
2024-06-15,north,thingamajig,39,32.56,amy
2024-04-06,west,doohickey,11,1.06,eli
2024-05-20,north,doohickey,32,0.97,eli
2024-04-20,east,gizmo,34,11.49,cyd
2024-05-10,east,widget,1,2.64,eli
2024-01-21,south,doohickey,25,0.9,cyd
2024-06-13,north,thingamajig,21,28.81,bob
2024-04-24,west,gizmo,16,10.91,amy
2024-06-15,south,gadget,39,7.55,amy
2024-05-14,west,widget,14,2.68,cyd
2024-05-05,north,gizmo,29,11.14,dee
2024-04-20,east,thingamajig,27,30.64,eli
2024-02-10,south,gadget,31,6.69,cyd
2024-05-20,east,gizmo,18,12.26,eli
2024-06-19,south,gizmo,16,11.26,bob
2024-05-21,south,widget,4,2.25,cyd
2024-04-01,north,widget,15,2.52,amy
2024-01-22,south,thingamajig,16,32.33,cyd
2024-04-16,east,gadget,22,7.02,bob
2024-01-26,north,doohickey,40,1.06,bob
2024-05-11,Nowhere,gizmo,2,12.0,dee
2024-05-12,EAST , Gizmo ,2,12.0,dee
2024-04-14,east,doohickey,10,0.96,eli
2024-03-10,south,doohickey,24,1.01,dee
2024-03-03,north,doohickey,37,1.0,dee
2024-03-27,north,widget,20,2.36,eli
2024-01-23,east,doohickey,21,0.95,bob
2024-02-12,east,gizmo,8,11.57,dee
2024-05-20,east,doohickey,34,1.05,cyd
2024-04-11,south,doohickey,34,0.94,cyd
2024-03-01,east,doohickey,38,0.97,bob
2024-06-19,east,doohickey,35,0.92,eli
2024-05-06,south,widget,30,2.69,cyd
2024-01-08,north,thingamajig,11,31.93,amy
2024-06-27,west,widget,38,2.52,eli
2024-01-11,north,gizmo,7,12.96,amy
2024-01-21,south,widget,28,2.37,bob
2024-04-25,east,doohickey,26,0.96,cyd
2024-06-05,west,doohickey,35,1.05,amy
2024-06-20,north,doohickey,23,1.06,dee
2024-01-15,north,thingamajig,24,32.82,amy
2024-04-14,south,gadget,11,7.96,dee
2024-06-06,east,gadget,25,7.14,cyd
2024-03-02,west,gizmo,8,11.53,bob
2024-02-02,west,widget,38,2.49,amy
2024-03-04,south,gadget,23,6.84,bob
2024-05-23,west,thingamajig,33,29.76,bob
EOF
