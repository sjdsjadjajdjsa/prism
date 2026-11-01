"""The README cards: features, install, models and how it works, each in a dark and a light version.

bench/charts.py imports this and renders every card with the charts, so one command rebuilds them all.
"""
from charts import SHARD, THEMES, blend, esc, pixel_grid, spectrum_at, themed_css

WIDTH = 1280

ICONS = {
    "zap": "M13 2 3 14h9l-1 8 10-12h-9l1-8z",
    "login": "M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4M10 17l5-5-5-5M15 12H3",
    "check": "M22 11.08V12a10 10 0 1 1-5.93-9.14M22 4 12 14.01l-3-3",
    "shield": "M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10zM9 12l2 2 4-4",
    "box": "M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16zM3.27 6.96 12 12.01l8.73-5.05M12 22.08V12",
    "message": "M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2zM8 9h8M8 13h5",
    "globe": "M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20zM2 12h20M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z",
    "folder": "M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z",
    "star": "M12 2l3.09 6.26L22 9.27l-5 4.87 1.18 6.88L12 17.77l-6.18 3.25L7 14.14 2 9.27l6.91-1.01L12 2z",
    "users": "M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2M13 7a4 4 0 1 1-8 0 4 4 0 0 1 8 0zM23 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75",
    "plug": "M12 22v-5M9 8V2M15 8V2M18 8v5a6 6 0 0 1-12 0V8z",
    "bookmark": "M19 21l-7-5-7 5V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2z",
    "terminal": "M4 17l6-6-6-6M12 19h8",
    "key": "M15.5 7.5 19 4m2-2-2 2 3 3-3.5 3.5-3-3M11.39 11.61a5.5 5.5 0 1 1-7.78 7.78 5.5 5.5 0 0 1 7.78-7.78z",
    "gift": "M20 12v10H4V12M2 7h20v5H2zM12 22V7M12 7H7.5a2.5 2.5 0 0 1 0-5C11 2 12 7 12 7zM12 7h4.5a2.5 2.5 0 0 0 0-5C13 2 12 7 12 7z",
    "laptop": "M2 4h20v12H2zM8 20h8M12 16v4",
}

FEATURES = [
    ("zap", "Free by default", "Races free models and skips the busy ones"),
    ("login", "Or your own plan", "ChatGPT, Claude, Grok, Gemini, Copilot"),
    ("check", "Checks its own work", "Runs tests, clicks through pages it builds"),
    ("shield", "Asks first", "Before deleting, pushing or installing"),
    ("box", "Runs in Docker", "Only sees the folder you pick"),
    ("message", "Talk while it works", "Enter corrects, alt+enter queues, esc stops"),
    ("globe", "Terminal or browser", "The same chat in both, with /web"),
    ("folder", "Files and editor", "A tree, an editor, pictures and video"),
    ("star", "Constellation map", "See how every file links to the others"),
    ("users", "Helpers", "Splits a big job between several agents"),
    ("plug", "Skills and tools", "24 skills, your commands, hooks, MCP"),
    ("bookmark", "Remembers", "PRISM.md rules, memory, saved chats"),
]

PLATFORMS = [
    ("laptop", "Linux", "Ubuntu, Mint, Debian", "tested"),
    ("laptop", "Arch Linux", "Arch, CachyOS, Omarchy", "tested"),
    ("laptop", "Windows 11", "WSL with Docker Desktop", "tested"),
    ("laptop", "macOS", "Docker Desktop", "untested"),
]

MODELS = [
    ("gift", "Free, no key", "works the moment you start", [
        ("Kilo Gateway", "Nemotron 3 Ultra, 3.5 Lightning"),
        ("BlockRun", "Nemotron 3 Ultra, 3.5 Lightning"),
        ("LLM7", "Codestral"),
    ]),
    ("key", "Free key", "prism setup asks, no card", [
        ("NVIDIA", "Nemotron 3 Ultra, GLM-5.3 Flash"),
        ("Groq", "GPT-OSS 120B"),
        ("Google AI Studio", "Gemini Flash"),
        ("Mistral", "Codestral"),
        ("OpenRouter", "Gemma 4 31B and its free pick"),
        ("Z.ai", "GLM-4.7 Flash"),
        ("Ollama", "any model on your computer"),
    ]),
    ("login", "Your plan", "/login in your browser", [
        ("ChatGPT", "GPT-6.1 Sol, GPT-6 Luna and more"),
        ("Claude", "Sonnet 5.5, Opus 5.5, Haiku 5.5"),
        ("Grok", "Grok 4.7, 4.7 Build Fast, 4.6"),
        ("Antigravity", "Gemini 3.8 Flash, Gemini 4 Argon"),
        ("Copilot", "the models your plan includes"),
    ]),
]


def icon(name, color, size=26):
    return (f'<svg width="{size}" height="{size}" viewBox="0 0 24 24" fill="none" stroke="{color}" stroke-width="2" '
            f'stroke-linecap="round" stroke-linejoin="round"><path d="{ICONS[name]}"/></svg>')


def accent(t, i, n):
    return blend(spectrum_at(i / max(1, n - 1)), "#2a2140", t["ink"] * 3)


def tint(color, theme):
    r, g, b = (int(color[i:i + 2], 16) for i in (1, 3, 5))
    return f"rgba({r},{g},{b},{.14 if theme == 'dark' else .16})"


def card_css(t, height):
    return themed_css(t, WIDTH, height) + f"""
body {{ padding: 44px; }}
.tile {{ border-radius: 20px; border: 2px solid {t["line"]}; background: {t["chip"]}; }}
.ico {{ width: 52px; height: 52px; border-radius: 14px; display: flex; align-items: center; justify-content: center; flex: none; }}
.title {{ font-size: 24px; font-weight: 800; letter-spacing: -.01em; color: {t["fg"]}; }}
.text {{ font-size: 18px; line-height: 25px; color: {t["muted"]}; }}
.beam {{ position: absolute; left: 44px; right: 44px; bottom: 0; height: 4px; border-radius: 2px 2px 0 0;
  background: linear-gradient(90deg, #ff5f6d, #ffa25f, #f9e27d, #7ee2a8, #6fd3ff, #8a9bff, #c69bff); opacity: {.7 if t is THEMES["dark"] else .55}; }}
"""


def doc(title, css, body):
    return f"""<!doctype html>
<html lang="en"><meta charset="utf-8">
<title>{esc(title)}</title>
<style>{css}</style>
<body>
{body}
<div class="beam"></div>
</body></html>
"""


def features(theme):
    t = THEMES[theme]
    cols, rows = 3, 4
    height = 44 * 2 + rows * 184 + (rows - 1) * 16
    tiles = []
    for i, (name, title, text) in enumerate(FEATURES):
        r, c = divmod(i, cols)
        color = accent(t, r + c, cols + rows - 2)
        tiles.append(f'<div class="tile f"><div class="ico" style="background:{tint(color, theme)}">{icon(name, color)}</div>'
                     f'<div><div class="title">{esc(title)}</div><div class="text">{esc(text)}</div></div></div>')
    css = card_css(t, height) + """
.grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 16px; }
.f { height: 184px; padding: 24px; display: flex; flex-direction: column; gap: 18px; }
.f .title { margin-bottom: 6px; }
"""
    return f"features-{theme}.html", doc("Prism features", css, f'<div class="grid">{"".join(tiles)}</div>'), height


def platforms(theme):
    t = THEMES[theme]
    height = 44 * 2 + 200
    tiles = []
    for i, (name, title, text, status) in enumerate(PLATFORMS):
        color = accent(t, i, len(PLATFORMS))
        ok = status == "tested"
        mark = f'<span class="status"{"" if ok else " style=\"color:" + t["muted"] + "\""}><i style="background:{color if ok else t["line"]}"></i>{esc(status)}</span>'
        tiles.append(f'<div class="tile p"><div class="top"><div class="ico" style="background:{tint(color, theme)}">{icon(name, color)}</div>{mark}</div>'
                     f'<div><div class="title">{esc(title)}</div><div class="text">{esc(text)}</div></div></div>')
    css = card_css(t, height) + f"""
.grid {{ display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 16px; }}
.p {{ height: 200px; padding: 24px; display: flex; flex-direction: column; gap: 20px; }}
.top {{ display: flex; justify-content: space-between; align-items: flex-start; }}
.status {{ display: flex; align-items: center; gap: 8px; font-size: 16px; font-weight: 700; padding: 6px 12px; border-radius: 999px; border: 2px solid {t["line"]}; background: {t["chip"]}; }}
.status i {{ width: 10px; height: 10px; border-radius: 5px; }}
"""
    return f"platforms-{theme}.html", doc("Where Prism runs", css, f'<div class="grid">{"".join(tiles)}</div>'), height


def models(theme):
    t = THEMES[theme]
    height = 44 * 2 + 112 + 7 * 58 + 24 + 56
    cols = []
    for i, (name, title, text, rows) in enumerate(MODELS):
        color = accent(t, i * 3, 7)
        items = "".join(f'<div class="m"><b>{esc(s)}</b><span>{esc(m)}</span></div>' for s, m in rows)
        cols.append(f'<div class="tile col"><div class="head"><div class="ico" style="background:{tint(color, theme)}">{icon(name, color)}</div>'
                    f'<div><div class="title">{esc(title)}</div><div class="text">{esc(text)}</div></div></div>{items}</div>')
    css = card_css(t, height) + f"""
.grid {{ display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 16px; align-items: start; }}
.col {{ padding: 24px 24px 12px; height: {112 + 7 * 58 + 24}px; }}
.head {{ display: flex; gap: 16px; align-items: center; height: 88px; border-bottom: 2px solid {t["line"]}; margin-bottom: 8px; }}
.head .text {{ font-size: 16px; line-height: 22px; }}
.m {{ height: 58px; display: flex; flex-direction: column; justify-content: center; }}
.m b {{ font-size: 20px; font-weight: 700; }}
.m span {{ font-size: 16px; color: {t["muted"]}; margin-top: 3px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }}
.foot {{ margin-top: 20px; text-align: center; font-size: 18px; color: {t["muted"]}; }}
.foot b {{ color: {t["fg"]}; }}
"""
    body = f'<div class="grid">{"".join(cols)}</div><div class="foot">Pay per use instead? <b>/key</b> adds any of 23 services, from OpenAI and Anthropic to DeepSeek and Venice.</div>'
    return f"models-{theme}.html", doc("Prism models", css, body), height


def how(theme):
    t = THEMES[theme]
    height = 44 * 2 + 360
    ink = t["ink"]

    def shard(x, y):
        return blend(blend(spectrum_at(x / 12), "#ffffff", max(0.0, 0.5 - y * 0.075)), "#2a2140", ink)

    grid = pixel_grid(SHARD, 6, shard, "shard")
    outs = [(accent(t, 0, 7), "Free models", "skips busy ones"), (accent(t, 3, 7), "Your plan", "only when you pick it"), (accent(t, 6, 7), "Your keys", "added with /key")]
    right = "".join(f'<div class="tile out" style="top:{44 + i * 124}px"><i style="background:{c}"></i><div><div class="title">{esc(a)}</div><div class="text">{esc(b)}</div></div></div>' for i, (c, a, b) in enumerate(outs))
    beams = "".join(f'<path d="M786 224 C 840 224, 846 {104 + i * 124}, 900 {104 + i * 124}" stroke="{c}" stroke-width="5" fill="none" stroke-linecap="round"/>' for i, (c, _, _) in enumerate(outs))
    white = "#ffffff" if theme == "dark" else "#8a7fb0"
    css = card_css(t, height) + f"""
.you {{ position: absolute; left: 44px; width: 250px; height: 112px; padding: 20px 22px; display: flex; gap: 16px; align-items: center; }}
.mid {{ position: absolute; left: 380px; top: 44px; width: 360px; height: 332px; padding: 22px; border-style: dashed; }}
.mid .label {{ font-size: 16px; color: {t["muted"]}; letter-spacing: .04em; }}
.inner {{ position: absolute; left: 22px; right: 22px; height: 116px; padding: 18px 20px; display: flex; gap: 16px; align-items: center; }}
.out {{ position: absolute; left: 900px; width: 336px; height: 112px; padding: 20px 22px; display: flex; gap: 18px; align-items: center; }}
.out i {{ width: 14px; height: 60px; border-radius: 7px; flex: none; }}
svg.lines {{ position: absolute; left: 0; top: 0; }}
#shard {{ position: relative; flex: none; }}
"""
    you = "".join(f'<div class="tile you" style="top:{100 + i * 136}px"><div class="ico" style="background:{t["chip"]}">{icon(n, t["fg"])}</div><div><div class="title">{esc(a)}</div><div class="text">{esc(b)}</div></div></div>'
                  for i, (n, a, b) in enumerate([("terminal", "Terminal", "prism"), ("globe", "Browser", "/web")]))
    body = f"""<svg class="lines" width="{WIDTH}" height="{height}">
<path d="M294 156 C 340 156, 340 224, 404 224 M294 292 C 340 292, 340 224, 404 224" stroke="{white}" stroke-width="5" fill="none" stroke-linecap="round" opacity=".8"/>
{beams}</svg>
{you}
<div class="tile mid"><div class="label">DOCKER · ONLY YOUR FOLDER</div>
<div class="tile inner" style="top:56px">{grid}<div><div class="title">Prism</div><div class="text">edits, runs, checks</div></div></div>
<div class="tile inner" style="top:188px"><div class="ico" style="background:{t["chip"]}">{icon("zap", t["fg"])}</div><div><div class="title">Router</div><div class="text">races free models</div></div></div>
</div>
{right}"""
    return f"how-{theme}.html", doc("How Prism works", css, body), height


MAKERS = (features, platforms, models, how)
