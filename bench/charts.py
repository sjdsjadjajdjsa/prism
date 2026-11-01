#!/usr/bin/env python3
"""Build the benchmark charts and the social preview.

Every graphic is its own HTML page in docs/graphics/, drawn from bench/history.json,
and a headless Chromium screenshot of that page is the PNG in docs/.

  python3 bench/charts.py             write the HTML pages, then the PNGs
  python3 bench/charts.py --html-only write only the HTML pages (needs no browser)
"""
import html
import json
import pathlib
import shutil
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
DOCS = ROOT / "docs"
PAGES = DOCS / "graphics"
DATA = json.loads((ROOT / "bench" / "history.json").read_text())

WIDTH = 1280
SPECTRUM = ["#ff5f6d", "#ffa25f", "#f9e27d", "#7ee2a8", "#6fd3ff", "#8a9bff", "#c69bff"]
RAINBOW = "linear-gradient(90deg, " + ", ".join(SPECTRUM) + ")"
TOOLS = {
    "prism": RAINBOW,
    "opencode": "#6fd3ff",
    "aider": "#8a9bff",
    "mini-swe-agent": "#c69bff",
}
NAMES = {"prism": "Prism", "opencode": "opencode", "aider": "Aider", "mini-swe-agent": "mini-swe-agent"}

CSS = """
* { box-sizing: border-box; margin: 0; }
html { background: #0b0b10; }
body { width: 1280px; height: var(--h); overflow: hidden; padding: 56px 64px 0;
  background: radial-gradient(1200px 520px at 20% -10%, #1a1730, #0b0b10 70%);
  color: #ece9f6; font: 500 24px/1.2 'JetBrains Mono', 'JetBrainsMono Nerd Font', 'JetBrainsMono NF', ui-monospace, monospace; }
h1 { font-size: 44px; font-weight: 800; letter-spacing: -.01em; height: 56px; white-space: nowrap; }
.sub { margin-top: 12px; font-size: 22px; line-height: 30px; color: #b4b0c8; max-width: 1100px; }
.rule { margin-top: 22px; width: 160px; height: 6px; border-radius: 3px; background: __RAINBOW__; }
.legend { display: flex; flex-wrap: wrap; gap: 14px 56px; margin-top: 30px; }
.legend div { display: flex; align-items: center; gap: 12px; font-size: 22px; color: #b4b0c8; }
.legend b { color: #ece9f6; font-weight: 700; }
.legend i, .key i { width: 22px; height: 22px; border-radius: 6px; display: inline-block; }
.group { margin-top: 34px; }
.group h2 { font-size: 28px; font-weight: 700; height: 40px; }
.row { display: flex; align-items: center; height: 46px; }
.row .name { width: 250px; font-size: 23px; font-weight: 700; flex: none; white-space: nowrap; }
.row .bar { height: 28px; border-radius: 8px; flex: none; }
.row .val { margin-left: 16px; font-size: 24px; font-weight: 700; white-space: nowrap; }
.row .val span { color: #b4b0c8; font-weight: 500; margin-left: 14px; }
.row .val span.warn { color: #f9e27d; font-weight: 700; }
.wide { display: flex; align-items: center; height: 84px; }
.wide .what { width: 560px; flex: none; padding-right: 24px; }
.wide .what b { display: block; font-size: 24px; font-weight: 700; }
.wide .what span { display: block; margin-top: 6px; font-size: 19px; color: #b4b0c8; }
.wide .bar { height: 30px; border-radius: 8px; flex: none; }
.wide .val { margin-left: 16px; font-size: 26px; font-weight: 700; }
.pair .name { width: 150px; font-size: 21px; color: #b4b0c8; font-weight: 500; }
.key { display: flex; gap: 36px; margin-top: 26px; font-size: 22px; color: #b4b0c8; }
.key span { display: flex; align-items: center; gap: 10px; }
""".replace("__RAINBOW__", RAINBOW)


def esc(text):
    return html.escape(str(text), quote=True)


def num(value):
    return f"{value:g}"


def sub_lines(subtitle):
    return -(-len(subtitle) // 84)


def head(subtitle):
    """Pixels above the first block: padding, title, subtitle lines and the rule."""
    return 56 + 56 + 12 + 30 * sub_lines(subtitle) + 22 + 6


def page(title, subtitle, body, height, extra=""):
    return f"""<!doctype html>
<html lang="en"><meta charset="utf-8">
<title>{esc(title)}</title>
<style>{CSS}</style>
<body style="--h:{height}px">
{extra}<h1>{esc(title)}</h1>
<p class="sub">{esc(subtitle)}</p>
<div class="rule"></div>
{body}
</body></html>
"""


def bar_style(color, width):
    return f'width:{width:.1f}px;background:{color}'


def harness_chart(section, tools, name):
    data = DATA[section]
    rows = data["rows"]
    tasks = []
    for r in rows:
        if r["task"] not in tasks:
            tasks.append(r["task"])
    by = {(r["harness"], r["task"]): r for r in rows}
    longest = max(r["seconds"] for r in rows if r.get("pass") is not None)
    span = 640
    legend = []
    for t in tools:
        mine = [r for r in rows if r["harness"] == t and r.get("pass") is not None]
        passed = sum(r.get("passed", int(bool(r["pass"]))) for r in mine)
        total = sum(r.get("runs", 1) for r in mine)
        seconds = round(sum(r["seconds"] for r in mine), 1)
        legend.append(f'<div><i style="background:{TOOLS[t]}"></i><span><b>{NAMES[t]}</b> {passed}/{total} passed · {num(seconds)}s total</span></div>')
    out = [f'<div class="legend">{"".join(legend)}</div>']
    for task in tasks:
        out.append(f'<div class="group"><h2>{esc(task)}</h2>')
        for t in tools:
            r = by.get((t, task))
            if r is None or r.get("pass") is None:
                out.append(f'<div class="row"><div class="name" style="color:#b4b0c8">{NAMES[t]}</div><div class="val"><span>no result</span></div></div>')
                continue
            w = max(6, span * r["seconds"] / longest)
            color = TOOLS[t]
            solid = color if color.startswith("#") else "#ece9f6"
            runs = r.get("runs")
            if runs:
                score = f'{r["passed"]}/{runs} passed'
                cls = "" if r["passed"] == runs else "warn"
            else:
                score = "passed" if r["pass"] else "did not pass"
                cls = "" if r["pass"] else "warn"
            out.append(
                f'<div class="row"><div class="name" style="color:{solid}">{NAMES[t]}</div>'
                f'<div class="bar" style="{bar_style(color, w)}"></div>'
                f'<div class="val">{num(r["seconds"])}s<span class="{cls}">{score}</span></div></div>'
            )
        out.append("</div>")
    height = head(data["subtitle"]) + 30 + (-(-len(tools) // 2)) * 30 + (-(-len(tools) // 2) - 1) * 14 + len(tasks) * (34 + 40 + len(tools) * 46) + 56
    return name, page(data["title"], data["subtitle"], "\n".join(out), height)


def bars_chart(section, name, unit="s"):
    data = DATA[section]
    rows = data["rows"]
    longest = max(r["value"] for r in rows)
    span = 460
    out = ['<div style="margin-top:26px">']
    for r in rows:
        color = RAINBOW if r.get("highlight") else "rgba(138,155,255,.55)"
        note = f'<span>{esc(r["note"])}</span>' if r.get("note") else ""
        out.append(
            f'<div class="wide"><div class="what"><b>{esc(r["label"])}</b>{note}</div>'
            f'<div class="bar" style="{bar_style(color, max(6, span * r["value"] / longest))}"></div>'
            f'<div class="val">{num(r["value"])}{unit}</div></div>'
        )
    out.append("</div>")
    height = head(data["subtitle"]) + 26 + len(rows) * 84 + 56
    return name, page(data["title"], data["subtitle"], "\n".join(out), height)


def pairs_chart(section, name, shared_scale):
    data = DATA[section]
    rows = data["rows"]
    span = 560
    out = [
        '<div class="key"><span><i style="background:rgba(138,155,255,.55)"></i>before</span>'
        f'<span><i style="background:{RAINBOW}"></i>now</span></div>'
    ]
    biggest = max(max(r["before"], r["after"]) for r in rows)
    for r in rows:
        unit = r.get("unit", "s")
        top = biggest if shared_scale else max(r["before"], r["after"])
        label = r["label"]
        out.append(f'<div class="group" style="margin-top:22px"><h2 style="height:36px;font-size:26px">{esc(label)}</h2>')
        for which, color, text in (("before", "rgba(138,155,255,.55)", "before"), ("after", RAINBOW, "now")):
            v = r[which]
            w = max(5, span * v / top) if top else 5
            out.append(
                f'<div class="row pair"><div class="name">{text}</div>'
                f'<div class="bar" style="{bar_style(color, w)}"></div>'
                f'<div class="val">{num(v)}{unit}</div></div>'
            )
        out.append("</div>")
    height = head(data["subtitle"]) + 22 + 26 + len(rows) * (22 + 36 + 92) + 56
    return name, page(data["title"], data["subtitle"], "\n".join(out), height)


def pixel_grid(rows, size, color_at, ident):
    cells = []
    cols = len(rows[0])
    for y, row in enumerate(rows):
        for x, ch in enumerate(row):
            if ch == "#":
                cells.append(f'<i style="background:{color_at(x, y)}"></i>')
            else:
                cells.append("<i></i>")
    return f'<div id="{ident}" class="px" style="grid-template-columns:repeat({cols},{size}px);grid-auto-rows:{size}px">{"".join(cells)}</div>'


def blend(a, b, f):
    pa = [int(a[i:i + 2], 16) for i in (1, 3, 5)]
    pb = [int(b[i:i + 2], 16) for i in (1, 3, 5)]
    return "#" + "".join(f"{round(x + (y - x) * f):02x}" for x, y in zip(pa, pb))


def spectrum_at(t):
    t = max(0, min(1, t)) * (len(SPECTRUM) - 1)
    i = min(len(SPECTRUM) - 2, int(t))
    return blend(SPECTRUM[i], SPECTRUM[i + 1], t - i)


def social():
    t = THEMES["dark"]
    ink = t["ink"]
    size, step = 20, 22

    def shard(x, y):
        return blend(blend(spectrum_at(x / 12), "#ffffff", max(0.0, 0.5 - y * 0.075)), "#2a2140", ink)

    shard_left, top = 150, 130
    word_left = shard_left + 13 * step + 56
    shard_html = pixel_grid(SHARD, size, shard, "shard")
    word_html = pixel_grid(LOGO, size, lambda x, y: blend(spectrum_at(x / 28), "#2a2140", ink), "word")
    eye = 4 * step
    chips = "".join(f"<span>{esc(c)}</span>" for c in ("free models", "no account", "terminal + browser", "runs in Docker"))
    css = themed_css(t, 1280, 640) + f"""
body {{ border: 0; border-radius: 0; }}
#shard {{ left: {shard_left}px; top: {top}px; }}
#shard::before, #shard::after {{ content: ''; position: absolute; width: {size}px; height: {size}px; background: {t["eye"]}; top: {eye}px; }}
#shard::before {{ left: {eye}px; }} #shard::after {{ left: {2 * eye}px; }}
#word {{ left: {word_left}px; top: {top + 2 * step}px; filter: drop-shadow(0 0 16px rgba(198,155,255,.35)); }}
.beam {{ position: absolute; left: 0; top: {top + 4 * step + 9}px; width: {shard_left + 2 * step - 4}px; height: 8px; border-radius: 4px;
  background: linear-gradient(90deg, rgba(255,255,255,0), #fff); }}
.tag {{ position: absolute; left: 0; right: 0; top: 386px; text-align: center; font-size: 54px; font-weight: 800; letter-spacing: -.02em; }}
.chips {{ position: absolute; left: 0; right: 0; top: 486px; display: flex; justify-content: center; gap: 16px; }}
.chips span {{ padding: 10px 22px; border-radius: 999px; border: 2px solid {t["line"]}; background: {t["chip"]}; font-size: 24px; color: {t["muted"]}; }}
"""
    doc = f"""<!doctype html>
<html lang="en"><meta charset="utf-8">
<title>Prism</title>
<style>{css}</style>
<body>
<div class="beam"></div>
{shard_html}
{word_html}
<div class="tag">Free AI that codes, beautifully.</div>
<div class="chips">{chips}</div>
</body></html>
"""
    return "social.html", doc, 640


THEMES = {
    "dark": {"bg": "#0b0b10", "glow": "#1b1833", "fg": "#ece9f6", "muted": "#b4b0c8", "line": "rgba(236,233,246,.10)", "chip": "rgba(236,233,246,.05)", "eye": "#0b0b10", "ink": 0.0},
    "light": {"bg": "#fbfaff", "glow": "#ece6ff", "fg": "#1d1a2e", "muted": "#5d5875", "line": "rgba(29,26,46,.12)", "chip": "rgba(29,26,46,.04)", "eye": "#1d1a2e", "ink": 0.1},
}
LOGO = ["####..####..#####..####.#...#", "#...#.#...#...#...#.....##.##", "####..####....#....###..#.#.#", "#.....#..#....#.......#.#...#", "#.....#...#.#####.####..#...#"]
SHARD = ["......#......", ".....###.....", "....#####....", "...#######...", "..#########..", ".###########.", "#############"]


def themed_css(t, width, height):
    return f"""
* {{ box-sizing: border-box; margin: 0; }}
html {{ background: transparent; }}
body {{ position: relative; width: {width}px; height: {height}px; overflow: hidden; border-radius: 28px; border: 2px solid {t["line"]};
  background: radial-gradient(900px 420px at 50% 0%, {t["glow"]}, {t["bg"]} 75%); color: {t["fg"]};
  font: 500 24px/1.2 'JetBrains Mono', 'JetBrainsMono Nerd Font', 'JetBrainsMono NF', ui-monospace, monospace; }}
.px {{ display: grid; gap: 2px; position: absolute; }}
.px i {{ display: block; }}
.muted {{ color: {t["muted"]}; }}
"""


def hero(theme):
    t = THEMES[theme]
    ink = t["ink"]
    size, step = 20, 22

    def shard(x, y):
        return blend(blend(spectrum_at(x / 12), "#ffffff", max(0.0, 0.5 - y * 0.075)), "#2a2140", ink)

    shard_left, word_left, top = 152, 152 + 13 * step + 56, 70
    shard_html = pixel_grid(SHARD, size, shard, "shard")
    word_html = pixel_grid(LOGO, size, lambda x, y: blend(spectrum_at(x / 28), "#2a2140", ink), "word")
    eye = 4 * step
    chips = "".join(f"<span>{esc(c)}</span>" for c in ("free models", "no account", "terminal + browser", "runs in Docker"))
    css = themed_css(t, WIDTH, 420) + f"""
#shard {{ left: {shard_left}px; top: {top}px; }}
#shard::before, #shard::after {{ content: ''; position: absolute; width: {size}px; height: {size}px; background: {t["eye"]}; top: {eye}px; }}
#shard::before {{ left: {eye}px; }} #shard::after {{ left: {2 * eye}px; }}
#word {{ left: {word_left}px; top: {top + 2 * step}px; filter: drop-shadow(0 0 14px rgba(198,155,255,{.35 if theme == "dark" else .18})); }}
.beam {{ position: absolute; left: 0; top: {top + 4 * step + 7}px; width: {shard_left + 2 * step - 4}px; height: 6px; border-radius: 3px;
  background: linear-gradient(90deg, rgba(255,255,255,0), {"#fff" if theme == "dark" else "#8a7fb0"}); }}
.tag {{ position: absolute; left: 0; right: 0; top: 262px; text-align: center; font-size: 40px; font-weight: 800; letter-spacing: -.02em; }}
.chips {{ position: absolute; left: 0; right: 0; top: 334px; display: flex; justify-content: center; gap: 14px; }}
.chips span {{ padding: 8px 18px; border-radius: 999px; border: 2px solid {t["line"]}; background: {t["chip"]}; font-size: 20px; color: {t["muted"]}; }}
"""
    doc = f"""<!doctype html>
<html lang="en"><meta charset="utf-8">
<title>Prism</title>
<style>{css}</style>
<body>
<div class="beam"></div>
{shard_html}
{word_html}
<div class="tag">Free AI that codes, beautifully.</div>
<div class="chips">{chips}</div>
</body></html>
"""
    return f"hero-{theme}.html", doc, 420


def summary(theme):
    t = THEMES[theme]
    rows = DATA["harness"]["rows"]
    agents = []
    for h in ("prism", "opencode", "aider", "mini-swe-agent"):
        mine = [r for r in rows if r["harness"] == h and r.get("pass") is not None]
        passed = sum(r.get("passed", int(bool(r["pass"]))) for r in mine)
        total = sum(r.get("runs", 1) for r in mine)
        agents.append((h, passed, total))
    left = []
    for h, passed, total in agents:
        color = TOOLS[h]
        solid = blend(color, "#2a2140", t["ink"] * 3) if color.startswith("#") else t["fg"]
        left.append(
            f'<div class="agent"><b style="color:{solid}">{NAMES[h]}</b>'
            f'<div class="track"><div style="width:{100 * passed / total:.1f}%;background:{color}"></div></div>'
            f'<span>{passed}/{total}</span></div>'
        )
    light = {r["label"]: r for r in DATA["light"]["rows"]}
    chat = {r["label"]: r for r in DATA["chat"]["rows"]}
    site = [r["value"] for r in DATA["website"]["rows"]]
    wins = (
        ("open Prism", light["prism to first screen"]["before"], light["prism to first screen"]["after"]),
        ("answer “hi”", chat["hi"]["before"], chat["hi"]["after"]),
        ("build a website", round(sum(site[:2]) / 2), round(sum(site[2:]) / 2)),
    )
    right = []
    for what, before, after in wins:
        right.append(
            f'<div class="win"><span class="muted">{esc(what)}</span>'
            f'<div><s class="muted">{num(before)}s</s><b>{num(after)}s</b><em>{before / after:.0f}× faster</em></div></div>'
        )
    css = themed_css(t, WIDTH, 404) + f"""
body {{ padding: 48px 64px; display: grid; grid-template-columns: 1fr 1fr; gap: 64px; }}
h2 {{ font-size: 28px; font-weight: 800; }}
p.muted {{ margin-top: 8px; font-size: 18px; line-height: 26px; }}
.rule {{ margin: 18px 0 24px; width: 120px; height: 5px; border-radius: 3px; background: {RAINBOW}; }}
.agent {{ display: grid; grid-template-columns: 200px 1fr 70px; align-items: center; gap: 16px; height: 46px; font-size: 21px; }}
.agent span {{ text-align: right; font-weight: 700; }}
.track {{ height: 14px; border-radius: 7px; background: {t["chip"]}; border: 1px solid {t["line"]}; overflow: hidden; }}
.track div {{ height: 100%; border-radius: 7px; }}
.win {{ display: flex; justify-content: space-between; align-items: baseline; height: 62px; white-space: nowrap; border-bottom: 2px solid {t["line"]}; font-size: 21px; }}
.win:last-child {{ border: 0; }}
.win div {{ display: grid; grid-template-columns: 84px 104px auto; justify-items: end; align-items: baseline; gap: 14px; }}
.win s {{ font-size: 19px; }}
.win b {{ font-size: 30px; font-weight: 800; }}
.win em {{ font-style: normal; font-size: 17px; font-weight: 700; padding: 3px 10px; border-radius: 999px; background: {RAINBOW}; color: #14121f; }}
"""
    doc = f"""<!doctype html>
<html lang="en"><meta charset="utf-8">
<title>Prism benchmarks</title>
<style>{css}</style>
<body>
<section><h2>Passes like the best</h2><p class="muted">15 runs per agent on the same free models, each one checked by a script.</p><div class="rule"></div>{"".join(left)}</section>
<section><h2>Faster than before</h2><p class="muted">Same jobs, same computer, before and after the speed work.</p><div class="rule"></div>{"".join(right)}</section>
</body></html>
"""
    return f"bench-summary-{theme}.html", doc, 404


def build():
    PAGES.mkdir(parents=True, exist_ok=True)
    pages = {}

    def add(file, doc, height=None):
        pages[file] = (doc, height)

    for file, doc in (
        harness_chart("harness", ["prism", "opencode", "aider", "mini-swe-agent"], "bench-harness.html"),
        harness_chart("compare", ["prism", "aider"], "bench-pinned.html"),
        bars_chart("coding", "bench-coding.html"),
        bars_chart("website", "bench-website.html"),
        pairs_chart("chat", "bench-chat.html", True),
        pairs_chart("light", "bench-light.html", False),
    ):
        add(file, doc)
    file, doc, height = social()
    add(file, doc, height)
    import cards
    for theme in THEMES:
        for make in (hero, summary, *cards.MAKERS):
            file, doc, height = make(theme)
            add(file, doc, height)
    for file, (doc, _) in pages.items():
        (PAGES / file).write_text(doc)
    return pages


def page_height(doc):
    marker = "--h:"
    i = doc.find(marker)
    return int(doc[i + len(marker): doc.find("px", i)]) if i >= 0 else 640


def screenshot(file, height):
    browser = next((shutil.which(b) for b in ("chromium", "chromium-browser", "google-chrome", "google-chrome-stable") if shutil.which(b)), None)
    if not browser:
        sys.exit("no Chromium found: run with --html-only, or install chromium")
    png = DOCS / (pathlib.Path(file).stem + ".png")
    subprocess.run(
        [browser, "--headless=new", "--disable-gpu", "--hide-scrollbars", "--force-device-scale-factor=2", "--default-background-color=00000000",
         f"--window-size={WIDTH},{height}", f"--screenshot={png}", (PAGES / file).as_uri()],
        check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    print(f"docs/{png.name}")


if __name__ == "__main__":
    pages = build()
    print(f"wrote {len(pages)} pages in docs/graphics/")
    if "--html-only" not in sys.argv:
        for file, (doc, height) in pages.items():
            screenshot(file, height or page_height(doc))
