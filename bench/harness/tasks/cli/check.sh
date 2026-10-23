# exercise taskbook.py exactly as README.md specifies; every invocation is compared on stdout and exit status
[ -f taskbook.py ] || { echo "taskbook.py missing"; exit 1; }
python3 - <<'PY'
import json, os, subprocess, sys, tempfile
tmp = tempfile.mkdtemp()
db = os.path.join(tmp, "db.json")
fails = []

def run(args, want_out=None, rc=0, err=None, d=None, today="2024-06-15"):
    cmd = [sys.executable, os.path.abspath("taskbook.py"), "--db", d or db] + (["--today", today] if today else []) + args
    p = subprocess.run(cmd, capture_output=True, text=True, timeout=10, cwd=tmp)
    label = " ".join(args)
    if p.returncode != rc:
        fails.append("%s: exit %s, want %s (stderr: %s)" % (label, p.returncode, rc, p.stderr.strip()[:120]))
    elif want_out is not None and p.stdout != want_out:
        fails.append("%s: stdout\n%s\nwant\n%s" % (label, p.stdout, want_out))
    elif err is not None and p.stderr.strip() != err:
        fails.append("%s: stderr %r, want %r" % (label, p.stderr.strip(), err))
    elif rc == 2 and p.stdout:
        fails.append("%s: usage error must not print to stdout" % label)
    return p

run(["add", "Write report", "--priority", "high", "--due", "2024-06-10", "--tag", "Work", "--tag", "urgent", "--tag", "WORK"], "added #1: Write report\n")
run(["add", "  Buy milk  "], "added #2: Buy milk\n")
run(["add", "Plan trip", "--priority", "low", "--due", "2024-07-01", "--tag", "Travel"], "added #3: Plan trip\n")
run(["add", "Pay rent", "--due", "2024-06-15", "--priority", "high", "--tag", "home"], "added #4: Pay rent\n")

L1 = "#1 [ ] Write report (high) due 2024-06-10 tags: work,urgent OVERDUE\n"
L2 = "#2 [ ] Buy milk (normal)\n"
L2x = "#2 [x] Buy milk (normal)\n"
L3 = "#3 [ ] Plan trip (low) due 2024-07-01 tags: travel\n"
L4 = "#4 [ ] Pay rent (high) due 2024-06-15 tags: home\n"
run(["list"], L1 + L2 + L3 + L4)
run(["list", "--sort", "priority"], L1 + L4 + L2 + L3)
run(["list", "--sort", "due"], L1 + L4 + L3 + L2)
run(["list", "--tag", "WORK"], L1)
run(["list", "--tag", "nope"], "no tasks\n")
run(["done", "2"], "done #2\n")
run(["done", "2"], "already done #2\n")
run(["list"], L1 + L3 + L4)
run(["list", "--status", "done"], L2x)
run(["list", "--status", "all", "--sort", "priority"], L1 + L4 + L2x + L3)
run(["stats"], "total: 4\nopen: 3\ndone: 1\noverdue: 1\nopen by priority: high=2 normal=0 low=1\n")
run(["stats"], "total: 4\nopen: 3\ndone: 1\noverdue: 2\nopen by priority: high=2 normal=0 low=1\n", today="2024-06-16")
run(["list"], L1 + L3 + L4.rstrip("\n") + " OVERDUE\n", today="2024-06-16")

want = [{"id": 2, "title": "Buy milk", "priority": "normal", "due": None, "tags": [], "done": True}]
run(["list", "--status", "done", "--json"], json.dumps(want, indent=2, sort_keys=True) + "\n")
run(["list", "--tag", "nope", "--json"], "[]\n")
p = run(["list", "--status", "all", "--json", "--sort", "due"])
try:
    data = json.loads(p.stdout)
    assert [t["id"] for t in data] == [1, 4, 3, 2]
    assert data[0] == {"id": 1, "title": "Write report", "priority": "high", "due": "2024-06-10", "tags": ["work", "urgent"], "done": False}
except Exception as e:
    fails.append("list --json --status all: %r" % e)

run(["rm", "4"], "removed #4\n")
run(["add", "Later"], "added #5: Later\n")           # ids are never reused
run(["list", "--sort", "id"], L1 + L3 + "#5 [ ] Later (normal)\n")
run(["done", "99"], "", 1, "error: no task #99")
run(["rm", "99"], "", 1, "error: no task #99")
run(["done", "4"], "", 1, "error: no task #4")

for bad in (["add", "x", "--priority", "urgent"], ["add", "x", "--due", "2024-02-30"], ["add", "x", "--due", "tomorrow"],
            ["add", "   "], ["add"], ["done", "abc"], ["rm"], ["list", "--sort", "name"], ["list", "--status", "weird"],
            [], ["frobnicate"]):
    run(bad, None, 2)
run(["list"], None, 2, today="2024-13-45")

bad_db = os.path.join(tmp, "bad.json")
open(bad_db, "w").write("{not json")
for cmd in (["list"], ["stats"], ["done", "1"], ["add", "x"]):
    run(cmd, "", 1, "error: cannot read database", d=bad_db)

missing = os.path.join(tmp, "missing.json")
run(["list"], "no tasks\n", d=missing)
run(["list", "--json"], "[]\n", d=missing)
run(["stats"], "total: 0\nopen: 0\ndone: 0\noverdue: 0\nopen by priority: high=0 normal=0 low=0\n", d=missing)
if os.path.exists(missing):
    fails.append("read-only commands must not create the database file")
run(["add", "First"], "added #1: First\n", d=missing)
if not os.path.exists(missing):
    fails.append("add must create the database file")

print("\n".join(fails) if fails else "all taskbook checks passed")
sys.exit(1 if fails else 0)
PY
