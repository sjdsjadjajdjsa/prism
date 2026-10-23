#!/usr/bin/env python3
"""Render the benchmark charts in docs/ from bench/history.json."""
import json
import pathlib

from PIL import Image, ImageDraw, ImageFont

ROOT = pathlib.Path(__file__).resolve().parent.parent
DATA = json.loads((ROOT / "bench" / "history.json").read_text())
SPECTRUM = ["#ff5f6d", "#ffa25f", "#f9e27d", "#7ee2a8", "#6fd3ff", "#8a9bff", "#c69bff"]
BG, PANEL, FG, MUTED, FAINT = "#0b0b10", "#14141c", "#e8e6f0", "#6e6a86", "#1d1c29"
FONT = "ui-monospace, 'JetBrains Mono', 'SFMono-Regular', Menlo, monospace"
SCALE = 2

FONT_FILES = {
    False: ["JetBrainsMonoNerdFont-Regular.ttf", "JetBrainsMono-Regular.ttf", "DejaVuSansMono.ttf"],
    True: ["JetBrainsMonoNerdFont-Bold.ttf", "JetBrainsMono-Bold.ttf", "DejaVuSansMono-Bold.ttf"],
}
FONT_DIRS = [
    pathlib.Path.home() / ".local/share/fonts",
    pathlib.Path("/usr/share/fonts/truetype/dejavu"),
    pathlib.Path("/usr/share/fonts/truetype/jetbrains-mono"),
    pathlib.Path("/usr/share/fonts/opentype/jetbrains-mono"),
]


def font_path(bold):
    for directory in FONT_DIRS:
        for name in FONT_FILES[bold]:
            candidate = directory / name
            if candidate.exists():
                return candidate
    raise SystemExit("no monospace font found")


def font(size, bold=False):
    return ImageFont.truetype(str(font_path(bold)), size * SCALE)


def rgb(value):
    value = value.lstrip("#")
    return tuple(int(value[i:i + 2], 16) for i in (0, 2, 4))


def mix(color, base, alpha):
    a, b = rgb(color), rgb(base)
    return tuple(round(a[i] * alpha + b[i] * (1 - alpha)) for i in range(3))


def gradient(width, height):
    img = Image.new("RGB", (width, height))
    draw = ImageDraw.Draw(img)
    last = len(SPECTRUM) - 1
    for x in range(width):
        t = x / max(1, width - 1) * last
        i = min(int(t), last - 1)
        f = t - i
        a, b = rgb(SPECTRUM[i]), rgb(SPECTRUM[i + 1])
        draw.line([(x, 0), (x, height)], fill=tuple(round(a[k] + (b[k] - a[k]) * f) for k in range(3)))
    return img


def paint_gradient(img, x, y, width, height, radius):
    width, height = max(1, width), max(1, height)
    mask = Image.new("L", (width, height), 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, width - 1, height - 1], radius=radius, fill=255)
    img.paste(gradient(width, height), (x, y), mask)


def canvas(width, height, title, subtitle):
    img = Image.new("RGB", (width * SCALE, height * SCALE), rgb(BG))
    draw = ImageDraw.Draw(img)
    draw.rounded_rectangle(
        [0, 0, width * SCALE - 1, height * SCALE - 1],
        radius=14 * SCALE,
        outline=rgb(FAINT),
        width=SCALE,
    )
    draw.text((32 * SCALE, 44 * SCALE), title, font=font(20, True), fill=rgb(FG), anchor="ls")
    draw.text((32 * SCALE, 68 * SCALE), subtitle, font=font(13), fill=rgb(MUTED), anchor="ls")
    paint_gradient(img, 32 * SCALE, 80 * SCALE, 56 * SCALE, 3 * SCALE, 2 * SCALE)
    return img, draw


def save(img, name):
    img.save(ROOT / "docs" / name.replace(".svg", ".png"), optimize=True)


def esc(s):
    return str(s).replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def frame(width, height, title, subtitle, body):
    stops = "".join(f'<stop offset="{i / (len(SPECTRUM) - 1):.3f}" stop-color="{c}"/>' for i, c in enumerate(SPECTRUM))
    return f"""<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}" font-family="{FONT}">
<defs><linearGradient id="rainbow" x1="0" x2="1" y1="0" y2="0">{stops}</linearGradient></defs>
<rect width="{width}" height="{height}" rx="14" fill="{BG}"/>
<rect x="0.5" y="0.5" width="{width - 1}" height="{height - 1}" rx="14" fill="none" stroke="{FAINT}"/>
<text x="32" y="44" fill="{FG}" font-size="20" font-weight="700">{esc(title)}</text>
<text x="32" y="68" fill="{MUTED}" font-size="13">{esc(subtitle)}</text>
<rect x="32" y="80" width="56" height="3" rx="1.5" fill="url(#rainbow)"/>
{body}
</svg>
"""


def bars(name, title, subtitle, rows, unit="s", label_width=230):
    width, top, row_h = 900, 110, 46
    height = top + row_h * len(rows) + 30
    longest = max(r["value"] for r in rows)
    span = width - label_width - 140

    img, draw = canvas(width, height, title, subtitle)
    out = []
    for i, r in enumerate(rows):
        y = top + i * row_h
        w = max(4, span * r["value"] / longest)
        highlight = bool(r.get("highlight"))
        color = SPECTRUM[min(i, len(SPECTRUM) - 1)]
        opacity = "1" if highlight else "0.85"

        draw.text((32 * SCALE, (y + 20) * SCALE), r["label"], font=font(14, highlight),
                  fill=rgb(FG if highlight else MUTED), anchor="ls")
        if r.get("note"):
            draw.text((32 * SCALE, (y + 37) * SCALE), r["note"], font=font(11), fill=rgb(MUTED), anchor="ls")
        draw.rounded_rectangle([label_width * SCALE, (y + 6) * SCALE, (label_width + span) * SCALE, (y + 26) * SCALE],
                               radius=4 * SCALE, fill=rgb(PANEL))
        if highlight:
            paint_gradient(img, label_width * SCALE, (y + 6) * SCALE, round(w * SCALE), 20 * SCALE, 4 * SCALE)
        else:
            draw.rounded_rectangle([label_width * SCALE, (y + 6) * SCALE, (label_width + w) * SCALE, (y + 26) * SCALE],
                                   radius=4 * SCALE, fill=mix(color, BG, 0.85))
        draw.text(((label_width + span + 14) * SCALE, (y + 21) * SCALE), f'{r["value"]:g}{unit}',
                  font=font(14, True), fill=rgb(FG), anchor="ls")

        out.append(f'<text x="32" y="{y + 20}" fill="{FG if highlight else MUTED}" font-size="14"{" font-weight=\"700\"" if highlight else ""}>{esc(r["label"])}</text>')
        if r.get("note"):
            out.append(f'<text x="32" y="{y + 37}" fill="{MUTED}" font-size="11" opacity="0.8">{esc(r["note"])}</text>')
        out.append(f'<rect x="{label_width}" y="{y + 6}" width="{span}" height="20" rx="4" fill="{PANEL}"/>')
        out.append(f'<rect x="{label_width}" y="{y + 6}" width="{w:.1f}" height="20" rx="4" fill="{"url(#rainbow)" if highlight else color}" opacity="{opacity}"/>')
        value = f'{r["value"]:g}{unit}'
        out.append(f'<text x="{label_width + span + 14}" y="{y + 21}" fill="{FG}" font-size="14" font-weight="700">{esc(value)}</text>')

    save(img, name)
    (ROOT / "docs" / name).write_text(frame(width, height, title, subtitle, "\n".join(out)))


def pairs(name, title, subtitle, rows):
    width, top, row_h, label_width = 900, 110, 52, 300
    height = top + row_h * len(rows) + 40
    own_scale = any("unit" in r for r in rows)
    longest = max(max(r["before"], r["after"]) for r in rows)
    span = width - label_width - 150

    img, draw = canvas(width, height, title, subtitle)
    draw.rounded_rectangle([label_width * SCALE, (top - 24) * SCALE, (label_width + 12) * SCALE, (top - 12) * SCALE],
                           radius=2 * SCALE, fill=mix(MUTED, BG, 0.6))
    draw.text(((label_width + 18) * SCALE, (top - 14) * SCALE), "before", font=font(12), fill=rgb(MUTED), anchor="ls")
    paint_gradient(img, (label_width + 90) * SCALE, (top - 24) * SCALE, 12 * SCALE, 12 * SCALE, 2 * SCALE)
    draw.text(((label_width + 108) * SCALE, (top - 14) * SCALE), "now", font=font(12), fill=rgb(MUTED), anchor="ls")

    out = [
        f'<rect x="{label_width}" y="{top - 24}" width="12" height="12" rx="2" fill="{MUTED}" opacity="0.6"/><text x="{label_width + 18}" y="{top - 14}" fill="{MUTED}" font-size="12">before</text>',
        f'<rect x="{label_width + 90}" y="{top - 24}" width="12" height="12" rx="2" fill="url(#rainbow)"/><text x="{label_width + 108}" y="{top - 14}" fill="{MUTED}" font-size="12">now</text>',
    ]
    for i, r in enumerate(rows):
        y = top + i * row_h
        draw.text((32 * SCALE, (y + 22) * SCALE), r["label"], font=font(14), fill=rgb(FG), anchor="ls")
        out.append(f'<text x="32" y="{y + 22}" fill="{FG}" font-size="14">{esc(r["label"])}</text>')
        unit = r.get("unit", "s")
        scale = max(r["before"], r["after"]) if own_scale else longest
        for k, (key, alpha) in enumerate([("before", 0.45), ("after", 1.0)]):
            w = max(3, span * r[key] / scale)
            by = y + 6 + k * 20
            if key == "after":
                paint_gradient(img, label_width * SCALE, by * SCALE, round(w * SCALE), 14 * SCALE, 3 * SCALE)
            else:
                draw.rounded_rectangle([label_width * SCALE, by * SCALE, (label_width + w) * SCALE, (by + 14) * SCALE],
                                       radius=3 * SCALE, fill=mix(MUTED, BG, alpha))
            draw.text(((label_width + w + 10) * SCALE, (by + 12) * SCALE), f'{r[key]:g}{unit}',
                      font=font(12, key == "after"), fill=rgb(FG if key == "after" else MUTED), anchor="ls")
            out.append(f'<rect x="{label_width}" y="{by}" width="{w:.1f}" height="14" rx="3" fill="{"url(#rainbow)" if key == "after" else MUTED}" opacity="{alpha}"/>')
            out.append(f'<text x="{label_width + w + 10:.1f}" y="{by + 12}" fill="{FG if key == "after" else MUTED}" font-size="12"{" font-weight=\"700\"" if key == "after" else ""}>{r[key]:g}{unit}</text>')

    save(img, name)
    (ROOT / "docs" / name).write_text(frame(width, height, title, subtitle, "\n".join(out)))


def verdict(mine, theirs):
    """Prism's result against one other harness on one task: more passes win, then the faster time wins.

    A run with no result (pass is None: the provider rate-limited it every try) is not counted.
    """
    if mine is None or theirs is None or mine["pass"] is None or theirs["pass"] is None:
        return "no run"
    a, b = mine.get("passed", int(mine["pass"])), theirs.get("passed", int(theirs["pass"]))
    if a != b:
        return "win" if a > b else "loss"
    if not a:
        return "tie"  # both failed: speed of a failure is not a win
    if mine["seconds"] == theirs["seconds"]:
        return "tie"
    return "win" if mine["seconds"] < theirs["seconds"] else "loss"


def grouped(name, title, subtitle, rows):
    """One group of bars per task, one bar per harness (seconds; failed runs dimmed).

    Each non-Prism row carries a verdict column (Prism wins / loses / tie on that task) and a
    scoreboard line sums them, so the chart shows where Prism loses, not just where it wins.
    """
    harnesses = list(dict.fromkeys(r["harness"] for r in rows))
    if len(harnesses) < 2:
        print(f"skipped docs/{name}: only {len(harnesses)} harness with results ({', '.join(harnesses) or 'none'}), need 2")
        return
    tasks = list(dict.fromkeys(r["task"] for r in rows))
    runs = {(r["harness"], r["task"]): r for r in rows}
    bar_h, gap = 14, 6
    others = [h for h in harnesses if h != "prism"] if "prism" in harnesses else []
    width, label_width = 900, 150
    legend_rows = (len(harnesses) + 1) // 2  # legend in two columns from y=115
    score_y = 115 + 20 * legend_rows + 6  # then one scoreboard line per opponent
    top = score_y + 16 * len(others) + 20
    group_h = len(harnesses) * (bar_h + gap) + 18
    height = top + group_h * len(tasks) + 24
    longest = max([r["seconds"] for r in rows] + [1])
    span = width - label_width - 360  # room for the longest label and the verdict column
    verdict_x = width - 32
    verdict_style = {"win": ("prism wins", SPECTRUM[3]), "loss": ("prism loses", SPECTRUM[0]),
                     "tie": ("tie", MUTED), "no run": ("no run", MUTED)}
    colors = {h: SPECTRUM[(4 + i) % len(SPECTRUM)] for i, h in enumerate(x for x in harnesses if x != "prism")}

    img, draw = canvas(width, height, title, subtitle)
    out = []

    def bar(h, x, y, w, alpha):
        if h == "prism":
            paint_gradient(img, x * SCALE, y * SCALE, round(w * SCALE), bar_h * SCALE, 3 * SCALE)
            if alpha < 1:  # dim a failed Prism bar by washing the gradient toward the background
                veil = Image.new("RGB", (round(w * SCALE), bar_h * SCALE), rgb(BG))
                region = (x * SCALE, y * SCALE, x * SCALE + round(w * SCALE), (y + bar_h) * SCALE)
                img.paste(Image.blend(img.crop(region), veil, 1 - alpha), region[:2])
        else:
            draw.rounded_rectangle([x * SCALE, y * SCALE, (x + w) * SCALE, (y + bar_h) * SCALE],
                                   radius=3 * SCALE, fill=mix(colors[h], BG, alpha))
        fill = "url(#rainbow)" if h == "prism" else colors[h]
        out.append(f'<rect x="{x}" y="{y}" width="{w:.1f}" height="{bar_h}" rx="3" fill="{fill}" opacity="{alpha}"/>')

    for i, h in enumerate(harnesses):
        lx, legend_y = 32 + (i % 2) * 430, 115 + (i // 2) * 20
        mine = [r for r in rows if r["harness"] == h]
        done = [r for r in mine if r["pass"] is not None]
        passed = sum(r.get("passed", int(bool(r["pass"]))) for r in done)
        legend = f'{h} · {passed}/{sum(r.get("runs", 1) for r in done)} pass · {round(sum(r["seconds"] for r in done), 1):g}s'
        bar(h, lx, legend_y - 11, 12, 1)
        draw.text(((lx + 18) * SCALE, legend_y * SCALE), legend, font=font(12, h == "prism"), fill=rgb(FG), anchor="ls")
        out.append(f'<text x="{lx + 18}" y="{legend_y}" fill="{FG}" font-size="12"{" font-weight=\"700\"" if h == "prism" else ""}>{esc(legend)}</text>')

    for j, h in enumerate(others):
        tally = [verdict(runs.get(("prism", t)), runs.get((h, t))) for t in tasks]
        score = (f'per task vs {h}: prism wins {tally.count("win")} · loses {tally.count("loss")}'
                 f' · ties {tally.count("tie")}  (more passes win, then the faster time)')
        sy = score_y + j * 16
        draw.text((32 * SCALE, sy * SCALE), score, font=font(12), fill=rgb(MUTED), anchor="ls")
        out.append(f'<text x="32" y="{sy}" fill="{MUTED}" font-size="12">{esc(score)}</text>')

    for i, t in enumerate(tasks):
        y0 = top + i * group_h
        draw.text((32 * SCALE, (y0 + bar_h) * SCALE), t, font=font(14), fill=rgb(FG), anchor="ls")
        out.append(f'<text x="32" y="{y0 + bar_h}" fill="{FG}" font-size="14">{esc(t)}</text>')
        for k, h in enumerate(harnesses):
            y = y0 + k * (bar_h + gap)
            r = runs.get((h, t))
            if r is None:
                label, w, color = "no run", 0, MUTED
            elif r["pass"] is None:
                label, w, color = f"{h} · rate-limited every try, no result", 0, MUTED
            else:
                w = max(3, span * r["seconds"] / longest)
                share = r.get("passed", int(r["pass"])) / r.get("runs", 1)
                bar(h, label_width, y, w, 0.35 + 0.65 * share)
                score = f'{r["passed"]}/{r["runs"]} pass' if "runs" in r else ("pass" if r["pass"] else "fail")
                label = f'{h} {r["seconds"]:g}s · {score}'
                color = FG if share == 1 else SPECTRUM[0]
            tx = label_width + w + 10
            draw.text((tx * SCALE, (y + 12) * SCALE), label, font=font(12, h == "prism"), fill=rgb(color), anchor="ls")
            out.append(f'<text x="{tx:.1f}" y="{y + 12}" fill="{color}" font-size="12"{" font-weight=\"700\"" if h == "prism" else ""}>{esc(label)}</text>')
            if h in others:
                text, vcolor = verdict_style[verdict(runs.get(("prism", t)), r)]
                draw.text((verdict_x * SCALE, (y + 12) * SCALE), text, font=font(12, True), fill=rgb(vcolor), anchor="rs")
                out.append(f'<text x="{verdict_x}" y="{y + 12}" fill="{vcolor}" font-size="12" font-weight="700" text-anchor="end">{esc(text)}</text>')

    save(img, name)
    (ROOT / "docs" / name).write_text(frame(width, height, title, subtitle, "\n".join(out)))
    print(f"wrote docs/{name} and .png")


bars("bench-coding.svg", DATA["coding"]["title"], DATA["coding"]["subtitle"], DATA["coding"]["rows"])
pairs("bench-chat.svg", DATA["chat"]["title"], DATA["chat"]["subtitle"], DATA["chat"]["rows"])
bars("bench-website.svg", DATA["website"]["title"], DATA["website"]["subtitle"], DATA["website"]["rows"])
print("wrote docs/bench-{coding,chat,website}.{svg,png}")
if "light" in DATA:
    pairs("bench-light.svg", DATA["light"]["title"], DATA["light"]["subtitle"], DATA["light"]["rows"])
    print("wrote docs/bench-light.{svg,png}")
for section, name in (("harness", "bench-harness.svg"), ("compare", "bench-pinned.svg")):
    if section in DATA:
        grouped(name, DATA[section]["title"], DATA[section]["subtitle"], DATA[section]["rows"])
    else:
        print(f"skipped docs/{name}: no {section} section in bench/history.json")
