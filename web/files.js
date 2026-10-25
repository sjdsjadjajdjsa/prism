// Files panel: the live project tree (GET /api/tree, then long-polls /api/tree?wait=<version>).
(() => {
  const toggle = document.getElementById('files-toggle')
  const panel = document.getElementById('files-panel')
  const box = document.getElementById('files-tree')
  const live = document.getElementById('files-live')
  if (!toggle || !panel) return
  let view = null, version = 0, open = false, loading = null, poll = 0

  const load = () => loading || (loading = new Promise((resolve, reject) => {
    const css = document.createElement('link')
    css.rel = 'stylesheet'; css.href = '/tree.css'
    document.head.append(css)
    const js = document.createElement('script')
    js.src = '/tree.js'; js.onload = resolve; js.onerror = reject
    document.head.append(js)
  }))

  async function follow(id) {
    while (open && id === poll) {
      try {
        const res = await fetch('/api/tree?wait=' + version, { cache: 'no-store' })
        if (id !== poll) return
        if (res.status === 200) {
          const data = await res.json()
          version = data.version
          view.update(data)
          live.textContent = 'live · updated ' + new Date().toLocaleTimeString()
        } else if (res.status !== 204) throw new Error(res.status)
      } catch {
        live.textContent = 'reconnecting…'
        await new Promise(r => setTimeout(r, 3000))
      }
    }
  }

  async function show() {
    open = true
    panel.hidden = false
    toggle.setAttribute('aria-expanded', 'true')
    document.body.classList.add('files-open')
    document.getElementById('files-close').focus({ preventScroll: true })
    try {
      await load()
      const data = await (await fetch('/api/tree', { cache: 'no-store' })).json()
      version = data.version
      if (view) view.update(data)
      else view = window.PrismTree.mount(box, data)
      live.textContent = 'live'
      follow(++poll)
    } catch {
      box.textContent = "couldn't load the project files"
    }
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

  // A Markdown picture's path is relative to the file it is in.
  function resolve(from, src) {
    if (/^[a-z]+:|^\/\//i.test(src)) return null
    const parts = src.startsWith('/') ? [] : from.split('/').slice(0, -1)
    for (const p of src.replace(/[?#].*$/, '').split('/')) {
      if (p === '..') parts.pop()
      else if (p && p !== '.') parts.push(p)
    }
    return rawURL(decodeURIComponent(parts.join('/')))
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
      window.renderMarkdown(fv.md, f.content, src => resolve(f.path, src))
    }
    fv.code.hidden = !!note || editing || media || md
    fv.editor.hidden = !editing
    fv.edit.hidden = editing || media
    fv.edit.disabled = !f || !f.editable
    fv.save.hidden = fv.cancel.hidden = !editing
    if (!note && !editing && f) {
      const lines = f.content.replace(/\r?\n$/, '').split(/\r?\n/)
      fv.gutter.textContent = lines.map((_, i) => i + 1).join('\n')
      fv.text.textContent = lines.join('\n')
    }
  }

  async function openFile(path) {
    if (editing && fv.editor.value !== current.content && !confirm('Discard your unsaved changes?')) return
    const id = ++seq
    editing = false
    current = { path, editable: false, content: '' }
    panel.classList.add('viewing')
    fv.pane.hidden = false
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
  }

  function closeFile() {
    if (editing && fv.editor.value !== current.content && !confirm('Discard your unsaved changes?')) return false
    seq++
    editing = false
    current = null
    fv.media.replaceChildren()
    fv.pane.hidden = true
    panel.classList.remove('viewing')
    const row = box.querySelector('.ptree-row.sel') || box.querySelector('.ptree-row')
    if (row && row.focus) row.focus()
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
    const fileRow = e => {
      const r = e.target.closest && e.target.closest('.ptree-row')
      return r && !r.classList.contains('dir') && r.dataset.path ? r.dataset.path : null
    }
    box.addEventListener('click', e => { const p = fileRow(e); if (p) openFile(p) })
    box.addEventListener('keydown', e => { if (e.key === 'Enter') { const p = fileRow(e); if (p) openFile(p) } })
    fv.back.addEventListener('click', closeFile)
    fv.preview.addEventListener('click', () => { previewing = !previewing; render() })
    fv.edit.addEventListener('click', startEdit)
    fv.cancel.addEventListener('click', cancelEdit)
    fv.save.addEventListener('click', saveEdit)
    fv.editor.addEventListener('keydown', e => {
      if ((e.ctrlKey || e.metaKey) && e.key === 's') { e.preventDefault(); saveEdit() }
      else if (e.key === 'Tab' && !e.shiftKey) { e.preventDefault(); document.execCommand('insertText', false, '\t') }
    })
  }

  function hide() {
    if (fv.pane && !fv.pane.hidden && !closeFile()) return
    open = false
    poll++
    const inside = panel.contains(document.activeElement)
    panel.hidden = true
    if (inside) toggle.focus({ preventScroll: true })
    toggle.setAttribute('aria-expanded', 'false')
    document.body.classList.remove('files-open')
  }

  toggle.addEventListener('click', () => (open ? hide() : show()))
  // /files and /open <path> from the chat input
  window.PrismFiles = {
    show: () => { if (!open) show(); else if (fv.pane && !fv.pane.hidden) closeFile() },
    hide,
    openFile: (path) => { if (!open) show(); return openFile(String(path).replace(/^\.?\//, '')) },
  }
  document.getElementById('files-close').addEventListener('click', hide)
  document.addEventListener('keydown', e => {
    if (e.key !== 'Escape' || !open) return
    if (fv.pane && !fv.pane.hidden) {
      if (editing) cancelEdit(); else closeFile()
      e.stopPropagation()
      return
    }
    if (!box.contains(document.activeElement)) hide()
  })
})()
