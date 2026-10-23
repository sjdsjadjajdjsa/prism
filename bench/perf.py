#!/usr/bin/env python3
"""Prism start-up, memory and idle cost, measured on a real terminal (pty).

usage: bench/perf.py <program> [project-dir] [--sessions N] [--shell]

<program> is a prism binary (cli: go build -o /tmp/prism .) or, with --shell, a
script such as ./prism (the Docker launcher). For each session it records:
  first_frame_ms   time until the first screen (2 KB of output) arrives
  splash_cpu_pct   CPU while the start screen sits untouched (seconds 5-15)
  idle_cpu_pct     CPU in the chat screen 4-14 s after the last key
  idle_bytes_s     bytes per second still sent to the terminal while idle
  private_kb       private (unshared) memory of the process when idle
N sessions run at the same time; the report gives the median and the extra
private memory of each session after the first.
"""
import json, os, pty, select, statistics, struct, sys, time, fcntl, termios

args = [a for a in sys.argv[1:] if not a.startswith("--")]
program = os.path.abspath(args[0])
project = os.path.abspath(args[1] if len(args) > 1 else ".")
sessions = int(sys.argv[sys.argv.index("--sessions") + 1]) if "--sessions" in sys.argv else 1
if "--sessions" in sys.argv:
    args = [a for a in args if a != str(sessions)]
shell = "--shell" in sys.argv
tick = os.sysconf("SC_CLK_TCK")


def cpu(pid):
    f = open(f"/proc/{pid}/stat").read().rsplit(")", 1)[1].split()
    return int(f[11]) + int(f[12])


def private(pid):
    fields = {l.split(":")[0]: int(l.split()[1]) for l in open(f"/proc/{pid}/smaps_rollup") if ":" in l and l.split()[1].isdigit()}
    return fields.get("Private_Dirty", 0) + fields.get("Private_Clean", 0)


def drain(fds, seconds):
    end, total = time.time() + seconds, 0
    while time.time() < end:
        ready, _, _ = select.select(fds, [], [], 0.05)
        for fd in ready:
            try:
                total += len(os.read(fd, 65536))
            except OSError:
                pass
    return total


def spawn():
    pid, fd = pty.fork()
    if pid == 0:
        os.chdir(project)
        env = dict(os.environ, TERM="xterm-256color", PRISM_MEMORY="off")
        if shell:
            os.execve("/bin/bash", ["bash", program], env)
        os.execve(program, [program], env)
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
    return pid, fd


def first_frame(fd, start):
    got = b""
    while time.time() - start < 90:
        if select.select([fd], [], [], 0.01)[0]:
            got += os.read(fd, 65536)
            if len(got) > 2000:
                return round((time.time() - start) * 1000, 1)
    return None


runs = []
for _ in range(sessions):
    start = time.time()
    pid, fd = spawn()
    runs.append({"pid": pid, "fd": fd, "first_frame_ms": first_frame(fd, start)})
fds = [r["fd"] for r in runs]
drain(fds, 5)
before = [cpu(r["pid"]) for r in runs]
drain(fds, 10)
for r, c in zip(runs, before):
    r["splash_cpu_pct"] = round((cpu(r["pid"]) - c) / tick / 10 * 100, 2)
for r in runs:
    os.write(r["fd"], b" ")
drain(fds, 4)
before = [cpu(r["pid"]) for r in runs]
counts = {}
end = time.time() + 10
while time.time() < end:
    for fd in select.select(fds, [], [], 0.05)[0]:
        try:
            counts[fd] = counts.get(fd, 0) + len(os.read(fd, 65536))
        except OSError:
            pass
for r, c in zip(runs, before):
    r["idle_cpu_pct"] = round((cpu(r["pid"]) - c) / tick / 10 * 100, 2)
    r["idle_bytes_s"] = counts.get(r["fd"], 0) // 10
    r["private_kb"] = private(r["pid"])
for r in runs:
    os.kill(r["pid"], 9)
    os.waitpid(r["pid"], 0)

keys = ["first_frame_ms", "splash_cpu_pct", "idle_cpu_pct", "idle_bytes_s", "private_kb"]
report = {k: statistics.median(r[k] for r in runs if r[k] is not None) for k in keys}
report["sessions"] = sessions
if sessions > 1:
    report["extra_private_kb_per_session"] = round(statistics.median(r["private_kb"] for r in runs[1:]))
print(json.dumps(report))
