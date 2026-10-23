import { test, before, after } from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { spawn } from 'node:child_process'
import { mkdtempSync, copyFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const servers = []
const upstream = handler =>
  new Promise(resolve => {
    const s = http.createServer(handler).listen(0, '127.0.0.1', () => resolve(`http://127.0.0.1:${s.address().port}`))
    servers.push(s)
  })

const streamOK = text => (req, res) => {
  res.writeHead(200, { 'content-type': 'text/event-stream' })
  res.end(`data: {"choices":[{"delta":{"content":"${text}"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n`)
}

let router, base
const calls = { busy: 0, slow: 0 }

before(async () => {
  const fast = await upstream(streamOK('fast'))
  const busy = await upstream((req, res) => {
    calls.busy++
    res.writeHead(429, { 'retry-after': '60' })
    res.end('busy')
  })
  const slow = await upstream((req, res) => {
    calls.slow++
    setTimeout(() => streamOK('slow')(req, res), 5000)
  })
  const dir = mkdtempSync(join(tmpdir(), 'router-'))
  copyFileSync(new URL('./server.mjs', import.meta.url), join(dir, 'server.mjs'))
  copyFileSync(new URL('./custom.mjs', import.meta.url), join(dir, 'custom.mjs'))
  writeFileSync(join(dir, 'providers.json'), JSON.stringify({ providers: [
    { name: 'busy', baseURL: busy, models: ['m'] },
    { name: 'slow', baseURL: slow, models: ['m'] },
    { name: 'fast', baseURL: fast, models: ['m'] },
  ] }))
  const port = 4900 + Math.floor(Math.random() * 90)
  base = `http://127.0.0.1:${port}`
  router = spawn(process.execPath, ['server.mjs'], { cwd: dir, env: { ...process.env, PORT: String(port), HEDGE_MS: '300', STATS_FILE: '' } })
  for (let i = 0; i < 50; i++) {
    try { if ((await fetch(base + '/health')).ok) return } catch {}
    await new Promise(r => setTimeout(r, 100))
  }
  throw new Error('router did not start')
})

after(() => {
  router?.kill()
  for (const s of servers) s.close()
})

const chat = body => fetch(base + '/v1/chat/completions', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ model: 'auto', messages: [{ role: 'user', content: 'hi' }], ...body }) })

test('skips a busy route and the race picks the fast one', async () => {
  const res = await chat()
  assert.equal(res.status, 200)
  assert.equal(res.headers.get('x-free-route'), 'fast/m')
  assert.match(await res.text(), /fast/)
  assert.ok(calls.busy >= 1)
})

test('status shows the busy route resting', async () => {
  const rows = await (await fetch(base + '/status')).json()
  const busy = rows.find(r => r.route === 'busy/m')
  assert.equal(busy.ready, false)
  assert.ok(busy.retryInSeconds > 0)
})

test('rejects bodies over 4 MB', async () => {
  const res = await fetch(base + '/v1/chat/completions', { method: 'POST', body: 'x'.repeat(5 * 1024 * 1024) })
  assert.equal(res.status, 413)
})

test('bad JSON is a 400', async () => {
  const res = await fetch(base + '/v1/chat/completions', { method: 'POST', body: '{nope' })
  assert.equal(res.status, 400)
})

const echo = (delay = 0) => (req, res) => {
  let raw = ''
  req.on('data', chunk => (raw += chunk))
  req.on('end', () =>
    setTimeout(() => {
      res.writeHead(200, { 'content-type': 'application/json' })
      res.end(JSON.stringify({ received: JSON.parse(raw) }))
    }, delay),
  )
}

const routers = []
const startRouter = async (providers, env = {}) => {
  const dir = mkdtempSync(join(tmpdir(), 'router-'))
  copyFileSync(new URL('./server.mjs', import.meta.url), join(dir, 'server.mjs'))
  copyFileSync(new URL('./custom.mjs', import.meta.url), join(dir, 'custom.mjs'))
  writeFileSync(join(dir, 'providers.json'), JSON.stringify({ providers }))
  const probe = http.createServer().listen(0, '127.0.0.1')
  await new Promise(resolve => probe.once('listening', resolve))
  const port = probe.address().port
  await new Promise(resolve => probe.close(resolve))
  const child = spawn(process.execPath, ['server.mjs'], { cwd: dir, env: { ...process.env, PORT: String(port), HEDGE_MS: '300', STATS_FILE: '', ...env } })
  routers.push(child)
  const url = `http://127.0.0.1:${port}`
  for (let i = 0; i < 50; i++) {
    try { if ((await fetch(url + '/health')).ok) break } catch {}
    await new Promise(r => setTimeout(r, 100))
  }
  const ask = (body, headers = {}) => fetch(url + '/v1/chat/completions', { method: 'POST', headers: { 'content-type': 'application/json', ...headers }, body: JSON.stringify({ model: 'auto', messages: [{ role: 'user', content: 'hi' }], ...body }) })
  return { url, ask }
}

after(() => {
  for (const child of routers) child.kill()
})

const model = (name, extra = {}) => ({ name, ...extra })

test('family header, status metadata, and prism_* stripped upstream', async () => {
  const a = await upstream(echo())
  const b = await upstream(echo())
  const r = await startRouter([
    { name: 'pa', baseURL: a, models: [model('alpha', { family: 'nemotron', roles: ['review'] })] },
    { name: 'pb', baseURL: b, models: [model('beta', { family: 'gemini', roles: ['fast'], vision: true })] },
  ])
  const res = await r.ask({ prism_quick: true, prism_avoid: ['nope'], prism_exclude_families: ['x'], prism_other: 1 })
  assert.equal(res.status, 200)
  assert.equal(res.headers.get('x-free-route'), 'pa/alpha')
  assert.equal(res.headers.get('x-prism-family'), 'nemotron')
  const { received } = await res.json()
  assert.equal(received.model, 'alpha')
  assert.deepEqual(Object.keys(received).filter(key => key.startsWith('prism_')), [])
  const rows = await (await fetch(r.url + '/status')).json()
  assert.deepEqual(rows.map(({ route, provider, family, roles, vision }) => ({ route, provider, family, roles, vision })), [
    { route: 'pa/alpha', provider: 'pa', family: 'nemotron', roles: ['review'], vision: false },
    { route: 'pb/beta', provider: 'pb', family: 'gemini', roles: ['fast'], vision: true },
  ])
})

test('auto:role ranks matching routes first and strips the suffix', async () => {
  const a = await upstream(echo())
  const b = await upstream(echo())
  const r = await startRouter([
    { name: 'pa', baseURL: a, models: [model('alpha', { family: 'glm' })] },
    { name: 'pb', baseURL: b, models: [model('beta', { family: 'mistral', roles: ['code'] })] },
  ])
  const code = await r.ask({ model: 'auto:code' })
  assert.equal(code.headers.get('x-free-route'), 'pb/beta')
  assert.equal((await code.json()).received.model, 'beta')
  assert.equal((await r.ask({ model: 'auto:review' })).headers.get('x-free-route'), 'pa/alpha')
  assert.equal((await r.ask({ model: 'auto' })).headers.get('x-free-route'), 'pa/alpha')
})

test('auto:vision uses only vision routes and 503s without one', async () => {
  const a = await upstream(echo())
  const b = await upstream(echo())
  const sighted = await startRouter([
    { name: 'pa', baseURL: a, models: [model('alpha', { family: 'glm' })] },
    { name: 'pb', baseURL: b, models: [model('beta', { family: 'gemini', vision: true })] },
  ])
  assert.equal((await sighted.ask({ model: 'auto:vision' })).headers.get('x-free-route'), 'pb/beta')
  const blind = await startRouter([{ name: 'pa', baseURL: a, models: [model('alpha', { family: 'glm' })] }])
  const res = await blind.ask({ model: 'auto:vision' })
  assert.equal(res.status, 503)
  assert.equal((await res.json()).error.message, 'no free model that can see images is available right now')
})

test('exact pin by route id or upstream model name', async () => {
  const a = await upstream(echo())
  const b = await upstream(echo())
  const c = await upstream(echo())
  const r = await startRouter([
    { name: 'pa', baseURL: a, models: [model('alpha', { family: 'glm' })] },
    { name: 'pb', baseURL: b, models: [model('shared', { family: 'qwen' })] },
    { name: 'pc', baseURL: c, models: [model('shared', { family: 'mistral' })] },
  ])
  assert.equal((await r.ask({ model: 'pb/shared' })).headers.get('x-free-route'), 'pb/shared')
  assert.equal((await r.ask({ model: 'alpha' })).headers.get('x-free-route'), 'pa/alpha')
  const viaName = await r.ask({ model: 'shared', prism_avoid: ['pb/shared'] })
  assert.equal(viaName.headers.get('x-free-route'), 'pc/shared')
})

test('prism_exclude_families drops routes hard, prism_avoid only reorders', async () => {
  const a = await upstream(echo())
  const b = await upstream(echo())
  const r = await startRouter([
    { name: 'pa', baseURL: a, models: [model('alpha', { family: 'glm' })] },
    { name: 'pb', baseURL: b, models: [model('beta', { family: 'qwen' })] },
  ])
  assert.equal((await r.ask({ prism_exclude_families: ['glm'] })).headers.get('x-free-route'), 'pb/beta')
  assert.equal((await r.ask({ prism_avoid: ['pa/alpha'] })).headers.get('x-free-route'), 'pb/beta')
  const none = await r.ask({ prism_exclude_families: ['glm', 'qwen'] })
  assert.equal(none.status, 503)
  assert.ok((await none.json()).error.message)
  const lone = await startRouter([{ name: 'pa', baseURL: a, models: [model('alpha', { family: 'glm' })] }])
  assert.equal((await lone.ask({ prism_avoid: ['pa/alpha'] })).status, 200)
})

test('prism_prefer puts the cached route first, but not ahead of prism_avoid', async () => {
  const a = await upstream(echo())
  const b = await upstream(echo())
  const r = await startRouter([
    { name: 'pa', baseURL: a, models: [model('alpha', { family: 'glm' })] },
    { name: 'pb', baseURL: b, models: [model('beta', { family: 'qwen' })] },
  ])
  assert.equal((await r.ask({})).headers.get('x-free-route'), 'pa/alpha')
  const sticky = await r.ask({ prism_prefer: 'pb/beta' })
  assert.equal(sticky.headers.get('x-free-route'), 'pb/beta')
  assert.deepEqual(Object.keys((await sticky.json()).received).filter(key => key.startsWith('prism_')), [])
  assert.equal((await r.ask({ prism_prefer: 'pb/beta', prism_avoid: ['pb/beta'] })).headers.get('x-free-route'), 'pa/alpha')
  assert.equal((await r.ask({ prism_prefer: 'gone/model' })).headers.get('x-free-route'), 'pa/alpha')
})

test('hedging is capped by ready providers and off for x-prism-crew', async () => {
  const hits = { two: 0, crew: 0 }
  const slow = () => upstream((req, res) => setTimeout(() => streamOK('slow')(req, res), 2600))
  const counting = key => upstream((req, res) => {
    hits[key]++
    streamOK('quick')(req, res)
  })
  const two = await startRouter([
    { name: 'pa', baseURL: await slow(), models: [model('m', { family: 'glm' })] },
    { name: 'pb', baseURL: await counting('two'), models: [model('m', { family: 'qwen' })] },
  ])
  const first = await two.ask()
  assert.equal(first.headers.get('x-free-route'), 'pa/m')
  await first.text()
  assert.equal(hits.two, 0)
  const open = await startRouter([
    { name: 'pa', baseURL: await slow(), models: [model('m', { family: 'glm' })] },
    { name: 'pb', baseURL: await counting('crew'), models: [model('m', { family: 'qwen' })] },
    { name: 'pc', baseURL: await slow(), models: [model('m', { family: 'llama' })] },
  ])
  const crew = await open.ask({}, { 'x-prism-crew': '1' })
  assert.equal(crew.headers.get('x-free-route'), 'pa/m')
  await crew.text()
  assert.equal(hits.crew, 0)
  const raced = await open.ask()
  assert.equal(raced.headers.get('x-free-route'), 'pb/m')
  assert.equal(hits.crew, 1)
})

test('per-provider in-flight cap sends extras elsewhere or makes them wait', async () => {
  const slow = await upstream(echo(900))
  const other = await upstream(echo(0))
  const r = await startRouter(
    [
      { name: 'pa', baseURL: slow, models: [model('m', { family: 'glm' })] },
      { name: 'pb', baseURL: other, models: [model('m', { family: 'qwen' })] },
    ],
    { PRISM_PROVIDER_INFLIGHT: '1' },
  )
  const first = r.ask()
  await new Promise(resolve => setTimeout(resolve, 200))
  const second = await r.ask()
  assert.equal(second.headers.get('x-free-route'), 'pb/m')
  assert.equal((await first).headers.get('x-free-route'), 'pa/m')

  let active = 0
  let peak = 0
  const solo = await upstream((req, res) => {
    active++
    peak = Math.max(peak, active)
    res.on('close', () => active--)
    echo(500)(req, res)
  })
  const lone = await startRouter([{ name: 'pa', baseURL: solo, models: [model('m', { family: 'glm' })] }], { PRISM_PROVIDER_INFLIGHT: '1' })
  const replies = await Promise.all([lone.ask(), lone.ask(), lone.ask()])
  assert.deepEqual(replies.map(reply => reply.status), [200, 200, 200])
  await Promise.all(replies.map(reply => reply.text()))
  assert.equal(peak, 1)
})

test('hedges sooner when the leading route has learned to be slower than the hedge delay', async () => {
  const hits = { b: 0 }
  const slow = await upstream((req, res) => setTimeout(() => streamOK('slow')(req, res), 900))
  const quick = await upstream((req, res) => {
    hits.b++
    streamOK('quick')(req, res)
  })
  const r = await startRouter(
    [
      { name: 'pa', baseURL: slow, models: [model('m', { family: 'glm' })] },
      { name: 'pb', baseURL: quick, models: [model('m', { family: 'qwen' })] },
      { name: 'pc', baseURL: await upstream(streamOK('c')), models: [model('m', { family: 'mistral' })] },
    ],
    { HEDGE_MS: '700', COLD_HEDGE_MS: '100' },
  )
  // Crew requests never hedge, so this teaches the router that pa takes ~900 ms.
  const taught = await r.ask({}, { 'x-prism-crew': '1' })
  assert.equal(taught.headers.get('x-free-route'), 'pa/m')
  await taught.text()
  assert.equal(hits.b, 0)

  const started = Date.now()
  const raced = await r.ask()
  assert.equal(raced.headers.get('x-free-route'), 'pb/m')
  await raced.text()
  assert.ok(Date.now() - started < 600, `hedged after ${Date.now() - started} ms; the learned-slow route should hedge at the short delay, not 700 ms`)
  assert.equal(hits.b, 1)

  // Crew requests still never hedge, however slow the route.
  const crew = await r.ask({}, { 'x-prism-crew': '1' })
  assert.equal(crew.headers.get('x-free-route'), 'pa/m')
  await crew.text()
  assert.equal(hits.b, 1)
})

test('your own paid models are used only when pinned ', async () => {
  const free = await upstream(echo())
  const mine = await upstream(echo())
  const file = join(mkdtempSync(join(tmpdir(), 'custom-')), 'custom-providers.json')
  writeFileSync(file, JSON.stringify([{ id: 'mine', baseURL: mine, keyVar: 'MINE_API_KEY', models: ['big'] }]))
  const env = { PRISM_CUSTOM_FILE: file, MINE_API_KEY: 'sk-test-1234' }
  const r = await startRouter([{ name: 'free', baseURL: free, models: ['m'] }], env)
  assert.equal((await (await r.ask({})).json()).received.model, 'm')
  assert.equal((await (await r.ask({ model: 'mine/big' })).json()).received.model, 'big')
  assert.equal((await (await r.ask({ model: 'big' })).json()).received.model, 'm')
  const rows = await (await fetch(r.url + '/status')).json()
  assert.deepEqual(rows.filter(row => row.source === 'yours').map(row => [row.route, row.paid]), [['mine/big', true]])
  assert.ok((await (await fetch(r.url + '/v1/models')).json()).data.some(m => m.id === 'mine/big'))
})

const deadURL = async () => {
  const s = http.createServer().listen(0, '127.0.0.1')
  await new Promise(resolve => s.once('listening', resolve))
  const url = `http://127.0.0.1:${s.address().port}`
  await new Promise(resolve => s.close(resolve))
  return url
}

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms))
const deferred = () => {
  let resolve
  const promise = new Promise(r => (resolve = r))
  return { promise, resolve }
}

test('falls back past 5xx, network errors and empty replies, and rests the failed routes', async () => {
  const hits = { five: 0, empty: 0 }
  const five = await upstream((req, res) => {
    hits.five++
    res.writeHead(500)
    res.end('boom')
  })
  const empty = await upstream((req, res) => {
    hits.empty++
    res.writeHead(200, { 'content-type': 'text/event-stream' })
    res.end()
  })
  const r = await startRouter([
    { name: 'dead', baseURL: await deadURL(), models: [model('m', { family: 'a' })] },
    { name: 'five', baseURL: five, models: [model('m', { family: 'b' })] },
    { name: 'empty', baseURL: empty, models: [model('m', { family: 'c' })] },
    { name: 'good', baseURL: await upstream(streamOK('good')), models: [model('m', { family: 'd' })] },
  ])
  const res = await r.ask()
  assert.equal(res.status, 200)
  assert.equal(res.headers.get('x-free-route'), 'good/m')
  assert.match(await res.text(), /good/)
  assert.equal(hits.five, 1)
  assert.equal(hits.empty, 1)

  const rows = await (await fetch(r.url + '/status')).json()
  const byRoute = Object.fromEntries(rows.map(row => [row.route, row]))
  for (const id of ['dead/m', 'five/m', 'empty/m']) {
    assert.equal(byRoute[id].ready, false, `${id} should be resting`)
    assert.ok(byRoute[id].retryInSeconds > 0)
  }
  assert.match(byRoute['dead/m'].lastError, /dead\/m failed \(network\)/)
  assert.match(byRoute['five/m'].lastError, /five\/m failed \(500\): boom/)
  assert.match(byRoute['empty/m'].lastError, /empty reply/)
  assert.equal(byRoute['good/m'].ready, true)
  assert.equal(byRoute['good/m'].lastError, null)

  // Resting routes are skipped on the next request.
  const again = await r.ask()
  assert.equal(again.headers.get('x-free-route'), 'good/m')
  await again.text()
  assert.equal(hits.five, 1)
  assert.equal(hits.empty, 1)

  const text = await (await fetch(r.url + '/status?format=text')).text()
  assert.match(text, /✓ ready\s+good\/m/)
  assert.match(text, /resting \d+s\s+five\/m\n\s+last error: five\/m failed \(500\)/)
})

test('503 lists every failure when all routes fail', async () => {
  const mk = status => upstream((req, res) => {
    res.writeHead(status)
    res.end(`status ${status}`)
  })
  const r = await startRouter([
    { name: 'pa', baseURL: await mk(401), models: [model('m', { family: 'a' })] },
    { name: 'pb', baseURL: await mk(503), models: [model('m', { family: 'b' })] },
    { name: 'pc', baseURL: await mk(404), models: [model('m', { family: 'c' })] },
  ])
  const res = await r.ask()
  assert.equal(res.status, 503)
  assert.match(res.headers.get('content-type'), /application\/json/)
  const { error } = await res.json()
  assert.equal(error.type, 'no_route_available')
  assert.match(error.message, /^Every free AI route failed\./)
  assert.deepEqual(error.failures.map(f => [f.route, f.status]).sort(), [['pa/m', 401], ['pb/m', 503], ['pc/m', 404]])

  // With everything resting, the router still tries again rather than refusing outright.
  const retry = await r.ask()
  assert.equal(retry.status, 503)
  assert.equal((await retry.json()).error.failures.length, 3)
})

test('503 when no routes are configured, and routes needing a missing key are skipped', async () => {
  const hits = { keyed: 0 }
  const keyed = await upstream((req, res) => {
    hits.keyed++
    echo()(req, res)
  })
  const none = await startRouter([{ name: 'keyed', baseURL: keyed, requires: 'PRISM_TEST_MISSING_KEY', models: ['m'] }], { PRISM_TEST_MISSING_KEY: '' })
  const res = await none.ask()
  assert.equal(res.status, 503)
  assert.equal((await res.json()).error.message, 'No free AI routes are configured')
  assert.equal(hits.keyed, 0)
})

test('provider key is sent as a bearer token, and a rejected key falls back', async () => {
  const seen = {}
  const spy = name => upstream((req, res) => {
    seen[name] = req.headers.authorization ?? null
    seen[`${name}-agent`] = req.headers['user-agent']
    echo()(req, res)
  })
  const bad = await upstream((req, res) => {
    seen.bad = req.headers.authorization
    res.writeHead(401)
    res.end('bad key')
  })
  const r = await startRouter(
    [
      { name: 'bad', baseURL: bad, requires: 'BAD_KEY', models: ['m'] },
      { name: 'open', baseURL: await spy('open'), models: ['m'] },
      { name: 'keyed', baseURL: await spy('keyed'), requires: 'GOOD_KEY', models: ['m'] },
    ],
    { BAD_KEY: 'sk-bad', GOOD_KEY: 'sk-good' },
  )
  const res = await r.ask()
  assert.equal(res.headers.get('x-free-route'), 'open/m')
  await res.text()
  assert.equal(seen.bad, 'Bearer sk-bad')
  assert.equal(seen.open, null)
  assert.equal(seen['open-agent'], 'free-ai-router')
  const keyed = await r.ask({ model: 'keyed/m' })
  assert.equal(keyed.headers.get('x-free-route'), 'keyed/m')
  await keyed.text()
  assert.equal(seen.keyed, 'Bearer sk-good')
})

test('streams chunks through as they arrive, with upstream content-type and route headers', async () => {
  const gate = deferred()
  const parts = ['data: {"n":1}\n\n', 'data: {"n":2}\n\n', 'data: [DONE]\n\n']
  const up = await upstream(async (req, res) => {
    res.writeHead(200, { 'content-type': 'text/event-stream' })
    res.write(parts[0])
    await gate.promise
    res.write(parts[1])
    await sleep(30)
    res.end(parts[2])
  })
  const r = await startRouter([{ name: 'pa', baseURL: up, models: [model('m', { family: 'glm' })] }])
  const res = await r.ask({ stream: true })
  assert.equal(res.status, 200)
  assert.equal(res.headers.get('content-type'), 'text/event-stream')
  assert.equal(res.headers.get('cache-control'), 'no-cache')
  assert.equal(res.headers.get('x-free-route'), 'pa/m')
  assert.equal(res.headers.get('x-prism-family'), 'glm')
  assert.ok(Number(res.headers.get('x-free-wait')) >= 0)

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  const firstChunk = await reader.read()
  // The first chunk is already here although the upstream reply is still open.
  assert.equal(decoder.decode(firstChunk.value), parts[0])
  gate.resolve()
  let rest = ''
  for (;;) {
    const { value, done } = await reader.read()
    if (done) break
    rest += decoder.decode(value)
  }
  assert.equal(rest, parts[1] + parts[2])
})

test('forwards the stream flag and other body fields untouched', async () => {
  const r = await startRouter([{ name: 'pa', baseURL: await upstream(echo()), models: [model('m', { family: 'glm' })] }])
  const messages = [{ role: 'user', content: 'héllo ✓' }]
  const res = await r.ask({ stream: true, temperature: 0.2, messages, tools: [{ type: 'function', function: { name: 'f' } }] })
  const { received } = await res.json()
  assert.equal(received.stream, true)
  assert.equal(received.temperature, 0.2)
  assert.deepEqual(received.messages, messages)
  assert.equal(received.tools[0].function.name, 'f')
  assert.equal(received.model, 'm')
})

test('prism_quick merges the provider quick overrides into the upstream body', async () => {
  const r = await startRouter([{ name: 'pa', baseURL: await upstream(echo()), quick: { max_tokens: 64 }, models: [model('m', { family: 'glm' })] }])
  assert.equal((await (await r.ask({ prism_quick: true })).json()).received.max_tokens, 64)
  assert.equal((await (await r.ask({})).json()).received.max_tokens, undefined)
})

test('a client that hangs up mid-stream does not break the router', async () => {
  let n = 0
  const up = await upstream((req, res) => {
    res.writeHead(200, { 'content-type': 'text/event-stream' })
    res.write('data: {"n":1}\n\n')
    const timer = setInterval(() => res.write(`data: {"n":${++n}}\n\n`), 50)
    res.on('close', () => clearInterval(timer))
  })
  const r = await startRouter([{ name: 'pa', baseURL: up, models: [model('m', { family: 'glm' })] }])
  const controller = new AbortController()
  const res = await fetch(r.url + '/v1/chat/completions', { method: 'POST', signal: controller.signal, body: JSON.stringify({ model: 'auto', stream: true }) })
  const reader = res.body.getReader()
  assert.ok((await reader.read()).value.length > 0)
  controller.abort()
  await reader.read().catch(() => {})
  await sleep(200)
  assert.equal((await fetch(r.url + '/health')).status, 200)
  const next = await r.ask({ stream: true })
  assert.equal(next.status, 200)
  assert.equal(next.headers.get('x-free-route'), 'pa/m')
  await next.body.cancel()
})

test('/health, /v1/models and unknown paths', async () => {
  const r = await startRouter([{ name: 'pa', baseURL: await upstream(echo()), models: [model('m', { family: 'glm' })] }])
  assert.deepEqual(await (await fetch(r.url + '/health')).json(), { ok: true })
  const models = await (await fetch(r.url + '/v1/models')).json()
  assert.equal(models.object, 'list')
  assert.deepEqual(models.data.map(m => m.id), ['auto', 'auto:code', 'auto:fast', 'auto:review', 'auto:vision'])
  const missing = await fetch(r.url + '/nope')
  assert.equal(missing.status, 404)
  assert.equal((await missing.json()).error.type, 'not_found')
  assert.equal((await fetch(r.url + '/v1/chat/completions')).status, 404)
})
