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

  function hide() {
    open = false
    poll++
    panel.hidden = true
    toggle.setAttribute('aria-expanded', 'false')
    document.body.classList.remove('files-open')
  }

  toggle.addEventListener('click', () => (open ? hide() : show()))
  document.getElementById('files-close').addEventListener('click', hide)
  document.addEventListener('keydown', e => { if (e.key === 'Escape' && open && !box.contains(document.activeElement)) hide() })
})()
