// Markdown remains the source of truth; the preview is disposable.
(() => {
  let active = null, timer, serial = 0
  let noticeTimer
  const notice = document.createElement('p')
  notice.setAttribute('role', 'status')
  notice.className = 'rich-notice'
  notice.hidden = true
  document.body.append(notice)
  const preview = document.createElement('section')
  preview.id = 'rich-preview'
  preview.className = 'rich-preview md'
  preview.setAttribute('aria-label', 'Live Markdown preview')
  preview.hidden = true
  function paint() {
    if (active) {
      window.PrismFiles.renderPreview(preview, active.path, active.editor.value)
      for (const img of preview.querySelectorAll('img')) img.loading = 'eager'
    }
  }
  function changed(editor) {
    editor.dispatchEvent(new Event('input', { bubbles: true }))
  }
  function replace(editor, start, end, text) {
    const caretAtEnd = editor.selectionStart === end && editor.selectionEnd === end
    editor.setRangeText(text, start, end, caretAtEnd ? 'end' : 'preserve')
    changed(editor)
  }
  async function upload(file, session) {
    const editor = session.editor
    const name = file.name.replace(/[^A-Za-z0-9._ -]/g, '_')
    const token = `<!-- uploading ${name} ${++serial} -->`
    session.pending.add(token)
    const at = editor.selectionStart
    editor.setRangeText(token, at, editor.selectionEnd, 'end')
    changed(editor)
    let result
    try {
      if (file.size > 50 * 1024 * 1024) throw new Error('50 MB limit')
      const dir = session.path.split('/').slice(0, -1).join('/')
      const query = new URLSearchParams({ dir, name: file.name })
      const res = await fetch('/api/upload?' + query, {
        method: 'POST', headers: { 'Content-Type': file.type || 'application/octet-stream' },
        body: file,
      })
      if (!res.ok) throw new Error((await res.text()).trim() || res.status)
      const saved = await res.json()
      const relative = saved.relative
      result = `![${name}](${relative.split('/').map(p => encodeURIComponent(p).replace(/[()]/g, c => '%' + c.charCodeAt(0).toString(16))).join('/')})`
    } catch (e) {
      result = `[upload failed: ${name} — ${String(e.message).replace(/[\r\n\[\]]/g, ' ')}]`
    }
    session.pending.delete(token)
    if (active !== session) {
      notice.textContent = `Upload for ${session.path} finished after the editor closed: ${result}`
      notice.hidden = false
      clearTimeout(noticeTimer)
      noticeTimer = setTimeout(() => { notice.hidden = true; notice.textContent = '' }, 6000)
      return
    }
    const start = editor.value.indexOf(token)
    if (start !== -1) replace(editor, start, start + token.length, result)
  }
  function receive(e, files) {
    if (!active || !files.length) return
    e.preventDefault()
    active.editor.classList.remove('rich-drag')
    for (const file of files) upload(file, active)
  }
  window.PrismRichEdit = {
    pending: () => !!active?.pending.size,
    sync(editor, path) {
      if (active && active.path === path && active.editor === editor) return
      active = path == null ? null : { editor, path, pending: new Set() }
      clearTimeout(timer)
      editor.classList.remove('rich-drag')
      preview.hidden = !active
      editor.classList.toggle('rich-source', !!active)
      if (!active) { preview.replaceChildren(); return }
      editor.after(preview)
      if (!editor.dataset.richBound) {
        editor.dataset.richBound = 'true'
        editor.addEventListener('input', () => { clearTimeout(timer); timer = setTimeout(paint, 120) })
        editor.addEventListener('paste', e => receive(e, [...(e.clipboardData?.files || [])]))
        editor.addEventListener('drop', e => receive(e, [...(e.dataTransfer?.files || [])]))
        editor.addEventListener('dragover', e => {
          if (active && e.dataTransfer?.types.includes('Files')) {
            e.preventDefault(); editor.classList.add('rich-drag')
          }
        })
        editor.addEventListener('dragleave', () => editor.classList.remove('rich-drag'))
      }
      paint()
    },
  }
})()
