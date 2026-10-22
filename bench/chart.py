#!/usr/bin/env python3
"""Render the benchmark charts in docs/ from bench/history.json."""
import json
import pathlib

ROOT = pathlib.Path(__file__).resolve().parent.parent
DATA = json.loads((ROOT / "bench" / "history.json").read_text())
SPECTRUM = ["#ff5f6d", "#ffa25f", "#f9e27d", "#7ee2a8", "#6fd3ff", "#8a9bff", "#c69bff"]
BG, PANEL, FG, MUTED, FAINT = "#0b0b10", "#14141c", "#e8e6f0", "#6e6a86", "#1d1c29"
FONT = "ui-monospace, 'JetBrains Mono', 'SFMono-Regular', Menlo, monospace"


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
    out = []
    for i, r in enumerate(rows):
        y = top + i * row_h
        w = max(4, span * r["value"] / longest)
        color = SPECTRUM[min(i, len(SPECTRUM) - 1)] if not r.get("highlight") else "url(#rainbow)"
        opacity = "1" if r.get("highlight") else "0.85"
        out.append(f'<text x="32" y="{y + 20}" fill="{FG if r.get("highlight") else MUTED}" font-size="14"{" font-weight=\"700\"" if r.get("highlight") else ""}>{esc(r["label"])}</text>')
        if r.get("note"):
            out.append(f'<text x="32" y="{y + 37}" fill="{MUTED}" font-size="11" opacity="0.8">{esc(r["note"])}</text>')
        out.append(f'<rect x="{label_width}" y="{y + 6}" width="{span}" height="20" rx="4" fill="{PANEL}"/>')
        out.append(f'<rect x="{label_width}" y="{y + 6}" width="{w:.1f}" height="20" rx="4" fill="{color}" opacity="{opacity}"/>')
        value = f'{r["value"]:g}{unit}'
        out.append(f'<text x="{label_width + span + 14}" y="{y + 21}" fill="{FG}" font-size="14" font-weight="700">{esc(value)}</text>')
    (ROOT / "docs" / name).write_text(frame(width, height, title, subtitle, "\n".join(out)))


def pairs(name, title, subtitle, rows):
    width, top, row_h, label_width = 900, 110, 52, 300
    height = top + row_h * len(rows) + 40
    longest = max(max(r["before"], r["after"]) for r in rows)
    span = width - label_width - 150
    out = [
        f'<rect x="{label_width}" y="{top - 24}" width="12" height="12" rx="2" fill="{MUTED}" opacity="0.6"/><text x="{label_width + 18}" y="{top - 14}" fill="{MUTED}" font-size="12">before</text>',
        f'<rect x="{label_width + 90}" y="{top - 24}" width="12" height="12" rx="2" fill="url(#rainbow)"/><text x="{label_width + 108}" y="{top - 14}" fill="{MUTED}" font-size="12">now</text>',
    ]
    for i, r in enumerate(rows):
        y = top + i * row_h
        out.append(f'<text x="32" y="{y + 22}" fill="{FG}" font-size="14">{esc(r["label"])}</text>')
        for k, (key, fill, opacity) in enumerate([("before", MUTED, "0.45"), ("after", "url(#rainbow)", "1")]):
            w = max(3, span * r[key] / longest)
            by = y + 6 + k * 20
            out.append(f'<rect x="{label_width}" y="{by}" width="{w:.1f}" height="14" rx="3" fill="{fill}" opacity="{opacity}"/>')
            out.append(f'<text x="{label_width + w + 10:.1f}" y="{by + 12}" fill="{FG if key == "after" else MUTED}" font-size="12"{" font-weight=\"700\"" if key == "after" else ""}>{r[key]:g}s</text>')
    (ROOT / "docs" / name).write_text(frame(width, height, title, subtitle, "\n".join(out)))


bars("bench-coding.svg", DATA["coding"]["title"], DATA["coding"]["subtitle"], DATA["coding"]["rows"])
pairs("bench-chat.svg", DATA["chat"]["title"], DATA["chat"]["subtitle"], DATA["chat"]["rows"])
bars("bench-website.svg", DATA["website"]["title"], DATA["website"]["subtitle"], DATA["website"]["rows"])
print("wrote docs/bench-coding.svg docs/bench-chat.svg docs/bench-website.svg")
