// PrismTree: an interactive project tree (no dependencies). window.PrismTree.mount(el, data) -> {update(data)}
(function () {
  const ICON = { go: ['GO', '#6fd3ff'], py: ['PY', '#f9e27d'], js: ['JS', '#f9e27d'], ts: ['TS', '#6fa8ff'], html: ['<>', '#ffa25f'],
    css: ['#', '#c69bff'], md: ['M↓', '#c9c4dc'], json: ['{}', '#7ee2a8'], yaml: ['YM', '#ff8fa3'], toml: ['TM', '#ff8fa3'],
    sh: ['$_', '#7ee2a8'], rs: ['RS', '#ffa25f'], docker: ['DK', '#6fd3ff'], c: ['C', '#8a9bff'], cpp: ['C+', '#8a9bff'],
    java: ['JV', '#ff8f6d'], rb: ['RB', '#ff6b81'], php: ['PH', '#8a9bff'], swift: ['SW', '#ffa25f'], kt: ['KT', '#c69bff'] }
  const IMAGE = /\.(png|jpe?g|gif|svg|webp|ico|avif)$/i
  const MEDIA = /\.(mp4|webm|mov|mp3|wav)$/i
  const FOLDER = '<svg viewBox="0 0 16 16"><path fill="currentColor" d="M1.5 3.5A1.5 1.5 0 0 1 3 2h3.2l1.6 1.6H13A1.5 1.5 0 0 1 14.5 5v7A1.5 1.5 0 0 1 13 13.5H3A1.5 1.5 0 0 1 1.5 12z" opacity=".9"/></svg>'
  const FOLDER_OPEN = '<svg viewBox="0 0 16 16"><path fill="currentColor" d="M1.5 3.5A1.5 1.5 0 0 1 3 2h3.2l1.6 1.6H13A1.5 1.5 0 0 1 14.5 5v1H4.4a1.5 1.5 0 0 0-1.4 1L1.5 11.6z" opacity=".55"/><path fill="currentColor" d="M3.1 7.4A1 1 0 0 1 4 6.8h10.6a.6.6 0 0 1 .6.8l-1.8 5.2a1 1 0 0 1-.9.7H2.3a.5.5 0 0 1-.5-.7z"/></svg>'
  const el = (tag, cls, text) => { const e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e }
  const size = n => n == null ? '' : n < 1000 ? n + ' B' : n < 1e6 ? (n / 1e3).toFixed(1) + ' KB' : (n / 1e6).toFixed(1) + ' MB'
  const base = p => p.slice(p.lastIndexOf('/') + 1)
  const parent = p => { const i = p.lastIndexOf('/'); return i < 0 ? '' : p.slice(0, i) }
  const changed = n => !!(n.git || n.agent === 'write' || n.agent === 'edit')

  function mount(root, data, opts) {
    opts = opts || {}
    root.classList.add('ptree')
    root.innerHTML = ''
    const head = el('div', 'ptree-head')
    const title = el('span', 'ptree-root')
    const stats = el('span', 'ptree-stats')
    const search = el('input', 'ptree-search'); search.type = 'search'; search.placeholder = 'Search files and symbols   /'; search.setAttribute('aria-label', 'Search files')
    const chips = el('div', 'ptree-chips')
    const modes = [['all', 'All'], ['changed', 'Changed'], ['touched', 'Touched by Prism']]
    const chipEls = modes.map(([id, label]) => { const b = el('button', 'ptree-chip', label); b.type = 'button'; b.dataset.mode = id; chips.append(b); return b })
    head.append(title, stats, search, chips)
    const list = el('div', 'ptree-list'); list.tabIndex = 0; list.setAttribute('role', 'tree'); list.setAttribute('aria-label', 'Project files')
    const rows = el('div', 'ptree-rows'); list.append(rows)
    const detail = el('aside', 'ptree-detail')
    root.append(head, list, detail)

    let keep = null, nodes = new Map(), kids = new Map(), mode = 'all', query = '', selected = null, cursor = 0, visible = []
    const open = new Set(), flashing = new Map()
    const rowH = () => parseFloat(getComputedStyle(root).getPropertyValue('--t-row')) || 26

    function index(d) {
      const prev = nodes
      nodes = new Map(); kids = new Map()
      for (const n of d.nodes || []) {
        nodes.set(n.path, n)
        const p = parent(n.path)
        if (!kids.has(p)) kids.set(p, [])
        kids.get(p).push(n)
      }
      for (const list of kids.values()) list.sort((a, b) => (b.dir - a.dir) || a.path.localeCompare(b.path))
      if (prev.size) {
        for (const n of nodes.values()) {
          const o = prev.get(n.path)
          if (!o ? !n.dir : (o.git !== n.git || o.agent !== n.agent || o.size !== n.size)) flashing.set(n.path, Date.now())
        }
      }
      title.textContent = d.root || 'project'
      const s = d.stats || {}
      stats.innerHTML = ''
      for (const [k, label] of [['files', 'files'], ['dirs', 'folders'], ['changed', 'changed'], ['touched', 'touched']]) {
        const v = k === 'changed' ? (s.modified || 0) + (s.added || 0) + (s.untracked || 0) : (s[k] || 0)
        const span = el('span'); const b = el('b', null, String(v)); span.append(b, ' ' + label); stats.append(span)
      }
    }

    function matches(n) {
      if (mode === 'changed' && !changed(n)) return false
      if (mode === 'touched' && !n.agent) return false
      if (!query) return true
      return n.path.toLowerCase().includes(query) || (n.symbols || []).some(s => s.toLowerCase().includes(query))
    }

    // keep: files that match and every folder above one.
    function keepSet() {
      if (mode === 'all' && !query) return null
      const keep = new Set()
      for (const n of nodes.values()) {
        if (n.dir || !matches(n)) continue
        for (let p = n.path; p; p = parent(p)) keep.add(p)
      }
      return keep
    }

    function flatten() {
      const out = []
      const walk = (dir, depth) => {
        for (const n of kids.get(dir) || []) {
          if (keep && !keep.has(n.path)) continue
          out.push([n, depth])
          if (n.dir && (keep || open.has(n.path))) walk(n.path, depth + 1)
        }
      }
      walk('', 0)
      return out
    }

    function highlight(span, text) {
      const i = query ? text.toLowerCase().indexOf(query) : -1
      if (i < 0) { span.textContent = text; return }
      span.append(text.slice(0, i), el('mark', null, text.slice(i, i + query.length)), text.slice(i + query.length))
    }

    function iconFor(n) {
      const i = el('span', 'ptree-icon')
      if (n.dir) { i.classList.add('dir'); i.innerHTML = open.has(n.path) || keep ? FOLDER_OPEN : FOLDER; return i }
      let [label, color] = ICON[n.lang] || (IMAGE.test(n.path) ? ['IM', '#7ee2a8'] : MEDIA.test(n.path) ? ['▶', '#ff8fa3'] : ['·', '#6e6a86'])
      i.textContent = label; i.style.background = color
      return i
    }

    function rowFor([n, depth], at) {
      const r = el('div', 'ptree-row' + (n.dir ? ' dir' : '') + (n.git === 'D' ? ' gone' : '') + (n.agent ? ' act-' + n.agent : '') + (n.path === selected ? ' sel' : ''))
      r.style.top = at * rowH() + 'px'
      r.style.paddingLeft = 10 + depth * 16 + 'px'
      r.dataset.path = n.path
      r.setAttribute('role', 'treeitem')
      if (n.dir) r.setAttribute('aria-expanded', String(open.has(n.path) || !!keep))
      const t = flashing.get(n.path)
      if (t && Date.now() - t < 1400) r.classList.add('flash')
      const twist = el('span', 'ptree-twist' + (n.dir && (open.has(n.path) || keep) ? ' open' : ''), n.dir ? '▶' : '')
      const name = el('span', 'ptree-name'); highlight(name, base(n.path))
      r.append(twist, iconFor(n), name)
      if (n.git) { const b = el('span', 'ptree-badge ' + (n.git === '?' ? 'Q' : n.git), n.git === '?' ? 'new' : n.git); b.title = { M: 'modified', A: 'added', D: 'deleted', '?': 'not in git yet' }[n.git] || ''; r.append(b) }
      if (n.agent) { const d = el('span', 'ptree-dot' + (n.agent === 'read' ? ' read' : '')); d.title = 'Prism ' + (n.agent === 'read' ? 'read' : 'changed') + ' this'; r.append(d) }
      r.append(el('span', 'ptree-spacer'))
      if (n.dir) {
        const count = countFiles(n.path); r.append(el('span', 'ptree-badge n', String(count)))
      } else {
        r.append(el('span', 'ptree-size', size(n.size)))
        const bar = el('span', 'ptree-rank'); bar.title = n.rank ? 'used by ' + (n.importedBy || []).length + ' files' : 'not imported by other files'
        const fill = el('i'); fill.style.width = Math.round((n.rank || 0) * 100) + '%'; bar.append(fill); r.append(bar)
      }
      return r
    }

    const counts = new Map()
    function countFiles(dir) {
      if (counts.has(dir)) return counts.get(dir)
      let c = 0
      for (const n of kids.get(dir) || []) c += n.dir ? countFiles(n.path) : 1
      counts.set(dir, c)
      return c
    }

    function paint() {
      const h = rowH(), top = list.scrollTop, height = list.clientHeight || 600
      rows.style.height = visible.length * h + 'px'
      const from = Math.max(0, Math.floor(top / h) - 10), to = Math.min(visible.length, Math.ceil((top + height) / h) + 10)
      rows.replaceChildren(...visible.slice(from, to).map((v, i) => rowFor(v, from + i)))
      if (!visible.length) rows.replaceChildren(el('div', 'ptree-empty', query || mode !== 'all' ? 'Nothing matches.' : 'No files.'))
    }

    function render() {
      counts.clear()
      keep = keepSet()
      visible = flatten()
      cursor = Math.min(cursor, Math.max(0, visible.length - 1))
      chipEls.forEach(c => c.setAttribute('aria-pressed', String(c.dataset.mode === mode)))
      paint()
    }

    function reveal(p) {
      for (let d = parent(p); d; d = parent(d)) open.add(d)
      query = ''; search.value = ''; if (mode !== 'all' && !matches(nodes.get(p) || {})) mode = 'all'
      render()
      const i = visible.findIndex(([n]) => n.path === p)
      if (i >= 0) { cursor = i; list.scrollTop = Math.max(0, i * rowH() - list.clientHeight / 2); paint() }
    }

    function select(p) {
      selected = p
      const n = nodes.get(p)
      detail.replaceChildren()
      if (!n) { detail.append(el('p', 'ptree-hint', 'Pick a file to see its details.')); paint(); return }
      detail.append(el('h3', null, base(n.path)), el('div', 'where', n.path))
      const facts = el('div', 'ptree-facts')
      const fact = t => facts.append(el('span', 'ptree-fact', t))
      if (n.dir) fact(countFiles(n.path) + ' files')
      else { fact(size(n.size)); if (n.lang) fact(n.lang) }
      if (n.git) fact({ M: 'modified', A: 'added', D: 'deleted', '?': 'new, not in git' }[n.git])
      if (n.agent) fact({ write: 'written by Prism', edit: 'edited by Prism', read: 'read by Prism' }[n.agent])
      if (n.rank) fact('importance ' + Math.round(n.rank * 100) + '%')
      detail.append(facts)
      const section = (label, items, link) => {
        if (!items || !items.length) return
        detail.append(el('h4', null, label + ' · ' + items.length))
        const ul = el('ul')
        for (const it of items) {
          const li = el('li')
          if (link) { const b = el('button', 'ptree-link', it); b.type = 'button'; b.onclick = () => { reveal(it); select(it) }; li.append(b) } else li.append(el('code', null, it))
          ul.append(li)
        }
        detail.append(ul)
      }
      section('Symbols', n.symbols)
      section('Imports', n.imports, true)
      section('Imported by', n.importedBy, true)
      if (!n.dir && !(n.symbols || []).length && !(n.imports || []).length && !(n.importedBy || []).length) detail.append(el('p', 'ptree-hint', 'No symbols or local imports found in this file.'))
      paint()
    }

    function toggle(p) { open.has(p) ? open.delete(p) : open.add(p); render() }

    rows.addEventListener('click', e => {
      const r = e.target.closest('.ptree-row'); if (!r) return
      const p = r.dataset.path, n = nodes.get(p)
      cursor = visible.findIndex(([v]) => v.path === p)
      if (n && n.dir && !keep) toggle(p)
      select(p)
    })
    list.addEventListener('scroll', () => requestAnimationFrame(paint), { passive: true })
    let typing
    search.addEventListener('input', () => { clearTimeout(typing); typing = setTimeout(() => { query = search.value.trim().toLowerCase(); render() }, 80) })
    search.addEventListener('keydown', e => { if (e.key === 'Escape') { search.value = ''; query = ''; render(); list.focus() } if (e.key === 'ArrowDown') { e.preventDefault(); list.focus() } })
    chips.addEventListener('click', e => { const c = e.target.closest('.ptree-chip'); if (c) { mode = c.dataset.mode; render() } })
    list.addEventListener('keydown', e => {
      const n = visible[cursor] && visible[cursor][0]
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault(); cursor = Math.max(0, Math.min(visible.length - 1, cursor + (e.key === 'ArrowDown' ? 1 : -1)))
        const p = visible[cursor] && visible[cursor][0].path
        if (p) { const y = cursor * rowH(); if (y < list.scrollTop) list.scrollTop = y; else if (y + rowH() > list.scrollTop + list.clientHeight) list.scrollTop = y + rowH() - list.clientHeight; select(p) }
      } else if ((e.key === 'Enter' || e.key === ' ') && n) { e.preventDefault(); if (n.dir) toggle(n.path); select(n.path) }
      else if (e.key === 'ArrowRight' && n && n.dir && !open.has(n.path)) toggle(n.path)
      else if (e.key === 'ArrowLeft' && n) { if (n.dir && open.has(n.path)) toggle(n.path); else if (parent(n.path)) { reveal(parent(n.path)); select(parent(n.path)) } }
    })
    const onSlash = e => { if (e.key === '/' && document.activeElement !== search && !/input|textarea/i.test(document.activeElement.tagName) && root.isConnected) { e.preventDefault(); search.focus() } }
    document.addEventListener('keydown', onSlash)
    if (typeof ResizeObserver !== 'undefined') new ResizeObserver(() => paint()).observe(list)

    function update(d) {
      index(d)
      if (!open.size) {
        for (const n of nodes.values()) if (n.dir && !parent(n.path)) open.add(n.path)
        for (const n of nodes.values()) if (!n.dir && changed(n)) for (let p = parent(n.path); p; p = parent(p)) open.add(p)
      }
      render()
      if (selected && nodes.has(selected)) select(selected); else if (!selected) select(null)
      if (flashing.size) setTimeout(() => { for (const [p, t] of flashing) if (Date.now() - t > 1400) flashing.delete(p); paint() }, 1500)
    }
    update(data)
    return { update }
  }

  window.PrismTree = { mount }
})()
