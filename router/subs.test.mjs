import { test } from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { createHash } from 'node:crypto'
import { mkdtempSync, writeFileSync, readFileSync, copyFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { loadSubs, startLogin, pollLogin, signOut, subscriptionProviders, subToken, startBrowserLogin, loginError, loginStatus, handleOAuthCallback, submitLoginCode, SUBSCRIPTIONS } from './subs.mjs'

const servers = []
const upstream = handler =>
  new Promise(resolve => {
    const s = http.createServer(handler).listen(0, '127.0.0.1', () => resolve(`http://127.0.0.1:${s.address().port}`))
    servers.push(s)
  })

const write = value => {
  const file = join(mkdtempSync(join(tmpdir(), 'subs-')), 'subscriptions.json')
  writeFileSync(file, typeof value === 'string' ? value : JSON.stringify(value))
  return file
}

test('pasted browser codes race callbacks and validate optional state', async t => {
  const file = write('{}')
  process.env.PRISM_SUBS_FILE = file
  t.after(() => { delete process.env.PRISM_SUBS_FILE })
  let exchanges = 0
  let expectedState
  let authorize
  const token = await upstream((req, res) => {
    let raw = ''
    req.on('data', chunk => { raw += chunk })
    req.on('end', () => {
      const body = new URLSearchParams(raw)
      exchanges++
      assert.equal(body.get('code'), 'the-code')
      assert.equal(body.get('state'), expectedState)
      assert.equal(body.get('redirect_uri'), authorize.searchParams.get('redirect_uri'))
      assert.equal(createHash('sha256').update(body.get('code_verifier')).digest('base64url'), authorize.searchParams.get('code_challenge'))
      res.end(JSON.stringify({ access_token: 'paste-token' }))
    })
  })
  const models = await upstream((req, res) => res.end(JSON.stringify({ data: [{ id: 'grok-test' }] })))
  const sub = { id: 'grok', name: 'Grok', kind: 'browser', authorizeURL: 'https://auth.test', tokenURL: token, clientID: 'cid', redirectPath: '/callback', stateInExchange: true, apiBase: models }
  const start = async () => {
    authorize = new URL((await startBrowserLogin(sub)).url)
    expectedState = authorize.searchParams.get('state')
  }
  assert.equal((await submitLoginCode(sub, 'code')).error, 'no sign-in is waiting for grok; run /login grok first')
  for (const input of [() => '  the-code  ', () => `http://localhost/callback?code=the-code&state=${expectedState}`, () => `the-code#${expectedState}`, () => 'http://localhost/callback?code=the-code']) {
    await start()
    assert.equal((await submitLoginCode(sub, `http://localhost/callback?code=the-code&state=wrong`)).error, 'that code did not work; copy it again')
    assert.equal((await submitLoginCode(sub, 'http://localhost/callback?error=access_denied&error_description=try+again')).error, 'try again')
    assert.equal((await submitLoginCode(sub, '')).error, 'that code did not work; copy it again')
    const result = await submitLoginCode(sub, input())
    assert.equal(result.ok, true)
    assert.deepEqual(result.models, ['grok-test'])
  }
  await start()
  const now = Date.now
  Date.now = () => now() + 601_000
  try {
    assert.equal((await submitLoginCode(sub, 'the-code')).error, 'the sign-in timed out; run /login again')
  } finally { Date.now = now }
  await start()
  const callback = `/callback?code=the-code&state=${expectedState}`
  assert.equal(await handleOAuthCallback({ url: callback }, { writeHead() {}, end() {} }, '/callback'), true)
  const before = readFileSync(file, 'utf8')
  const count = exchanges
  assert.match((await submitLoginCode(sub, 'the-code')).error, /already signed in/)
  assert.equal(exchanges, count)
  assert.equal(readFileSync(file, 'utf8'), before)
  signOut(sub)
})

test('a pasted code that cannot reach the vendor says so and keeps waiting', async t => {
  process.env.PRISM_SUBS_FILE = write('{}')
  t.after(() => { delete process.env.PRISM_SUBS_FILE })
  const sub = { id: 'grok', name: 'Grok', kind: 'browser', authorizeURL: 'https://auth.test', tokenURL: 'http://127.0.0.1:9/token', clientID: 'cid', redirectPath: '/callback', apiBase: 'http://127.0.0.1:9' }
  await startBrowserLogin(sub)
  const result = await submitLoginCode(sub, 'the-code')
  assert.match(result.error, /^could not reach Grok/)
  assert.equal(loginStatus(sub).pending, true)
  signOut(sub)
})

test('Grok OAuth uses the CLI proxy and probes its /v1/models endpoint', async t => {
  const grok = SUBSCRIPTIONS.find(s => s.id === 'grok')
  assert.equal(grok.apiBase, 'https://cli-chat-proxy.grok.com/v1')
  const os = process.platform === 'darwin' ? 'macos' : process.platform === 'win32' ? 'windows' : process.platform
  const arch = { x64: 'x86_64', arm64: 'aarch64', ia32: 'x86' }[process.arch] ?? process.arch
  assert.equal(grok.apiUserAgent, `grok-shell/1.0.45 (${os}; ${arch})`)
  let probes = 0
  const api = await upstream((req, res) => {
    if (req.url === '/token') return res.end(JSON.stringify({ access_token: 'grok-token', refresh_token: 'refresh', expires_in: 3600 }))
    assert.equal(req.method, 'GET')
    assert.equal(req.url, '/v1/models')
    assert.equal(req.headers.authorization, 'Bearer grok-token')
    assert.equal(req.headers['x-xai-token-auth'], 'xai-grok-cli')
    assert.equal(req.headers['user-agent'], grok.apiUserAgent)
    probes++
    res.end(JSON.stringify({ data: [{ id: 'grok-test' }] }))
  })
  const server = servers.at(-1)
  const file = write('{}')
  process.env.PRISM_SUBS_FILE = file
  t.after(() => { delete process.env.PRISM_SUBS_FILE; server.close() })
  const sub = { ...grok, redirectPort: 0, tokenURL: api + '/token', apiBase: api + '/v1' }
  const started = await startBrowserLogin(sub)
  const state = new URL(started.url).searchParams.get('state')
  const done = await submitLoginCode(sub, `code#${state}`)
  assert.equal(done.ok, true)
  assert.equal(probes, 1)
  assert.deepEqual(loadSubs(file).grok.models, ['grok-test'])
  assert.match(loadSubs(file).grok.sessionId, /^[0-9a-f-]{36}$/)
})

test('server: Grok OAuth sends CLI headers and an unchanged chat body to /v1/chat/completions', async t => {
  const { spawn } = await import('node:child_process')
  const requests = []
  const api = await upstream((req, res) => {
    assert.equal(req.url, '/v1/chat/completions')
    assert.equal(req.headers.authorization, 'Bearer grok-token')
    assert.equal(req.headers['x-xai-token-auth'], 'xai-grok-cli')
    assert.equal(req.headers.accept, 'text/event-stream')
    assert.match(req.headers['user-agent'], /^grok-shell\/1\.0\.45 \(/)
    assert.equal(req.headers['x-grok-client-identifier'], 'grok-shell')
    assert.equal(req.headers['x-grok-client-version'], '1.0.45')
    assert.equal(req.headers['x-grok-model-override'], 'grok-test')
    assert.match(req.headers['x-grok-session-id'], /^[0-9a-f-]{36}$/)
    assert.match(req.headers['x-grok-conv-id'], /^[0-9a-f-]{36}$/)
    assert.match(req.headers['x-grok-req-id'], /^[0-9a-f-]{36}$/)
    assert.ok(req.headers['x-grok-agent-id'])
    requests.push(req.headers)
    let raw = ''
    req.on('data', chunk => { raw += chunk })
    req.on('end', () => {
      assert.deepEqual(JSON.parse(raw), { model: 'grok-test', messages: [{ role: 'user', content: 'hi' }], stream: true })
      res.writeHead(200, { 'content-type': 'text/event-stream' })
      res.end('data: {"choices":[{"delta":{"content":"hello"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n')
    })
  })
  const apiServer = servers.at(-1)
  t.after(() => apiServer.close())
  const dir = mkdtempSync(join(tmpdir(), 'grok-server-'))
  for (const f of ['server.mjs', 'custom.mjs', 'subs.mjs', 'wire.mjs']) copyFileSync(new URL(`./${f}`, import.meta.url), join(dir, f))
  writeFileSync(join(dir, 'providers.json'), JSON.stringify({ providers: [] }))
  const subsFile = join(dir, 'subscriptions.json')
  // Existing signed-in accounts without a session id must be migrated once.
  writeFileSync(subsFile, JSON.stringify({ grok: { accessToken: 'grok-token', models: ['grok-test'], apiBase: api + '/v1' } }))
  const probe = http.createServer().listen(0, '127.0.0.1')
  await new Promise(resolve => probe.once('listening', resolve))
  const port = probe.address().port
  await new Promise(resolve => probe.close(resolve))
  const child = spawn(process.execPath, ['server.mjs'], { cwd: dir, env: { ...process.env, PORT: String(port), STATS_FILE: '', PRISM_SUBS_FILE: subsFile } })
  t.after(() => child.kill())
  const base = `http://127.0.0.1:${port}`
  for (let i = 0; i < 50; i++) {
    try { if ((await fetch(base + '/health')).ok) break } catch {}
    await new Promise(r => setTimeout(r, 100))
  }
  for (let i = 0; i < 2; i++) {
    const res = await fetch(base + '/v1/chat/completions', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ model: 'grok/grok-test', messages: [{ role: 'user', content: 'hi' }], stream: true }),
    })
    assert.equal(res.status, 200)
    assert.match(await res.text(), /hello/)
  }
  assert.equal(requests.length, 2)
  assert.equal(requests[0]['x-grok-session-id'], JSON.parse(readFileSync(subsFile, 'utf8')).grok.sessionId)
  assert.equal(requests[0]['x-grok-session-id'], requests[1]['x-grok-session-id'])
  assert.equal(requests[0]['x-grok-conv-id'], requests[1]['x-grok-conv-id'])
  assert.notEqual(requests[0]['x-grok-req-id'], requests[1]['x-grok-req-id'])
})

test('the subscription catalog is sane', () => {
  assert.ok(SUBSCRIPTIONS.length >= 1)
  for (const sub of SUBSCRIPTIONS) {
    assert.match(sub.id, /^[a-z0-9][a-z0-9-]*$/)
    if (sub.deviceURL) assert.match(sub.deviceURL, /^https:\/\//)
    if (sub.kind === 'browser') assert.match(sub.authorizeURL, /^https:\/\//)
    assert.match(sub.tokenURL, /^https:\/\//)
    assert.ok(sub.clientID)
    assert.match(sub.apiBase, /^https:\/\//)
  }
})

test('loadSubs survives a missing or broken file', () => {
  assert.deepEqual(loadSubs(write('{}')), {})
  assert.deepEqual(loadSubs(write('not json')), {})
  assert.deepEqual(loadSubs(join(tmpdir(), 'no-such-subs.json')), {})
})

test('the device-code flow signs in, probes models, and serves providers', async () => {
  let approved = false
  const device = await upstream((req, res) => {
    let body = ''
    req.on('data', chunk => (body += chunk))
    req.on('end', () => {
      assert.ok(body.includes('client_id'))
      res.writeHead(200, { 'content-type': 'application/json' })
      res.end(JSON.stringify({ device_code: 'DEV123', user_code: 'ABCD-1234', verification_uri: 'https://example.com/device', expires_in: 900, interval: 1 }))
    })
  })
  const token = await upstream((req, res) => {
    let body = ''
    req.on('data', chunk => (body += chunk))
    req.on('end', () => {
      if (!approved) {
        res.writeHead(200, { 'content-type': 'application/json' })
        res.end(JSON.stringify({ error: 'authorization_pending' }))
        return
      }
      assert.ok(body.includes('device_code=DEV123'.replace('device_code=', '')) || body.includes('DEV123'))
      res.writeHead(200, { 'content-type': 'application/json' })
      res.end(JSON.stringify({ access_token: 'gh-token-123456', token_type: 'bearer' }))
    })
  })
  const exchange = await upstream((req, res) => {
    assert.equal(req.headers.authorization, 'Bearer gh-token-123456')
    res.writeHead(200, { 'content-type': 'application/json' })
    res.end(JSON.stringify({ token: 'api-token-987', expires_at: Date.now() / 1000 + 3600 }))
  })
  const models = await upstream((req, res) => {
    assert.equal(req.headers.authorization, 'Bearer api-token-987')
    res.writeHead(200, { 'content-type': 'application/json' })
    res.end(JSON.stringify({ data: [{ id: 'gpt-test-big' }, { id: 'claude-test' }, { id: 'gpt-audio-test' }, { id: 'text-embedding-test' }] }))
  })
  const file = write('{}')
  const sub = {
    id: 'fake',
    name: 'Fake',
    deviceURL: device,
    tokenURL: token,
    clientID: 'cid',
    exchangeURL: exchange,
    apiBase: models,
  }
  process.env.PRISM_SUBS_FILE = file
  const started = await startLogin(sub)
  assert.equal(started.userCode, 'ABCD-1234')
  assert.equal(started.verificationURI, 'https://example.com/device')
  assert.equal((await pollLogin(sub)).status, 'pending')
  approved = true
  const done = await pollLogin(sub)
  assert.equal(done.status, 'ok')
  assert.deepEqual(done.models, ['gpt-test-big', 'claude-test'])
  const saved = JSON.parse(readFileSync(file, 'utf8'))
  assert.equal(saved.fake.accessToken, 'gh-token-123456')
  assert.deepEqual(saved.fake.models, ['gpt-test-big', 'claude-test', 'gpt-audio-test'])
  const providers = subscriptionProviders()
  // subscriptionProviders reads SUBSCRIPTIONS, not the fake; check via loadSubs instead
  assert.deepEqual(loadSubs(file).fake.models, ['gpt-test-big', 'claude-test', 'gpt-audio-test'])
  const fresh = await subToken(sub)
  assert.equal(fresh.token, 'api-token-987')
  signOut(sub)
  assert.equal(loadSubs(file).fake, undefined)
  delete process.env.PRISM_SUBS_FILE
})

test('subscriptionProviders lists signed-in catalog accounts', () => {
  const file = write('{}')
  process.env.PRISM_SUBS_FILE = file
  assert.deepEqual(subscriptionProviders(), [])
  const subs = { copilot: { accessToken: 'gh-token-123456', models: ['gpt-test-big'] } }
  writeFileSync(file, JSON.stringify(subs))
  const providers = subscriptionProviders()
  assert.equal(providers.length, 1)
  assert.equal(providers[0].name, 'copilot')
  assert.equal(providers[0].baseURL, 'https://api.githubcopilot.com')
  assert.equal(providers[0].mark.subscription, 'copilot')
  assert.equal(providers[0].mark.paid, true)
  delete process.env.PRISM_SUBS_FILE
})

test('a failed exchange says to sign in again', async () => {
  const exchange = await upstream((req, res) => {
    res.writeHead(401, { 'content-type': 'application/json' })
    res.end('{}')
  })
  const file = write('{}')
  process.env.PRISM_SUBS_FILE = file
  const sub = { id: 'fake', name: 'Fake', exchangeURL: exchange, apiBase: exchange }
  writeFileSync(file, JSON.stringify({ fake: { accessToken: 'stale', models: ['m'] } }))
  await assert.rejects(() => subToken(sub), /fake sign-in expired/)
  delete process.env.PRISM_SUBS_FILE
})

test('server: /logins lists, signs out, and polls idle', async () => {
  const { spawn } = await import('node:child_process')
  const { copyFileSync } = await import('node:fs')
  const dir = mkdtempSync(join(tmpdir(), 'subs-server-'))
  copyFileSync(new URL('./server.mjs', import.meta.url), join(dir, 'server.mjs'))
  copyFileSync(new URL('./custom.mjs', import.meta.url), join(dir, 'custom.mjs'))
  copyFileSync(new URL('./subs.mjs', import.meta.url), join(dir, 'subs.mjs'))
  copyFileSync(new URL('./wire.mjs', import.meta.url), join(dir, 'wire.mjs'))
  writeFileSync(join(dir, 'providers.json'), JSON.stringify({ providers: [] }))
  const subsFile = join(dir, 'subscriptions.json')
  writeFileSync(subsFile, '{}')
  const probe = http.createServer().listen(0, '127.0.0.1')
  await new Promise(resolve => probe.once('listening', resolve))
  const port = probe.address().port
  await new Promise(resolve => probe.close(resolve))
  const child = spawn(process.execPath, ['server.mjs'], { cwd: dir, env: { ...process.env, PORT: String(port), STATS_FILE: '', PRISM_SUBS_FILE: subsFile } })
  servers.push({ close: () => child.kill() })
  const base = `http://127.0.0.1:${port}`
  for (let i = 0; i < 50; i++) {
    try { if ((await fetch(base + '/health')).ok) break } catch {}
    await new Promise(r => setTimeout(r, 100))
  }
  const list = await (await fetch(base + '/logins')).json()
  assert.equal(list.subscriptions[0].id, 'copilot')
  assert.equal(list.subscriptions[0].signedIn, false)
  const poll = await (await fetch(base + '/logins', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ id: 'copilot', poll: true }) })).json()
  assert.equal(poll.status, 'idle')
  const missing = await fetch(base + '/logins', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ id: 'nope' }) })
  assert.equal(missing.status, 404)
  writeFileSync(subsFile, JSON.stringify({ copilot: { accessToken: 'gh-token-123456', models: ['gpt-test-big'] } }))
  const signed = await (await fetch(base + '/logins')).json()
  assert.equal(signed.subscriptions[0].signedIn, true)
  assert.equal(signed.subscriptions[0].models, 1)
  const models = await (await fetch(base + '/v1/models')).json()
  assert.ok(models.data.some(m => m.id === 'copilot/gpt-test-big'))
  const out = await fetch(base + '/logins', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ id: 'copilot', signOut: true }) })
  assert.equal(out.status, 200)
  const after = await (await fetch(base + '/logins')).json()
  assert.equal(after.subscriptions[0].signedIn, false)
})

test('cleanup', () => {
  for (const s of servers) s.close()
})

test('a browser sign-in exchanges the code and stores the account', async () => {
  const token = await upstream((req, res) => {
    let body = ''
    req.on('data', chunk => (body += chunk))
    req.on('end', () => {
      assert.ok(body.includes('grant_type=authorization_code'))
      assert.ok(body.includes('code_verifier='))
      res.writeHead(200, { 'content-type': 'application/json' })
      res.end(JSON.stringify({ access_token: 'at-123456789', refresh_token: 'rt-123456789', expires_in: 3600, id_token: 'x.eyJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9hY2NvdW50X2lkIjoiYWNjLTEyMyJ9fQ.x' }))
    })
  })
  const models = await upstream((req, res) => {
    assert.equal(req.headers.authorization, 'Bearer at-123456789')
    res.writeHead(200, { 'content-type': 'application/json' })
    res.end(JSON.stringify({ data: [{ id: 'gpt-test-big' }] }))
  })
  const probe = http.createServer().listen(0, '127.0.0.1')
  await new Promise(resolve => probe.once('listening', resolve))
  const port = probe.address().port
  await new Promise(resolve => probe.close(resolve))
  const file = write('{}')
  process.env.PRISM_SUBS_FILE = file
  const sub = {
    id: 'fake',
    name: 'Fake',
    kind: 'browser',
    authorizeURL: 'https://auth.example.test/authorize',
    tokenURL: token,
    clientID: 'cid',
    redirectPort: port,
    redirectPath: '/cb',
    scopes: 'openid',
    apiBase: models,
    models: ['fallback'],
  }
  const started = await startBrowserLogin(sub)
  assert.match(started.url, /https:\/\/auth\.example\.test\/authorize\?/)
  assert.match(started.url, /code_challenge_method=S256/)
  const state = new URL(started.url).searchParams.get('state')
  const badHost = await new Promise((resolve, reject) => {
    http.get(`http://127.0.0.1:${port}/cb`, { headers: { host: 'evil.com' } }, res => {
      res.resume()
      resolve(res.statusCode)
    }).on('error', reject)
  })
  assert.equal(badHost, 403)
  assert.equal((await fetch(`http://127.0.0.1:${port}/cb`, { headers: { origin: 'http://evil.com' } })).status, 403)
  const res = await fetch(`http://127.0.0.1:${port}/cb?code=the-code&state=${state}`, { headers: { host: `localhost:${port}` } })
  assert.equal(res.status, 200)
  assert.match(await res.text(), /signed in/)
  let saved = {}
  for (let i = 0; i < 50; i++) {
    await new Promise(r => setTimeout(r, 100))
    saved = loadSubs(file)
    if (saved.fake?.accessToken) break
  }
  assert.equal(saved.fake.accessToken, 'at-123456789')
  assert.equal(saved.fake.refreshToken, 'rt-123456789')
  assert.equal(saved.fake.accountId, 'acc-123')
  assert.deepEqual(saved.fake.models, ['gpt-test-big'])
  delete process.env.PRISM_SUBS_FILE
})

test('an expired browser token is refreshed', async () => {
  const token = await upstream((req, res) => {
    let body = ''
    req.on('data', chunk => (body += chunk))
    req.on('end', () => {
      assert.ok(body.includes('grant_type=refresh_token'))
      res.writeHead(200, { 'content-type': 'application/json' })
      res.end(JSON.stringify({ access_token: 'at-fresh-123456', expires_in: 3600 }))
    })
  })
  const file = write('{}')
  process.env.PRISM_SUBS_FILE = file
  const sub = { id: 'fake', name: 'Fake', kind: 'browser', tokenURL: token, clientID: 'cid', apiBase: token }
  writeFileSync(file, JSON.stringify({ fake: { accessToken: 'stale', refreshToken: 'rt-old', expiresAt: Date.now() - 1000, models: ['m'] } }))
  const fresh = await subToken(sub)
  assert.equal(fresh.token, 'at-fresh-123456')
  assert.equal(loadSubs(file).fake.refreshToken, 'rt-old')
  delete process.env.PRISM_SUBS_FILE
})

test('server: a wired chatgpt route translates responses to chat chunks', async () => {
  const { spawn } = await import('node:child_process')
  const codex = await upstream((req, res) => {
    assert.equal(req.url, '/responses?client_version=0.175.0')
    assert.equal(req.headers['chatgpt-account-id'], 'acc-9')
    let body = ''
    req.on('data', chunk => (body += chunk))
    req.on('end', () => {
      const parsed = JSON.parse(body)
      assert.equal(parsed.store, false)
      assert.equal(parsed.model, 'gpt-test')
      res.writeHead(200, { 'content-type': 'text/event-stream' })
      res.end('data: {"type":"response.output_text.delta","delta":"hello "}\n\ndata: {"type":"response.output_text.delta","delta":"world"}\n\ndata: {"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":2}}}\n\n')
    })
  })
  const dir = mkdtempSync(join(tmpdir(), 'wire-server-'))
  for (const f of ['server.mjs', 'custom.mjs', 'subs.mjs', 'wire.mjs']) copyFileSync(new URL(`./${f}`, import.meta.url), join(dir, f))
  writeFileSync(join(dir, 'providers.json'), JSON.stringify({ providers: [] }))
  const subsFile = join(dir, 'subscriptions.json')
  writeFileSync(subsFile, JSON.stringify({ chatgpt: { accessToken: 't', accountId: 'acc-9', models: ['gpt-test'], apiBase: codex } }))
  const probe = http.createServer().listen(0, '127.0.0.1')
  await new Promise(resolve => probe.once('listening', resolve))
  const port = probe.address().port
  await new Promise(resolve => probe.close(resolve))
  const child = spawn(process.execPath, ['server.mjs'], { cwd: dir, env: { ...process.env, PORT: String(port), STATS_FILE: '', PRISM_SUBS_FILE: subsFile } })
  servers.push({ close: () => child.kill() })
  const base = `http://127.0.0.1:${port}`
  for (let i = 0; i < 50; i++) {
    try { if ((await fetch(base + '/health')).ok) break } catch {}
    await new Promise(r => setTimeout(r, 100))
  }
  const res = await fetch(base + '/v1/chat/completions', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ model: 'chatgpt/gpt-test', messages: [{ role: 'user', content: 'hi' }], stream: true }),
  })
  assert.equal(res.status, 200)
  assert.match(res.headers.get('content-type'), /event-stream/)
  const text = await res.text()
  assert.match(text, /"content":"hello "/)
  assert.match(text, /"content":"world"/)
  assert.match(text, /finish_reason/)
  assert.match(text, /\[DONE\]/)
})

test('an antigravity sign-in onboards the project and lists models by POST', async () => {
  const token = await upstream((req, res) => {
    let body = ''
    req.on('data', chunk => (body += chunk))
    req.on('end', () => {
      assert.ok(body.includes('grant_type=authorization_code'))
      assert.ok(body.includes('client_secret='))
      res.writeHead(200, { 'content-type': 'application/json' })
      res.end(JSON.stringify({ access_token: 'ag-at-123', refresh_token: 'ag-rt-123', expires_in: 3600 }))
    })
  })
  let onboarded = 0
  const api = await upstream((req, res) => {
    let body = ''
    req.on('data', chunk => (body += chunk))
    req.on('end', () => {
      if (req.url === '/v1internal:loadCodeAssist') {
        assert.equal(req.method, 'POST')
        assert.equal(req.headers.authorization, 'Bearer ag-at-123')
        assert.deepEqual(JSON.parse(body), { metadata: { ideType: 'ANTIGRAVITY' } })
        // No project yet: onboarding must kick in with the default tier.
        res.writeHead(200, { 'content-type': 'application/json' })
        res.end(JSON.stringify({ allowedTiers: [{ id: 'free-tier', isDefault: true }] }))
        return
      }
      if (req.url === '/v1internal:onboardUser') {
        onboarded++
        assert.equal(req.method, 'POST')
        assert.equal(req.headers['x-goog-api-client'], 'gl-node/22.21.1')
        assert.match(req.headers['user-agent'], /google-api-nodejs-client\/10\.3\.0$/)
        const parsed = JSON.parse(body)
        assert.equal(parsed.tier_id, 'free-tier')
        assert.equal(parsed.metadata.ide_type, 'ANTIGRAVITY')
        assert.equal(parsed.metadata.ide_name, 'antigravity')
        res.writeHead(200, { 'content-type': 'application/json' })
        res.end(JSON.stringify({ done: true, response: { cloudaicompanionProject: 'proj-77' } }))
        return
      }
      if (req.url === '/v1internal:fetchAvailableModels') {
        assert.equal(req.method, 'POST')
        assert.equal(req.headers.authorization, 'Bearer ag-at-123')
        assert.deepEqual(JSON.parse(body), { project: 'proj-77' })
        res.writeHead(200, { 'content-type': 'application/json' })
        res.end(JSON.stringify({ webSearchModelIds: [], models: { 'gemini-3.8-flash': {}, 'claude-sonnet-5-5': {}, 'text-embedding-004': {} } }))
        return
      }
      res.writeHead(404)
      res.end()
    })
  })
  const probe = http.createServer().listen(0, '127.0.0.1')
  await new Promise(resolve => probe.once('listening', resolve))
  const port = probe.address().port
  await new Promise(resolve => probe.close(resolve))
  const file = write('{}')
  process.env.PRISM_SUBS_FILE = file
  const sub = {
    id: 'fake',
    name: 'Fake',
    kind: 'browser',
    authorizeURL: 'https://accounts.google.com/o/oauth2/v2/auth',
    tokenURL: token,
    clientID: 'cid',
    clientSecret: 'sec',
    redirectPort: port,
    redirectPath: '/oauth-callback',
    scopes: 'openid',
    apiBase: api,
    wire: 'antigravity',
    onboard: true,
    apiUserAgent: 'antigravity/hub/2.24.0 (aidev_client; os_type=darwin; arch=arm64; cl=1000335682)',
    models: ['fallback-model'],
  }
  const started = await startBrowserLogin(sub)
  const state = new URL(started.url).searchParams.get('state')
  const res = await fetch(`http://127.0.0.1:${port}/oauth-callback?code=the-code&state=${state}`)
  assert.equal(res.status, 200)
  let saved = {}
  for (let i = 0; i < 50; i++) {
    await new Promise(r => setTimeout(r, 100))
    saved = loadSubs(file)
    if (saved.fake?.models?.length) break
  }
  assert.equal(onboarded, 1)
  assert.equal(saved.fake.projectId, 'proj-77')
  assert.deepEqual(saved.fake.models, ['gemini-3.8-flash', 'claude-sonnet-5-5'])
  delete process.env.PRISM_SUBS_FILE
})

test('server: a wired antigravity route streams the envelope and unwraps chunks', async () => {
  const { spawn } = await import('node:child_process')
  const upstreamSeen = []
  const api = await upstream((req, res) => {
    let body = ''
    req.on('data', chunk => (body += chunk))
    req.on('end', () => {
      upstreamSeen.push({ url: req.url, ua: req.headers['user-agent'], body: JSON.parse(body) })
      assert.equal(req.url, '/v1internal:streamGenerateContent?alt=sse')
      assert.equal(req.headers.authorization, 'Bearer ag-t')
      assert.match(req.headers['user-agent'], /^antigravity\/hub\//)
      const parsed = JSON.parse(body)
      assert.equal(parsed.model, 'gemini-test')
      assert.equal(parsed.project, 'proj-9')
      assert.equal(parsed.requestType, 'agent')
      assert.equal(parsed.userAgent, 'antigravity')
      assert.match(parsed.requestId, /^agent-/)
      assert.equal(parsed.request.contents[0].parts[0].text, 'hi')
      res.writeHead(200, { 'content-type': 'text/event-stream' })
      res.end(
        'data: {"response":{"candidates":[{"content":{"parts":[{"text":"hello "}]}}]},"traceId":"t1"}\n\n' +
          'data: {"response":{"candidates":[{"content":{"parts":[{"text":"world"}]}}]},"traceId":"t2"}\n\n' +
          'data: {"response":{"candidates":[{"content":{"parts":[]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2}},"traceId":"t3"}\n\n',
      )
    })
  })
  const dir = mkdtempSync(join(tmpdir(), 'ag-server-'))
  for (const f of ['server.mjs', 'custom.mjs', 'subs.mjs', 'wire.mjs']) copyFileSync(new URL(`./${f}`, import.meta.url), join(dir, f))
  writeFileSync(join(dir, 'providers.json'), JSON.stringify({ providers: [] }))
  const subsFile = join(dir, 'subscriptions.json')
  writeFileSync(subsFile, JSON.stringify({ antigravity: { accessToken: 'ag-t', projectId: 'proj-9', models: ['gemini-test'], apiBase: api } }))
  const probe = http.createServer().listen(0, '127.0.0.1')
  await new Promise(resolve => probe.once('listening', resolve))
  const port = probe.address().port
  await new Promise(resolve => probe.close(resolve))
  const child = spawn(process.execPath, ['server.mjs'], { cwd: dir, env: { ...process.env, PORT: String(port), STATS_FILE: '', PRISM_SUBS_FILE: subsFile } })
  servers.push({ close: () => child.kill() })
  const base = `http://127.0.0.1:${port}`
  for (let i = 0; i < 50; i++) {
    try { if ((await fetch(base + '/health')).ok) break } catch {}
    await new Promise(r => setTimeout(r, 100))
  }
  const res = await fetch(base + '/v1/chat/completions', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ model: 'antigravity/gemini-test', messages: [{ role: 'user', content: 'hi' }], stream: true }),
  })
  assert.equal(res.status, 200)
  assert.match(res.headers.get('content-type'), /event-stream/)
  const text = await res.text()
  assert.equal(upstreamSeen.length, 1)
  assert.match(text, /"content":"hello "/)
  assert.match(text, /"content":"world"/)
  assert.match(text, /finish_reason/)
  assert.match(text, /\[DONE\]/)
})

test('cleanup after the late tests', () => {
  for (const s of servers) s.close()
})

test('visibleModels hides helpers, previews and retired generations', async () => {
  const { visibleModels } = await import('./subs.mjs')
  assert.deepEqual(visibleModels('chatgpt', ['gpt-6.1-sol', 'codex-auto-review', 'gpt-reserve', 'gpt-5.6-luna', 'gpt-4o']), ['gpt-6.1-sol', 'gpt-5.6-luna'])
  assert.deepEqual(visibleModels('claude', ['claude-opus-5-5', 'claude-opus-4-7', 'claude-sonnet-4-5-20250929', 'claude-opus-4-6']), ['claude-opus-5-5', 'claude-opus-4-7'])
  assert.deepEqual(
    visibleModels('antigravity', ['tab_flash_lite_preview', 'gemini-4-argon-low', 'chat_20706', 'gemini-2.5-pro', 'claude-opus-5-5-high', 'gpt-oss-120b-medium', 'gemini-3.8-flash-tiered']),
    ['claude-opus-5-5-high', 'gemini-4-argon-low'],
  )
  assert.deepEqual(visibleModels('grok', ['grok-4.7', 'text-embedding-x']), ['grok-4.7'])
})


test('Copilot keeps sign-in and records an actionable error when models fail', async () => {
  const api = await upstream((req, res) => {
    if (req.url === '/device') return res.end(JSON.stringify({ device_code: 'dev', user_code: 'code', verification_uri: 'https://example.test', expires_in: 900 }))
    if (req.url === '/token') return res.end(JSON.stringify({ access_token: 'github-token' }))
    if (req.url === '/exchange') return res.end(JSON.stringify({ token: 'api-token', expires_at: Date.now() / 1000 + 3600 }))
    res.writeHead(503)
    res.end('{}')
  })
  const file = write('{}')
  process.env.PRISM_SUBS_FILE = file
  try {
    const sub = { ...SUBSCRIPTIONS[0], deviceURL: api + '/device', tokenURL: api + '/token', exchangeURL: api + '/exchange', apiBase: api }
    await startLogin(sub)
    const result = await pollLogin(sub)
    assert.equal(result.status, 'ok')
    assert.deepEqual(result.models, [])
    assert.match(result.note, /signed in, but GitHub Copilot did not list its models; run \/login copilot again/)
    assert.equal(loginError('copilot'), result.note)
    assert.equal(loadSubs(file).copilot.loginError, result.note)
    assert.equal(loadSubs(file).copilot.accessToken, 'github-token')
    signOut(sub)
  } finally {
    delete process.env.PRISM_SUBS_FILE
    for (const server of servers) server.close()
  }
})

const deferred = () => {
  let resolve
  const promise = new Promise(done => { resolve = done })
  return { promise, resolve }
}

const browserSub = id => ({ id, name: id, kind: 'browser', authorizeURL: 'https://auth.test/authorize', tokenURL: 'https://auth.test/token', clientID: 'cid', redirectPath: '/callback', apiBase: 'https://api.test' })

for (const phase of ['token', 'models']) {
  test(`a duplicate browser callback during ${phase} leaves the first sign-in finishing`, async t => {
    const sub = browserSub(`duplicate-${phase}`)
    const file = write('{}')
    process.env.PRISM_SUBS_FILE = file
    const entered = deferred(), release = deferred()
    let exchanges = 0
    t.after(() => { signOut(sub); delete process.env.PRISM_SUBS_FILE })
    t.mock.method(globalThis, 'fetch', async url => {
      if (url === sub.tokenURL && ++exchanges > 1) return new Response('code already used', { status: 400 })
      if (phase === 'token' ? url === sub.tokenURL : url.endsWith('/models')) {
        entered.resolve()
        await release.promise
      }
      return Response.json(url === sub.tokenURL ? { access_token: 'new-token' } : { data: [{ id: 'new-model' }] })
    })
    const started = await startBrowserLogin(sub)
    const finishing = callback(started)
    await entered.promise
    let html = ''
    try {
      await handleOAuthCallback(
        { url: `/callback?code=code&state=${new URL(started.url).searchParams.get('state')}` },
        { writeHead() {}, end(value) { html = value } }, '/callback')
      assert.match(html, /the sign-in is already finishing/)
      assert.equal(exchanges, 1)
      assert.equal(loginError(sub.id), null)
      assert.equal(loginStatus(sub).finishing, true)
      assert.equal(loginStatus(sub).pending, true)
    } finally {
      release.resolve()
      await finishing
    }
    assert.equal(loadSubs(file)[sub.id].accessToken, 'new-token')
    assert.deepEqual(loadSubs(file)[sub.id].models, ['new-model'])
    assert.equal(loginStatus(sub).signedIn, true)
    assert.equal(loginError(sub.id), null)
  })
}

test('browser sign-ins advertise their ten-minute lifetime', async t => {
  const sub = browserSub('expiry-contract')
  t.after(() => signOut(sub))
  assert.equal((await startBrowserLogin(sub)).expires, 600)
})

for (const expiry of ['status', 'timer']) {
  test(`browser ${expiry} expiry reports a timeout even for an existing account`, async t => {
    const sub = browserSub(`expiry-${expiry}`)
    process.env.PRISM_SUBS_FILE = write({ [sub.id]: { accessToken: 'old', models: ['old-model'] } })
    t.after(() => { signOut(sub); delete process.env.PRISM_SUBS_FILE })
    let now = Date.now()
    t.mock.method(Date, 'now', () => now)
    if (expiry === 'timer') {
      const probe = http.createServer().listen(0, '127.0.0.1')
      await new Promise(resolve => probe.once('listening', resolve))
      sub.redirectPort = probe.address().port
      await new Promise(resolve => probe.close(resolve))
      t.mock.timers.enable({ apis: ['setTimeout'] })
    }
    await startBrowserLogin(sub)
    now += 600_000
    if (expiry === 'timer') {
      t.mock.timers.tick(600_000)
      assert.equal(loginError(sub.id), 'the sign-in timed out; run /login again')
    }
    const status = loginStatus(sub)
    assert.equal(status.pending, false)
    assert.equal(status.finishing, false)
    assert.equal(status.error, 'the sign-in timed out; run /login again')
    assert.equal(loadSubs()[sub.id].accessToken, 'old')
    // An old listener timer must not poison a replacement sign-in.
    await startBrowserLogin(sub)
    assert.equal(loginError(sub.id), null)
  })
}
const callback = started => handleOAuthCallback(
  { url: `/callback?code=code&state=${new URL(started.url).searchParams.get('state')}` },
  { writeHead() {}, end() {} }, '/callback')

test('re-sign-in stays pending instead of reporting the stored browser token', async t => {
  const sub = browserSub('resign')
  process.env.PRISM_SUBS_FILE = write({ resign: { accessToken: 'old', models: ['old-model'] } })
  t.after(() => { signOut(sub); delete process.env.PRISM_SUBS_FILE })
  await startBrowserLogin(sub)
  assert.equal(loginStatus(sub).signedIn, false)
  assert.equal(loginStatus(sub).pending, true)
  signOut(sub)
  assert.equal(loginStatus(sub).pending, false)
})

for (const phase of ['token', 'models', 'onboard']) {
  test(`browser sign-out during ${phase} cannot resurrect the account`, async t => {
    const sub = { ...browserSub(`cancel-${phase}`), ...(phase === 'onboard' && { onboard: true, wire: 'antigravity' }) }
    const file = write('{}')
    process.env.PRISM_SUBS_FILE = file
    const originalFetch = globalThis.fetch
    const entered = deferred(), release = deferred()
    t.after(() => { globalThis.fetch = originalFetch; delete process.env.PRISM_SUBS_FILE })
    globalThis.fetch = async url => {
      const atPhase = phase === 'token' ? url === sub.tokenURL : phase === 'models' ? url.endsWith('/models') : url.endsWith(':loadCodeAssist')
      if (atPhase) { entered.resolve(); await release.promise }
      return Response.json(url === sub.tokenURL ? { access_token: 'new-token' } : phase === 'onboard' ? { projectId: 'project', models: { 'gemini-4-argon': {} } } : { data: [{ id: 'new-model' }] })
    }
    const started = await startBrowserLogin(sub)
    const finishing = callback(started)
    await entered.promise
    signOut(sub)
    release.resolve()
    await finishing
    assert.equal(loadSubs(file)[sub.id], undefined)
    assert.equal(loginStatus(sub).signedIn, false)
    assert.equal(loginStatus(sub).pending, false)
  })
}

for (const phase of ['token', 'models']) {
  test(`device sign-out during ${phase} cancels an in-flight poll`, async t => {
    const sub = { id: `device-${phase}`, name: 'Device', deviceURL: 'https://api.test/device', tokenURL: 'https://api.test/token', exchangeURL: 'https://api.test/exchange', apiBase: 'https://api.test' }
    const file = write('{}')
    process.env.PRISM_SUBS_FILE = file
    const originalFetch = globalThis.fetch
    const entered = deferred(), release = deferred()
    t.after(() => { globalThis.fetch = originalFetch; delete process.env.PRISM_SUBS_FILE })
    globalThis.fetch = async url => {
      if (url === sub.deviceURL) return Response.json({ device_code: 'dev' })
      if (url === (phase === 'token' ? sub.tokenURL : sub.apiBase + '/models')) { entered.resolve(); await release.promise }
      return Response.json(url === sub.tokenURL ? { access_token: 'new-token' } : url === sub.exchangeURL ? { token: 'api-token' } : { data: [{ id: 'new-model' }] })
    }
    await startLogin(sub)
    const finishing = pollLogin(sub)
    await entered.promise
    signOut(sub)
    release.resolve()
    const result = await finishing
    assert.equal(loadSubs(file)[sub.id], undefined)
    assert.notEqual(result.status, 'ok')
    assert.equal((await pollLogin(sub)).status, 'idle')
  })
}

test('Copilot reports an actionable note when all listed models are hidden', async t => {
  process.env.PRISM_SUBS_FILE = write('{}')
  const originalFetch = globalThis.fetch
  const sub = { ...SUBSCRIPTIONS[0], deviceURL: 'https://api.test/device', tokenURL: 'https://api.test/token', exchangeURL: 'https://api.test/exchange', apiBase: 'https://api.test' }
  t.after(() => { signOut(sub); globalThis.fetch = originalFetch; delete process.env.PRISM_SUBS_FILE })
  globalThis.fetch = async url => Response.json(url === sub.deviceURL ? { device_code: 'dev' } : url === sub.tokenURL ? { access_token: 'github-token' } : url === sub.exchangeURL ? { token: 'api-token' } : { data: [{ id: 'gpt-3.5-turbo' }, { id: 'copilot-helper' }] })
  await startLogin(sub)
  const result = await pollLogin(sub)
  assert.deepEqual(result.models, [])
  assert.match(result.note ?? '', /did not list its models; run \/login copilot again/)
  assert.equal(loginStatus(sub).error, result.note)
})
