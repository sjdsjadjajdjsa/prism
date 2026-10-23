import http from 'node:http'
import { readFileSync, writeFileSync, renameSync } from 'node:fs'
import { customProviders, eligible } from './custom.mjs'

const config = JSON.parse(readFileSync(new URL('./providers.json', import.meta.url), 'utf8'))
const port = Number(process.env.PORT) || 4848
const host = process.env.HOST || '127.0.0.1'
const firstByteTimeout = 150_000
const stallTimeout = 240_000
const hedgeDelay = Number(process.env.HEDGE_MS) || 6000
const maxParallel = 3
const providerInflight = Number(process.env.PRISM_PROVIDER_INFLIGHT) || 2
const roleNames = ['code', 'fast', 'review']
const rankPenalty = 4000
const statsTTL = 6 * 60 * 60_000
const coldHedgeDelay = Number(process.env.COLD_HEDGE_MS) || 2000
const statsFile = process.env.STATS_FILE || ''
const stats = new Map()
let saveTimer

try {
  for (const [id, stat] of Object.entries(JSON.parse(readFileSync(statsFile, 'utf8')))) stats.set(id, stat)
} catch {}

const saveStats = () => {
  if (!statsFile || saveTimer) return
  saveTimer = setTimeout(() => {
    saveTimer = null
    try {
      writeFileSync(`${statsFile}.tmp`, JSON.stringify(Object.fromEntries(stats)))
      renameSync(`${statsFile}.tmp`, statsFile)
    } catch (error) {
      console.log(`could not save speed stats: ${error.message}`)
    }
  }, 2000)
}
const cooldowns = new Map()
const lastErrors = new Map()
const inflight = new Map()
const slotWaiters = new Set()

const expand = text => text.replace(/\$\{(\w+)(?::-([^}]*))?\}/g, (_, name, fallback = '') => process.env[name] || fallback)

const routes = () =>
  [...config.providers, ...customProviders()]
    .filter(provider => !provider.requires || process.env[provider.requires])
    .flatMap(provider =>
      provider.models.map(entry => {
        const spec = typeof entry === 'string' ? { name: entry } : entry
        const model = expand(spec.name)
        return {
          id: `${provider.name}/${model}`,
          provider: provider.name,
          family: spec.family ?? provider.name,
          roles: spec.roles ?? [],
          vision: spec.vision === true,
          model,
          baseURL: expand(provider.baseURL),
          key: provider.requires && provider.auth !== false ? process.env[provider.requires] : undefined,
          quick: provider.quick,
          paid: provider.mark?.paid,
          source: provider.mark?.source,
        }
      }),
    )

const expectedWait = route => {
  const stat = stats.get(route.id)
  return stat && Date.now() - stat.at < statsTTL ? stat.wait : 0
}

const learn = (route, ms) => {
  const fresh = expectedWait(route)
  stats.set(route.id, { wait: fresh ? fresh * 0.6 + ms * 0.4 : ms, at: Date.now() })
  saveStats()
}

const ordered = keep => {
  const now = Date.now()
  const all = routes().filter(keep)
  const ready = all
    .map((route, rank) => ({ route, score: rank * rankPenalty + expectedWait(route) }))
    .filter(({ route }) => (cooldowns.get(route.id) ?? 0) <= now)
    .sort((a, b) => a.score - b.score)
    .map(({ route }) => route)
  return ready.length ? ready : all.sort((a, b) => (cooldowns.get(a.id) ?? 0) - (cooldowns.get(b.id) ?? 0))
}

const within = (promise, ms) => {
  let timer
  return Promise.race([promise, new Promise(resolve => (timer = setTimeout(() => resolve(null), ms)))]).finally(() => clearTimeout(timer))
}

const seconds = ms => `${(ms / 1000).toFixed(1)}s`

const pauseFor = (status, headers) => {
  if (status === 429) return (Number(headers?.get('retry-after')) || 60) * 1000
  if (status === 401 || status === 402 || status === 403) return 15 * 60_000
  if (status === 404) return 60 * 60_000
  if (status === 0 || status >= 500) return 30_000
  return 0
}

const fail = (route, status, headers, detail) => {
  const pause = pauseFor(status, headers)
  if (pause) cooldowns.set(route.id, Date.now() + pause)
  const message = `${route.id} failed (${status || 'network'}): ${String(detail).slice(0, 200)}`
  lastErrors.set(route.id, message)
  console.log(`✗ ${message}`)
  return message
}

const send = (res, status, body) => {
  res.writeHead(status, { 'content-type': 'application/json' })
  res.end(JSON.stringify(body))
}

const maxBody = 4 * 1024 * 1024

const readBody = async req => {
  const chunks = []
  let size = 0
  for await (const chunk of req) {
    size += chunk.length
    if (size > maxBody) {
      const error = new Error('Request body exceeds 4 MB')
      error.tooLarge = true
      throw error
    }
    chunks.push(chunk)
  }
  return Buffer.concat(chunks).toString('utf8')
}

const complete = async (req, res) => {
  let payload
  try {
    payload = JSON.parse(await readBody(req))
  } catch (error) {
    if (error?.tooLarge) {
      req.destroy()
      return send(res, 413, { error: { message: 'Request body exceeds 4 MB', type: 'invalid_request' } })
    }
    return send(res, 400, { error: { message: 'Request body must be JSON', type: 'invalid_request' } })
  }
  const { prism_quick: quick, prism_avoid: avoid, prism_exclude_families: exclude } = payload
  const rest = Object.fromEntries(Object.entries(payload).filter(([key]) => !key.startsWith('prism_')))
  const avoided = new Set(Array.isArray(avoid) ? avoid : [])
  const excluded = new Set(Array.isArray(exclude) ? exclude : [])
  const [head, tail] = String(payload.model ?? 'auto').split(/:(.*)/s)
  const role = head === 'auto' && roleNames.includes(tail) ? tail : ''
  const wantsVision = head === 'auto' && tail === 'vision'
  // Your own paid models only answer when pinned by their full name (as listed in /v1/models).
  const matches = route => route.id === payload.model || (route.paid !== true && route.model === payload.model)
  const pinned = head !== 'auto' && routes().some(matches)
  const queue = ordered(route => (pinned ? matches(route) : eligible(route)) && (!wantsVision || route.vision) && !excluded.has(route.family)).sort(
    (a, b) => avoided.has(a.id) - avoided.has(b.id) || (role ? b.roles.includes(role) - a.roles.includes(role) : 0),
  )
  if (!queue.length) {
    if (wantsVision && !routes().some(route => route.vision && !excluded.has(route.family))) return send(res, 503, { error: { message: 'no free model that can see images is available right now', type: 'no_route_available', failures: [] } })
    if (excluded.size && routes().length) return send(res, 503, { error: { message: 'Every matching free AI route is in an excluded family', type: 'no_route_available', failures: [] } })
    return send(res, 503, { error: { message: 'No free AI routes are configured', type: 'no_route_available', failures: [] } })
  }
  const readyProviders = new Set(queue.filter(route => (cooldowns.get(route.id) ?? 0) <= Date.now()).map(route => route.provider)).size
  const crew = req.headers['x-prism-crew'] === '1'
  const failures = []
  const live = new Set()
  let winner = null
  let closed = false
  const pending = [...queue]
  let hedgeTimer
  res.on('close', () => {
    if (res.writableFinished) return
    closed = true
    slotWaiters.delete(launch)
    for (const attempt of live) attempt.controller.abort()
  })

  const request = async attempt => {
    const headers = new AbortController()
    const timer = setTimeout(() => headers.abort(new DOMException('no headers', 'TimeoutError')), firstByteTimeout)
    try {
      return await post(attempt, AbortSignal.any([attempt.controller.signal, headers.signal]))
    } finally {
      clearTimeout(timer)
    }
  }

  const post = (attempt, signal) =>
    fetch(`${attempt.route.baseURL}/chat/completions`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'user-agent': 'free-ai-router',
        ...(attempt.route.key && { authorization: `Bearer ${attempt.route.key}` }),
      },
      body: JSON.stringify({ ...rest, ...(quick && attempt.route.quick), model: attempt.route.model }),
      signal,
    })

  const giveUp = () => {
    if (winner || closed || live.size || pending.length) return
    send(res, 503, {
      error: {
        message: `Every free AI route failed. ${failures.map(failure => failure.message).join(' | ')}`,
        type: 'no_route_available',
        failures: failures.map(({ route, status }) => ({ route, status })),
      },
    })
  }

  const free = attempt => {
    if (attempt.freed) return
    attempt.freed = true
    inflight.set(attempt.route.provider, Math.max(0, (inflight.get(attempt.route.provider) ?? 0) - 1))
    for (const wake of [...slotWaiters]) wake()
  }

  const lose = (attempt, status, headers, detail) => {
    live.delete(attempt)
    free(attempt)
    if (winner || closed || attempt.controller.signal.aborted) return
    failures.push({ route: attempt.route.id, status, message: fail(attempt.route, status, headers, detail) })
    launch()
    giveUp()
  }

  const pump = async (attempt, upstream, reader, first) => {
    for (const other of live) {
      if (other !== attempt) {
        learn(other.route, Math.max(expectedWait(other.route), Date.now() - other.started))
        other.controller.abort()
      }
    }
    live.clear()
    clearTimeout(hedgeTimer)
    const wait = Date.now() - attempt.started
    learn(attempt.route, wait)
    cooldowns.delete(attempt.route.id)
    lastErrors.delete(attempt.route.id)
    console.log(`→ ${attempt.route.id} (started in ${seconds(wait)})`)
    res.writeHead(200, {
      'content-type': upstream.headers.get('content-type') ?? 'application/json',
      'cache-control': 'no-cache',
      'x-free-route': attempt.route.id,
      'x-prism-family': attempt.route.family,
      'x-free-wait': String(wait),
    })
    res.write(first)
    try {
      while (!closed) {
        const chunk = await within(reader.read(), stallTimeout)
        if (!chunk) {
          console.log(`✗ ${attempt.route.id} stalled for ${seconds(stallTimeout)}, cut off`)
          lastErrors.set(attempt.route.id, `${attempt.route.id} stalled mid-reply`)
          attempt.controller.abort()
          break
        }
        if (chunk.done) break
        res.write(chunk.value)
      }
    } catch {}
    res.end()
  }

  const execute = async attempt => {
    let upstream
    let networkError
    for (let tries = 0; tries < 2 && !upstream && !attempt.controller.signal.aborted; tries++) {
      try {
        upstream = await request(attempt)
      } catch (error) {
        if (error.name === 'TimeoutError' && !attempt.controller.signal.aborted) {
          networkError = `no reply in ${seconds(firstByteTimeout)}`
          break
        }
        networkError = [error.cause?.code, error.cause?.message ?? error.message].filter(Boolean).join(' ')
      }
    }
    if (!upstream) return lose(attempt, 0, null, networkError)
    if (!upstream.ok) return lose(attempt, upstream.status, upstream.headers, await upstream.text().catch(() => ''))
    const reader = upstream.body.getReader()
    const remaining = firstByteTimeout - (Date.now() - attempt.started)
    const first = await within(reader.read(), Math.max(1000, remaining)).catch(() => null)
    if (winner || closed || attempt.controller.signal.aborted) {
      reader.cancel().catch(() => {})
      return
    }
    if (!first || first.done) {
      attempt.controller.abort()
      live.delete(attempt)
      free(attempt)
      failures.push({ route: attempt.route.id, status: 0, message: fail(attempt.route, 0, null, first ? 'empty reply' : `no reply after ${seconds(firstByteTimeout)}`) })
      launch()
      return giveUp()
    }
    winner = attempt
    await pump(attempt, upstream, reader, first.value)
  }

  const run = async attempt => {
    try {
      await execute(attempt)
    } finally {
      free(attempt)
    }
  }

  const launch = () => {
    clearTimeout(hedgeTimer)
    slotWaiters.delete(launch)
    if (winner || closed || !pending.length) return
    if (live.size && (crew || live.size >= Math.min(maxParallel, Math.max(1, readyProviders - 1)))) return
    const slot = pending.findIndex(route => (inflight.get(route.provider) ?? 0) < providerInflight)
    if (slot < 0) {
      if (!live.size) slotWaiters.add(launch)
      return
    }
    const [route] = pending.splice(slot, 1)
    const attempt = { route, controller: new AbortController(), started: Date.now() }
    inflight.set(route.provider, (inflight.get(route.provider) ?? 0) + 1)
    if (live.size) console.log(`⇄ ${attempt.route.id} joins the race`)
    live.add(attempt)
    run(attempt)
    if (!crew) {
      // A route we know is slower than hedgeDelay (or know nothing about) gets the short delay.
      const ew = expectedWait(attempt.route)
      hedgeTimer = setTimeout(launch, !ew || ew > hedgeDelay ? coldHedgeDelay : hedgeDelay)
    }
  }

  launch()
}

const status = () => {
  const now = Date.now()
  return routes().map(route => ({
    route: route.id,
    provider: route.provider,
    family: route.family,
    roles: route.roles,
    vision: route.vision,
    ...(route.source && { source: route.source, paid: route.paid === true }),
    ready: (cooldowns.get(route.id) ?? 0) <= now,
    retryInSeconds: Math.max(0, Math.ceil(((cooldowns.get(route.id) ?? 0) - now) / 1000)),
    lastError: lastErrors.get(route.id) ?? null,
    startsInSeconds: Number((expectedWait(route) / 1000).toFixed(1)),
  }))
}

http
  .createServer((req, res) => {
    const path = new URL(req.url, 'http://router').pathname
    if (req.method === 'POST' && path === '/v1/chat/completions') return complete(req, res)
    if (path === '/v1/models') return send(res, 200, { object: 'list', data: ['auto', ...roleNames.map(role => `auto:${role}`), 'auto:vision', ...routes().filter(route => route.source === 'yours').map(route => route.id)].map(id => ({ id, object: 'model', owned_by: 'free-ai' })) })
    if (path === '/status' && new URL(req.url, 'http://router').searchParams.get('format') === 'text') {
      res.writeHead(200, { 'content-type': 'text/plain' })
      return res.end(status().map(row => `${row.ready ? '✓ ready  ' : `… resting ${row.retryInSeconds}s`}  ${row.route}${row.lastError ? `\n             last error: ${row.lastError}` : ''}`).join('\n') + '\n')
    }
    if (path === '/status') return send(res, 200, status())
    if (path === '/health') return send(res, 200, { ok: true })
    send(res, 404, { error: { message: 'Not found', type: 'not_found' } })
  })
  .listen(port, host, () => console.log(`Free AI router on ${host}:${port} → ${routes().map(route => route.id).join(', ')}`))
