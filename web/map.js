// PrismMap: the project as a constellation, like Obsidian's graph. Stars are files,
// sized by how many files use them and coloured by folder; beams are imports and
// Markdown links. Folders gather into loose galaxies. Hover lights up a star's links,
// click flies to it, double-click opens it. PrismMap.mount(canvas, data, { onOpen }).
(() => {
  const css = name => getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  const base = p => p.slice(p.lastIndexOf('/') + 1)
  const top = p => (p.includes('/') ? p.slice(0, p.indexOf('/')) : '')
  const reduced = () => matchMedia('(prefers-reduced-motion: reduce)').matches
  const isTest = p => /(^|\/)(tests?|__tests__|spec)\//.test(p) || /[._-](test|spec)\.[a-z]+$/.test(p)

  if (!document.querySelector('link[href="map.css"]')) {
    const l = document.createElement('link')
    l.rel = 'stylesheet'; l.href = 'map.css'
    document.head.append(l)
  }

  function el(tag, cls, text) {
    const n = document.createElement(tag)
    if (cls) n.className = cls
    if (text != null) n.textContent = text
    return n
  }

  // rgba turns a CSS colour (#rrggbb or rgb()) into rgba() with alpha a.
  function rgba(c, a) {
    let m = /^#([0-9a-f]{6})$/i.exec(c)
    if (m) { const n = parseInt(m[1], 16); return `rgba(${n >> 16},${(n >> 8) & 255},${n & 255},${a})` }
    m = /rgba?\(([^)]+)\)/.exec(c)
    if (m) { const [r, g, b] = m[1].split(',').map(s => parseFloat(s)); return `rgba(${r},${g},${b},${a})` }
    return c
  }

  function mount(canvas, data, opts) {
    const ctx = canvas.getContext('2d')
    const section = canvas.closest('section') || canvas.parentElement
    const stage = el('div', 'map-stage')
    canvas.replaceWith(stage)
    stage.append(canvas)

    const card = el('div', 'map-card'); card.hidden = true
    const tools = el('div', 'map-tools')
    const legend = el('div', 'map-legend')
    const clock = el('div', 'map-time'); clock.hidden = true
    stage.append(tools, legend, card, clock)

    const button = (text, title, on) => { const b = el('button', 'map-btn', text); b.type = 'button'; b.title = title; b.setAttribute('aria-pressed', 'false'); b.addEventListener('click', on); tools.append(b); return b }
    const find = el('input', 'map-find'); find.placeholder = 'find a star…'; find.hidden = true; find.setAttribute('aria-label', 'Find a file on the map')
    const findBtn = button('/ find', 'Find a file and fly to it', () => openFind())
    tools.append(find)
    const trailBtn = button('trail', "The path Prism took through the files in this chat", () => { showTrail = !showTrail; trailBtn.setAttribute('aria-pressed', String(showTrail)); kick() })
    const testsBtn = button('tests', 'Show or hide test files', () => { hideTests = !hideTests; testsBtn.setAttribute('aria-pressed', String(!hideTests)); reload() })
    testsBtn.setAttribute('aria-pressed', 'true')
    const playBtn = button('▶ time-lapse', 'Replay how the project grew, from its git history', () => (timelapse ? stopTimelapse() : startTimelapse()))
    const introBtn = button('✦ intro', 'Play the intro: the project assembles itself', () => startIntro(true))
    const autoBtn = button('auto', 'Play a short intro every time the map opens', () => {
      try { localStorage.setItem('prism-map-intro', autoIntro() ? 'off' : 'on') } catch { /* private window */ }
      autoBtn.setAttribute('aria-pressed', String(autoIntro()))
    })
    autoBtn.setAttribute('aria-pressed', String(autoIntro()))
    tools.append(el('span', 'spacer'))
    const wideBtn = button('⤢', 'Bigger map', () => { const p = section.closest('.files-panel'); if (p) { p.classList.toggle('map-wide'); wideBtn.setAttribute('aria-pressed', String(p.classList.contains('map-wide'))); fitted = false; setTimeout(() => { fit(); kick() }, 60) } })
    const hint = section.querySelector('.file-msg')
    if (hint) { hint.textContent = 'hover a star · click to fly · double-click to open'; hint.title = 'drag to move · scroll to zoom · double-click empty space to fit · / to find' }

    let nodes = [], edges = [], byPath = new Map(), heat = 1, groups = [], anchors = new Map()
    let view = { x: 0, y: 0, k: 1 }, goal = null
    let hover = null, chosen = null, drag = null, pan = null, moved = false, frame = 0, alive = true
    let palette = [], fitted = false, last = data, linkedOnly = false, center = null, hideTests = false, hidden = new Set()
    let showTrail = false, trail = [], seen = new Set(), born = new Map(), first = true
    let timelapse = null, history = null
    let intro = null, labelsUntil = 0, galaxies = [], pendingData = null
    let stars = [], sprites = new Map(), pal = {}

    function colors() {
      palette = ['--s0', '--s1', '--s2', '--s3', '--s4', '--s5', '--s6'].map(css).filter(Boolean)
      if (!palette.length) palette = ['#c69bff']
      pal = { bg: css('--bg') || '#0b0b10', fg: css('--fg') || '#e8e6f0', muted: css('--muted') || '#6e6a86', accent: css('--s6') || palette[0], out: css('--s4') || '#6fd3ff', in: css('--s1') || '#ffa25f', mono: css('--mono') || 'ui-monospace, monospace' }
      sprites.clear()
    }
    const colorOf = i => (i < palette.length ? palette[i] : ['#d9d6e6', '#9a96b0', '#f2c6de', '#c6e2f2'][(i - palette.length) % 4])

    // A soft glowing dot, drawn once per colour and stamped for every star.
    function sprite(color) {
      let s = sprites.get(color)
      if (s) return s
      s = document.createElement('canvas'); s.width = s.height = 64
      const g = s.getContext('2d'), grad = g.createRadialGradient(32, 32, 0, 32, 32, 32)
      grad.addColorStop(0, 'rgba(255,255,255,1)')
      grad.addColorStop(0.12, rgba(color, 1))
      grad.addColorStop(0.35, rgba(color, 0.35))
      grad.addColorStop(1, rgba(color, 0))
      g.fillStyle = grad; g.fillRect(0, 0, 64, 64)
      sprites.set(color, s)
      return s
    }

    function backdrop() {
      stars = []
      let seed = 7
      const rnd = () => ((seed = (seed * 16807) % 2147483647) / 2147483647)
      for (let i = 0; i < 260; i++) stars.push({ x: rnd(), y: rnd(), z: 0.15 + rnd() * 0.85, r: rnd() < 0.08 ? 1.4 : 0.7, t: rnd() * 6.28, c: rnd() < 0.15 ? palette[Math.floor(rnd() * palette.length)] : null })
    }

    function load(d) {
      if (intro) endIntro()
      last = d
      colors()
      if (!stars.length) backdrop()
      let files = (d.nodes || []).filter(n => !n.dir && n.git !== 'D')
      if (hideTests) files = files.filter(n => !isTest(n.path))
      files = files.filter(n => !hidden.has(top(n.path)))
      files.sort((a, b) => (b.rank || 0) - (a.rank || 0) || ((b.importedBy || []).length - (a.importedBy || []).length))
      let keep = files.slice(0, 1500)
      if (center && files.some(n => n.path === center)) {
        const by = new Map(files.map(n => [n.path, n]))
        const near = new Map([[center, 0]])
        let edge = [center]
        for (let step = 1; step <= 2; step++) {
          const next = []
          for (const p of edge) for (const q of [...(by.get(p).imports || []), ...(by.get(p).importedBy || [])]) if (by.has(q) && !near.has(q)) { near.set(q, step); next.push(q) }
          edge = next
        }
        keep = files.filter(n => near.has(n.path))
      }
      const all = (d.nodes || []).filter(n => !n.dir)
      const allGroups = [...new Set(all.map(n => top(n.path)))].sort()
      groups = allGroups
      // Galaxies: one anchor per folder on a spiral, bigger folders nearer the middle.
      const sizes = new Map()
      for (const n of keep) sizes.set(top(n.path), (sizes.get(top(n.path)) || 0) + 1)
      const order = [...sizes.keys()].sort((a, b) => sizes.get(b) - sizes.get(a))
      // Each galaxy sits clear of the biggest one, so the clouds don't run together.
      const radius = g => 24 + Math.sqrt(sizes.get(g) || 1) * 26
      const r0 = order.length ? radius(order[0]) : 0
      anchors = new Map(order.map((g, i) => {
        if (i === 0) return [g, { x: 0, y: 0 }]
        const d = r0 + radius(g) + 70 + i * 18
        return [g, { x: Math.cos(i * 2.39996) * d, y: Math.sin(i * 2.39996) * d }]
      }))

      const old = byPath, now = performance.now()
      byPath = new Map()
      nodes = keep.map((n, i) => {
        const a = anchors.get(top(n.path)) || { x: 0, y: 0 }
        const ang = i * 2.39996, r = 14 * Math.sqrt(i % 60 + 1)
        const node = old.get(n.path) || { x: a.x + Math.cos(ang) * r, y: a.y + Math.sin(ang) * r, vx: 0, vy: 0 }
        if (!old.get(n.path) && !first) born.set(n.path, now)
        if (node.ox == null) {
          // A home spot inside its galaxy, on one of two loose spiral arms.
          let h = 2166136261
          for (const ch of n.path) h = Math.imul(h ^ ch.charCodeAt(0), 16777619)
          const u = ((h >>> 0) % 1000) / 1000, v = ((h >>> 10) % 1000) / 1000
          const rr = Math.sqrt(u) * (24 + Math.sqrt(sizes.get(top(n.path)) || 1) * 26)
          const arm = (v < 0.5 ? 0 : Math.PI) + rr * 0.035 + (v - 0.5) * 0.9
          node.ox = Math.cos(arm) * rr; node.oy = Math.sin(arm) * rr
        }
        Object.assign(node, { path: n.path, agent: n.agent, git: n.git, lang: n.lang, symbols: n.symbols || [], group: top(n.path), color: colorOf(groups.indexOf(top(n.path))), out: 0, inn: 0 })
        byPath.set(n.path, node)
        if (n.agent && !seen.has(n.path)) { seen.add(n.path); trail.push(n.path) }
        return node
      })
      first = false
      edges = []
      for (const n of keep) for (const t of n.imports || []) {
        const a = byPath.get(n.path), b = byPath.get(t)
        if (a && b && a !== b) { edges.push([a, b]); a.out++; b.inn++ }
      }
      if (linkedOnly) nodes = nodes.filter(n => n.out + n.inn > 0)
      for (const n of nodes) n.r = 2.6 + Math.min(11, Math.sqrt(n.inn) * 2.4 + Math.sqrt(n.out) * 0.7)
      heat = Math.max(heat, old.size ? 0.35 : 1)
      paintLegend()
      kick()
    }
    const reload = () => { fitted = false; heat = Math.max(heat, 0.6); load(last) }

    function paintLegend() {
      legend.replaceChildren()
      groups.slice(0, 14).forEach((g, i) => {
        const b = el('button', hidden.has(g) ? 'off' : '')
        b.type = 'button'
        const dot = el('i'); b.style.setProperty('--c', colorOf(i))
        b.append(dot, document.createTextNode(g ? g + '/' : 'top level'))
        b.title = 'Click to hide or show this folder'
        b.addEventListener('click', () => { hidden.has(g) ? hidden.delete(g) : hidden.add(g); reload() })
        legend.append(b)
      })
    }

    // Forces: each star is pulled to its galaxy, pushed from close neighbours
    // (looked up through a grid so big projects stay smooth) and held by its beams.
    function step() {
      const cell = 90, grid = new Map()
      for (const n of nodes) {
        const k = Math.floor(n.x / cell) + ',' + Math.floor(n.y / cell)
        let list = grid.get(k); if (!list) grid.set(k, (list = [])); list.push(n)
      }
      for (const a of nodes) {
        const anc = anchors.get(a.group) || { x: 0, y: 0 }
        a.vx += (anc.x + a.ox - a.x) * 0.012 * heat; a.vy += (anc.y + a.oy - a.y) * 0.012 * heat
        const gx = Math.floor(a.x / cell), gy = Math.floor(a.y / cell)
        for (let i = -1; i <= 1; i++) for (let j = -1; j <= 1; j++) {
          const list = grid.get((gx + i) + ',' + (gy + j)); if (!list) continue
          for (const b of list) {
            if (b === a) continue
            let dx = a.x - b.x, dy = a.y - b.y, d2 = dx * dx + dy * dy
            if (d2 < 1) { dx = Math.random() - 0.5; dy = Math.random() - 0.5; d2 = 1 }
            if (d2 > 900) continue
            const f = 60 * heat / d2
            a.vx += dx * f; a.vy += dy * f
          }
        }
      }
      for (const [a, b] of edges) {
        const dx = b.x - a.x, dy = b.y - a.y, d = Math.sqrt(dx * dx + dy * dy) || 1
        const f = (d - (a.group === b.group ? 50 : 140)) * 0.012 * heat / d
        a.vx += dx * f; a.vy += dy * f; b.vx -= dx * f; b.vy -= dy * f
      }
      for (const n of nodes) {
        if (n === drag) { n.vx = n.vy = 0; continue }
        n.vx *= 0.55; n.vy *= 0.55
        n.x += Math.max(-18, Math.min(18, n.vx)); n.y += Math.max(-18, Math.min(18, n.vy))
      }
      heat = Math.max(0, heat * 0.984 - 0.0008)
    }

    function size() {
      const r = canvas.getBoundingClientRect(), dpr = devicePixelRatio || 1
      if (canvas.width !== Math.round(r.width * dpr) || canvas.height !== Math.round(r.height * dpr)) { canvas.width = Math.round(r.width * dpr); canvas.height = Math.round(r.height * dpr) }
      return { w: r.width, h: r.height, dpr }
    }
    const toScreen = (n, s) => [s.w / 2 + (n.x + view.x) * view.k, s.h / 2 + (n.y + view.y) * view.k]
    const toWorld = (x, y, s) => [(x - s.w / 2) / view.k - view.x, (y - s.h / 2) / view.k - view.y]

    // The bend of a beam: a gentle arc so beams between the same galaxies don't stack.
    function ctrl(x1, y1, x2, y2) { return [(x1 + x2) / 2 - (y2 - y1) * 0.12, (y1 + y2) / 2 + (x2 - x1) * 0.12] }
    function along(x1, y1, cx, cy, x2, y2, t) { const u = 1 - t; return [u * u * x1 + 2 * u * t * cx + t * t * x2, u * u * y1 + 2 * u * t * cy + t * t * y2] }

    function autoIntro() { try { return localStorage.getItem('prism-map-intro') !== 'off' } catch { return true } }
    const clamp01 = v => Math.max(0, Math.min(1, v))
    const ease = v => 1 - Math.pow(1 - clamp01(v), 3)
    function hash01(text, salt) {
      let h = (2166136261 ^ salt) >>> 0
      for (const ch of text) h = Math.imul(h ^ ch.charCodeAt(0), 16777619)
      return ((h >>> 0) % 10000) / 10000
    }
    // Where a star is while it flies out of the centre: it swings round in a spiral as it goes.
    function flight(n, t) {
      const e = ease(t), a = (1 - e) * 1.8 * n.spin, c = Math.cos(a), s = Math.sin(a)
      return [e * (n.fx * c - n.fy * s), e * (n.fx * s + n.fy * c)]
    }

    // Intro: a beam of light crosses the map, splits into the rainbow, and every file
    // flies out of the centre into its galaxy. The short one plays when the map opens.
    function startIntro(full) {
      if (timelapse) stopTimelapse()
      if (!nodes.length) return
      for (let i = 0; i < 500 && heat > 0.03; i++) step()
      heat = 0; fitted = true; goal = null; hover = null; chosen = null; drag = null; pan = null
      fit(false)
      if (reduced()) { kick(); return }
      const target = { ...view }
      const names = [...anchors.keys()], total = Math.max(1, names.length)
      const sums = new Map()
      for (const n of nodes) {
        n.fx = n.x; n.fy = n.y
        n.spin = hash01(n.path, 1) < 0.5 ? 1 : -1
        n.t0 = 0.14 + (Math.max(0, names.indexOf(n.group)) / total) * 0.28 + hash01(n.path, 2) * 0.14
        n.t1 = n.t0 + 0.3
        n.ip = 0
        const g = sums.get(n.group) || { name: n.group, x: 0, y: 0, c: 0, land: 0, color: n.color }
        g.x += n.fx; g.y += n.fy; g.c++; g.land = Math.max(g.land, n.t1)
        sums.set(n.group, g)
      }
      galaxies = [...sums.values()].map(g => ({ ...g, x: g.x / g.c, y: g.y / g.c, r: 40 + Math.sqrt(g.c) * 24 }))
      intro = { start: performance.now(), dur: full ? 9000 : 4200, target, p: 0 }
      introBtn.setAttribute('aria-pressed', 'true')
      kick()
    }
    function endIntro(now) {
      if (!intro) return
      view = { ...intro.target }
      intro = null
      labelsUntil = (now || performance.now()) + 1400
      introBtn.setAttribute('aria-pressed', 'false')
      // A tree update that arrived while the intro played is applied now, so it isn't cut short by one.
      if (pendingData) { const d = pendingData; pendingData = null; queueMicrotask(() => load(d)) }
      kick()
    }
    function stepIntro(now) {
      intro.p = (now - intro.start) / intro.dur
      if (intro.p >= 1) { endIntro(now); return }
      const ce = ease(intro.p * 1.05)
      view = { x: intro.target.x * ce, y: intro.target.y * ce, k: intro.target.k * (1.7 - 0.7 * ce) }
      for (const n of nodes) {
        const t = (intro.p - n.t0) / 0.3
        n.ip = clamp01(t)
        if (t >= 1) { n.x = n.fx; n.y = n.fy } else [n.x, n.y] = flight(n, t)
      }
    }
    function restoreIntro() { for (const n of nodes) { n.x = n.fx; n.y = n.fy } }

    function drawIntroEdges(s) {
      ctx.lineWidth = 1
      for (const [a, b] of edges) {
        const r = clamp01((intro.p - Math.max(a.t1, b.t1)) / 0.1)
        if (r <= 0) continue
        const [x1, y1] = toScreen(a, s), [x2, y2] = toScreen(b, s), [cx, cy] = ctrl(x1, y1, x2, y2)
        ctx.strokeStyle = rgba(pal.muted, 0.22 * r)
        ctx.beginPath(); ctx.moveTo(x1, y1); ctx.quadraticCurveTo(cx, cy, x2, y2); ctx.stroke()
      }
    }

    function drawIntroFx(s) {
      const p = intro.p, [ox, oy] = toScreen({ x: 0, y: 0 }, s)
      ctx.globalCompositeOperation = 'lighter'
      // The beam arrives from the left and meets the centre.
      const head = ox - (1 - ease(p / 0.12)) * (ox + 40), beam = 1 - clamp01((p - 0.12) / 0.06)
      if (beam > 0) {
        const g = ctx.createLinearGradient(head - 340, 0, head, 0)
        g.addColorStop(0, 'rgba(255,255,255,0)'); g.addColorStop(1, 'rgba(255,255,255,' + beam + ')')
        ctx.fillStyle = g; ctx.fillRect(head - 340, oy - 2, 340, 4)
      }
      // A flash, then the beam splits into the rainbow.
      const flash = clamp01((p - 0.1) / 0.04) * (1 - clamp01((p - 0.13) / 0.22))
      if (flash > 0) {
        const r = 40 + 190 * ease((p - 0.1) / 0.2)
        ctx.globalAlpha = flash * 0.8; ctx.drawImage(sprite('#ffffff'), ox - r, oy - r, r * 2, r * 2); ctx.globalAlpha = 1
      }
      const fan = clamp01((p - 0.12) / 0.16) * (1 - clamp01((p - 0.3) / 0.2))
      if (fan > 0) palette.forEach((col, i) => {
        const ang = (i - (palette.length - 1) / 2) * 0.16, len = 460 * ease((p - 0.12) / 0.2)
        const x2 = ox + Math.cos(ang) * len, y2 = oy + Math.sin(ang) * len
        const g = ctx.createLinearGradient(ox, oy, x2, y2)
        g.addColorStop(0, rgba(col, 0.9 * fan)); g.addColorStop(1, rgba(col, 0))
        ctx.strokeStyle = g; ctx.lineWidth = 2.4
        ctx.beginPath(); ctx.moveTo(ox, oy); ctx.lineTo(x2, y2); ctx.stroke()
      })
      // Comet tails behind the stars in flight.
      for (const n of nodes) {
        const t = (p - n.t0) / 0.3
        if (t <= 0 || t >= 1) continue
        const [x1, y1] = toScreen({ x: n.x, y: n.y }, s), [px, py] = flight(n, t - 0.07)
        const [x0, y0] = toScreen({ x: px, y: py }, s)
        const g = ctx.createLinearGradient(x0, y0, x1, y1)
        g.addColorStop(0, rgba(n.color, 0)); g.addColorStop(1, rgba(n.color, 0.55 * (1 - t * 0.5)))
        ctx.strokeStyle = g; ctx.lineWidth = 1.6
        ctx.beginPath(); ctx.moveTo(x0, y0); ctx.lineTo(x1, y1); ctx.stroke()
      }
      // A soft ring as each galaxy settles.
      for (const g of galaxies) {
        const age = (p - g.land) / 0.14
        if (age <= 0 || age >= 1) continue
        const [x, y] = toScreen({ x: g.x, y: g.y }, s)
        ctx.strokeStyle = rgba(g.color, 0.5 * (1 - age)); ctx.lineWidth = 2
        ctx.beginPath(); ctx.arc(x, y, g.r * view.k * (0.4 + age * 1.1), 0, 7); ctx.stroke()
      }
      ctx.globalCompositeOperation = 'source-over'
    }

    // Folder names fade in as the galaxies settle and linger a moment after the intro.
    function drawGalaxyNames(s, now) {
      const a = intro ? clamp01((intro.p - 0.78) / 0.14) : clamp01((labelsUntil - now) / 1400)
      if (a <= 0) return
      ctx.textAlign = 'center'; ctx.font = '600 15px ' + pal.mono
      for (const g of galaxies) {
        const [x, y] = toScreen({ x: g.x, y: g.y }, s)
        const label = g.name ? g.name + '/' : 'top level', w = ctx.measureText(label).width
        ctx.fillStyle = rgba(pal.bg, 0.6 * a); ctx.fillRect(x - w / 2 - 6, y + g.r * view.k - 2, w + 12, 22)
        ctx.fillStyle = rgba(g.color, a); ctx.fillText(label, x, y + g.r * view.k + 14)
      }
    }

    function visible(n) {
      if (intro) return Math.min(1, n.ip * 5)
      if (!timelapse) return 1
      const t = timelapse.added.get(n.path)
      if (t == null) return timelapse.at >= timelapse.end ? 1 : 0
      return Math.max(0, Math.min(1, (timelapse.at - t) / (timelapse.span * 0.02 + 1)))
    }

    function draw(now) {
      const s = size(), t = now / 1000, still = reduced()
      ctx.setTransform(s.dpr, 0, 0, s.dpr, 0, 0)
      ctx.clearRect(0, 0, s.w, s.h)
      // Background: drifting stars at three depths.
      for (const st of stars) {
        const px = ((st.x * s.w + view.x * view.k * st.z * 0.15) % s.w + s.w) % s.w
        const py = ((st.y * s.h + view.y * view.k * st.z * 0.15) % s.h + s.h) % s.h
        const tw = still ? 0.6 : 0.45 + 0.35 * Math.sin(t * (0.6 + st.z) + st.t)
        ctx.fillStyle = st.c ? rgba(st.c, tw * 0.7) : rgba(pal.fg, tw * 0.35 * st.z)
        ctx.fillRect(px, py, st.r, st.r)
      }
      // Nebulae: a faint cloud behind each galaxy.
      ctx.globalCompositeOperation = 'lighter'
      const sums = new Map()
      for (const n of nodes) { const v = sums.get(n.group) || { x: 0, y: 0, c: 0, col: n.color }; v.x += n.x; v.y += n.y; v.c++; sums.set(n.group, v) }
      for (const v of sums.values()) {
        if (v.c < 3) continue
        const [x, y] = toScreen({ x: v.x / v.c, y: v.y / v.c }, s), r = (60 + Math.sqrt(v.c) * 26) * view.k
        const g = ctx.createRadialGradient(x, y, 0, x, y, r)
        g.addColorStop(0, rgba(v.col, hover ? 0.03 : 0.07)); g.addColorStop(1, rgba(v.col, 0))
        ctx.fillStyle = g; ctx.fillRect(x - r, y - r, r * 2, r * 2)
      }
      ctx.globalCompositeOperation = 'source-over'

      const focus = hover || chosen
      const near = focus ? new Set([focus]) : null
      if (focus) for (const [a, b] of edges) { if (a === focus) near.add(b); if (b === focus) near.add(a) }

      // Beams: all of them faint in one stroke, then the lit ones on top.
      ctx.lineWidth = 1
      ctx.strokeStyle = rgba(pal.muted, focus ? 0.07 : 0.22)
      ctx.beginPath()
      for (const [a, b] of edges) {
        if (focus && (a === focus || b === focus)) continue
        if (intro) continue
        const va = visible(a), vb = visible(b)
        if (va < 1 || vb < 1) continue
        const [x1, y1] = toScreen(a, s), [x2, y2] = toScreen(b, s), [cx, cy] = ctrl(x1, y1, x2, y2)
        ctx.moveTo(x1, y1); ctx.quadraticCurveTo(cx, cy, x2, y2)
      }
      ctx.stroke()
      if (intro) drawIntroEdges(s)
      if (focus) {
        ctx.globalCompositeOperation = 'lighter'
        for (const [a, b] of edges) {
          if (a !== focus && b !== focus) continue
          const [x1, y1] = toScreen(a, s), [x2, y2] = toScreen(b, s), [cx, cy] = ctrl(x1, y1, x2, y2)
          const col = a === focus ? pal.out : pal.in
          const g = ctx.createLinearGradient(x1, y1, x2, y2)
          g.addColorStop(0, rgba(a.color, 0.75)); g.addColorStop(1, rgba(b.color, 0.75))
          ctx.strokeStyle = g; ctx.lineWidth = 1.4
          ctx.beginPath(); ctx.moveTo(x1, y1); ctx.quadraticCurveTo(cx, cy, x2, y2); ctx.stroke()
          // Light running along the beam, from the file that uses to the file used.
          const sp = still ? [0.5] : [0, 0.33, 0.66].map(o => (t * 0.55 + o + (x1 % 7) * 0.05) % 1)
          for (const p of sp) {
            const [px, py] = along(x1, y1, cx, cy, x2, y2, p)
            ctx.drawImage(sprite(col), px - 7, py - 7, 14, 14)
          }
        }
        ctx.globalCompositeOperation = 'source-over'
      }

      // Prism's trail through the files it touched, in order.
      if (showTrail && trail.length > 1) {
        ctx.globalCompositeOperation = 'lighter'
        ctx.setLineDash([6, 6]); ctx.lineDashOffset = still ? 0 : -t * 30
        ctx.lineWidth = 2; ctx.strokeStyle = rgba(pal.accent, 0.8)
        ctx.beginPath()
        let started = false
        for (const p of trail) { const n = byPath.get(p); if (!n) continue; const [x, y] = toScreen(n, s); if (started) ctx.lineTo(x, y); else { ctx.moveTo(x, y); started = true } }
        ctx.stroke(); ctx.setLineDash([])
        trail.forEach((p, i) => { const n = byPath.get(p); if (!n) return; const [x, y] = toScreen(n, s); ctx.fillStyle = pal.fg; ctx.font = '600 10px ' + pal.mono; ctx.textAlign = 'center'; ctx.fillText(String(i + 1), x, y - n.r * view.k - 10) })
        ctx.globalCompositeOperation = 'source-over'
      }

      // Stars.
      ctx.globalCompositeOperation = 'lighter'
      const scale = Math.min(1.8, Math.max(0.55, view.k))
      for (const n of nodes) {
        const v = visible(n)
        if (!v) continue
        const [x, y] = toScreen(n, s)
        if (x < -40 || y < -40 || x > s.w + 40 || y > s.h + 40) continue
        let r = n.r * scale
        if (intro) r *= 0.4 + 0.6 * ease(n.ip)
        const bornAt = born.get(n.path)
        if (bornAt && !still) { const age = (now - bornAt) / 900; if (age < 1) r *= 1 + (1 - age) * 1.5 }
        let a = near && !near.has(n) ? 0.28 : 1
        a *= v
        if (n === focus) r *= 1.35
        const glow = r * (n === focus ? 7 : 5)
        ctx.globalAlpha = a
        ctx.drawImage(sprite(n.color), x - glow, y - glow, glow * 2, glow * 2)
        if (n.agent) {
          // Files Prism is working on pulse, with a turning prism ring.
          const pulse = still ? 0.6 : 0.5 + 0.5 * Math.sin(t * 5)
          ctx.globalAlpha = a * (n.agent === 'read' ? 0.35 : 0.55 + 0.35 * pulse)
          for (let k = 0; k < palette.length; k++) {
            ctx.strokeStyle = palette[k]; ctx.lineWidth = 1.6
            const s0 = (still ? 0 : t * 1.4) + (k / palette.length) * 6.283
            ctx.beginPath(); ctx.arc(x, y, r + 5 + pulse * 2, s0, s0 + 6.283 / palette.length - 0.12); ctx.stroke()
          }
        }
      }
      ctx.globalAlpha = 1
      ctx.globalCompositeOperation = 'source-over'

      // Names: the focused star and its neighbours always, big stars when there's room.
      ctx.textAlign = 'center'
      for (const n of nodes) {
        if (!visible(n)) continue
        const show = !intro && (n === focus || (near && near.has(n)) || (!near && (nodes.length <= 50 || view.k > 1.5 || (n.inn >= 4 && view.k > 0.5) || n.inn >= 10)))
        if (!show) continue
        const [x, y] = toScreen(n, s)
        if (x < -120 || y < -30 || x > s.w + 120 || y > s.h + 30) continue
        ctx.font = (n === focus ? '600 13px ' : '11px ') + pal.mono
        ctx.fillStyle = rgba(pal.bg, 0.65)
        const label = base(n.path), w = ctx.measureText(label).width
        ctx.fillRect(x - w / 2 - 3, y + n.r * scale + 6, w + 6, 15)
        ctx.fillStyle = n === focus ? pal.fg : near ? rgba(pal.fg, 0.85) : rgba(pal.fg, 0.6)
        ctx.fillText(label, x, y + n.r * scale + 17)
      }
      if (intro) drawIntroFx(s)
      drawGalaxyNames(s, now)
      if (!nodes.length) { ctx.fillStyle = pal.muted; ctx.font = '13px ' + pal.mono; ctx.fillText(hidden.size || hideTests ? 'every star here is hidden' : 'no files yet', s.w / 2, s.h / 2) }
      placeCard(s)
    }

    function placeCard(s) {
      const n = hover || chosen
      if (!n || drag) { card.hidden = true; return }
      const [x, y] = toScreen(n, s)
      card.style.setProperty('--c', n.color)
      card.replaceChildren()
      card.append(el('b', '', base(n.path)), el('div', 'dim', (n.group ? n.group + '/' : 'top level') + (n.lang ? ' · ' + n.lang : '') + (n.git ? ' · ' + ({ M: 'changed', A: 'new', '?': 'new' }[n.git] || n.git) : '') + (n.agent ? ' · Prism ' + (n.agent === 'read' ? 'read it' : 'is editing it') : '')))
      const row = el('div', 'row')
      row.append(el('span', 'out', '→ uses ' + n.out), el('span', 'in', '← used by ' + n.inn))
      card.append(row)
      if (n.symbols.length) card.append(el('code', '', n.symbols.slice(0, 4).join(' · ')))
      card.hidden = false
      const cw = card.offsetWidth, ch = card.offsetHeight
      let cx = x + 34, cy = y - ch - 14
      if (cx + cw > s.w - 8) cx = x - cw - 34
      card.style.left = Math.max(8, cx) + 'px'
      card.style.top = Math.max(40, Math.min(s.h - ch - 8, cy)) + 'px'
    }

    function loop(now) {
      frame = 0
      if (!alive) return
      for (const [p, at] of born) if (now - at > 900 || reduced()) born.delete(p)
      if (timelapse) tickTimelapse(now)
      if (intro) stepIntro(now)
      else for (let i = 0; i < 2 && heat > 0.002; i++) step()
      if (!fitted && heat < 0.12) { fitted = true; fit(true) }
      if (goal && !intro) {
        const k = reduced() ? 1 : 0.14
        view.x += (goal.x - view.x) * k; view.y += (goal.y - view.y) * k; view.k += (goal.k - view.k) * k
        if (Math.abs(goal.x - view.x) + Math.abs(goal.y - view.y) < 0.5 && Math.abs(goal.k - view.k) < 0.005) { view = { ...goal }; goal = null }
      }
      draw(now)
      if (intro) restoreIntro()
      const busy = nodes.some(n => n.agent && n.agent !== 'read')
      if (heat > 0.002 || goal || intro || labelsUntil > now || ((hover || chosen || busy || showTrail) && !reduced()) || timelapse || born.size) frame = requestAnimationFrame(loop)
      else if (!reduced() && document.visibilityState === 'visible') twinkle = setTimeout(kick, 120)
    }
    // When nothing moves, the background stars still twinkle, at a few frames a second.
    let twinkle = 0
    function kick() { clearTimeout(twinkle); if (!frame && alive) frame = requestAnimationFrame(loop) }

    function fit(glide, all) {
      const list = all ? nodes : nodes.filter(n => visible(n))
      if (!list.length) return
      const s = size()
      let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity
      for (const n of list) { x0 = Math.min(x0, n.x); y0 = Math.min(y0, n.y); x1 = Math.max(x1, n.x); y1 = Math.max(y1, n.y) }
      const target = { x: -(x0 + x1) / 2, y: -(y0 + y1) / 2, k: Math.max(0.15, Math.min(2.2, Math.min((s.w - 140) / (x1 - x0 + 1), (s.h - 140) / (y1 - y0 + 1)))) }
      if (glide) goal = target; else view = target
      kick()
    }
    function flyTo(n) { if (!n) return; chosen = n; goal = { x: -n.x, y: -n.y, k: Math.max(view.k, 1.5) }; kick() }

    function hit(e) {
      const s = size(), b = canvas.getBoundingClientRect()
      const [wx, wy] = toWorld(e.clientX - b.left, e.clientY - b.top, s)
      let best = null, bd = Infinity
      for (const n of nodes) {
        if (!visible(n)) continue
        const d = (n.x - wx) ** 2 + (n.y - wy) ** 2, r = (n.r + 6) / Math.min(1.6, Math.max(0.6, view.k))
        if (d < r * r * 1.6 && d < bd) { best = n; bd = d }
      }
      return best
    }

    canvas.addEventListener('pointerdown', e => {
      if (intro) { endIntro(); return }
      canvas.setPointerCapture(e.pointerId)
      moved = false
      const n = hit(e)
      if (n) drag = n; else pan = { x: e.clientX, y: e.clientY, vx: view.x, vy: view.y }
      goal = null
    })
    canvas.addEventListener('pointermove', e => {
      if (intro) return
      if (drag) {
        const s = size(), b = canvas.getBoundingClientRect()
        const [wx, wy] = toWorld(e.clientX - b.left, e.clientY - b.top, s)
        if (Math.abs(wx - drag.x) + Math.abs(wy - drag.y) > 2) moved = true
        if (moved) { drag.x = wx; drag.y = wy; heat = Math.max(heat, 0.15) }
        kick(); return
      }
      if (pan) {
        view.x = pan.vx + (e.clientX - pan.x) / view.k; view.y = pan.vy + (e.clientY - pan.y) / view.k
        moved = moved || Math.abs(e.clientX - pan.x) + Math.abs(e.clientY - pan.y) > 3
        kick(); return
      }
      const n = hit(e)
      if (n !== hover) { hover = n; canvas.style.cursor = n ? 'pointer' : 'grab'; kick() }
    })
    canvas.addEventListener('pointerup', () => {
      const n = drag, wasPan = pan
      drag = null; pan = null
      if (n && !moved) flyTo(n)
      if (wasPan && !moved) { chosen = null; kick() }
    })
    canvas.addEventListener('dblclick', e => {
      const n = hit(e)
      if (n && opts.onOpen) opts.onOpen(n.path)
      else { chosen = null; fit(true) }
    })
    canvas.addEventListener('pointercancel', () => { drag = null; pan = null })
    canvas.addEventListener('lostpointercapture', () => { drag = null; pan = null })
    canvas.addEventListener('pointerleave', () => { if (hover) { hover = null; kick() } })
    canvas.addEventListener('wheel', e => {
      e.preventDefault()
      if (intro) endIntro()
      goal = null
      const s = size(), b = canvas.getBoundingClientRect()
      const [wx, wy] = toWorld(e.clientX - b.left, e.clientY - b.top, s)
      view.k = Math.max(0.12, Math.min(5, view.k * Math.exp(-e.deltaY * 0.0015)))
      const [nx, ny] = toWorld(e.clientX - b.left, e.clientY - b.top, s)
      view.x += nx - wx; view.y += ny - wy
      kick()
    }, { passive: false })
    // Esc skips the intro before anything else sees it.
    document.addEventListener('keydown', e => {
      if (intro && e.key === 'Escape') { e.stopImmediatePropagation(); endIntro() }
    }, true)
    canvas.tabIndex = 0
    canvas.setAttribute('aria-label', 'Map of how the files link. Press / to find a file.')
    canvas.addEventListener('keydown', e => {
      if (e.key === '/') { e.preventDefault(); openFind() }
      else if (e.key === 'Enter' && chosen && opts.onOpen) opts.onOpen(chosen.path)
    })
    if (typeof ResizeObserver !== 'undefined') new ResizeObserver(kick).observe(canvas)

    function openFind() {
      find.hidden = false; findBtn.hidden = true; find.value = ''; find.focus()
    }
    function closeFind() { find.hidden = true; findBtn.hidden = false; canvas.focus({ preventScroll: true }) }
    const best = q => {
      q = q.toLowerCase()
      return nodes.find(n => base(n.path).toLowerCase().startsWith(q)) || nodes.find(n => n.path.toLowerCase().includes(q))
    }
    find.addEventListener('input', () => { const n = find.value && best(find.value); hover = n || null; if (n) flyTo(n); kick() })
    find.addEventListener('keydown', e => {
      e.stopPropagation()
      if (e.key === 'Escape') { closeFind(); return }
      if (e.key === 'Enter') { const n = best(find.value); if (n && opts.onOpen) opts.onOpen(n.path); closeFind() }
    })

    // Time-lapse: stars appear in the order git first saw them.
    async function startTimelapse() {
      if (intro) endIntro()
      if (!history) {
        try { const r = await fetch('/api/births', { cache: 'no-store' }); if (!r.ok) throw new Error(r.status); history = await r.json() } catch { history = null }
      }
      const files = ((history && history.files) || []).filter(f => byPath.has(f.path))
      if (!files.length) { history = null; playBtn.textContent = 'no git history yet'; playBtn.disabled = true; return }
      const added = new Map(files.map(f => [f.path, f.t]))
      const start = Math.min(...files.map(f => f.t)), end = Math.max(...files.map(f => f.t))
      timelapse = { added, start, end, span: Math.max(1, end - start), at: start, began: performance.now() }
      playBtn.textContent = '■ stop'; playBtn.setAttribute('aria-pressed', 'true'); clock.hidden = false
      fit(true, true); kick()
    }
    function tickTimelapse(now) {
      const p = Math.min(1, (now - timelapse.began) / 14000)
      timelapse.at = timelapse.start + timelapse.span * p
      clock.textContent = new Date(timelapse.at * 1000).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
      if (p >= 1 && !timelapse.ending) timelapse.ending = setTimeout(stopTimelapse, 1500)
    }
    function stopTimelapse() {
      if (timelapse) clearTimeout(timelapse.ending)
      timelapse = null; clock.hidden = true
      playBtn.textContent = '▶ time-lapse'; playBtn.setAttribute('aria-pressed', 'false'); kick()
    }

    load(data)
    return {
      update: d => { if (intro) pendingData = d; else load(d) },
      fit: () => fit(true),
      around: p => { center = p && (last.nodes || []).some(n => n.path === p && (n.imports || []).length + (n.importedBy || []).length) ? p : null; fitted = false; heat = 1; hover = null; chosen = null; load(last); if (center) { const c = byPath.get(center); if (c) { c.x = 0; c.y = 0; chosen = c } } return !!center },
      linkedOnly: on => { linkedOnly = on; reload() },
      focus: p => flyTo(byPath.get(p)),
      redraw: () => { colors(); kick() },
      stop: () => { alive = false; cancelAnimationFrame(frame); frame = 0; clearTimeout(twinkle); if (timelapse) stopTimelapse(); if (intro) endIntro(); card.hidden = true },
      intro: full => startIntro(full),
      open: () => { if (autoIntro()) startIntro(false) },
      start: () => { alive = true; wideBtn.setAttribute('aria-pressed', String(!!section.closest('.files-panel')?.classList.contains('map-wide'))); if (playBtn.disabled) { playBtn.disabled = false; playBtn.textContent = '▶ time-lapse' } kick() },
    }
  }

  window.PrismMap = { mount }
})()
