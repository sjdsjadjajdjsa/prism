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
