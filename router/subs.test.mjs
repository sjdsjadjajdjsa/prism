import { test } from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { mkdtempSync, writeFileSync, readFileSync, copyFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { loadSubs, startLogin, pollLogin, signOut, subscriptionProviders, subToken, startBrowserLogin, SUBSCRIPTIONS } from './subs.mjs'

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
    res.end(JSON.stringify({ data: [{ id: 'gpt-test-big' }, { id: 'claude-test' }, { id: 'text-embedding-test' }] }))
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
  assert.deepEqual(saved.fake.models, ['gpt-test-big', 'claude-test'])
  const providers = subscriptionProviders()
  // subscriptionProviders reads SUBSCRIPTIONS, not the fake; check via loadSubs instead
  assert.deepEqual(loadSubs(file).fake.models, ['gpt-test-big', 'claude-test'])
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
  const res = await fetch(`http://127.0.0.1:${port}/cb?code=the-code&state=${state}`)
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
