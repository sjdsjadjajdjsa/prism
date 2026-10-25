// PrismMap: the project as a graph, like Obsidian's graph view. Dots are files,
// lines are imports and Markdown links. PrismMap.mount(canvas, data, { onOpen }).
(() => {
  const css = name => getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  const base = p => p.slice(p.lastIndexOf('/') + 1)
  const top = p => (p.includes('/') ? p.slice(0, p.indexOf('/')) : '')

  function mount(canvas, data, opts) {
    const ctx = canvas.getContext('2d')
    let nodes = [], edges = [], byPath = new Map(), heat = 1
    let view = { x: 0, y: 0, k: 1 }, hover = null, drag = null, pan = null, moved = false, frame = 0, alive = true
    let palette = [], groups = [], fitted = false, last = data, linkedOnly = false

    function colors() {
      palette = ['--s0', '--s1', '--s2', '--s3', '--s4', '--s5', '--s6'].map(css).filter(Boolean)
      if (!palette.length) palette = ['#c69bff']
    }

    const colorOf = i => (i < palette.length ? palette[i] : ['#d9d6e6', '#9a96b0', '#f2c6de', '#c6e2f2'][(i - palette.length) % 4])

    function load(d) {
      last = d
      colors()
      const files = (d.nodes || []).filter(n => !n.dir && n.git !== 'D')
      files.sort((a, b) => (b.rank || 0) - (a.rank || 0) || ((b.importedBy || []).length - (a.importedBy || []).length))
      const keep = files.slice(0, 1500)
      groups = [...new Set(keep.map(n => top(n.path)))].sort()
      const old = byPath
      byPath = new Map()
      nodes = keep.map((n, i) => {
        const prev = old.get(n.path)
        const a = i * 2.39996, r = 12 * Math.sqrt(i + 1)
        const node = prev || { x: Math.cos(a) * r, y: Math.sin(a) * r, vx: 0, vy: 0 }
        Object.assign(node, { path: n.path, agent: n.agent, git: n.git, color: colorOf(groups.indexOf(top(n.path))), links: 0 })
        byPath.set(n.path, node)
        return node
      })
      edges = []
      for (const n of keep) {
        for (const t of n.imports || []) {
          const a = byPath.get(n.path), b = byPath.get(t)
          if (a && b) { edges.push([a, b]); a.links++; b.links++ }
        }
      }
      if (linkedOnly) nodes = nodes.filter(n => n.links > 0)
      for (const n of nodes) n.r = 3 + Math.min(9, Math.sqrt(n.links) * 2)
      heat = Math.max(heat, old.size ? 0.3 : 1)
      kick()
    }

    function step() {
      const k = 0.06 * heat
      for (let i = 0; i < nodes.length; i++) {
        const a = nodes[i]
        a.vx -= a.x * 0.002 * heat; a.vy -= a.y * 0.002 * heat
        for (let j = i + 1; j < nodes.length; j++) {
          const b = nodes[j]
          let dx = a.x - b.x, dy = a.y - b.y, d2 = dx * dx + dy * dy
          if (d2 > 90000) continue
          if (d2 < 1) { dx = Math.random() - 0.5; dy = Math.random() - 0.5; d2 = 1 }
          const f = 420 * heat / d2
          a.vx += dx * f; a.vy += dy * f; b.vx -= dx * f; b.vy -= dy * f
        }
      }
      for (const [a, b] of edges) {
        const dx = b.x - a.x, dy = b.y - a.y, d = Math.sqrt(dx * dx + dy * dy) || 1
        const f = (d - 80) * k / d
        a.vx += dx * f; a.vy += dy * f; b.vx -= dx * f; b.vy -= dy * f
      }
      for (const n of nodes) {
        if (n === drag) { n.vx = n.vy = 0; continue }
        n.vx *= 0.6; n.vy *= 0.6
        n.x += Math.max(-20, Math.min(20, n.vx)); n.y += Math.max(-20, Math.min(20, n.vy))
      }
      heat = Math.max(0, heat * 0.985 - 0.001)
    }

    function size() {
      const r = canvas.getBoundingClientRect(), dpr = devicePixelRatio || 1
      if (canvas.width !== Math.round(r.width * dpr) || canvas.height !== Math.round(r.height * dpr)) {
        canvas.width = Math.round(r.width * dpr); canvas.height = Math.round(r.height * dpr)
      }
      return { w: r.width, h: r.height, dpr }
    }

    const toScreen = (n, s) => [s.w / 2 + (n.x + view.x) * view.k, s.h / 2 + (n.y + view.y) * view.k]
    const toWorld = (x, y, s) => [(x - s.w / 2) / view.k - view.x, (y - s.h / 2) / view.k - view.y]

    function draw() {
      const s = size()
      ctx.setTransform(s.dpr, 0, 0, s.dpr, 0, 0)
      ctx.clearRect(0, 0, s.w, s.h)
      const near = hover ? new Set(edges.filter(([a, b]) => a === hover || b === hover).flatMap(e => e)) : null
      const faint = css('--faint') || '#2a2838', muted = css('--muted') || '#6e6a86', fg = css('--fg') || '#eee', accent = css('--s6') || css('--s0') || '#c69bff'
      ctx.lineWidth = 1
      for (const [a, b] of edges) {
        const lit = hover && (a === hover || b === hover)
        ctx.strokeStyle = lit ? accent : muted
        ctx.globalAlpha = hover && !lit ? 0.12 : lit ? 0.9 : 0.45
        const [x1, y1] = toScreen(a, s), [x2, y2] = toScreen(b, s)
        ctx.beginPath(); ctx.moveTo(x1, y1); ctx.lineTo(x2, y2); ctx.stroke()
      }
      const t = performance.now() / 1000
      for (const n of nodes) {
        const [x, y] = toScreen(n, s)
        if (x < -20 || y < -20 || x > s.w + 20 || y > s.h + 20) continue
        const r = n.r * Math.min(1.6, Math.max(0.6, view.k))
        ctx.globalAlpha = near && !near.has(n) ? 0.25 : 1
        if (n.agent) {
          ctx.fillStyle = accent
          ctx.globalAlpha *= n.agent === 'read' ? 0.25 : 0.35 + 0.2 * Math.sin(t * 4)
          ctx.beginPath(); ctx.arc(x, y, r + 6, 0, 7); ctx.fill()
          ctx.globalAlpha = near && !near.has(n) ? 0.25 : 1
        }
        ctx.fillStyle = n.color
        ctx.beginPath(); ctx.arc(x, y, r, 0, 7); ctx.fill()
        if (n === hover) { ctx.strokeStyle = fg; ctx.lineWidth = 2; ctx.stroke(); ctx.lineWidth = 1 }
        const label = n === hover || (near && near.has(n)) || nodes.length <= 60 || view.k > 1.6 || n.links >= 3
        if (label) {
          ctx.globalAlpha = near && !near.has(n) ? 0.3 : 1
          ctx.fillStyle = n === hover ? fg : muted
          ctx.font = (n === hover ? '600 ' : '') + '12px ' + (css('--mono') || 'monospace')
          ctx.textAlign = 'center'
          ctx.fillText(n === hover ? n.path : base(n.path), x, y + r + 13)
        }
      }
      ctx.globalAlpha = 1
      ctx.font = '11px ' + (css('--mono') || 'monospace'); ctx.textAlign = 'left'
      groups.slice(0, 8).forEach((g, i) => {
        const y = s.h - 12 - (Math.min(groups.length, 8) - 1 - i) * 16
        ctx.fillStyle = colorOf(i); ctx.beginPath(); ctx.arc(14, y - 4, 4, 0, 7); ctx.fill()
        ctx.fillStyle = muted; ctx.fillText(g ? g + '/' : 'top level', 24, y)
      })
      if (!nodes.length) { ctx.fillStyle = muted; ctx.textAlign = 'center'; ctx.font = '13px ' + (css('--mono') || 'monospace'); ctx.fillText('no files yet', s.w / 2, s.h / 2) }
    }

    function loop() {
      frame = 0
      if (!alive) return
      for (let i = 0; i < 2 && heat > 0.002; i++) step()
      if (!fitted && heat < 0.08) { fitted = true; fit() }
      draw()
      if (heat > 0.002 || nodes.some(n => n.agent && n.agent !== 'read')) frame = requestAnimationFrame(loop)
    }
    // fit centres the graph and zooms so every dot is on screen.
    function fit() {
      if (!nodes.length) return
      const s = size()
      let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity
      for (const n of nodes) { x0 = Math.min(x0, n.x); y0 = Math.min(y0, n.y); x1 = Math.max(x1, n.x); y1 = Math.max(y1, n.y) }
      view.x = -(x0 + x1) / 2; view.y = -(y0 + y1) / 2
      view.k = Math.max(0.2, Math.min(2.2, Math.min((s.w - 120) / (x1 - x0 + 1), (s.h - 120) / (y1 - y0 + 1))))
    }
    function kick() { if (!frame && alive) frame = requestAnimationFrame(loop) }

    function hit(e) {
      const s = size(), b = canvas.getBoundingClientRect()
      const [wx, wy] = toWorld(e.clientX - b.left, e.clientY - b.top, s)
      let best = null, bd = Infinity
      for (const n of nodes) {
        const d = (n.x - wx) ** 2 + (n.y - wy) ** 2, r = (n.r + 4) / Math.min(1.6, Math.max(0.6, view.k))
        if (d < r * r * 1.8 && d < bd) { best = n; bd = d }
      }
      return best
    }

    canvas.addEventListener('pointerdown', e => {
      canvas.setPointerCapture(e.pointerId)
      moved = false
      const n = hit(e)
      if (n) drag = n; else pan = { x: e.clientX, y: e.clientY, vx: view.x, vy: view.y }
    })
    canvas.addEventListener('pointermove', e => {
      if (drag) {
        const s = size(), b = canvas.getBoundingClientRect()
        ;[drag.x, drag.y] = toWorld(e.clientX - b.left, e.clientY - b.top, s)
        moved = true; heat = Math.max(heat, 0.2); kick(); return
      }
      if (pan) {
        view.x = pan.vx + (e.clientX - pan.x) / view.k; view.y = pan.vy + (e.clientY - pan.y) / view.k
        moved = moved || Math.abs(e.clientX - pan.x) + Math.abs(e.clientY - pan.y) > 3
        kick(); return
      }
      const n = hit(e)
      if (n !== hover) { hover = n; canvas.style.cursor = n ? 'pointer' : 'grab'; canvas.title = n ? n.path : ''; kick() }
    })
    canvas.addEventListener('pointerup', e => {
      const n = drag
      drag = null; pan = null
      if (n && !moved && opts.onOpen) opts.onOpen(n.path)
    })
    canvas.addEventListener('dblclick', () => { fit(); kick() })
    canvas.addEventListener('pointerleave', () => { if (hover) { hover = null; kick() } })
    canvas.addEventListener('wheel', e => {
      e.preventDefault()
      const s = size(), b = canvas.getBoundingClientRect()
      const [wx, wy] = toWorld(e.clientX - b.left, e.clientY - b.top, s)
      view.k = Math.max(0.2, Math.min(4, view.k * Math.exp(-e.deltaY * 0.0015)))
      const [nx, ny] = toWorld(e.clientX - b.left, e.clientY - b.top, s)
      view.x += nx - wx; view.y += ny - wy
      kick()
    }, { passive: false })
    if (typeof ResizeObserver !== 'undefined') new ResizeObserver(kick).observe(canvas)

    load(data)
    return {
      update: d => load(d),
      fit: () => { fit(); kick() },
      linkedOnly: on => { linkedOnly = on; fitted = false; heat = Math.max(heat, 0.5); load(last) },
      focus: p => { const n = byPath.get(p); if (n) { view.x = -n.x; view.y = -n.y; view.k = Math.max(view.k, 1.4); hover = n; kick() } },
      redraw: () => { colors(); kick() },
      stop: () => { alive = false; cancelAnimationFrame(frame); frame = 0 },
      start: () => { alive = true; kick() },
    }
  }

  window.PrismMap = { mount }
})()
