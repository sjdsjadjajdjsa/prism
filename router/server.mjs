import http from 'node:http'
import { readFileSync, writeFileSync, renameSync } from 'node:fs'
import { randomUUID, createHash, timingSafeEqual } from 'node:crypto'
import { customProviders, eligible, PRESETS, maskKey, syncKeys, setKeyVar, keysPath, customFilePath, upsertPresetEntry, probeModels } from './custom.mjs'
import { SUBSCRIPTIONS, subscriptionProviders, startLogin, pollLogin, signOut, loadSubs, subToken, startBrowserLogin, submitLoginCode, handleOAuthCallback, pendingLoginURL, loginStatus, localOnly, visibleModels } from './subs.mjs'
import { wireFor, grokRequestHeaders } from './wire.mjs'

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

// Keys from keys.conf, live: a key added with POST /keys works without a restart.
syncKeys()

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
  [...config.providers, ...customProviders(), ...subscriptionProviders()]
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
            ...(provider.mark?.subscription && { sub: provider.mark.subscription }),
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
  const { prism_quick: quick, prism_avoid: avoid, prism_exclude_families: exclude, prism_prefer: prefer } = payload
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
    // prism_prefer is the route that served this conversation last: the provider still holds its
    // prompt in cache, so it goes first (unless avoided); slow answers are still hedged as usual.
    (a, b) => avoided.has(a.id) - avoided.has(b.id) || (role ? b.roles.includes(role) - a.roles.includes(role) : 0) || (b.id === prefer) - (a.id === prefer),
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

  const post = async (attempt, signal) => {
    let key = attempt.route.key
      let url = `${attempt.route.baseURL}/chat/completions`
      let headers = {
        'content-type': 'application/json',
        'user-agent': 'free-ai-router',
        ...(key && { authorization: `Bearer ${key}` }),
        ...(attempt.route.sub && { 'editor-version': 'prism/1.0', 'copilot-integration-id': 'prism' }),
      }
      // Gemini tool calls carry their signature in the id ("<id>~<sig>"); plain
      // OpenAI-style routes only get the id part.
      const messages = Array.isArray(rest.messages)
        ? rest.messages.map(m => ({
            ...m,
            ...(m.tool_call_id && { tool_call_id: String(m.tool_call_id).split('~')[0] }),
            ...(Array.isArray(m.tool_calls) && { tool_calls: m.tool_calls.map(c => ({ ...c, id: String(c.id ?? '').split('~')[0] })) }),
          }))
        : rest.messages
      let body = { ...rest, messages, ...(quick && attempt.route.quick), model: attempt.route.model }
    if (!key && attempt.route.sub) {
      const sub = SUBSCRIPTIONS.find(s => s.id === attempt.route.sub)
      try {
        key = (await subToken(sub)).token
        } catch (error) {
        return new Response(JSON.stringify({ error: { message: error.message, type: 'subscription' } }), { status: 401, headers: { 'content-type': 'application/json' } })
        }
      const stored = loadSubs()[sub.id] ?? {}
      const wire = sub.wire ? wireFor(sub.wire) : null
      if (wire) {
          attempt.wireStream = new wire.stream()
          // The antigravity wire takes the onboarding project as a second argument.
          body = wire.to({ ...rest, ...(quick && attempt.route.quick), model: attempt.route.model }, stored.projectId)
          headers = {
            'content-type': 'application/json',
            'user-agent': 'free-ai-router',
            authorization: `Bearer ${key}`,
            ...(sub.apiHeaders ?? {}),
            ...(stored.accountId && { 'chatgpt-account-id': stored.accountId }),
            ...(stored.apiKey && { 'x-goog-api-key': stored.apiKey }),
          }
          if (sub.wire === 'codex') url = `${attempt.route.baseURL}/responses${sub.modelsQuery ? `?${new URLSearchParams(sub.modelsQuery)}` : ''}`
          else if (sub.wire === 'anthropic') {
            url = `${attempt.route.baseURL}/messages`
            // Claude Code sends a stable device identity on every request; without
            // it Anthropic throttles OAuth requests with a bare 429.
            if (stored.deviceId) {
              body.metadata = { user_id: JSON.stringify({ device_id: stored.deviceId, account_uuid: stored.accountUuid, session_id: stored.sessionId }) }
            }
            headers['x-client-request-id'] = randomUUID()
          }
          else if (sub.wire === 'antigravity') {
            // Antigravity sends the whole Gemini body to one endpoint, in its own
            // envelope, with the IDE's user-agent (request.go buildRequest).
            url = `${attempt.route.baseURL}/v1internal:streamGenerateContent?alt=sse`
            headers['user-agent'] = sub.apiUserAgent ?? 'free-ai-router'
          }
        } else {
          headers = {
            'content-type': 'application/json',
            'user-agent': sub.id === 'grok' ? sub.apiUserAgent : 'free-ai-router',
            authorization: `Bearer ${key}`,
            ...(sub.apiHeaders ?? {}),
            ...(sub.id === 'grok' ? grokRequestHeaders(body, stored.sessionId) : { 'editor-version': 'prism/1.0', 'copilot-integration-id': 'prism' }),
          }
        }
      }
      return fetch(url, {
        method: 'POST',
        headers,
        body: JSON.stringify(body),
        signal,
      })
    }

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
      // A wired route's body is SSE by construction (the wire adapter emits it),
      // whatever content-type the vendor's backend declared.
      'content-type': attempt.wireStream ? 'text/event-stream' : upstream.headers.get('content-type') ?? 'application/json',
      'cache-control': 'no-cache',
      'x-free-route': attempt.route.id,
      'x-prism-family': attempt.route.family,
      'x-free-wait': String(wait),
      'x-prism-prefer': '1',
    })
    res.write(attempt.wireStream ? attempt.wireStream.write(first) : first)
    try {
      while (!closed) {
        const chunk = await within(reader.read(), stallTimeout)
        if (!chunk) {
          console.log(`✗ ${attempt.route.id} stalled for ${seconds(stallTimeout)}, cut off`)
          lastErrors.set(attempt.route.id, `${attempt.route.id} stalled mid-reply`)
          attempt.controller.abort()
          break
        }
        if (chunk.done) {
          // A wired stream that ended cleanly without its own end marker still
          // closes the reply, so the agent does not read it as cut off.
          if (attempt.wireStream?.finish && !attempt.wireStream.done) res.write(attempt.wireStream.finish())
          break
        }
        res.write(attempt.wireStream ? attempt.wireStream.write(chunk.value) : chunk.value)
        // A wired reply is complete once its end marker went out.
        if (attempt.wireStream?.done) {
          reader.cancel().catch(() => {})
          break
        }
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

const freeKeyRows = () => {
  const rows = []
  const seen = new Set()
  for (const provider of config.providers) {
    if (!provider.requires || seen.has(provider.requires)) continue
    seen.add(provider.requires)
    rows.push({ id: provider.name, name: provider.name, var: provider.requires, set: process.env[provider.requires] ? maskKey(process.env[provider.requires]) : null })
  }
  return rows
}

const keysHandler = async (req, res) => {
  try {
    return await keysRoute(req, res)
  } catch {
    return send(res, 500, { error: { message: 'the key could not be saved; check the disk and file permissions', type: 'internal' } })
  }
}

const keysRoute = async (req, res) => {
  if (req.method !== 'GET' && req.method !== 'POST') return send(res, 405, { error: { message: 'Method not allowed', type: 'invalid_request' } })
  if (req.method === 'GET') {
    syncKeys()
    return send(res, 200, {
      free: freeKeyRows(),
      presets: PRESETS.map(p => ({
        id: p.id,
        name: p.name,
        var: p.keyVar ?? '',
        paid: p.paid,
        keyless: p.keyless === true,
        keyUrl: p.keyUrl ?? '',
        set: p.keyVar && process.env[p.keyVar] ? maskKey(process.env[p.keyVar]) : null,
      })),
    })
  }
  let body
  try {
    body = JSON.parse(await readBody(req))
  } catch {
    return send(res, 400, { error: { message: 'Request body must be JSON', type: 'invalid_request' } })
  }
  const id = String(body?.id ?? '').trim()
  const keyVar = String(body?.var ?? '').trim()
  if (body?.value !== undefined && typeof body.value !== 'string') return send(res, 400, { error: { message: 'the key must be text', type: 'invalid_request' } })
  const value = String(body?.value ?? '').trim()
  if (value && (value.length > 4096 || /[\r\n\0]/.test(value))) return send(res, 400, { error: { message: 'that does not look like a key', type: 'invalid_request' } })
  const preset = PRESETS.find(p => p.id === id || (keyVar && p.keyVar === keyVar)) || null
  const freeVar = preset ? null : config.providers.find(p => p.requires && (p.requires === keyVar || p.name === id))?.requires ?? null
  if (!preset && !freeVar) return send(res, 404, { error: { message: `no provider called "${id || keyVar}"`, type: 'not_found' } })
  if (preset?.keyless) return send(res, 400, { error: { message: `${preset.id} needs no key`, type: 'invalid_request' } })
  const name = preset ? preset.keyVar : freeVar
  setKeyVar(keysPath(), name, value)
  if (!value) return send(res, 200, { ok: true, removed: true })
  let models = null
  let note = ''
  if (preset) {
    let existing = []
    try {
      const parsed = JSON.parse(readFileSync(customFilePath(), 'utf8'))
      existing = parsed.find?.(entry => entry?.id === preset.id)?.models ?? []
    } catch {}
    const probed = await probeModels(preset, value)
    const list = probed.models?.length ? probed.models : [...(existing || []), ...preset.models]
    if (list.length) {
      const saved = upsertPresetEntry(customFilePath(), preset, list)
      if (saved.error) note = `key saved, but the model list was not: ${saved.error}`
      else {
        models = saved.models.length
        if (!probed.models?.length) note = 'the service did not list its models, so the known ones are used'
      }
    } else {
      note = 'key saved, but the service did not list its models; add them with: prism keys add'
    }
  }
  return send(res, 200, { ok: true, masked: maskKey(value), models, note })
}

const loginsHandler = async (req, res) => {
  try {
    return await loginsRoute(req, res)
  } catch {
    return send(res, 502, { error: { message: 'the sign-in service could not be reached; try again in a minute', type: 'login_failed' } })
  }
}

const loginsRoute = async (req, res) => {
  let body = {}
  if (req.method === 'POST') {
    try {
      body = JSON.parse(await readBody(req))
    } catch {
      return send(res, 400, { error: { message: 'Request body must be JSON', type: 'invalid_request' } })
    }
  }
  const sub = SUBSCRIPTIONS.find(s => s.id === String(body?.id ?? '').trim())
  if (req.method === 'GET') {
    if (new URL(req.url, 'http://router').pathname === '/logins/pending') return send(res, 200, { url: pendingLoginURL() })
    return send(res, 200, { subscriptions: SUBSCRIPTIONS.map(loginStatus) })
  }
  if (!sub) return send(res, 404, { error: { message: `no subscription called "${body?.id}"`, type: 'not_found' } })
  if (req.method === 'POST' && Object.hasOwn(body, 'code')) {
    const result = await submitLoginCode(sub, body.code)
    if (result.error) return send(res, 400, { error: { message: result.error } })
    return send(res, 200, result)
  }
  if (body?.signOut === true) {
    signOut(sub)
    return send(res, 200, { ok: true, signedOut: true })
  }
  if (req.method === 'POST' && body?.poll === true) {
    const result = await pollLogin(sub)
    return send(res, 200, result)
  }
  if (sub.kind === 'browser' || body?.browser === true) {
    const started = await startBrowserLogin(sub)
    if (started.error) return send(res, 502, { error: { message: started.error, type: 'login_failed' } })
    return send(res, 200, started)
  }
  const started = await startLogin(sub)
  if (started.error) return send(res, 502, { error: { message: started.error, type: 'login_failed' } })
  return send(res, 200, started)
}

http
  .createServer((req, res) => {
    if (!localOnly(req, res, port)) return
    const path = new URL(req.url, 'http://router').pathname
    if (req.method !== 'GET' && req.method !== 'HEAD' && (path === '/keys' || path === '/logins') && process.env.PRISM_ROUTER_TOKEN) {
      const digest = value => createHash('sha256').update(value).digest()
      if (!timingSafeEqual(digest(req.headers['x-prism-token'] ?? ''), digest(process.env.PRISM_ROUTER_TOKEN))) {
        res.writeHead(403, { 'content-type': 'text/plain' })
        return res.end('router admin token required\n')
      }
    }
    if (req.method === 'POST' && path === '/v1/chat/completions') return complete(req, res)
    if (path === '/keys') return keysHandler(req, res)
    if (path === '/logins/pending' && req.method !== 'GET') return send(res, 405, { error: { message: 'Method not allowed', type: 'invalid_request' } })
    if (path === '/logins' || path === '/logins/pending') return loginsHandler(req, res)
    if ((path === '/callback' || path === '/oauth2callback' || path === '/auth/callback') && req.method === 'GET') return void handleOAuthCallback(req, res, path).then(handled => { if (!handled) send(res, 404, { error: { message: 'Not found', type: 'not_found' } }) })
    if (path === '/v1/models') return send(res, 200, { object: 'list', data: ['auto', ...roleNames.map(role => `auto:${role}`), 'auto:vision', ...routes().filter(route => route.source === 'yours' || route.source === 'subscription').map(route => route.id)].map(id => ({ id, object: 'model', owned_by: 'free-ai' })) })
    if (path === '/status' && new URL(req.url, 'http://router').searchParams.get('format') === 'text') {
      res.writeHead(200, { 'content-type': 'text/plain' })
      return res.end(status().map(row => `${row.ready ? '✓ ready  ' : `… resting ${row.retryInSeconds}s`}  ${row.route}${row.lastError ? `\n             last error: ${row.lastError}` : ''}`).join('\n') + '\n')
    }
    if (path === '/status') return send(res, 200, status())
    if (path === '/health') return send(res, 200, { ok: true })
    send(res, 404, { error: { message: 'Not found', type: 'not_found' } })
  })
  .listen(port, host, () => console.log(`Free AI router on ${host}:${port} → ${routes().map(route => route.id).join(', ')}`))
