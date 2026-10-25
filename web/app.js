// prism in the browser. No dependencies, no build step.

const $ = (id) => document.getElementById(id);
const params = new URLSearchParams(location.search);
const DEMO = params.has('demo');
const REDUCED = matchMedia('(prefers-reduced-motion: reduce)').matches;
const THEMES = ['prism', 'tokyo-night', 'catppuccin', 'rose-pine', 'matte-black', 'gruvbox', 'nord', 'everforest'];

// Theme
function cssVar(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}
function spectrum() {
  const out = [];
  for (let i = 0; i < 7; i++) {
    const color = cssVar('--s' + i);
    if (!color) break;
    out.push(hexToRgb(color));
  }
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
  let h = (x * 374761393 + y * 668265263 + s * 1442695041) >>> 0;
  h = Math.imul(h ^ (h >>> 13), 1274126177) >>> 0;
  return ((h ^ (h >>> 16)) >>> 0) % 10000 / 10000;
}

const palette = { spec: [], bg: [0, 0, 0], fg: [255, 255, 255], muted: [100, 100, 100] };
// The palette as hex strings, the shape beam.js and shard.js expect.
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
  themeCache.spectrum = palette.spec.map((_, i) => cssVar('--s' + i));
  return true;
}

// DOM helpers. Never use innerHTML with model text.
function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}
function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); }

const TOOL_COLOR = { read: '--x4', edit: '--s3', write: '--s3', bash: '--x6', search: '--s2', grep: '--s2', ls: '--s1', list: '--s1', browse: '--x4', screenshot: '--x5' };
function toolColor(tool) { return `var(${TOOL_COLOR[tool] || '--x5'})`; }

// Transcript
const transcript = $('transcript');
const liveEl = $('live');
const state = {
  busy: false, model: null, pinned: '', plan: false, planPick: null, tokens: 0, window: 0, modelsReady: null,
  crew: null, turn: null, steps: null, stepMap: new Map(), textBuf: '', textEl: null,
  reasoning: '', phase: 'idle', phaseAt: 0, draft: null, abort: null, route: null, source: '', pinSource: '', waitMs: 0,
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

// While streaming, markdown is re-rendered at most once per animation frame.
// textPending keeps the element and buffer so clearing state.textEl can't lose the last render.
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
// dropText takes back the end of the answer, e.g. a reply dropped for repeating itself.
function dropText(text) {
  state.draft = null; state.reasoning = '';
  if (!state.textEl || !text || !state.textBuf.endsWith(text)) return;
  state.textBuf = state.textBuf.slice(0, -text.length);
  if (!state.textBuf.trim()) {
    if (textRaf) { cancelAnimationFrame(textRaf); textRaf = 0; }
    textPending = null; state.textEl.remove(); state.textEl = null; state.textBuf = '';
    const last = curTurn().lastElementChild;
    if (last && last.classList.contains('steps-group')) state.steps = last; // the next step joins its group again
    return;
  }
  textPending = { el: state.textEl, buf: state.textBuf };
  flushText();
}
function addNote(text) { curTurn().appendChild(el('div', 'note', text)); state.textEl = null; }
function addError(headline, detail) {
  const e = el('div', 'error');
  e.appendChild(el('div', 'head', headline || 'error'));
  if (detail) e.appendChild(el('div', 'detail', detail));
  curTurn().appendChild(e); state.textEl = null;
}
// Who answered ("free", "plan" or "yours") and its colour.
const SOURCE_TONE = { free: 'tone-ok', plan: 'tone-plan', yours: 'tone-muted' };
function addMeta({ model, seconds, source }) {
  const m = el('div', 'meta');
  const d = el('span', 'grad', '◆ ');
  m.appendChild(d);
  m.appendChild(el('span', 'model', model || state.model || 'auto'));
  m.appendChild(el('span', 'dot', '  ·  '));
  m.appendChild(el('span', '', seconds != null ? `${Number(seconds).toFixed(1)}s` : ''));
  m.appendChild(el('span', 'dot', '  ·  '));
  m.appendChild(el('span', SOURCE_TONE[source] || 'tone-ok', source || 'free'));
  curTurn().appendChild(m); state.textEl = null;
}

// Crew panel
const CREW_MARK = { queued: '·', running: '⠋', done: '✓', failed: '✕', cancelled: '–' };
const CREW_SPIN = [...'⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏'];
let crewTimer = 0, crewFrame = 0;
function crewModelName(m) {
  m = String(m || '');
  m = m.slice(m.lastIndexOf('/') + 1);
  return m.endsWith(':free') ? m.slice(0, -5) : m;
}
function crewRow(c, r) {
  const row = el('div', 'crew-row');
  const note = el('div', 'crew-note');
  const parts = {
    row, note, status: 'queued',
    glyph: el('span', 'crew-glyph', '◆'), id: el('span', 'crew-id', r.id || ''),
    task: el('span', 'crew-task', String(r.task || '').replace(/\s+/g, ' ').trim()),
    model: el('span', 'crew-model', crewModelName(r.model)), mark: el('span', 'crew-mark', CREW_MARK.queued), steps: el('span', 'crew-steps'),
  };
  row.append(parts.glyph, parts.id, parts.task, parts.model, parts.mark, parts.steps);
  c.rows.set(r.id, parts);
  c.list.append(row, note);
  crewApply(c, parts, r);
  return parts;
}
function crewApply(c, p, r) {
  if (r.status) p.status = r.status;
  if (r.model != null) p.model.textContent = crewModelName(r.model);
  p.row.dataset.status = p.status;
  p.mark.textContent = p.status === 'running' ? (REDUCED ? '◌' : CREW_SPIN[crewFrame % CREW_SPIN.length]) : (CREW_MARK[p.status] || '·');
  p.steps.textContent = r.steps > 0 ? String(r.steps) : '';
  const note = r.note && (p.status === 'running' || p.status === 'failed') ? r.note : '';
  p.note.textContent = note ? '↳ ' + note.replace(/\s+/g, ' ').trim() : '';
  p.note.hidden = !note;
}
function crewRecolor(c) {
  const n = Math.max(1, c.rows.size); let i = 0;
  for (const p of c.rows.values()) {
    const col = rgbStr(specAt(palette.spec, i++ / n));
    p.row.style.setProperty('--hue', col);
    p.glyph.style.color = col;
  }
}
function crewHead(c) {
  const n = c.review ? c.rows.size - 1 : c.rows.size;
  c.count.textContent = ` · ${n} ${n === 1 ? 'worker' : 'workers'}${c.review ? ' + review' : ''}`;
}
function crewSpin() {
  const any = document.querySelector('.crew-row[data-status="running"]');
  if (!any || REDUCED) { clearInterval(crewTimer); crewTimer = 0; return; }
  crewFrame++;
  document.querySelectorAll('.crew-row[data-status="running"] .crew-mark').forEach((m) => { m.textContent = CREW_SPIN[crewFrame % CREW_SPIN.length]; });
}
function crewEnsureSpin() { if (!crewTimer && !REDUCED) crewTimer = setInterval(crewSpin, 80); }
function crewSummary(c, text) {
  c.done = true;
  const rows = [...c.rows.values()];
  let mark = '✓', cls = 'ok';
  if (rows.some((p) => p.status === 'failed')) { mark = '✕'; cls = 'bad'; }
  else if (rows.some((p) => p.status !== 'done')) { mark = '–'; cls = 'dim'; }
  clear(c.sum); c.sum.className = 'crew-sum ' + cls; c.sum.hidden = !text;
  c.sum.append(el('span', 'crew-sumark', mark + ' '), el('span', 'crew-sumtext', text || ''));
}
function crewMount(data) {
  const t = curTurn();
  flushText(); state.textEl = null; state.steps = null;
  const root = el('div', 'crew');
  const head = el('div', 'crew-head');
  const count = el('span', 'crew-count');
  head.append(el('span', 'grad', '◆ crew'), count);
  const list = el('div', 'crew-list');
  const sum = el('div', 'crew-sum'); sum.hidden = true;
  root.append(head, list, sum);
  t.appendChild(root);
  const c = { root, list, sum, count, rows: new Map(), review: false, done: false };
  for (const r of (data.rows || data.tasks || [])) {
    if (r.id === 'review') c.review = true;
    crewRow(c, r);
  }
  crewHead(c); crewRecolor(c);
  if (data.review) { c.review = true; crewHead(c); }
  return c;
}
// The crew tool's own step row is replaced by the panel.
function dropCrewStep() {
  const steps = [...document.querySelectorAll('.step.running')].filter((s) => s.querySelector('.tool')?.textContent === 'crew');
  const s = steps[steps.length - 1];
  if (!s) return;
  const g = s.parentNode;
  s.remove();
  if (g && g.classList.contains('steps-group') && !g.children.length) g.remove();
  state.steps = null;
}
function crewStart(ev) {
  dropCrewStep();
  state.crew = crewMount({ rows: (ev.tasks || []).map((t) => ({ id: t.id, task: t.task, model: 'auto:' + t.role, status: 'queued' })) });
  crewEnsureSpin();
}
function crewWorker(ev) {
  const c = state.crew;
  if (!c || c.done) return;
  let p = c.rows.get(ev.id);
  if (!p) {
    if (ev.id === 'review') c.review = true;
    p = crewRow(c, { id: ev.id, task: ev.id === 'review' ? 'review what changed' : '', model: ev.model, status: ev.status });
    crewHead(c); crewRecolor(c);
  }
  crewApply(c, p, ev);
  crewEnsureSpin();
}
function crewDone(ev) {
  const c = state.crew;
  if (!c) return;
  crewSummary(c, ev.summary || '');
}
// On esc, an error or a dropped connection, rows that never finished read cancelled.
function crewSettle() {
  const c = state.crew;
  if (!c || c.done) return;
  for (const p of c.rows.values()) if (p.status === 'queued' || p.status === 'running') crewApply(c, p, { status: 'cancelled' });
  crewSummary(c, '');
}
function crewFromHistory(data) {
  const c = crewMount(data);
  crewSummary(c, data.summary || '');
  state.crew = null;
}

// While the reader is at the end, new content keeps the view pinned to the bottom.
// Scrolling up pauses that and shows ↓ latest. Scrolling back down, sending, a command
// or the button resumes it.
const column = document.querySelector('.column');
const jumpBtn = $('jump');
const follow = { on: true, top: 0, height: 0 };
function pin() {
  if (!follow.on || document.hidden) return;
  column.scrollTop = column.scrollHeight;
  follow.top = column.scrollTop; follow.height = column.scrollHeight;
}
function scrollDown() { flushText(); follow.on = true; jumpBtn.hidden = true; pin(); }
// Moving up while the content didn't shrink means the reader is going back. A shrink pulls
// the view up too but lands it on the end, so following turns back on.
column.addEventListener('scroll', () => {
  const top = column.scrollTop, height = column.scrollHeight;
  if (top < follow.top && height >= follow.height) follow.on = false;
  else if (height - top - column.clientHeight < 2) follow.on = true;
  follow.top = top; follow.height = height;
  jumpBtn.hidden = follow.on;
}, { passive: true });
const grown = new ResizeObserver(() => { if (state.busy) pin(); });
grown.observe(transcript); grown.observe(liveEl);
new ResizeObserver(pin).observe(column);
document.addEventListener('visibilitychange', pin);
jumpBtn.addEventListener('click', () => { scrollDown(); if (!matchMedia('(pointer: coarse)').matches) input.focus({ preventScroll: true }); });

// Markdown, built with DOM calls so it is escaped by construction
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

// Live line and status
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

// The plan picker waits until the chat box is empty so it never takes keys mid-sentence.
// Sending something else means keep planning, and only on the tab whose plan it is.
function offerPlan() {
  const p = state.planPick;
  if (!p || p.tab !== tabState.active || state.busy || $('input').value.trim() || window.PrismCommands?.pickerOpen()) return;
  state.planPick = null;
  if (state.plan) openServerPicker(p.data);
}

function renderStatus() {
  $('route').textContent = state.pinned ? crewModelName(state.pinned) : (state.model || state.route || 'auto');
  // A chat pinned with /model says so in the status bar.
  const pin = $('pinned');
  if (pin) { pin.hidden = !state.pinned; pin.textContent = state.pinned ? ' · pinned' : ''; pin.title = state.pinned || ''; }
  $('plan-badge').hidden = !state.plan;
  const cost = state.source || state.pinSource || 'free';
  $('cost').textContent = cost; $('cost').className = SOURCE_TONE[cost] || 'tone-ok';
  $('input').placeholder = state.plan ? 'plan mode · ask for a plan…' : 'ask anything…  / for commands';
  const pct = state.window ? Math.min(100, Math.round((state.tokens / state.window) * 100)) : 0;
  $('usage').textContent = pct + '%';
  const cells = $('meter').children;
  const on = Math.round((pct / 100) * cells.length);
  for (let i = 0; i < cells.length; i++) {
    cells[i].className = i < on ? 'on' + (pct > 85 ? ' max' : pct > 60 ? ' hi' : '') : '';
  }
  const m = $('models');
  if (state.modelsReady == null) m.textContent = '';
  else m.textContent = state.modelsReady > 0 ? `${state.modelsReady} model${state.modelsReady === 1 ? '' : 's'} ready` : 'no models ready';
  m.style.color = state.modelsReady === 0 ? 'var(--red)' : '';
}

// Shard buddy
const SHARD = ['......#......', '.....###.....', '....#####....', '...#######...', '..#########..', '.###########.', '#############'];
const shardCv = $('shard');
const SHARD_W = 170, SHARD_H = 140;
const DPR = Math.max(1, Math.min(3, window.devicePixelRatio || 1));
shardCv.width = Math.round(SHARD_W * DPR); shardCv.height = Math.round(SHARD_H * DPR);
shardCv.style.width = SHARD_W + 'px'; shardCv.style.height = SHARD_H + 'px'; shardCv.style.marginTop = '-40px';
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
  split: 'splitting the light.', crack: 'hm. a crack in the lens.', fogged: 'all lenses fogged. give it a minute.', hello: 'light in. rainbow out.',
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
  const md = beamStop ? 'working' : buddy.mood;
  const W = 13, H = 7, px = 10;
  shardCtx.clearRect(0, 0, SHARD_W, SHARD_H);
  const ox = Math.round((SHARD_W - W * px) / 2), oy = Math.round(SHARD_H - H * px - 6);
  const white = [255, 255, 255], bg = palette.bg;
  let glow = 0;
  if (md === 'working') glow = 0.18 + 0.12 * Math.sin(n / 140);
  else if (md === 'thinking') glow = 0.06;
  else if (md === 'proud') glow = 0.12 + 0.08 * Math.sin(n / 200);
  const charge = beamStop ? 1 : Math.max(buddy.charge, state.busy ? Math.min(1, (n - buddy.busySince) / 20000) : 0);
  glow += charge * (0.22 + 0.06 * Math.sin(n / 90));
  const dim = md === 'sleepy' ? 0.5 : 0;

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
  // Eyes only. Keep in sync with drawShardSmall in cli/shard.go: dark pixels on row 4,
  // columns 4 and 8 shifted by look, no mouth, nothing drawn while blinking or sleepy.
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
  // decorations
  const cellX = (x) => ox + (x + 0.5) * px;
  const cellY = (y) => oy + (y + 0.5) * px * 2;
  const empty = (x, y) => !inShard(ox, oy, px, cellX(x), cellY(y));
  shardCtx.textBaseline = 'middle'; shardCtx.textAlign = 'center';
  shardCtx.font = '16px ' + MONO;
  if (charge > 0.05 && !REDUCED) {
    for (let i = 0; i < 3; i++) {
      const a = n / 260 + i * 2.1;
      const x = 6 + Math.round(Math.cos(a) * (8 - charge * 3));
      const y = 1 + Math.round(Math.sin(a) * 1.6);
      if (!empty(x, y)) continue;
      shardCtx.fillStyle = rgbStr(mix(bg, specAt(palette.spec, i / 3 + n / 4000), 0.4 + 0.6 * charge));
      shardCtx.fillText('·', cellX(x), cellY(y));
    }
  }
  if (md === 'sleepy') {
    for (let i = 0; i < 3; i++) {
      if (i > (n / 900) % 3) continue;
      shardCtx.fillStyle = rgbStr(mix(bg, palette.muted, 0.9 - 0.2 * i));
      shardCtx.fillText('z', cellX(12 + i), cellY(-1 - i));
    }
  } else if ((md === 'happy' || md === 'proud') && !REDUCED) {
    for (let i = 0; i < 5; i++) {
      const a = i / 5 * 2 * Math.PI + n / 700;
      const x = 6 + Math.trunc(Math.cos(a) * 9);
      const y = 1 + Math.trunc(Math.sin(a) * 2.2);
      if (!empty(x, y) || hash(i, Math.trunc(n / 180), 9) <= 0.3) continue;
      shardCtx.fillStyle = rgbStr(specAt(palette.spec, i / 5));
      shardCtx.fillText('✦·✧'[i % 3], cellX(x), cellY(y));
    }
  } else if (md === 'surprised') {
    shardCtx.fillStyle = rgbStr(specAt(palette.spec, 0.05));
    shardCtx.fillText('!', cellX(13), cellY(-1));
  }
  if (n < buddy.waveUntil && !REDUCED) {
    shardCtx.fillStyle = rgbStr(specAt(palette.spec, 0.15 + n / 6000));
    shardCtx.fillText('▝▗'[Math.trunc(n / 170) % 2], cellX(13), cellY(1));
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

// Last prism beam
let beamStop = null;
let beamEnabled = true; // /beam
function playBeam() {
  if (REDUCED || !beamEnabled || beamStop) return;
  const wrap = $('beam'), cv = $('beam-canvas');
  if (!wrap || !cv || !window.PrismBeam) return;
  const bdpr = Math.min(2, DPR); // full-screen canvas: cap the backing store
  cv.width = Math.round(innerWidth * bdpr); cv.height = Math.round(innerHeight * bdpr);
  wrap.hidden = false;
  const body = document.body;
  const cell = { w: 12, h: 20 };
  // The origin sits on Shard's left edge; PrismBeam fires leftward from there.
  const r = shardCv.getBoundingClientRect();
  const originX = Math.max(1, Math.round((r.left + (SHARD_W - 130) / 2 - 22) / cell.w));
  const originY = Math.max(0, Math.round((r.top + SHARD_H - 70 - 6 + 50) / cell.h));
  let timer = 0;
  const finish = () => {
    clearTimeout(timer);
    removeEventListener('keydown', finish, true);
    removeEventListener('pointerdown', finish, true);
    if (beamStop) { const s = beamStop; beamStop = null; s(); drawBuddy(performance.now()); }
    wrap.hidden = true;
    const c2 = cv.getContext('2d');
    c2.setTransform(1, 0, 0, 1, 0, 0);
    c2.clearRect(0, 0, cv.width, cv.height);
    body.classList.remove('shake');
  };
  beamStop = window.PrismBeam.play({
    canvas: cv,
    originX,
    originY,
    cell,
    dpr: bdpr,
    theme: themeCache,
    onFrame: (ctx) => {
      drawBuddy(performance.now());
      const r = shardCv.getBoundingClientRect();
      ctx.drawImage(shardCv, r.left, r.top, r.width, r.height);
    },
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

// Splash
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
  const hint = el('p', 'splash-hint', matchMedia('(pointer: coarse)').matches ? 'tap to start' : 'press any key');
  $('splash-motto').after(hint);
  const beam = document.querySelector('.splash-beam');
  const rays = [...document.querySelectorAll('.splash-rays i')];
  const tag = document.querySelector('.splash-tag');
  const cv = $('splash-stars'), ctx = cv.getContext('2d');
  const t0 = performance.now();
  const draw = () => {
    if (splashDone) return;
    const n = performance.now() - t0;
    // beam in, logo pixels, rays out, tag, motto. Keep timings in sync with cli/splash.go.
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

// API and stream
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
      state.route = ev.route; state.source = ev.source || ''; state.waitMs = ev.wait_ms || 0; renderStatus(); break;
    case 'thinking':
      setPhase('thinking'); setMood('thinking'); say('thinking', 2600); break;
    case 'reasoning':
      state.reasoning += ev.text || ''; if (state.phase !== 'thinking') setPhase('thinking'); break;
    case 'text':
      if (state.phase !== 'text') setPhase('text');
      buddy.lookAt = performance.now();
      state.draft = null; addText(ev.text || ''); break;
    case 'drop_text': dropText(ev.text || ''); break;
    case 'draft':
      state.draft = { tool: ev.tool, arg: ev.arg, chars: ev.chars || 0 }; setPhase('draft'); buddy.charge = Math.min(1, (ev.chars || 0) / 4000); break;
    case 'tool_start':
      state.draft = null; buddy.charge = 0; setPhase('tool'); setMood('working'); flushText(); state.textEl = null;
      addStep({ id: ev.id, tool: ev.tool, arg: ev.arg, running: true }); break;
    case 'tool_done':
      finishStep(ev); setPhase('thinking'); state.reasoning = '';
      if (ev.ok === false) { setMood('surprised', 1500); say('crack', 2600); } break;
    case 'note': addNote(ev.text || ''); break;
    case 'approve': addAsk(ev); break;
    case 'crew_start': state.draft = null; setPhase('tool'); setMood('working'); say('split', 3000); crewStart(ev); break;
    case 'crew_worker': crewWorker(ev); break;
    case 'crew_done': crewDone(ev); say('done', 3000); break;
    case 'usage': state.tokens = ev.tokens || 0; state.window = ev.window || 0; renderStatus(); break;
    case 'done': {
      if (ev.model) state.model = ev.model;
      state.plan = !!ev.plan; state.planPick = ev.pick ? { tab: state.sendTab, data: ev.pick } : null;
      addMeta({ model: ev.model, seconds: ev.seconds, source: ev.source }); renderStatus();
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
  state.planPick = null; state.sendTab = tabState.active;
  addUser(message);
  collapseOldTurns();
  scrollDown();
  setBusy(true); setPhase('thinking'); setMood('thinking'); state.reasoning = '';
  const ac = new AbortController(); state.abort = ac;
  try {
    if (DEMO) { for await (const ev of demoStream(message, ac.signal)) handleEvent(ev); }
    else {
      const tabQ = tabState.active > 1 ? '?tab=' + tabState.active : '';
      const res = await fetch('/api/chat' + tabQ, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ message }), signal: ac.signal });
      if (res.status === 409) { addError('busy', 'prism is still working on the last message'); say('fogged', 3000); setMood('sleepy', 3000); }
      else if (!res.ok) addError(`request failed`, `${res.status} ${res.statusText}`);
      else for await (const ev of readStream(res)) handleEvent(ev);
    }
  } catch (e) {
    if (e.name !== 'AbortError') { addError('connection lost', String(e.message || e)); say('crack', 3000); setMood('oops', 2400); }
    else addNote('stopped.');
  } finally {
    setBusy(false); state.abort = null;
    if (state.ask) { settleAsk(state.ask.box, 'stopped'); state.ask = null; }
    offerPlan();
    crewSettle();
    document.querySelectorAll('.step.running').forEach((s) => { s.classList.remove('running'); s.querySelector('.dia').textContent = '◆'; });
    if (buddy.mood === 'thinking' || buddy.mood === 'working') setMood('idle');
    flushText(); pin();
  }
}

// Asking before a risky command
const ASK_DONE = { yes: 'allowed once', chat: 'allowed for this chat', no: 'you said no' };
function addAsk(ev) {
  hideHello(); flushText(); state.textEl = null;
  const verb = { bash: 'run this command', write: 'write this file', edit: 'apply this edit' }[ev.tool] || 'use ' + ev.tool;
  const box = el('div', 'ask');
  box.setAttribute('role', 'group');
  box.setAttribute('aria-label', 'prism asks first');
  box.appendChild(el('div', 'ask-head', '? prism wants to ' + verb + ':'));
  box.appendChild(el('pre', 'ask-arg', ev.arg || ''));
  if (ev.why) box.appendChild(el('div', 'ask-why', ev.why));
  const keys = el('div', 'ask-keys');
  const opts = [['yes', 'y', 'yes, once']];
  if (ev.chat) opts.push(['chat', 'a', 'yes for this chat']);
  opts.push(['no', 'n', 'no']);
  for (const [ans, key, label] of opts) {
    const b = el('button', 'ask-btn ask-' + ans);
    b.type = 'button';
    b.appendChild(el('b', '', key));
    b.appendChild(document.createTextNode(' ' + label));
    b.addEventListener('click', () => { if (state.ask && performance.now() - state.ask.at >= 600) answerAsk(ans); });
    keys.appendChild(b);
  }
  box.appendChild(keys);
  // the question sits under the step it is about; answered, it shrinks to one line there
  const step = [...curTurn().querySelectorAll('.step.running')].pop();
  if (step) step.after(box); else curTurn().appendChild(box);
  state.ask = { id: ev.id, box, chat: !!ev.chat, at: performance.now() };
  scrollDown();
  // the keys move to the question only from an empty chat box or the page, never from a field you type in
  const at = document.activeElement;
  if (!matchMedia('(pointer: coarse)').matches && (at === document.body || (at === input && !input.value.trim()))) keys.firstChild.focus({ preventScroll: true });
}
function settleAsk(box, text) {
  box.replaceWith(el('div', 'ask-done', '↳ ' + text));
}
// the box shows the answer only once prism has it; if it didn't arrive, the buttons stay
async function answerAsk(ans) {
  const a = state.ask;
  if (!a || a.sending || (ans === 'chat' && !a.chat)) return;
  a.sending = true;
  const buttons = a.box.querySelectorAll('.ask-btn');
  buttons.forEach((b) => { b.disabled = true; });
  try { await api('/api/approve', { id: a.id, answer: ans }); }
  catch (e) {
    a.sending = false;
    buttons.forEach((b) => { b.disabled = false; });
    addError('your answer did not reach prism', String((e && e.message) || e));
    return;
  }
  if (state.ask === a) state.ask = null;
  const hadFocus = a.box.contains(document.activeElement);
  settleAsk(a.box, ASK_DONE[ans]);
  scrollDown();
  if (hadFocus && !matchMedia('(pointer: coarse)').matches) input.focus({ preventScroll: true });
}

// askKey is the answer a key gives, or nothing: keys typed into a field (other than an empty
// chat box on a computer) or right as the question appeared are not answers
function askKey(e) {
  const a = document.activeElement;
  if (!state.ask || e.ctrlKey || e.metaKey || e.altKey || performance.now() - state.ask.at < 600) return null;
  const typing = a && (['INPUT', 'TEXTAREA', 'SELECT'].includes(a.tagName) || a.isContentEditable) && !a.closest('.ask');
  if (typing && (a !== input || input.value || matchMedia('(pointer: coarse)').matches)) return null;
  return { y: 'yes', a: 'chat', n: 'no', escape: 'no' }[e.key.toLowerCase()] || null;
}

async function stop() {
  if (!state.busy) return;
  try { await api('/api/stop', {}); } catch { /* ignore */ }
  if (state.abort) state.abort.abort();
}

// Offline and ?demo fallback for /help. The server answers /help with a grouped view.
function showHelp() {
  const list = (window.PrismCommands && window.PrismCommands.list) || [];
  addView({
    title: 'commands',
    blocks: [{ title: '', items: list.map((c) => ({ name: c.name, desc: c.help })) }],
    footer: 'keys: enter send · shift+enter new line · tab completes · ↑↓ pick · esc stop or close',
  });
}

// Command output (info lines, views, tables)
function commandTurn() { hideHello(); const t = newTurn(); state.textEl = null; return t; }

// linkify appends text to parent, turning http(s) URLs into links that open in a new tab
function linkify(parent, text) {
  const re = /https?:\/\/[^\s)]+/g;
  let last = 0, m;
  while ((m = re.exec(text))) {
    if (m.index > last) parent.appendChild(document.createTextNode(text.slice(last, m.index)));
    const a = el('a', '', m[0]); a.href = m[0]; a.target = '_blank'; a.rel = 'noopener';
    parent.appendChild(a);
    last = re.lastIndex;
  }
  if (last < text.length) parent.appendChild(document.createTextNode(text.slice(last)));
}

// Lines from a server command (/usage, /memory, /key …), kept as typed so columns line up.
function addInfo(title, lines) {
  const box = el('div', 'info');
  if (title) box.appendChild(el('div', 'info-title', title));
  const list = Array.isArray(lines) ? lines : String(lines == null ? '' : lines).split('\n');
  if (list.length) {
    const pre = el('pre', 'info-lines');
    list.forEach((l, i) => { if (i) pre.appendChild(document.createTextNode('\n')); linkify(pre, String(l)); });
    box.appendChild(pre);
  }
  commandTurn().appendChild(box);
  scrollDown();
  return box;
}
function addInfoBlock(text) { return addInfo('', String(text == null ? '' : text).split('\n')); }

// A view from the server (kind "view": /help, /skills): title, grouped grids, footer.
function addView(v) {
  const box = el('div', 'cmdview');
  box.appendChild(el('div', 'cv-title', v.title || ''));
  for (const b of v.blocks || []) {
    const blk = el('div', 'cv-block');
    if (b.title) blk.appendChild(el('div', 'cv-head', b.title));
    const grid = el('div', 'cv-grid');
    for (const it of b.items || []) {
      grid.appendChild(el('span', 'cv-dot tone-' + (it.tone || 'muted'), it.dot || ''));
      const name = el('span', 'cv-name', it.name || '');
      if (it.name) name.title = it.name;
      grid.appendChild(name);
      const desc = el('span', 'cv-desc'); linkify(desc, it.desc || ''); grid.appendChild(desc);
      if (it.sub) { grid.appendChild(el('span', 'cv-dot', '')); grid.appendChild(el('span', 'cv-name', '')); grid.appendChild(el('span', 'cv-sub', it.sub)); }
    }
    blk.appendChild(grid);
    box.appendChild(blk);
  }
  if (v.footer) box.appendChild(el('div', 'cv-foot', v.footer));
  commandTurn().appendChild(box);
  scrollDown();
}

// a table from the server (kind "table": /models): dot + aligned columns
function addTable(v) {
  const box = el('div', 'cmdview');
  box.appendChild(el('div', 'cv-title', v.title || ''));
  const rows = v.rows || [];
  const cols = rows.reduce((n, r) => Math.max(n, (r.cells || []).length), 0);
  if (rows.length) {
    // the name column may shrink (its spans clip with …) so a phone keeps the state column on
    // screen; anything still too wide scrolls inside .tbl-wrap, never the chat column
    const wrap = el('div', 'tbl-wrap');
    const grid = el('div', 'tbl-grid');
    grid.style.gridTemplateColumns = '2ch minmax(0,max-content) ' + Array(Math.max(0, cols - 2)).fill('max-content').join(' ') + ' minmax(0,1fr)';
    for (const r of rows) {
      if (r.head) {
        const head = el('span', 'tbl-head ' + (SOURCE_TONE[r.tone] || 'tone-fg'), r.head);
        head.style.gridColumn = '1 / -1';
        grid.appendChild(head);
        continue;
      }
      grid.appendChild(el('span', 'tone-' + (r.tone || 'muted'), r.dot || ''));
      for (let i = 0; i < cols; i++) {
        const text = (r.cells || [])[i] || '';
        const cell = el('span', 'c' + i + (i === 2 ? ' tone-' + (r.tone || 'muted') : ''), text);
        if (i === 0) { cell.style.color = r.tone === 'muted' ? 'var(--muted)' : 'var(--fg)'; cell.title = text; }
        grid.appendChild(cell);
      }
    }
    wrap.appendChild(grid);
    box.appendChild(wrap);
  }
  for (const f of v.footer || []) box.appendChild(el('div', 'tbl-foot', f));
  commandTurn().appendChild(box);
  scrollDown();
}

function resetTranscript() {
  clear(transcript); state.turn = null; state.steps = null; state.textEl = null; state.tokens = 0; renderStatus();
  const hello = el('div', 'hello'); hello.id = 'hello';
  hello.appendChild(el('p', 'hello-title', 'what are we building today?'));
  hello.appendChild(el('p', 'muted', 'try:  fix a bug  ·  make a website  ·  /help'));
  transcript.appendChild(hello);
  scrollDown();
}

// Pickers
const tabQuery = () => (tabState.active > 1 ? '?tab=' + tabState.active : '');
const picker = (opts) => window.PrismCommands.picker(opts);

// /theme with no name: moving the highlight previews, enter keeps, esc puts the old one back.
function openThemePicker() {
  const orig = themeCache.name;
  picker({
    title: 'theme', hint: '↑↓ preview · enter keep · esc undo', noun: 'theme',
    items: THEMES.map((t) => ({ label: t, dot: '◆', dotTone: 'accent', state: t === orig ? 'current' : '' })),
    sel: Math.max(0, THEMES.indexOf(orig)),
    onHover: (it) => setTheme(it.label),
    onPick: (it) => { setTheme(it.label); hideHello(); addNote(`theme: ${it.label}`); },
    onClose: () => setTheme(orig),
  });
}

// /tabs lists the open chats; enter switches this page to one.
async function openTabsPicker() {
  const p = picker({ title: 'tabs', hint: 'enter switch · esc close', noun: 'tab', empty: 'no open chats', loading: true });
  let data = null;
  try { data = await api('/api/tabs'); } catch (e) { p.update({ loading: false, error: String(e.message || e) }); return; }
  const tabs = (data && data.tabs) || [];
  const cur = Math.max(0, tabs.findIndex((t) => t.ID === (tabState.active || (tabs[0] && tabs[0].ID))));
  p.update({
    loading: false, sel: cur,
    hint: tabs.length > 1 ? 'enter switch · esc close' : 'one chat open · new tabs start in the terminal (ctrl+t)',
    items: tabs.map((t, i) => ({
      label: `${i + 1}  ${t.Name}`, detail: t.Status, dot: i === cur ? '●' : '◌', dotTone: i === cur ? 'accent' : 'muted',
      state: (i === cur ? 'current · ' : '') + (t.Task || '—'), id: t.ID,
    })),
    onPick: (it) => { if (state.busy && it.id !== tabState.active) { addNote('wait until prism finishes, then switch tabs'); return; } switchTab(it.id); },
  });
}

// A picker the server built (kind "pick"); picking runs the row's command.
function openServerPicker(data, p) {
  const opts = {
    title: data.title, hint: data.hint, noun: data.noun, empty: data.empty, filterable: data.filterable !== false,
    items: data.items || [], sel: data.sel || 0, error: data.error, loading: false,
    onPick: (it) => { if (it.run) runCommand(it.run); },
  };
  if (p && p.open) p.update(opts); else picker(opts);
}

// /login: browser or device-code sign-in, polled until it finishes
let loginPolls = 0;

// Without clipboard access (plain http on a LAN address) copyText falls back to a
// hidden textarea and execCommand.
async function copyText(text) {
  try { if (navigator.clipboard?.writeText) { await navigator.clipboard.writeText(text); return true; } } catch (_) {}
  const t = el('textarea', 'offscreen');
  t.value = text; t.setAttribute('readonly', ''); t.setAttribute('aria-hidden', 'true');
  document.body.appendChild(t); t.select();
  let ok = false;
  try { ok = document.execCommand?.('copy') || false; } catch (_) {}
  t.remove();
  return ok;
}

// A sign-in link is hundreds of characters, so show a short link and a copy button,
// with the full address in the tooltip.
function signInLink(url) {
  const wrap = el('span', 'login-link');
  const a = el('a', '', 'open sign-in page');
  a.href = url; a.target = '_blank'; a.rel = 'noopener'; a.title = url;
  const copy = el('button', 'web-copy login-copy', 'copy link');
  copy.type = 'button';
  copy.setAttribute('aria-label', 'Copy the sign-in link');
  copy.title = url;
  copy.addEventListener('click', async () => {
    const ok = await copyText(url);
    copy.textContent = ok ? 'copied' : 'copy failed';
    copy.setAttribute('aria-label', ok ? 'Sign-in link copied' : 'Could not copy the sign-in link');
  });
  wrap.append(a, ' ', copy);
  return wrap;
}

let loginSeq = 0;
async function startLogin(data) {
  const box = addInfo(data.title || ('sign in to ' + data.id), []);
  box.classList.add('login');
  const pre = el('pre', 'info-lines');
  if (data.browser) {
    pre.appendChild(document.createTextNode('opening your browser…\nif nothing opens: '));
    pre.appendChild(signInLink(data.url));
    const w = window.open(data.url, '_blank', 'noopener');
    void w;
  } else {
    pre.appendChild(document.createTextNode('open  '));
    if (String(data.url || '').length > 60) pre.appendChild(signInLink(data.url)); else linkify(pre, data.url);
    pre.appendChild(document.createTextNode('\ntype  ')); pre.appendChild(el('span', 'login-code', data.code || ''));
  }
  pre.appendChild(document.createTextNode('\nprism keeps checking; the models appear in /models when you are done'));
  box.appendChild(pre);
  const status = el('div', 'login-state', 'waiting for the sign-in…');
  status.setAttribute('role', 'status');
  status.id = 'login-state-' + (++loginSeq);
  const me = ++loginPolls;
  const tq = tabQuery(); // the tab that started the sign-in, even after a tab switch
  let done = false; // signed in by a pasted code, cancelled with esc, or over
  const end = (cls, text) => {
    done = true;
    if (form) form.remove();
    status.className = 'login-state' + (cls ? ' ' + cls : '');
    status.textContent = text;
  };
  // Some providers (Grok) show a code on their page instead of coming back here.
  // The user pastes it (or the whole link) and the router finishes.
  let form = null;
  if (data.browser) {
    form = el('form', 'login-paste');
    form.setAttribute('aria-label', 'finish the ' + data.id + ' sign-in');
    const fid = 'login-paste-' + loginSeq;
    const label = el('label', 'login-paste-label', 'paste the code or link from the sign-in page');
    label.htmlFor = fid;
    const row = el('div', 'login-paste-row');
    const caret = el('span', 'pick-caret', '›');
    caret.setAttribute('aria-hidden', 'true');
    const box2 = el('input', 'login-paste-input');
    box2.id = fid; box2.type = 'text'; box2.autocomplete = 'off'; box2.spellcheck = false;
    box2.setAttribute('autocapitalize', 'off'); box2.setAttribute('autocorrect', 'off');
    box2.placeholder = 'code or link';
    box2.setAttribute('aria-describedby', status.id);
    const go = el('button', 'login-paste-go', 'finish');
    go.type = 'submit';
    row.append(caret, box2, go);
    const head = el('div', 'login-paste-head');
    head.append(label, el('span', 'login-paste-hint', 'enter finish · esc cancel'));
    form.append(head, row);
    let sending = false;
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const code = box2.value.trim();
      if (done || sending || me !== loginPolls) { if (me !== loginPolls) form.remove(); return; }
      if (!code) { status.className = 'login-state err'; status.textContent = 'paste the code from the sign-in page first'; box2.focus(); return; }
      sending = true; box2.disabled = true; go.disabled = true;
      status.className = 'login-state'; status.textContent = 'checking the code…';
      let r = null;
      try { r = await api('/api/login' + tq, { id: data.id, code }); }
      catch (err) { r = { ok: false, error: String(err.message || err) }; }
      sending = false; box2.disabled = false; go.disabled = false;
      if (done || me !== loginPolls) { if (me !== loginPolls) form.remove(); return; }
      if (r && r.ok) {
        end('ok', (r.text || ('signed in · ' + (r.models || 0) + ' models')) + (r.note ? ' · ' + r.note : ''));
        refreshInfo();
        $('input').focus();
        return;
      }
      status.className = 'login-state err';
      status.textContent = (r && r.error) || 'that code did not work; copy it again';
      box2.focus(); box2.select();
    });
    box2.addEventListener('keydown', (e) => {
      if (e.key !== 'Escape') return;
      e.preventDefault(); e.stopPropagation();
      end('', 'stopped waiting · run /login ' + data.id + ' to sign in again');
      $('input').focus();
    });
    box.appendChild(form);
  }
  box.appendChild(status);
  scrollDown();
  if (form && !matchMedia('(pointer: coarse)').matches) form.querySelector('input').focus({ preventScroll: true });
  const deadline = Date.now() + (data.expires || 900) * 1000;
  let wait = data.browser ? 3 : Math.max(3, data.interval || 5);
  while (Date.now() < deadline && me === loginPolls && !done) {
    await sleep(wait * 1000);
    if (me !== loginPolls || done) { form?.remove(); return; }
    let r;
    try { r = await api('/api/login' + tq, { id: data.id, browser: !!data.browser }); }
    catch (e) {
      if (me !== loginPolls || done) { form?.remove(); return; }
      status.textContent = 'still checking… (' + String(e.message || e) + ')'; continue;
    }
    if (me !== loginPolls) { form?.remove(); return; }
    if (!r || done) continue;
    if (r.status === 'pending' && r.message) {
      status.className = 'login-state err';
      status.textContent = r.message;
    }
    if (r.status === 'ok') {
      end('ok', (r.text || 'signed in') + (r.note ? ' · ' + r.note : ''));
      refreshInfo();
      return;
    }
    if (r.status === 'error') { end('err', data.id + ': ' + (r.message || 'sign-in failed')); return; }
    if (!data.browser && r.interval) wait = Math.max(3, r.interval);
  }
  if (me !== loginPolls) form?.remove();
  else if (!done) end('err', 'the sign-in timed out; run /login ' + data.id + ' again');
}

// /beam and /quiet are saved on the server so a reload keeps them.
function saveSetting(cmd, on) {
  if (DEMO || !window.PrismCommands) return;
  window.PrismCommands.runServer(cmd + (on ? ' on' : ' off'), tabQuery()).catch(() => {});
}

async function refreshInfo() {
  try {
    const info = await api('/api/info' + tabQuery());
    if (info) { state.modelsReady = info.models_ready; state.pinned = info.model || ''; state.pinSource = info.source || ''; state.plan = !!info.plan; renderStatus(); }
  } catch { /* ignore */ }
}

// waits up to 5s for a running chat to end (after stop) before /clear
async function idle() { for (let i = 0; i < 50 && state.busy; i++) await sleep(100); }

// commands that open a picker the server fills: show it at once with "loading…"
const SERVER_PICKERS = { '/model': 'model', '/login': 'subscriptions', '/logins': 'subscriptions', '/resume': 'earlier chats', '/continue': 'earlier chats' };

// /new /theme /beam /quiet /web /files /open /tabs are handled here; the rest go to the server.
async function runCommand(line) {
  const text = String(line || '').trim();
  const fromTab = tabState.active;
  // a message a command sends goes to the tab it was typed in, even if you switched meanwhile
  const sendHere = (message) => { if (tabState.active === fromTab) send(message); else addNote('switched tabs, so ' + text.split(/\s+/)[0] + ' did not send'); };
  if (!text) return;
  if (text.charAt(0) !== '/') { send(text); return; }
  const [cmd, ...rest] = text.split(/\s+/);
  switch (cmd) {
    case '/new':
      try { await api('/api/new' + tabQuery(), {}); } catch (e) { addError('could not start new session', String(e.message)); return; }
      resetTranscript(); state.pinned = ''; state.pinSource = ''; state.source = ''; state.plan = false; state.model = null; renderStatus();
      say('hello', 2400); setMood('happy', 1500);
      return;
    case '/theme': case '/themes':
      if (!rest[0]) { openThemePicker(); return; }
      if (!setTheme(rest[0])) addNote(`themes: ${THEMES.join(', ')}`);
      else addNote(`theme: ${rest[0]}`);
      return;
    case '/beam':
      beamEnabled = rest[0] ? rest[0] !== 'off' : !beamEnabled;
      addNote(`finish beam ${beamEnabled ? 'on' : 'off'}`);
      saveSetting('/beam', beamEnabled);
      return;
    case '/quiet':
      quiet = rest[0] ? rest[0] !== 'off' : !quiet;
      if (quiet) hush();
      addNote(quiet ? 'shard is quiet' : 'shard can talk again');
      saveSetting('/quiet', quiet);
      return;
    case '/files':
      if (window.PrismFiles) window.PrismFiles.show(); else addNote('the file viewer did not load');
      return;
    case '/open':
      if (!window.PrismFiles) { addNote('the file viewer did not load'); return; }
      if (rest.length) window.PrismFiles.openFile(rest.join(' ')); else window.PrismFiles.show();
      return;
    case '/tabs':
      if (DEMO) { addNote('/tabs needs the prism server'); return; }
      openTabsPicker();
      return;
    case '/help':
      if (DEMO || !window.PrismCommands) { showHelp(); return; }
      break;
    case '/clear':
      if (DEMO) { resetTranscript(); return; }
      if (state.busy) { await stop(); await idle(); }
      break;
    case '/web': {
      const note = el('div', 'note');
      const link = el('a', 'web-link', location.origin);
      link.href = location.origin;
      const copy = el('button', 'web-copy', 'copy');
      copy.type = 'button';
      copy.setAttribute('aria-label', 'Copy browser link');
      copy.addEventListener('click', async () => {
        let copied = false;
        try {
          if (navigator.clipboard?.writeText) {
            await navigator.clipboard.writeText(location.origin);
            copied = true;
          }
        } catch (_) {}
        if (!copied) {
          const selection = window.getSelection();
          const range = document.createRange();
          range.selectNodeContents(link);
          selection.removeAllRanges();
          selection.addRange(range);
          copied = document.execCommand?.('copy') || false;
          if (copied) selection.removeAllRanges();
        }
        if (copied) { copy.textContent = 'copied'; copy.setAttribute('aria-label', 'Browser link copied'); }
      });
      note.append(link, ' ', copy, el('div', '', handoffSeen ? 'to go back to the terminal, close this tab or press enter there' : 'this chat is already in your browser'));
      curTurn().appendChild(note);
      state.textEl = null;
      scrollDown();
      return;
      }
    default: break;
  }
  if (DEMO || !window.PrismCommands) { addNote(`${cmd} needs the prism server`); return; }
  let p = null;
  if (rest.length === 0 && SERVER_PICKERS[cmd]) p = picker({ title: SERVER_PICKERS[cmd], loading: true });
  else if (cmd === '/mcp' && rest.length === 1 && rest[0].toLowerCase() === 'add') p = picker({ title: 'add a tool server', loading: true });
  // /compact is a model request, so the page is busy like a chat (live line, esc stops it).
  const compacting = cmd === '/compact';
  if (compacting) {
    if (state.busy) { hideHello(); addNote('wait until prism finishes, then run /compact'); return; }
    hideHello(); addNote('compacting the conversation…');
    setBusy(true); setPhase('tool');
  }
  let data = null;
  try { data = await window.PrismCommands.runServer(text, tabQuery()); }
  catch (e) { if (p) p.close(); addError('command failed', String((e && e.message) || e)); return; }
  finally { if (compacting) setBusy(false); }
  if (data && data.pick && data.kind !== 'pick') data = Object.assign({}, data.pick);
  const action = data && (data.kind || data.action || data.type);
  if (p && action !== 'pick') p.close();
  switch (action) {
    case 'pick':
      if (p && !p.open) return; // closed while loading
      openServerPicker(data, p);
      return;
    case 'info':
      addInfo(data.title, Array.isArray(data.lines) ? data.lines : String(data.text || data.detail || '').split('\n'));
      if (data.tokens != null) { state.tokens = data.tokens; if (data.window) state.window = data.window; renderStatus(); }
      return;
    case 'view': addView(data); return;
    case 'table': addTable(data); return;
    case 'note': hideHello(); addNote(data.text || ''); scrollDown(); return;
    case 'plan':
      if (tabState.active !== fromTab) { addNote('switched tabs, so /plan changed the other tab'); return; }
      state.plan = !!data.plan; if (!state.plan) state.planPick = null; renderStatus();
      if (data.text) { hideHello(); addNote(data.text); }
      if (data.message) sendHere(data.message);
      return;
    case 'model':
      state.pinned = data.model || ''; state.pinSource = data.source || ''; state.source = '';
      renderStatus();
      hideHello(); addNote(data.text || ('model: ' + (data.model || 'auto')));
      scrollDown();
      return;
    case 'login': startLogin(data); return;
    case 'clear':
      loginPolls++;
      resetTranscript(); state.pinned = ''; state.pinSource = ''; state.source = ''; state.plan = false; state.model = null; renderStatus();
      say('hello', 2400); setMood('happy', 1500);
      return;
    case 'reload':
      try {
        const hist = await api('/api/history' + tabQuery());
        resetTranscript();
        if (hist) loadHistory(hist.entries);
        state.pinned = data.model || ''; state.pinSource = data.source || ''; state.source = ''; renderStatus();
        addNote('chat reopened · keep going');
      } catch (e) { addError('could not reload', String((e && e.message) || e)); }
      return;
    case 'send': sendHere(data.message || data.text || text); return;
    case 'error': addError(data.headline || 'command failed', data.detail || data.text || ''); scrollDown(); return;
    default:
      if (data && (data.text || data.lines)) addInfo(data.title || '', Array.isArray(data.lines) ? data.lines : String(data.text).split('\n'));
      else addNote(`unknown command: ${cmd}`);
      return;
  }
}

// History
function loadHistory(entries) {
  if (!entries || !entries.length) return;
  hideHello();
  for (const e of entries) {
    switch (e.kind) {
      case 'user': addUser(e.text || ''); break;
      case 'text': state.textEl = null; addText(e.text || ''); state.textEl = null; break;
      case 'tool': addStep({ tool: e.tool, arg: e.arg, ok: e.ok, summary: e.summary }); break;
      case 'note': addNote(e.text || ''); break;
      case 'crew': if (e.crew) crewFromHistory(e.crew); break;
      case 'error': addError(e.text || 'error'); break;
      default: break;
    }
  }
  collapseOldTurns();
  scrollDown();
}

// Demo mode (no server)
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
async function* demoCrew(d) {
  const tasks = [
    { id: 'api', role: 'code', task: 'build the checkout api with validation, error states and a retry friendly submit flow' },
    { id: 'ui', role: 'fast', task: 'build the checkout form' },
    { id: 'docs', role: 'code', task: 'write the readme' },
  ];
  yield { type: 'route', route: 'nemotron-3-ultra-550b-a55b', wait_ms: 0 };
  yield { type: 'tool_start', id: 'c1', tool: 'crew', arg: '3 workers' };
  yield { type: 'crew_start', tasks };
  for (const t of tasks) yield { type: 'crew_worker', id: t.id, model: 'auto:' + t.role, status: 'queued', steps: 0, note: '' };
  await d(1500);
  yield { type: 'crew_worker', id: 'api', model: 'nvidia/nemotron-3-ultra:free', status: 'running', steps: 2, note: 'writing api.go' };
  yield { type: 'crew_worker', id: 'ui', model: 'groq/llama-4-scout:free', status: 'running', steps: 1, note: '' };
  await d(2500);
  yield { type: 'crew_worker', id: 'api', model: 'nvidia/nemotron-3-ultra:free', status: 'done', steps: 5, note: '' };
  yield { type: 'crew_worker', id: 'ui', model: 'groq/llama-4-scout:free', status: 'failed', steps: 3, note: 'the router gave up' };
  yield { type: 'crew_worker', id: 'docs', model: 'auto:code', status: 'cancelled', steps: 0, note: 'stopped before it started' };
  yield { type: 'crew_worker', id: 'review', model: 'auto:review', status: 'running', steps: 1, note: 'reading api.go' };
  await d(2500);
  yield { type: 'crew_worker', id: 'review', model: 'auto:review', status: 'done', steps: 2, note: '' };
  yield { type: 'crew_done', summary: '1 of 3 workers done, 2 did not finish' };
  yield { type: 'tool_done', id: 'c1', ok: true, summary: '1 of 3 workers done, 2 did not finish' };
  yield { type: 'text', text: 'the api is in; the form needs another pass.' };
  yield { type: 'done', seconds: 9.8, model: 'nemotron-3-ultra-550b-a55b', tools: 1, tests_passed: false };
}
async function* demoStream(message, signal) {
  const fast = params.get('demo') === 'fast';
  const d = (ms) => sleep(fast ? 10 : ms, signal);
  if (/crew/i.test(message)) { yield* demoCrew(d); return; }
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

// Input
const input = $('input');
function autosize() { input.style.height = 'auto'; input.style.height = Math.min(180, input.scrollHeight) + 'px'; }
input.addEventListener('input', autosize);
input.addEventListener('input', () => { if (!input.value) offerPlan(); });
input.addEventListener('focus', offerPlan);
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
    if (state.ask) return; // the page's key handler answers no; with text in the box esc does nothing
    const suggest = $('cmd-suggest');
    if (suggest && !suggest.hidden) { suggest.hidden = true; return; }
    if (state.busy) stop(); else { input.value = ''; autosize(); }
  }
});
$('composer').addEventListener('submit', (e) => e.preventDefault());
$('theme').addEventListener('change', (e) => setTheme(e.target.value));
addEventListener('keydown', (e) => {
  const a = document.activeElement;
  // an open picker owns the keys: esc closes it, a typed letter goes to its filter, not the chat box
  const PC = window.PrismCommands;
  if (PC && PC.pickerOpen && PC.pickerOpen() && !(a && a.closest && a.closest('.pick'))) {
    if (e.key === 'Escape') { e.preventDefault(); PC.closePicker(); return; }
    if (a !== input && e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) { PC.focusPicker(); return; }
  }
  // y / a / n (esc for no) answer a question before a risky command.
  const ans = askKey(e);
  if (ans) { e.preventDefault(); answerAsk(ans); return; }
  if (e.key === 'Escape' && state.busy && document.activeElement !== input && !state.ask) stop();
  if (a !== input && !['SELECT', 'BUTTON', 'A', 'INPUT', 'TEXTAREA'].includes(a.tagName) && !a.isContentEditable && e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) input.focus();
});

// Shard buddy drawing (shard.js when it is loaded)
function drawBuddy(now) {
  const S = window.PrismShard;
  if (S && typeof S.drawSmall === 'function') {
    if (buddy.moodUntil && now > buddy.moodUntil) setMood(state.busy ? 'working' : 'idle');
    const charge = beamStop ? 1 : Math.max(buddy.charge, state.busy ? Math.min(1, (now - buddy.busySince) / 20000) : 0);
    const look = now - buddy.lookAt < 700 ? -1 : 0;
    const md = beamStop ? 'working' : buddy.mood;
    S.drawSmall(shardCv, themeCache, { md, mood: md, charge, look, wave: now < buddy.waveUntil }, now);
    return;
  }
  drawShard(now);
}

// Terminal handoff
let handoffSeen = false;
function showHandoff() {
  handoffSeen = true;
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
  o.style.cssText = 'position:fixed;inset:0;z-index:90;display:flex;flex-direction:column;gap:6px;align-items:center;justify-content:center;text-align:center;'
    + 'background:rgba(0,0,0,.72);backdrop-filter:blur(2px);color:var(--fg);font-size:15px;letter-spacing:.02em';
  o.setAttribute('role', 'alertdialog');
  o.setAttribute('aria-label', 'prism disconnected');
  o.appendChild(el('p', '', handoffSeen ? 'this chat went back to the terminal' : 'prism stopped'));
  const hint = el('p', '', handoffSeen
    ? 'you can close this tab. if you closed the terminal too, run prism continue to pick the chat up again'
    : 'you can close this tab. run prism continue to pick the chat up again');
  hint.style.cssText = 'color:var(--muted);font-size:13px;margin:0';
  o.appendChild(hint);
  document.body.appendChild(o);
}
// Liveness ping every 3s. Raw fetch, never the demo stub.
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

// Tab strip
const tabStrip = $('tab-strip');
let tabState = { tabs: [], version: 0, active: 0 };
let tabPollTimer = null;
let tabSwitchGen = 0; // incremented on each switchTab to drop stale history loads

function statusDotClass(status) {
  switch (status) {
    case 'working':  return 'tab-dot tab-dot-working';
    case 'waiting':  return 'tab-dot tab-dot-waiting';
    case 'done':     return 'tab-dot tab-dot-done';
    case 'error':    return 'tab-dot tab-dot-error';
    default:         return 'tab-dot tab-dot-idle';
  }
}

function renderTabStrip() {
  const tabs = tabState.tabs;
  if (!tabs || tabs.length <= 1) { tabStrip.hidden = true; return; }
  tabStrip.hidden = false;
  tabStrip.innerHTML = '';
  tabs.forEach((tab, i) => {
    const btn = document.createElement('button');
    btn.className = 'tab-btn';
    btn.type = 'button';
    btn.setAttribute('role', 'tab');
    btn.setAttribute('aria-selected', String(tab.ID === tabState.active));
    btn.setAttribute('aria-label', `Tab ${i + 1}: ${tab.Name}, ${tab.Status}`);

    const num = document.createElement('span');
    num.className = 'tab-num';
    num.textContent = i + 1;

    const dot = document.createElement('span');
    dot.className = statusDotClass(tab.Status);
    dot.title = tab.Status + (tab.Task ? ': ' + tab.Task : '');

    const name = document.createElement('span');
    name.className = 'tab-name';
    name.textContent = tab.Name;

    btn.appendChild(num);
    btn.appendChild(dot);
    btn.appendChild(name);
    btn.addEventListener('click', () => switchTab(tab.ID));
    tabStrip.appendChild(btn);
  });
}

function switchTab(id) {
  if (id === tabState.active) return;
  // Block switching while a chat is running to avoid mixing streams.
  if (state.busy) return;
  tabState.active = id;
  renderTabStrip();
  // A generation token drops history responses from an earlier switch.
  const gen = ++tabSwitchGen;
  resetTranscript();
  api('/api/history?tab=' + id).then((hist) => {
    if (gen !== tabSwitchGen) return; // stale response from a superseded switch
    if (hist) loadHistory(hist.entries);
  }).catch(() => {});
  refreshInfo();
}

async function pollTabs() {
  if (DEMO) return;
  const started = Date.now();
  try {
    const data = await api('/api/tabs?since=' + tabState.version);
    if (data && data.tabs) {
      tabState.tabs = data.tabs;
      tabState.version = data.version || 0;
      if (!tabState.active && data.tabs.length > 0) tabState.active = data.tabs[0].ID;
      // The active tab was closed: switch to the first one.
      const activeStillExists = data.tabs.some((t) => t.ID === tabState.active);
      if (!activeStillExists && data.tabs.length > 0) {
        const newId = data.tabs[0].ID;
        tabState.active = newId;
        if (!state.busy) {
          const gen = ++tabSwitchGen;
          resetTranscript();
          api('/api/history?tab=' + newId).then((hist) => {
            if (gen !== tabSwitchGen) return;
            if (hist) loadHistory(hist.entries);
          }).catch(() => {});
        }
      }
      renderTabStrip();
    }
  } catch { /* ignore */ }
  // The server holds the request while tabs exist; a quick answer means no tabs or an error.
  tabPollTimer = setTimeout(pollTabs, Date.now() - started < 1000 ? 5000 : 100);
}

// Boot
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
      state.modelsReady = info.models_ready; state.pinned = info.model || ''; state.pinSource = info.source || ''; state.plan = !!info.plan; renderStatus();
      if (info.beam != null) beamEnabled = !!info.beam;
      if (info.quiet != null) quiet = !!info.quiet;
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
      run: (text) => { input.value = ''; autosize(); runCommand(text).finally(() => { if (!state.busy) scrollDown(); }); },
    });
  }
  startHeartbeat();
  pollTabs();
  if (DEMO) {
    setTimeout(() => {
      input.value = ''; send(params.get('demo') === 'crew' ? 'build the checkout with a crew' : 'make my todo list persist between reloads, then run the tests');
    }, params.get('demo') === 'fast' ? 50 : 3000);
  }
}
boot();
