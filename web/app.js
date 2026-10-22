// prism — browser front end. No deps, no build. Mirrors the terminal UI in cli/.

const $ = (id) => document.getElementById(id);
const params = new URLSearchParams(location.search);
const DEMO = params.has('demo');
const REDUCED = matchMedia('(prefers-reduced-motion: reduce)').matches;
const THEMES = ['prism', 'tokyo-night', 'catppuccin', 'rose-pine', 'matte-black', 'gruvbox', 'nord', 'everforest'];

// ---------- theme ----------
function cssVar(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}
function spectrum() {
  const out = [];
  for (let i = 0; i < 7; i++) out.push(hexToRgb(cssVar('--s' + i)));
  return out;
}
function hexToRgb(h) {
  h = h.replace('#', '');
  if (h.length !== 6) return [255, 255, 255];
  return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)];
}
function mix(a, b, t) {
  t = Math.max(0, Math.min(1, t));
  return [0, 1, 2].map((i) => Math.round(a[i] + (b[i] - a[i]) * t));
}
function rgbStr(c) { return `rgb(${c[0]},${c[1]},${c[2]})`; }
function specAt(spec, v) {
  v = v - Math.floor(v);
  const n = spec.length, p = v * n, i = Math.floor(p) % n;
  return mix(spec[i], spec[(i + 1) % n], p - Math.floor(p));
}
function ease(t) { t = Math.max(0, Math.min(1, t)); return t * t * (3 - 2 * t); }
function hash(x, y, s) {
  let h = (x * 374761393 + y * 668265263 + s * 2246822519) | 0;
  h = Math.imul(h ^ (h >>> 13), 1274126177);
  return ((h ^ (h >>> 16)) >>> 0) / 4294967296;
}

const palette = { spec: [], bg: [0, 0, 0], fg: [255, 255, 255], muted: [100, 100, 100] };
// hex-string mirror of the palette, the shape web/beam.js and web/shard.js expect
const themeCache = { name: 'prism', bg: '#0b0b10', fg: '#e8e6f0', muted: '#6e6a86', faint: '#1d1c29', spectrum: [] };
function setTheme(name) {
  if (!THEMES.includes(name)) return false;
  document.documentElement.dataset.theme = name;
  $('theme').value = name;
  try { localStorage.setItem('prism-theme', name); } catch { /* ignore */ }
  palette.spec = spectrum();
  palette.bg = hexToRgb(cssVar('--bg'));
  palette.fg = hexToRgb(cssVar('--fg'));
  palette.muted = hexToRgb(cssVar('--muted'));
  themeCache.name = name;
  themeCache.bg = cssVar('--bg') || themeCache.bg;
  themeCache.fg = cssVar('--fg') || themeCache.fg;
  themeCache.muted = cssVar('--muted') || themeCache.muted;
  themeCache.faint = cssVar('--faint') || themeCache.faint;
  themeCache.spectrum = [0, 1, 2, 3, 4, 5, 6].map((i) => cssVar('--s' + i));
  return true;
}

// ---------- DOM helpers (never innerHTML with model text) ----------
function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}
function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); }

const TOOL_COLOR = { read: '--s4', edit: '--s3', write: '--s3', bash: '--s6', search: '--s2', grep: '--s2', ls: '--s1', list: '--s1' };
function toolColor(tool) { return `var(${TOOL_COLOR[tool] || '--s5'})`; }

// ---------- transcript ----------
const transcript = $('transcript');
const liveEl = $('live');
const state = {
  busy: false, model: null, tokens: 0, window: 0, modelsReady: null,
  turn: null, steps: null, stepMap: new Map(), textBuf: '', textEl: null,
  reasoning: '', phase: 'idle', phaseAt: 0, draft: null, abort: null, route: null, waitMs: 0,
};

function hideHello() { const h = $('hello'); if (h) h.remove(); }

function newTurn() {
  const t = el('div', 'turn');
  transcript.appendChild(t);
  state.turn = t; state.steps = null; state.textEl = null; state.textBuf = ''; state.stepMap.clear(); flushText();
  return t;
}
function curTurn() { return state.turn || newTurn(); }

function addUser(text) {
  hideHello();
  const t = newTurn();
  t.appendChild(el('div', 'user', text));
}

function stepGroup() {
  const t = curTurn();
  if (!state.steps || state.steps.parentNode !== t) {
    flushText();
    state.steps = el('div', 'steps-group');
    state.steps.dataset.open = '1';
    t.appendChild(state.steps);
    state.textEl = null;
  }
  return state.steps;
}

function addStep({ id, tool, arg, ok, summary, running }) {
  const g = stepGroup();
  const s = el('div', 'step' + (running ? ' running' : '') + (ok === false ? ' failed' : ''));
  const d = el('span', 'dia', running ? '◇' : '◆');
  d.style.color = ok === false ? 'var(--red)' : toolColor(tool);
  s.appendChild(d);
  s.appendChild(el('span', 'tool', tool || 'tool'));
  s.appendChild(el('span', 'arg', arg || ''));
  s.appendChild(el('span', 'sum', summary || ''));
  g.appendChild(s);
  if (id != null) state.stepMap.set(String(id), s);
  return s;
}

function finishStep(ev) {
  let s = ev.id != null && state.stepMap.get(String(ev.id));
  if (ev.id != null) state.stepMap.delete(String(ev.id));
  if (!s) {
    const running = stepGroup().querySelectorAll('.step.running');
    s = running[running.length - 1];
  }
  if (!s) s = addStep({ tool: ev.tool, arg: ev.arg, ok: ev.ok, summary: ev.summary });
  s.classList.remove('running');
  s.querySelector('.dia').textContent = '◆';
  if (ev.ok === false) { s.classList.add('failed'); s.querySelector('.dia').style.color = 'var(--red)'; }
  if (ev.summary) s.querySelector('.sum').textContent = ev.summary;
  if (ev.diff && ev.diff.length) s.after(renderDiff(ev.diff));
  scrollDown();
}

function renderDiff(lines) {
  const box = el('div', 'diff');
  const max = 12;
  lines.slice(0, max).forEach((l) => {
    const kind = l.kind === '+' ? 'add' : l.kind === '-' ? 'del' : 'ctx';
    const row = el('div', 'ln ' + kind);
    row.appendChild(el('span', 'num', l.num != null ? String(l.num) : ''));
    row.appendChild(el('span', 'k', l.kind === ' ' ? '' : l.kind));
    row.appendChild(el('span', 'txt', l.text || ''));
    box.appendChild(row);
  });
  if (lines.length > max) box.appendChild(el('div', 'more', `… ${lines.length - max} more lines`));
  return box;
}

// Collapse older turns' step groups into one summary line.
function collapseGroup(g) {
  if (g.querySelector('.collapsed')) return;
  const steps = [...g.querySelectorAll('.step')];
  if (steps.length < 2) return;
  const tools = [...new Set(steps.map((s) => s.querySelector('.tool').textContent))];
  let add = 0, del = 0;
  g.querySelectorAll('.diff .ln.add').forEach(() => add++);
  g.querySelectorAll('.diff .ln.del').forEach(() => del++);
  const line = el('div', 'collapsed');
  line.appendChild(el('span', 'tri', '▸'));
  line.appendChild(el('span', '', `${steps.length} steps  ${tools.join(' · ')}`));
  if (add || del) {
    line.appendChild(el('span', '', '  '));
    line.appendChild(el('span', 'add', `+${add}`));
    line.appendChild(el('span', '', ' '));
    line.appendChild(el('span', 'del', `-${del}`));
  }
  line.addEventListener('click', () => { g.dataset.open = g.dataset.open === '1' ? '0' : '1'; });
  g.prepend(line);
  g.dataset.open = '0';
}
function collapseOldTurns() {
  const turns = transcript.querySelectorAll('.turn');
  turns.forEach((t, i) => { if (i < turns.length - 1) t.querySelectorAll('.steps-group').forEach(collapseGroup); });
}

// markdown re-render is coalesced to one per animation frame while streaming;
// textPending remembers the element + buffer so a later state.textEl = null can't drop the last render
let textRaf = 0, textPending = null;
function flushText() {
  if (textRaf) { cancelAnimationFrame(textRaf); textRaf = 0; }
  if (textPending) { renderMarkdown(textPending.el, textPending.buf); textPending = null; }
}
function addText(text) {
  const t = curTurn();
  if (!state.textEl) { flushText(); state.textEl = el('div', 'md'); t.appendChild(state.textEl); state.textBuf = ''; state.steps = null; }
  state.textBuf += text;
  textPending = { el: state.textEl, buf: state.textBuf };
  if (!textRaf) textRaf = requestAnimationFrame(() => { textRaf = 0; flushText(); });
}
function addNote(text) { curTurn().appendChild(el('div', 'note', text)); state.textEl = null; }
function addError(headline, detail) {
  const e = el('div', 'error');
  e.appendChild(el('div', 'head', headline || 'error'));
  if (detail) e.appendChild(el('div', 'detail', detail));
  curTurn().appendChild(e); state.textEl = null;
}
function addMeta({ model, seconds }) {
  const m = el('div', 'meta');
  const d = el('span', 'grad', '◆ ');
  m.appendChild(d);
  m.appendChild(el('span', 'model', model || state.model || 'auto'));
  m.appendChild(el('span', 'dot', '  ·  '));
  m.appendChild(el('span', '', seconds != null ? `${Number(seconds).toFixed(1)}s` : ''));
  m.appendChild(el('span', 'dot', '  ·  '));
  m.appendChild(el('span', 'free', 'free'));
  curTurn().appendChild(m); state.textEl = null;
}

function scrollDown() {
  if (document.hidden) return;
  const col = document.querySelector('.column');
  col.scrollTop = col.scrollHeight;
}

// ---------- markdown (DOM-built, escaped by construction) ----------
function renderMarkdown(root, src) {
  clear(root);
  const lines = src.split('\n');
  let i = 0, para = [];
  const flush = () => {
    if (!para.length) return;
    const p = el('p');
    inline(p, para.join('\n'));
    root.appendChild(p); para = [];
  };
  while (i < lines.length) {
    const ln = lines[i];
    const fence = ln.match(/^\s*```\s*(\w*)/);
    if (fence) {
      flush();
      const lang = fence[1]; const body = [];
      i++;
      while (i < lines.length && !/^\s*```/.test(lines[i])) body.push(lines[i++]);
      i++;
      const pre = el('pre');
      if (lang) pre.appendChild(el('span', 'lang', lang));
      const code = el('code');
      highlight(code, body.join('\n'), lang);
      pre.appendChild(code); root.appendChild(pre);
      continue;
    }
    const h = ln.match(/^(#{1,4})\s+(.*)/);
    if (h) { flush(); const e = el('h' + h[1].length); inline(e, h[2]); root.appendChild(e); i++; continue; }
    if (/^\s*(---|\*\*\*)\s*$/.test(ln)) { flush(); root.appendChild(el('hr')); i++; continue; }
    if (/^\s*[-*•]\s+/.test(ln)) {
      flush(); const ul = el('ul');
      while (i < lines.length && (/^\s*[-*•]\s+/.test(lines[i]) || (/^\s{2,}\S/.test(lines[i]) && ul.lastChild))) {
        const m = lines[i].match(/^\s*[-*•]\s+(.*)/);
        if (m) { const li = el('li'); inline(li, m[1]); ul.appendChild(li); }
        else { ul.lastChild.appendChild(document.createTextNode(' ')); inline(ul.lastChild, lines[i].trim()); }
        i++;
      }
      root.appendChild(ul); continue;
    }
    if (/^\s*\d+[.)]\s+/.test(ln)) {
      flush(); const ol = el('ol');
      while (i < lines.length && /^\s*\d+[.)]\s+/.test(lines[i])) {
        const li = el('li'); inline(li, lines[i].replace(/^\s*\d+[.)]\s+/, '')); ol.appendChild(li); i++;
      }
      root.appendChild(ol); continue;
    }
    if (/^\s*>\s?/.test(ln)) {
      flush(); const q = el('blockquote'); const buf = [];
      while (i < lines.length && /^\s*>\s?/.test(lines[i])) buf.push(lines[i++].replace(/^\s*>\s?/, ''));
      const p = el('p'); inline(p, buf.join('\n')); q.appendChild(p); root.appendChild(q); continue;
    }
    if (ln.trim() === '') { flush(); i++; continue; }
    para.push(ln); i++;
  }
  flush();
}

function inline(parent, text) {
  // tokens: `code`, **bold**, *em*, [text](url)
  const re = /(`[^`\n]+`)|(\*\*[^*\n]+\*\*)|((?<![\w*])\*(?=[A-Za-z])[^*\n]*?[^\s\d*]\*(?![\w*]))|(\[[^\]\n]+\]\((https?:\/\/[^)\s]+)\))/g;
  let last = 0, m;
  while ((m = re.exec(text))) {
    if (m.index > last) parent.appendChild(document.createTextNode(text.slice(last, m.index)));
    if (m[1]) parent.appendChild(el('code', '', m[1].slice(1, -1)));
    else if (m[2]) parent.appendChild(el('strong', '', m[2].slice(2, -2)));
    else if (m[3]) parent.appendChild(el('em', '', m[3].slice(1, -1)));
    else if (m[4]) { const a = el('a', '', m[4].slice(1, m[4].indexOf(']'))); a.href = m[5]; a.target = '_blank'; a.rel = 'noopener'; parent.appendChild(a); }
    last = re.lastIndex;
  }
  if (last < text.length) parent.appendChild(document.createTextNode(text.slice(last)));
}

const KEYWORDS = new Set(('function return if else for while const let var class new import export from default async await try catch throw ' +
  'switch case break continue def elif None True False in not and or is lambda pass with as yield struct type func package go range ' +
  'nil interface map chan defer select fn pub mut impl use match enum self static void int string bool null true false this extends').split(' '));
function highlight(code, src, lang) {
  const re = /(\/\/[^\n]*|#[^\n]*|\/\*[\s\S]*?\*\/)|("(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*'|`(?:[^`\\]|\\.)*`)|(\b\d+(?:\.\d+)?\b)|([A-Za-z_$][\w$]*)(?=\s*\()|(\b[A-Za-z_$][\w$]*\b)/g;
  let last = 0, m;
  while ((m = re.exec(src))) {
    if (m.index > last) code.appendChild(document.createTextNode(src.slice(last, m.index)));
    const s = m[0];
    if (m[1]) {
      if (s[0] === '#' && (lang === 'js' || lang === 'javascript' || lang === 'go' || lang === 'c')) code.appendChild(document.createTextNode(s));
      else code.appendChild(el('span', 'tok-c', s));
    } else if (m[2]) code.appendChild(el('span', 'tok-s', s));
    else if (m[3]) code.appendChild(el('span', 'tok-n', s));
    else if (m[4]) code.appendChild(el('span', KEYWORDS.has(s) ? 'tok-k' : 'tok-f', s));
    else if (m[5]) code.appendChild(KEYWORDS.has(s) ? el('span', 'tok-k', s) : document.createTextNode(s));
    last = re.lastIndex;
  }
  if (last < src.length) code.appendChild(document.createTextNode(src.slice(last)));
}

// ---------- live line + status ----------
const SPIN = ['◐', '◓', '◑', '◒'];
function setPhase(p) { state.phase = p; state.phaseAt = performance.now(); }
function renderLive(now) {
  clear(liveEl);
  if (!state.busy) return;
  const secs = Math.max(0, Math.floor((performance.now() - state.phaseAt) / 1000));
  const spin = el('span', 'spin', SPIN[Math.floor(now / 160) % SPIN.length]);
  spin.style.color = rgbStr(specAt(palette.spec, now / 3000));
  const line = el('div');
  line.appendChild(spin); line.appendChild(el('span', '', ' '));
  if (state.phase === 'draft' && state.draft) {
    line.appendChild(el('span', 'lbl', `writing `));
    line.appendChild(el('span', 'arg', state.draft.arg || state.draft.tool));
    line.appendChild(el('span', '', `  ${state.draft.chars} chars`));
  } else if (state.phase === 'tool') {
    line.appendChild(el('span', 'lbl', 'working'));
    line.appendChild(el('span', '', ` ${secs}s`));
  } else if (state.phase === 'text') {
    line.appendChild(el('span', 'lbl', 'writing'));
    line.appendChild(el('span', 'cursor'));
  } else if (secs > 4 && !state.reasoning && state.phase !== 'text') {
    line.appendChild(el('span', 'wait', 'waiting for a free model'));
    line.appendChild(el('span', '', ` ${secs}s`));
  } else {
    line.appendChild(el('span', 'lbl', 'thinking'));
    line.appendChild(el('span', '', ` ${secs}s`));
  }
  liveEl.appendChild(line);
  if (state.reasoning && state.phase === 'thinking') {
    const tail = state.reasoning.trimEnd().split('\n').slice(-3).join('\n');
    liveEl.appendChild(el('div', 'think', tail));
  }
}

function setBusy(b) {
  state.busy = b;
  if (b) buddy.busySince = performance.now();
  document.body.classList.toggle('busy', b);
  $('busyhint').hidden = !b;
  $('key-esc-label').textContent = b ? 'stop' : 'clear';
  if (!b) { clear(liveEl); state.draft = null; state.reasoning = ''; flushText(); }
}

function renderStatus() {
  $('route').textContent = state.model || state.route || 'auto';
  const pct = state.window ? Math.min(100, Math.round((state.tokens / state.window) * 100)) : 0;
  $('usage').textContent = pct + '%';
  const cells = $('meter').children;
  const on = Math.round((pct / 100) * cells.length);
  for (let i = 0; i < cells.length; i++) {
    cells[i].className = i < on ? 'on' + (pct > 85 ? ' max' : pct > 60 ? ' hi' : '') : '';
  }
  const m = $('models');
  if (state.modelsReady == null) m.textContent = '';
  else m.textContent = state.modelsReady > 0 ? `${state.modelsReady} free models ready` : 'no free models ready';
  m.style.color = state.modelsReady === 0 ? 'var(--red)' : '';
}

// ---------- shard buddy ----------
const SHARD = ['......#......', '.....###.....', '....#####....', '...#######...', '..#########..', '.###########.', '#############'];
const shardCv = $('shard');
const SHARD_W = 170, SHARD_H = 100; // room for '!', the wave hand and orbiting dots
const DPR = Math.max(1, Math.min(3, window.devicePixelRatio || 1));
shardCv.width = Math.round(SHARD_W * DPR); shardCv.height = Math.round(SHARD_H * DPR);
shardCv.style.width = SHARD_W + 'px'; shardCv.style.height = SHARD_H + 'px';
const shardCtx = shardCv.getContext('2d');
shardCtx.setTransform(DPR, 0, 0, DPR, 0, 0);
shardCtx.imageSmoothingEnabled = false;
const MONO = 'ui-monospace, Menlo, Consolas, monospace';
const buddy = { mood: 'idle', charge: 0, since: 0, moodUntil: 0, wink: 0, waveUntil: 0, lookAt: -1e9, busySince: 0 };
function inShard(ox, oy, px, sx, sy) {
  const gx = Math.floor((sx - ox) / px), gy = Math.floor((sy - oy) / px);
  return gy >= 0 && gy < SHARD.length && gx >= 0 && gx < SHARD[0].length && SHARD[gy][gx] === '#';
}
const BUBBLES = {
  thinking: 'refracting…', working: 'focusing the beam.', done: 'converged.', tests: 'green across the spectrum.',
  crack: 'hm. a crack in the lens.', fogged: 'all lenses fogged. give it a minute.', hello: 'light in. rainbow out.',
};
let bubbleTimer = 0;
let quiet = false; // /quiet
function say(key, ms = 3200) {
  if (quiet) return;
  const text = BUBBLES[key] || key;
  $('bubble-text').textContent = text; $('bubble').hidden = false;
  clearTimeout(bubbleTimer);
  if (ms > 0) bubbleTimer = setTimeout(() => { $('bubble').hidden = true; }, ms);
}
function hush() { clearTimeout(bubbleTimer); $('bubble').hidden = true; }
function setMood(m, ms = 0) { buddy.mood = m; buddy.since = performance.now(); buddy.moodUntil = ms ? buddy.since + ms : 0; }

function drawShard(now) {
  const n = now;
  if (buddy.moodUntil && n > buddy.moodUntil) setMood(state.busy ? 'working' : 'idle');
  const md = buddy.mood;
  const W = 13, H = 7, px = 10;
  const bob = (REDUCED ? 0 : Math.round(Math.sin(n / (md === 'working' ? 260 : 900)) * (md === 'working' ? 2 : 1.5)));
  shardCtx.clearRect(0, 0, SHARD_W, SHARD_H);
  const ox = Math.round((SHARD_W - W * px) / 2), oy = Math.round(SHARD_H - H * px - 6 + bob);
  const white = [255, 255, 255], bg = palette.bg;
  let glow = 0;
  if (md === 'working') glow = 0.18 + 0.12 * Math.sin(n / 140);
  else if (md === 'thinking') glow = 0.06;
  else if (md === 'proud') glow = 0.12 + 0.08 * Math.sin(n / 200);
  // charge rises over 20s while busy (cli/buddy.go pose), or with the size of a tool draft
  const charge = Math.max(buddy.charge, state.busy ? Math.min(1, (n - buddy.busySince) / 20000) : 0);
  glow += charge * (0.22 + 0.06 * Math.sin(n / 90));
  const dim = md === 'sleepy' ? 0.5 : 0;
  if (md === 'proud' && !REDUCED) {
    // extra halo behind the body
    const hx = ox + 6.5 * px, hy = oy + 3.5 * px, hr = 58 + 6 * Math.sin(n / 200);
    const halo = shardCtx.createRadialGradient(hx, hy, 4, hx, hy, hr);
    halo.addColorStop(0, rgbStr(mix(bg, specAt(palette.spec, n / 3000), 0.55)));
    halo.addColorStop(1, rgbStr(bg));
    shardCtx.globalAlpha = 0.45 + 0.15 * Math.sin(n / 200);
    shardCtx.fillStyle = halo; shardCtx.fillRect(hx - hr, hy - hr, hr * 2, hr * 2);
    shardCtx.globalAlpha = 1;
  }
  const speed = md === 'thinking' || md === 'working' ? 900 : 1700;
  const band = ((n / speed) % 1.8) * (W + H) - H;
  const center = 6;
  const blink = md === 'idle' || md === 'thinking' || md === 'working' ? (n % 4200) < 260 : false;
  for (let y = 0; y < H; y++) {
    const row = SHARD[y];
    for (let x = 0; x < W; x++) {
      if (row[x] !== '#') continue;
      const edge = y === H - 1 || x === 0 || row[x - 1] === '.' || x + 1 >= W || row[x + 1] === '.';
      const apex = y === 0;
      const base = specAt(palette.spec, (x / W) * 0.68 + (y / H) * 0.1 + Math.sin(n / 9000) * 0.04);
      let col;
      if (apex) col = mix(base, white, 0.75);
      else if (x === center) col = mix(base, white, 0.45);
      else if (x < center) col = mix(base, white, 0.22 + 0.04 * (center - x) / center);
      else col = mix(base, bg, 0.12 + 0.18 * (x - center) / center);
      if (edge && !apex) col = mix(col, white, 0.18);
      const beam = Math.exp(-Math.pow(x - y * 0.6 - band, 2) / 2.5);
      col = mix(mix(col, white, beam * 0.3), white, glow);
      col = mix(col, bg, dim);
      shardCtx.fillStyle = rgbStr(col);
      shardCtx.fillRect(ox + x * px, oy + y * px, px, px);
    }
  }
  // eyes only — exactly eyeGlyphs + drawShardSmall in cli/shard.go: dark pixels on row 4,
  // columns 4 and 8 shifted by look; no mouth; closed (nothing drawn) while blinking or sleepy.
  const drift = md === 'thinking' ? Math.round(Math.sin(n / 700)) : 0;
  const look = Math.max(-1, Math.min(1, drift + (n - buddy.lookAt < 700 ? -1 : 0))); // glance left at fresh text
  const eyesClosed = (md === 'sleepy' || blink) && md !== 'happy';
  if (!eyesClosed) {
    shardCtx.fillStyle = rgbStr(mix(bg, [0, 0, 0], 0.3));
    const eyeH = md === 'surprised' ? px * 2 : px; // '█' spans rows 4-5, '▀' is row 4 only
    for (const ex of [4 + look, 8 + look]) {
      shardCtx.fillRect(ox + ex * px, oy + 4 * px, px, eyeH);
    }
  }
  // decorations — mirrors drawShardSmall in cli/shard.go
  const cx = ox + 6.5 * px, cy = oy + 3 * px;
  shardCtx.textBaseline = 'middle'; shardCtx.textAlign = 'center';
  if (charge > 0.05 && !REDUCED) {
    // three dots orbiting the body, tighter as charge builds
    for (let i = 0; i < 3; i++) {
      const a = n / 260 + i * 2.1;
      const dx = cx + Math.cos(a) * (8 - charge * 3) * px * 0.9, dy = cy + Math.sin(a) * 1.6 * px * 1.3;
      if (inShard(ox, oy, px, dx, dy)) continue;
      shardCtx.fillStyle = rgbStr(mix(bg, specAt(palette.spec, i / 3 + n / 4000), 0.4 + 0.6 * charge));
      shardCtx.fillRect(Math.round(dx) - 2, Math.round(dy) - 2, 4, 4);
    }
  }
  if (md === 'surprised') {
    shardCtx.font = 'bold 18px ' + MONO;
    shardCtx.fillStyle = rgbStr(specAt(palette.spec, 0.05));
    shardCtx.fillText('!', ox + 12 * px, oy - 8);
  } else if (md === 'happy' || md === 'proud') {
    shardCtx.font = '12px ' + MONO;
    for (let i = 0; i < 5; i++) {
      if (hash(i, Math.floor(n / 180), 9) <= 0.3) continue;
      const a = (i / 5) * 2 * Math.PI + n / 700;
      const sx = cx + Math.cos(a) * 7.5 * px, sy = cy + Math.sin(a) * 2.6 * px;
      if (inShard(ox, oy, px, sx, sy)) continue;
      shardCtx.fillStyle = rgbStr(specAt(palette.spec, i / 5));
      shardCtx.fillText('✦·✧'[i % 3], sx, sy);
    }
  } else if (md === 'sleepy') {
    shardCtx.font = '12px ' + MONO;
    for (let i = 0; i < 3; i++) {
      if (i > (n / 900) % 3) continue;
      shardCtx.fillStyle = rgbStr(mix(bg, palette.muted, 0.9 - 0.2 * i));
      shardCtx.fillText('z', ox + (12.5 + i) * px, oy - 2 - i * 10);
    }
  }
  if (n < buddy.waveUntil && !REDUCED) {
    // waving hand: ▝ / ▗ alternate every 170ms, just right of the body on row 1
    const up = Math.floor(n / 170) % 2 === 0;
    shardCtx.fillStyle = rgbStr(specAt(palette.spec, 0.15 + n / 6000));
    shardCtx.fillRect(ox + 13 * px + 3, oy + 2 * px + (up ? 0 : px), px, px);
    shardCtx.fillRect(ox + 12 * px + 3, oy + 3 * px, px, px / 2);
  }
}

function sparkle(count = 6, chars = '✦✧·+◇') {
  if (REDUCED) return;
  const box = $('sparks');
  for (let i = 0; i < count; i++) {
    const s = el('span', 'spark', chars[Math.floor(Math.random() * chars.length)]);
    s.style.left = (10 + Math.random() * 150) + 'px';
    s.style.top = (10 + Math.random() * 90) + 'px';
    s.style.color = rgbStr(specAt(palette.spec, Math.random()));
    s.style.animationDelay = (Math.random() * 0.5) + 's';
    box.appendChild(s);
    setTimeout(() => s.remove(), 2200);
  }
}

// ---------- last prism beam (web/beam.js) ----------
let beamStop = null;
let beamEnabled = true; // /beam
function playBeam() {
  if (REDUCED || !beamEnabled || beamStop) return;
  const wrap = $('beam'), cv = $('beam-canvas');
  if (!wrap || !cv || !window.PrismBeam) return;
  cv.width = innerWidth; cv.height = innerHeight;
  wrap.hidden = false;
  const body = document.body;
  const cell = { w: 12, h: 18 };
  // origin sits on Shard's left edge — PrismBeam fires leftward from there, like the terminal
  const r = shardCv.getBoundingClientRect();
  const originX = Math.max(1, Math.round(r.left / cell.w));
  const originY = Math.max(0, Math.round((r.top + r.height * 0.29) / cell.h));
  let timer = 0;
  const finish = () => {
    clearTimeout(timer);
    removeEventListener('keydown', finish, true);
    removeEventListener('pointerdown', finish, true);
    if (beamStop) { const s = beamStop; beamStop = null; s(); }
    wrap.hidden = true;
    cv.getContext('2d').clearRect(0, 0, cv.width, cv.height);
    body.classList.remove('shake');
  };
  beamStop = window.PrismBeam.play({
    canvas: cv,
    originX,
    originY,
    cell,
    theme: themeCache,
    onShake: (dx) => {
      body.classList.toggle('shake', !!dx);
      body.style.setProperty('--shake-x', (dx * 4) + 'px');
      body.style.setProperty('--shake-y', (-dx * 2) + 'px');
    },
  });
  addEventListener('keydown', finish, true);
  addEventListener('pointerdown', finish, true);
  timer = setTimeout(finish, 1900);
}

// ---------- splash ----------
const LOGO = ['####..####..#####..####.#...#', '#...#.#...#...#...#.....##.##', '####..####....#....###..#.#.#', '#.....#..#....#.......#.#...#', '#.....#...#.#####.####..#...#'];
let splashDone = false;
function buildSplash() {
  const logo = $('splash-logo');
  const cols = LOGO[0].length;
  logo.style.gridTemplateColumns = `repeat(${cols}, 14px)`;
  const pixels = [];
  LOGO.forEach((row, y) => {
    for (let x = 0; x < cols; x++) {
      const i = el('i');
      if (row[x] === '#') {
        const base = specAt(palette.spec, (x / cols) * 0.92 + 0.02);
        pixels.push({ i, x, base, delay: 700 + hash(x, y, 3) * 700 + x * 14 });
        i.style.background = rgbStr(base);
      } else i.style.visibility = 'hidden';
      logo.appendChild(i);
    }
  });
  const motto = 'inspired by the best · built to be the best';
  const m = $('splash-motto');
  const letters = [...motto].map((ch, i) => {
    const s = el('span', '', ch);
    s.style.color = rgbStr(specAt(palette.spec, (i / motto.length) * 0.9));
    m.appendChild(s);
    return s;
  });
  const hint = el('p', 'splash-hint', 'press any key');
  $('splash-motto').after(hint);
  const beam = document.querySelector('.splash-beam');
  const rays = [...document.querySelectorAll('.splash-rays i')];
  const tag = document.querySelector('.splash-tag');
  // twinkling squares
  const cv = $('splash-stars'), ctx = cv.getContext('2d');
  const t0 = performance.now();
  const draw = () => {
    if (splashDone) return;
    const n = performance.now() - t0;
    // beam in, logo pixels, rays out, tag, motto — same timings as cli/splash.go
    beam.style.width = (190 * ease((n - 250) / 700)) + 'px';
    const shimmer = ((n / 2200) % 1.4) * (cols + 16) - 8;
    for (const p of pixels) {
      if (n < p.delay - 260) { p.i.style.opacity = '0'; continue; }
      if (n < p.delay) { p.i.style.opacity = String(0.25 + 0.35 * hash(p.x, 0, Math.floor(n / 60))); p.i.style.background = rgbStr(mix(palette.bg, p.base, 0.6)); continue; }
      const flash = 1 - ease((n - p.delay) / 260);
      const hl = Math.exp(-Math.pow(p.x - shimmer, 2) / 6);
      p.i.style.opacity = '1';
      p.i.style.background = rgbStr(mix(mix(p.base, palette.fg, 0.55 * hl), [255, 255, 255], flash * 0.8));
    }
    const rw = 190 * ease((n - 1500) / 700);
    rays.forEach((r) => { r.style.width = rw + 'px'; });
    tag.style.opacity = String(ease((n - 1900) / 600));
    letters.forEach((s, i) => { s.style.opacity = String(ease((n - 1700 - i * 18) / 260)); });
    hint.style.opacity = String(ease((n - 2400) / 500) * (0.55 + 0.45 * Math.sin(n / 420)));
    if (cv.width !== innerWidth || cv.height !== innerHeight) { cv.width = innerWidth; cv.height = innerHeight; }
    ctx.clearRect(0, 0, cv.width, cv.height);
    const cell = 16, cx = cv.width / 2, cy = cv.height / 2;
    const W = Math.ceil(cv.width / cell), H = Math.ceil(cv.height / cell);
    for (let y = 0; y < H; y++) for (let x = 0; x < W; x++) {
      const X = x * cell, Y = y * cell;
      const dx = Math.abs(X - cx) / cx, dy = Math.abs(Y - cy) / cy;
      const edge = Math.max(dx, dy * 0.9);
      if (hash(x, y, 7) > 0.04 + 0.55 * Math.pow(edge, 2.4)) continue;
      if (Math.abs(X - cx) < 330 && Math.abs(Y - cy) < 110) continue;
      const reveal = ease((n - 600 * edge) / 900);
      let col;
      if (hash(x, y, Math.floor(n / 380)) < 0.025) col = mix(palette.bg, specAt(palette.spec, X / cv.width), reveal * 0.85);
      else col = mix(palette.bg, palette.muted, reveal * 0.22 + 0.05 * edge);
      ctx.fillStyle = rgbStr(col); ctx.fillRect(X + 5, Y + 5, 4, 4);
    }
    requestAnimationFrame(draw);
  };
  requestAnimationFrame(draw);
  const end = () => {
    if (splashDone) return;
    splashDone = true;
    const s = $('splash'); s.classList.add('out');
    setTimeout(() => { s.hidden = true; }, 650);
    removeEventListener('keydown', end, true); removeEventListener('pointerdown', end, true);
    say('hello', 2600); setMood('happy', 1800); sparkle(4);
    buddy.waveUntil = performance.now() + 2200;
    $('input').focus();
  };
  addEventListener('keydown', end, true); addEventListener('pointerdown', end, true);
  setTimeout(end, REDUCED ? 600 : 2900);
}

// ---------- API + stream ----------
async function api(path, body) {
  if (DEMO) return demoApi(path, body);
  const r = await fetch(path, body ? { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) } : undefined);
  if (r.status === 409) throw new Error('busy');
  if (r.status === 204) return null;
  if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
  return r.json();
}

async function* readStream(res) {
  const reader = res.body.getReader(), dec = new TextDecoder();
  let buf = '', data = [];
  const parse = () => {
    if (!data.length) return null;
    const s = data.join('\n'); data = [];
    try { return JSON.parse(s); } catch { return null; /* skip bad event */ }
  };
  const handleLine = (line) => {
    if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''));
  };
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let m;
    while ((m = /\r\n|\r|\n/.exec(buf))) {
      const line = buf.slice(0, m.index); buf = buf.slice(m.index + m[0].length);
      if (line === '') { const ev = parse(); if (ev) yield ev; }
      else handleLine(line);
    }
  }
  // flush a final unterminated event
  buf += dec.decode();
  if (buf) handleLine(buf);
  const last = parse(); if (last) yield last;
}

function handleEvent(ev) {
  const type = ev.type || ev.event || ev.kind;
  switch (type) {
    case 'route':
      state.route = ev.route; state.waitMs = ev.wait_ms || 0; renderStatus(); break;
    case 'thinking':
      setPhase('thinking'); setMood('thinking'); say('thinking', 2600); break;
    case 'reasoning':
      state.reasoning += ev.text || ''; if (state.phase !== 'thinking') setPhase('thinking'); break;
    case 'text':
      if (state.phase !== 'text') setPhase('text');
      buddy.lookAt = performance.now();
      state.draft = null; addText(ev.text || ''); scrollDown(); break;
    case 'draft':
      state.draft = { tool: ev.tool, arg: ev.arg, chars: ev.chars || 0 }; setPhase('draft'); buddy.charge = Math.min(1, (ev.chars || 0) / 4000); break;
    case 'tool_start':
      state.draft = null; buddy.charge = 0; setPhase('tool'); setMood('working'); flushText(); state.textEl = null;
      addStep({ id: ev.id, tool: ev.tool, arg: ev.arg, running: true }); scrollDown(); break;
    case 'tool_done':
      finishStep(ev); setPhase('thinking'); state.reasoning = '';
      if (ev.ok === false) { setMood('surprised', 1500); say('crack', 2600); } break;
    case 'note': addNote(ev.text || ''); break;
    case 'usage': state.tokens = ev.tokens || 0; state.window = ev.window || 0; renderStatus(); break;
    case 'done': {
      if (ev.model) state.model = ev.model;
      addMeta({ model: ev.model, seconds: ev.seconds }); renderStatus();
      if (ev.tests_passed) { say('tests', 3600); setMood('proud', 3000); sparkle(8); }
      else if (ev.tools > 0) { say('done', 3000); setMood('happy', 2400); sparkle(6); }
      else setMood('idle');
      if (ev.tools > 0) playBeam();
      break;
    }
    case 'error': {
      const text = `${ev.headline || ''} ${ev.detail || ''}`.toLowerCase();
      addError(ev.headline, ev.detail);
      if (/busy|rate|limit|fogged/.test(text)) { say('fogged', 4000); setMood('sleepy', 4000); }
      else { say('crack', 3000); setMood('oops', 2400); }
      break;
    }
    default: break;
  }
}

async function send(message) {
  if (state.busy) return;
  addUser(message);
  collapseOldTurns();
  setBusy(true); setPhase('thinking'); setMood('thinking'); state.reasoning = '';
  const ac = new AbortController(); state.abort = ac;
  try {
    if (DEMO) { for await (const ev of demoStream(message, ac.signal)) handleEvent(ev); }
    else {
      const res = await fetch('/api/chat', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ message }), signal: ac.signal });
      if (res.status === 409) { addError('busy', 'prism is still working on the last message'); say('fogged', 3000); setMood('sleepy', 3000); }
      else if (!res.ok) addError(`request failed`, `${res.status} ${res.statusText}`);
      else for await (const ev of readStream(res)) handleEvent(ev);
    }
  } catch (e) {
    if (e.name !== 'AbortError') { addError('connection lost', String(e.message || e)); say('crack', 3000); setMood('oops', 2400); }
    else addNote('stopped.');
  } finally {
    setBusy(false); state.abort = null;
    document.querySelectorAll('.step.running').forEach((s) => { s.classList.remove('running'); s.querySelector('.dia').textContent = '◆'; });
    if (buddy.mood === 'thinking' || buddy.mood === 'working') setMood('idle');
    scrollDown();
  }
}

async function stop() {
  if (!state.busy) return;
  try { await api('/api/stop', {}); } catch { /* ignore */ }
  if (state.abort) state.abort.abort();
}

function showHelp() {
  hideHello();
  const t = newTurn();
  const md = el('div', 'md');
  const list = (window.PrismCommands && window.PrismCommands.list) || [];
  const rows = list.length
    ? list.map((c) => '- `' + c.name + '` — ' + c.help).join('\n')
    : '- `/new` — start a fresh chat\n- `/theme <name>` — ' + THEMES.join(', ') + '\n- `/help` — this list';
  renderMarkdown(md, '### commands\n' + rows + '\n\nenter sends · shift+enter newline · esc stops');
  t.appendChild(md);
  scrollDown();
}

// a server command that answered with text (/models, /usage, /memory …)
function addInfoBlock(text) {
  hideHello();
  const t = newTurn();
  const md = el('div', 'md');
  renderMarkdown(md, String(text == null ? '' : text));
  t.appendChild(md);
  scrollDown();
}

function resetTranscript() {
  clear(transcript); state.turn = null; state.steps = null; state.textEl = null; state.tokens = 0; renderStatus();
  const hello = el('div', 'hello'); hello.id = 'hello';
  hello.appendChild(el('p', 'hello-title', 'what are we building today?'));
  hello.appendChild(el('p', 'muted', 'try:  fix a bug  ·  make a website  ·  /help'));
  transcript.appendChild(hello);
}

// /new /theme /beam /quiet /help answer here; everything else goes to cli/serve.go command().
async function runCommand(line) {
  const text = String(line || '').trim();
  if (!text) return;
  if (text.charAt(0) !== '/') { send(text); return; }
  const [cmd, ...rest] = text.split(/\s+/);
  switch (cmd) {
    case '/new':
      try { await api('/api/new', {}); } catch (e) { addError('could not start new session', String(e.message)); return; }
      resetTranscript();
      say('hello', 2400); setMood('happy', 1500);
      return;
    case '/theme':
      if (!setTheme(rest[0] || '')) addNote(`themes: ${THEMES.join(', ')}`);
      else addNote(`theme: ${rest[0]}`);
      return;
    case '/beam':
      beamEnabled = !beamEnabled;
      addNote(`finish beam ${beamEnabled ? 'on' : 'off'}`);
      return;
    case '/quiet':
      quiet = !quiet;
      if (quiet) hush();
      addNote(`shard's lines ${quiet ? 'off' : 'on'}`);
      return;
    case '/help': showHelp(); return;
    default: break;
  }
  if (DEMO || !window.PrismCommands) { addNote(`${cmd} needs the prism server`); return; }
  let data = null;
  try { data = await window.PrismCommands.runServer(text); }
  catch (e) { addError('command failed', String((e && e.message) || e)); return; }
  const action = data && (data.kind || data.action || data.type);
  switch (action) {
    case 'info': {
      // server sends {kind:'info', title, lines:[]} — format into markdown
      let body = '';
      if (data.title) body += `### ${data.title}\n`;
      if (Array.isArray(data.lines)) body += data.lines.join('\n');
      else body += data.text || data.detail || '';
      addInfoBlock(body || data.text || data.detail || '');
      return;
    }
    case 'reload':
      try {
        const hist = await api('/api/history');
        resetTranscript();
        if (hist) loadHistory(hist.entries);
      } catch (e) { addError('could not reload', String((e && e.message) || e)); }
      return;
    case 'send': send(data.message || data.text || text); return;
    case 'error': addError(data.headline || 'command failed', data.detail || data.text || ''); return;
    default:
      if (data && (data.text || data.lines)) addInfoBlock(data.text || (Array.isArray(data.lines) ? data.lines.join('\n') : ''));
      else addNote(`unknown command: ${cmd}`);
      return;
  }
}

// ---------- history ----------
function loadHistory(entries) {
  if (!entries || !entries.length) return;
  hideHello();
  for (const e of entries) {
    switch (e.kind) {
      case 'user': addUser(e.text || ''); break;
      case 'text': state.textEl = null; addText(e.text || ''); state.textEl = null; break;
      case 'tool': addStep({ tool: e.tool, arg: e.arg, ok: e.ok, summary: e.summary }); break;
      case 'note': addNote(e.text || ''); break;
      case 'error': addError(e.text || 'error'); break;
      default: break;
    }
  }
  collapseOldTurns();
  scrollDown();
}

// ---------- demo mode (no server) ----------
const sleep = (ms, signal) => new Promise((res, rej) => {
  const t = setTimeout(res, ms);
  signal && signal.addEventListener('abort', () => { clearTimeout(t); rej(Object.assign(new Error('aborted'), { name: 'AbortError' })); }, { once: true });
});
async function demoApi(path) {
  if (path === '/api/info') return { project: '~/test-free-ai', theme: document.documentElement.dataset.theme, models_ready: 7 };
  if (path === '/api/history') return { entries: [] };
  return null;
}
const DEMO_MD = `Done! I added a **save** step so tasks survive reloads.

### what changed
- \`app.js\` — new \`save()\` writes tasks to \`localStorage\`
- \`index.html\` — footer hint about persistence
- tests pass (\`npm test\`, 12/12)

\`\`\`js
// persist tasks between sessions
function save() {
  const json = JSON.stringify(tasks);
  localStorage.setItem('todo-tasks', json);
  return json.length > 0;
}
\`\`\`

Open \`index.html\` in a browser to try it.`;
async function* demoStream(message, signal) {
  const fast = params.get('demo') === 'fast';
  const d = (ms) => sleep(fast ? 10 : ms, signal);
  yield { type: 'route', route: 'nemotron-3-ultra-550b-a55b', wait_ms: 0 };
  yield { type: 'thinking' };
  for (const t of ['the user wants persistence.', '\nI should read app.js first,', '\nthen add a save() helper and call it after every mutation.']) { await d(200); yield { type: 'reasoning', text: t }; }
  await d(300);
  yield { type: 'tool_start', id: 1, tool: 'read', arg: 'app.js' };
  await d(450);
  yield { type: 'tool_done', id: 1, ok: true, summary: '37 lines' };
  await d(300);
  for (let c = 300; c <= 1500; c += 300) { yield { type: 'draft', tool: 'edit', arg: 'app.js', chars: c }; await d(110); }
  yield { type: 'tool_start', id: 2, tool: 'edit', arg: 'app.js' };
  await d(500);
  yield { type: 'tool_done', id: 2, ok: true, summary: '+5 -1', added: 5, removed: 1, diff: [
    { kind: ' ', num: 7, text: '  render();' },
    { kind: '-', num: 8, text: '  // TODO: persist' },
    { kind: '+', num: 8, text: '  save();' },
    { kind: '+', num: 9, text: '  function save() {' },
    { kind: '+', num: 10, text: "    localStorage.setItem('todo-tasks', JSON.stringify(tasks));" },
    { kind: '+', num: 11, text: '  }' },
    { kind: '+', num: 12, text: '' },
    { kind: ' ', num: 13, text: '}' },
  ] };
  await d(300);
  yield { type: 'tool_start', id: 3, tool: 'bash', arg: 'npm test' };
  await d(600);
  yield { type: 'tool_done', id: 3, ok: true, summary: '→ 12 passing' };
  yield { type: 'usage', tokens: 5100, window: 128000 };
  await d(300);
  for (const w of DEMO_MD.match(/\S+\s*|\s+/g)) { yield { type: 'text', text: w }; await d(fast ? 1 : 14); }
  yield { type: 'done', seconds: 4.2, model: 'nemotron-3-ultra-550b-a55b', tools: 3, tests_passed: /test/.test(message) };
}

// ---------- input ----------
const input = $('input');
function autosize() { input.style.height = 'auto'; input.style.height = Math.min(180, input.scrollHeight) + 'px'; }
input.addEventListener('input', autosize);
input.addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault();
    const v = input.value.trim();
    if (!v) return;
    if (v.charAt(0) === '/' && window.PrismCommands) return; // commands.js owns slash lines
    if (state.busy) { say('fogged', 1800); return; }
    input.value = ''; autosize();
    runCommand(v);
  } else if (e.key === 'Escape') {
    e.preventDefault();
    const suggest = $('cmd-suggest');
    if (suggest && !suggest.hidden) { suggest.hidden = true; return; }
    if (state.busy) stop(); else { input.value = ''; autosize(); }
  }
});
$('composer').addEventListener('submit', (e) => e.preventDefault());
$('theme').addEventListener('change', (e) => setTheme(e.target.value));
addEventListener('keydown', (e) => {
  if (e.key === 'Escape' && state.busy && document.activeElement !== input) stop();
  const a = document.activeElement;
  if (a !== input && a.tagName !== 'SELECT' && e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) input.focus();
});

// ---------- shard buddy draw (web/shard.js when it is loaded) ----------
function drawBuddy(now) {
  const S = window.PrismShard;
  if (S && typeof S.drawSmall === 'function') {
    if (buddy.moodUntil && now > buddy.moodUntil) setMood(state.busy ? 'working' : 'idle');
    const charge = Math.max(buddy.charge, state.busy ? Math.min(1, (now - buddy.busySince) / 20000) : 0);
    const look = now - buddy.lookAt < 700 ? -1 : 0;
    S.drawSmall(shardCv, themeCache, { md: buddy.mood, mood: buddy.mood, charge, look, wave: now < buddy.waveUntil }, now);
    return;
  }
  drawShard(now);
}

// ---------- terminal handoff ----------
function showHandoff() {
  if ($('handoff')) return;
  const note = el('div', 'handoff muted', 'this chat is shared with your terminal · close this tab to go back');
  note.id = 'handoff';
  note.style.cssText = 'text-align:center;font-size:12px;padding:4px 16px 0;color:var(--muted)';
  const top = document.querySelector('header.top');
  if (top) top.insertAdjacentElement('afterend', note);
}
function showClosed() {
  if ($('closed')) return;
  const o = el('div', '', '');
  o.id = 'closed';
  o.style.cssText = 'position:fixed;inset:0;z-index:90;display:flex;align-items:center;justify-content:center;'
    + 'background:rgba(0,0,0,.72);backdrop-filter:blur(2px);color:var(--fg);font-size:15px;letter-spacing:.02em';
  o.appendChild(el('p', '', 'prism was closed in the terminal'));
  document.body.appendChild(o);
}
// 3s liveness ping — raw fetch, never the demo stub
function startHeartbeat() {
  if (DEMO) return;
  let everOk = false;
  setInterval(async () => {
    try {
      const r = await fetch('/api/ping');
      if (!r.ok) throw new Error(String(r.status));
      everOk = true;
    } catch {
      if (everOk) showClosed();
    }
  }, 3000);
}

// ---------- boot ----------
async function boot() {
  let saved = null;
  try { saved = localStorage.getItem('prism-theme'); } catch { /* ignore */ }
  setTheme(params.get('theme') || saved || 'prism');
  buildSplash();
  renderStatus();
  let loopRaf = 0;
  const loop = () => { loopRaf = 0; if (document.hidden) return; const now = performance.now(); drawBuddy(now); renderLive(now); loopRaf = requestAnimationFrame(loop); };
  const startLoop = () => { if (!loopRaf && !document.hidden) loopRaf = requestAnimationFrame(loop); };
  document.addEventListener('visibilitychange', () => { if (document.hidden) { if (loopRaf) { cancelAnimationFrame(loopRaf); loopRaf = 0; } } else startLoop(); });
  startLoop();
  try {
    const info = await api('/api/info');
    if (info) {
      if (info.project) $('project').textContent = info.project;
      if (info.theme && !params.get('theme') && !saved) setTheme(info.theme);
      state.modelsReady = info.models_ready; renderStatus();
      if (info.handoff) showHandoff();
    }
    const hist = await api('/api/history');
    if (hist) loadHistory(hist.entries);
  } catch (e) {
    state.modelsReady = 0; renderStatus();
    $('top-say').textContent = 'server unreachable';
  }
  if (window.PrismCommands) {
    const box = $('cmd-suggest');
    window.PrismCommands.attach({
      input,
      suggestBox: box,
      run: (text) => { input.value = ''; autosize(); if (box) box.hidden = true; runCommand(text); },
    });
  }
  startHeartbeat();
  if (DEMO) {
    setTimeout(() => {
      input.value = ''; send('make my todo list persist between reloads, then run the tests');
    }, params.get('demo') === 'fast' ? 50 : 3000);
  }
}
boot();
