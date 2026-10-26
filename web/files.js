// Files: the project tree in a sidebar on the left (GET /api/tree, then long-polls
// /api/tree?wait=<version>), the file viewer on the right, and the map.
(() => {
  const toggle = document.getElementById('files-toggle')
  const side = document.getElementById('sidebar')
  const panel = document.getElementById('files-panel')
  const box = document.getElementById('files-tree')
  const live = document.getElementById('files-live')
  const menu = document.getElementById('side-menu')
  const mapView = document.getElementById('map-view')
  const mapBtn = document.getElementById('files-map')
  if (!toggle || !side || !panel) return
  let mapCenter = null
  let view = null, map = null, data = null, version = 0, open = false, loading = null, starting = null, poll = 0

  const load = () => loading || (loading = new Promise((resolve, reject) => {
    const css = document.createElement('link')
    css.rel = 'stylesheet'; css.href = '/tree.css'
    document.head.append(css)
    const js = document.createElement('script')
    js.src = '/tree.js'; js.onload = resolve; js.onerror = reject
    document.head.append(js)
  }))

  function apply(d) {
    data = d
    version = d.version
    if (view) view.update(d)
    if (map && !mapView.hidden) map.update(d)
  }

  async function follow(id) {
    while (id === poll) {
      try {
        const res = await fetch('/api/tree?wait=' + version, { cache: 'no-store' })
        if (id !== poll) return
        if (res.status === 200) {
          apply(await res.json())
          live.textContent = ''
        } else if (res.status !== 204) throw new Error(res.status)
      } catch {
        live.textContent = 'reconnecting…'
        await new Promise(r => setTimeout(r, 3000))
      }
    }
  }

  // Which files Prism reads or edits isn't a file change, so ask again while it works.
  setInterval(async () => {
    if (!view || !document.body.classList.contains('busy')) return
    try { apply(await (await fetch('/api/tree', { cache: 'no-store' })).json()) } catch { /* the long poll reports trouble */ }
  }, 1500)

  const start = () => starting || (starting = (async () => {
    await load()
    const d = await (await fetch('/api/tree', { cache: 'no-store' })).json()
    view = window.PrismTree.mount(box, d, { compact: true, onOpen: path => openFile(path), onMenu: showMenu })
    apply(d)
    follow(++poll)
  })().catch(() => { starting = null; box.textContent = "couldn't load the project files" }))

  function show() {
    open = true
    side.hidden = false
    document.body.classList.add('sidebar-open')
    toggle.setAttribute('aria-expanded', 'true')
    return start()
  }

  function hide() {
    open = false
    closeMenu()
    const inside = side.contains(document.activeElement)
    side.hidden = true
    document.body.classList.remove('sidebar-open')
    toggle.setAttribute('aria-expanded', 'false')
    if (inside) toggle.focus({ preventScroll: true })
  }

  function showPanel(kind) {
    panel.hidden = false
    document.body.classList.add('panel-open')
    fv.pane.hidden = kind !== 'file'
    mapView.hidden = kind !== 'map'
    mapBtn.setAttribute('aria-pressed', String(kind === 'map'))
    if (kind !== 'map') {
      panel.classList.remove('map-wide')
      if (map) map.stop()
    }
  }

  function closePanel() {
    if (fv.pane && !fv.pane.hidden && !closeFile()) return
    panel.hidden = true
    panel.classList.remove('map-wide')
    document.body.classList.remove('panel-open')
    mapView.hidden = true
    mapBtn.setAttribute('aria-pressed', 'false')
    if (map) map.stop()
  }

  async function openMap() {
    if (!mapView.hidden) { closePanel(); return }
    const here = (current && current.path) || (view && view.selected && view.selected()) || null
    if (fv.pane && !fv.pane.hidden && !closeFile()) return
    await show()
    if (!window.PrismMap || !data) return
    showPanel('map')
    if (!map) map = window.PrismMap.mount(document.getElementById('map-canvas'), data, { onOpen: path => openFile(path) })
    else { map.update(data); map.start() }
    mapCenter = map.around(here) ? here : null
    scopeBtn.hidden = !mapCenter
    scopeBtn.setAttribute('aria-pressed', String(!!mapCenter))
    if (mapCenter) scopeBtn.textContent = 'around ' + mapCenter.slice(mapCenter.lastIndexOf('/') + 1)
    map.open()
  }

  async function change(op, path, to) {
    const res = await fetch('/api/fs', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ op, path, to: to || '' }) })
    if (!res.ok) throw new Error((await res.text()).trim() || res.status)
    const done = (await res.json()).path
    apply(await (await fetch('/api/tree', { cache: 'no-store' })).json())
    return done
  }

  function mention(path) {
    const input = document.getElementById('input')
    if (!input) return
    const v = input.value
    input.value = v + (v && !/\s$/.test(v) ? ' ' : '') + '`' + path + '` '
    input.dispatchEvent(new Event('input', { bubbles: true }))
    input.focus()
  }

  const dirOf = p => (p.includes('/') ? p.slice(0, p.lastIndexOf('/')) : '')
  const join = (dir, name) => (dir ? dir + '/' : '') + name.replace(/^\/+/, '')

  function closeMenu() { if (!menu.hidden) { menu.hidden = true; menu.replaceChildren() } }

  function place(x, y) {
    menu.hidden = false
    const r = menu.getBoundingClientRect()
    menu.style.left = Math.max(6, Math.min(x, innerWidth - r.width - 6)) + 'px'
    menu.style.top = Math.max(6, Math.min(y, innerHeight - r.height - 6)) + 'px'
  }

  // ask turns the menu into a one-line form; enter runs it, esc closes.
  function ask(label, value, run, x, y) {
    menu.replaceChildren()
    const l = document.createElement('div'); l.className = 'menu-label'; l.textContent = label
    const input = document.createElement('input'); input.value = value; input.setAttribute('aria-label', label)
    const err = document.createElement('div'); err.className = 'menu-err'
    menu.append(l, input, err)
    place(x, y)
    input.focus()
    const dot = value.lastIndexOf('.')
    input.setSelectionRange(0, dot > 0 ? dot : value.length)
    input.addEventListener('keydown', async e => {
      if (e.key === 'Escape') { e.stopPropagation(); closeMenu(); return }
      if (e.key !== 'Enter') return
      e.preventDefault()
      const name = input.value.trim()
      if (!name) { closeMenu(); return }
      try { await run(name); closeMenu() } catch (x) { err.textContent = String(x.message || x) }
    })
  }

  function showMenu(path, isDir, x, y) {
    menu.replaceChildren()
    const here = isDir ? path : dirOf(path)
    const item = (text, fn, cls) => {
      const b = document.createElement('button'); b.type = 'button'; b.textContent = text; b.setAttribute('role', 'menuitem')
      if (cls) b.className = cls
      b.addEventListener('click', e => { e.stopPropagation(); fn() })
      menu.append(b)
    }
    const sep = () => menu.append(document.createElement('hr'))
    if (path) { const l = document.createElement('div'); l.className = 'menu-label'; l.textContent = path; menu.append(l) }
    if (path && !isDir) {
      item('Open', () => { closeMenu(); openFile(path) })
      item('Mention in the chat', () => { closeMenu(); mention(path) })
      item('Show on the map', () => { closeMenu(); openMap().then(() => map && map.focus(path)) })
      sep()
    }
    item(here ? 'New file in ' + here.slice(here.lastIndexOf('/') + 1) + '…' : 'New file…', () => ask('new file', '', async name => { const p = await change('file', join(here, name)); view.reveal(p); openFile(p, true) }, x, y))
    item(here ? 'New folder in ' + here.slice(here.lastIndexOf('/') + 1) + '…' : 'New folder…', () => ask('new folder', '', async name => { view.reveal(await change('folder', join(here, name))) }, x, y))
    if (path) {
      sep()
      item('Rename…', () => ask('rename', path.slice(path.lastIndexOf('/') + 1), async name => {
        const p = await change('rename', path, join(dirOf(path), name))
        view.reveal(p)
        if (current && current.path === path) openFile(p)
      }, x, y))
      item('Copy path', () => { closeMenu(); navigator.clipboard && navigator.clipboard.writeText(path) })
      item('Delete', async () => {
        closeMenu()
        if (!confirm('Delete ' + path + (isDir ? ' and everything in it' : '') + '?')) return
        try {
          await change('delete', path)
          if (current && (current.path === path || current.path.startsWith(path + '/'))) { editing = false; closePanel() }
        } catch (e) { alert("couldn't delete · " + (e.message || e)) }
      }, 'danger')
    }
    place(x, y)
    const first = menu.querySelector('button')
    if (first) first.focus()
  }

  // File viewer: click a file in the tree to read it; Edit switches to a textarea with Save/Cancel.
  const fv = {
    pane: document.getElementById('file-view'), path: document.getElementById('file-path'),
    msg: document.getElementById('file-msg'), note: document.getElementById('file-note'),
    code: document.getElementById('file-code'), gutter: document.getElementById('file-gutter'),
    text: document.getElementById('file-text'), editor: document.getElementById('file-editor'),
    edit: document.getElementById('file-edit'), save: document.getElementById('file-save'),
    cancel: document.getElementById('file-cancel'), back: document.getElementById('file-back'),
    media: document.getElementById('file-media'), md: document.getElementById('file-md'),
    preview: document.getElementById('file-preview'),
  }
  const rawURL = path => '/api/raw?path=' + encodeURIComponent(path)
  const isMarkdown = path => /\.(md|markdown)$/i.test(path)
  let previewing = false

  // A Markdown picture or link path is relative to the file it is in.
  function resolvePath(from, src) {
    if (!src || /^[a-z]+:|^\/\/|^#/i.test(src)) return null
    const parts = src.startsWith('/') ? [] : from.split('/').slice(0, -1)
    for (const p of src.replace(/[?#].*$/, '').split('/')) {
      if (p === '..') parts.pop()
      else if (p && p !== '.') parts.push(p)
    }
    try { return decodeURIComponent(parts.join('/')) } catch { return parts.join('/') }
  }
  const resolve = (from, src) => { const p = resolvePath(from, src); return p == null ? null : rawURL(p) }

  // READMEs often use HTML for centred pictures. The preview turns those into
  // Markdown pictures and drops the layout tags; nothing else of the HTML is kept.
  function htmlToMarkdown(text) {
    return text
      .replace(/<img\b[^>]*>/gi, tag => {
        const src = /\bsrc\s*=\s*["']([^"']+)/i.exec(tag), alt = /\balt\s*=\s*["']([^"']*)/i.exec(tag)
        return src ? '![' + (alt ? alt[1] : '') + '](' + src[1] + ')' : ''
      })
      .replace(/<br\s*\/?>/gi, '\n')
      .replace(/<h([1-6])\b[^>]*>(.*?)<\/h\1>/gi, (_, n, t) => '#'.repeat(Math.min(3, +n)) + ' ' + t)
      .replace(/<\/?(p|div|span|b|strong|em|i|center|sub|sup|details|summary|picture|source)\b[^>]*>/gi, '')
  }

  function showMedia(f) {
    fv.media.replaceChildren()
    if (f.kind === 'image') {
      const img = document.createElement('img')
      img.src = rawURL(f.path); img.alt = f.path
      img.onerror = () => { fv.media.textContent = "couldn't show this picture" }
      fv.media.append(img)
    } else {
      const video = document.createElement('video')
      video.src = rawURL(f.path); video.controls = true; video.preload = 'metadata'
      video.onerror = () => { fv.media.textContent = "this browser can't play this video · try an .mp4 or .webm" }
      fv.media.append(video)
    }
  }
  function renderPreview(el, path, text) {
    const next = document.createElement('div'), media = []
    // Render inert image markers first: assigning real src values to throwaway
    // nodes would start requests even before they are inserted in the document.
    window.renderMarkdown(next, htmlToMarkdown(text), src => {
      const url = resolve(path, src)
      if (!url) return null
      media.push({ url, video: /\.(mp4|m4v|webm|mov|mkv|ogv)(?:[?#].*)?$/i.test(src) })
      return 'data:,'
    }, href => {
      const target = resolvePath(path, href)
      return target && view && view.has(target) ? () => openFile(target) : null
    })
    const existing = new Map()
    for (const node of el.querySelectorAll('img, video')) {
      const key = node.tagName + ':' + node.getAttribute('src')
      if (!existing.has(key)) existing.set(key, [])
      existing.get(key).push(node)
    }
    const reused = new Map()
    ;[...next.querySelectorAll('img')].forEach((marker, i) => {
      const item = media[i], tag = item.video ? 'VIDEO' : 'IMG'
      const old = existing.get(tag + ':' + item.url)?.shift()
      if (old) {
        reused.set(marker, old)
        if (item.video) old.setAttribute('aria-label', marker.alt)
        else old.alt = marker.alt
      } else {
        const node = document.createElement(tag.toLowerCase())
        if (item.video) {
          node.controls = true; node.preload = 'metadata'
          node.setAttribute('aria-label', marker.alt)
        } else { node.alt = marker.alt; node.loading = 'eager' }
        node.src = item.url
        marker.replaceWith(node)
      }
    })
    function reconcile(parent, desired) {
      let cursor = parent.firstChild
      for (const fresh of [...desired.childNodes]) {
        const retained = reused.get(fresh)
        let node
        if (retained) node = retained
        else if (cursor && cursor.nodeType === fresh.nodeType && cursor.nodeName === fresh.nodeName &&
                 !['IMG', 'VIDEO', 'A'].includes(fresh.nodeName)) {
          node = cursor
          if (node.nodeType === Node.TEXT_NODE) node.nodeValue = fresh.nodeValue
          else if (node.nodeType === Node.ELEMENT_NODE) {
            for (const attr of [...node.attributes]) {
              if (!fresh.hasAttribute(attr.name)) node.removeAttribute(attr.name)
            }
            for (const attr of fresh.attributes) {
              if (node.getAttribute(attr.name) !== attr.value) node.setAttribute(attr.name, attr.value)
            }
            reconcile(node, fresh)
          }
        } else node = fresh
        if (node !== cursor) {
          if (parent.moveBefore && node.isConnected) parent.moveBefore(node, cursor)
          else parent.insertBefore(node, cursor)
        }
        cursor = node.nextSibling
      }
      while (cursor) { const next = cursor.nextSibling; cursor.remove(); cursor = next }
    }
    reconcile(el, next)
    // New blocks can contain retained-media markers deeper inside.
    for (const [marker, node] of reused) {
      if (!el.contains(marker)) continue
      if (marker.parentNode.moveBefore && node.isConnected) marker.parentNode.moveBefore(node, marker)
      else marker.before(node)
      marker.remove()
    }
  }
  let current = null, editing = false, seq = 0

  const say = (text, err) => { fv.msg.textContent = text || ''; fv.msg.classList.toggle('err', !!err) }

  function render() {
    const f = current
    fv.path.textContent = f ? f.path : ''
    fv.path.title = fv.path.textContent
    const media = !!(f && !f.error && f.kind)
    const md = !!(f && previewing && !editing && !media && isMarkdown(f.path))
    const note = f && !media && (f.error || f.note)
    fv.note.hidden = !note
    fv.note.textContent = note || ''
    fv.media.hidden = !media
    fv.md.hidden = !md
    fv.preview.hidden = !f || media || editing || !isMarkdown(f.path)
    fv.preview.setAttribute('aria-pressed', String(previewing))
    fv.preview.textContent = previewing ? 'Source' : 'Preview'
    if (media && !fv.media.firstChild) showMedia(f)
    if (!media) fv.media.replaceChildren()
    if (md) {
      fv.md.replaceChildren()
      renderPreview(fv.md, f.path, f.content)
    }
    fv.code.hidden = !!note || editing || media || md
    fv.editor.hidden = !editing
    window.PrismRichEdit?.sync(fv.editor, editing && f && isMarkdown(f.path) ? f.path : null)
    fv.edit.hidden = editing || media
    fv.edit.disabled = !f || !f.editable
    fv.save.hidden = fv.cancel.hidden = !editing
    if (!note && !editing && f) {
      const lines = f.content.replace(/\r?\n$/, '').split(/\r?\n/)
      fv.gutter.textContent = lines.map((_, i) => i + 1).join('\n')
      fv.text.textContent = lines.join('\n')
    }
  }

  async function openFile(path, edit) {
    if (editing && fv.editor.value !== current.content && !confirm('Discard your unsaved changes?')) return
    const id = ++seq
    editing = false
    current = { path, editable: false, content: '' }
    showPanel('file')
    if (view) view.setCurrent(path)
    say('loading…')
    render()
    try {
      const res = await fetch('/api/file?path=' + encodeURIComponent(path), { cache: 'no-store' })
      if (id !== seq) return
      if (!res.ok) throw new Error((await res.text()).trim() || res.status)
      const data = await res.json()
      current = { path: data.path, editable: !!data.editable, content: data.content || '', note: data.note, kind: data.kind }
      previewing = isMarkdown(data.path) && /!\[[^\]]*\]\(/.test(current.content)
      say('')
    } catch (e) {
      if (id !== seq) return
      current = { path, editable: false, content: '', error: "couldn't open this file · " + e.message }
      say('')
    }
    render()
    fv.code.scrollTop = 0
    if (edit && current.editable) startEdit()
  }

  function closeFile() {
    if (editing && fv.editor.value !== current.content && !confirm('Discard your unsaved changes?')) return false
    seq++
    editing = false
    window.PrismRichEdit?.sync(fv.editor, null)
    current = null
    fv.media.replaceChildren()
    fv.pane.hidden = true
    panel.hidden = true
    panel.classList.remove('map-wide')
    document.body.classList.remove('panel-open')
    if (view) view.setCurrent(null)
    return true
  }

  function startEdit() {
    if (!current || !current.editable) return
    editing = true
    fv.editor.value = current.content
    say('')
    render()
    fv.editor.focus()
  }

  function cancelEdit() {
    if (fv.editor.value !== current.content && !confirm('Discard your unsaved changes?')) return
    editing = false
    say('')
    render()
  }

  async function saveEdit() {
    if (fv.save.disabled || !current) return
    if (window.PrismRichEdit?.pending()) { say('Wait for uploads to finish before saving.', true); return }
    const file = current
    const content = fv.editor.value
    fv.save.disabled = true
    say('saving…')
    try {
      const res = await fetch('/api/file', {
        method: 'POST', headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ path: file.path, content }),
      })
      if (!res.ok) throw new Error((await res.text()).trim() || res.status)
      file.content = content
      // Another file was opened while this one saved: leave that view alone.
      if (file !== current) return
      editing = false
      render()
      say('saved ' + new Date().toLocaleTimeString())
    } catch (e) {
      say("couldn't save · " + e.message, true)
    } finally {
      fv.save.disabled = false
    }
  }

  if (fv.pane) {
    fv.back.addEventListener('click', closePanel)
    fv.preview.addEventListener('click', () => { previewing = !previewing; render() })
    fv.edit.addEventListener('click', startEdit)
    fv.cancel.addEventListener('click', cancelEdit)
    fv.save.addEventListener('click', saveEdit)
    fv.editor.addEventListener('keydown', e => {
      if ((e.ctrlKey || e.metaKey) && e.key === 's') { e.preventDefault(); saveEdit() }
      else if (e.key === 'Tab' && !e.shiftKey) { e.preventDefault(); document.execCommand('insertText', false, '\t') }
    })
  }

  toggle.addEventListener('click', () => (open ? hide() : show()))
  document.getElementById('files-close').addEventListener('click', hide)
  document.getElementById('files-new').addEventListener('click', e => { const r = e.currentTarget.getBoundingClientRect(); ask('new file', '', async name => { const p = await change('file', name); view.reveal(p); openFile(p, true) }, r.left, r.bottom + 4) })
  mapBtn.addEventListener('click', openMap)
  document.getElementById('files-changed').addEventListener('click', e => {
    const on = e.currentTarget.getAttribute('aria-pressed') !== 'true'
    e.currentTarget.setAttribute('aria-pressed', String(on))
    if (view) view.setMode(on ? 'changed' : 'all')
  })
  const scopeBtn = document.getElementById('map-scope')
  scopeBtn.addEventListener('click', () => {
    const local = scopeBtn.getAttribute('aria-pressed') !== 'true'
    scopeBtn.setAttribute('aria-pressed', String(local))
    if (map) map.around(local ? mapCenter : null)
  })
  document.getElementById('map-close').addEventListener('click', closePanel)
  document.getElementById('map-linked').addEventListener('click', e => {
    const on = e.currentTarget.getAttribute('aria-pressed') !== 'true'
    e.currentTarget.setAttribute('aria-pressed', String(on))
    if (map) map.linkedOnly(on)
  })
  document.addEventListener('pointerdown', e => { if (!menu.hidden && !menu.contains(e.target)) closeMenu() }, true)
  new MutationObserver(() => { if (map && data) map.update(data) }).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })

  // A file path in the chat opens the file when clicked.
  const transcript = document.getElementById('transcript')
  if (transcript) {
    const known = el => {
      if (!view || el.tagName !== 'CODE' || el.closest('pre')) return null
      const p = el.textContent.trim().replace(/^\.\//, '').replace(/:\d+(:\d+)?$/, '')
      return view.has(p) ? p : null
    }
    transcript.addEventListener('mouseover', e => { const p = known(e.target); if (p) e.target.classList.add('path-link') })
    transcript.addEventListener('click', e => { const p = known(e.target); if (p) { show().then(() => view && view.reveal(p)); openFile(p) } })
  }

  window.PrismFiles = {
    show: () => show(),
    hide,
    toggle: () => (open ? hide() : show()),
    map: openMap,
    openFile: (path) => { show(); return openFile(String(path).replace(/^\.?\//, '')) },
    known: p => !!(view && view.has(p)),
    resolvePath, rawURL, renderPreview,
  }
  document.addEventListener('keydown', e => {
    if (e.key !== 'Escape') return
    if (!menu.hidden) { closeMenu(); e.stopPropagation(); return }
    if (side.contains(document.activeElement)) {
      e.stopPropagation()
      const input = document.getElementById('input')
      if (input) input.focus()
      return
    }
    if (fv.pane && !fv.pane.hidden) {
      if (editing) cancelEdit(); else closePanel()
      e.stopPropagation()
      return
    }
    if (!mapView.hidden) { closePanel(); e.stopPropagation() }
  })

})()
