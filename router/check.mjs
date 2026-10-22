// Checks every model in providers.json with a tiny request and prints a report.
// Providers that need a key are checked only when that key is set.
import { readFileSync, writeFileSync } from 'node:fs'

const { providers } = JSON.parse(readFileSync(new URL('./providers.json', import.meta.url), 'utf8'))
const expand = s => s.replace(/\$\{(\w+)(?::-([^}]*))?\}/g, (_, n, d = '') => process.env[n] || d)

const rows = []
for (const p of providers) {
  if (p.requires && !process.env[p.requires]) continue
  if (p.name === 'ollama') continue
  for (const name of p.models) {
    const model = expand(name)
    const started = Date.now()
    let status = 0
    try {
      const res = await fetch(`${expand(p.baseURL)}/chat/completions`, {
        method: 'POST',
        headers: { 'content-type': 'application/json', ...(p.requires && p.auth !== false && { authorization: `Bearer ${process.env[p.requires]}` }) },
        body: JSON.stringify({ model, messages: [{ role: 'user', content: 'say ok' }], max_tokens: 20 }),
        signal: AbortSignal.timeout(90_000),
      })
      status = res.status
      await res.text()
    } catch {}
    const ok = status === 200
    rows.push({ route: `${p.name}/${model}`, ok, status, seconds: ((Date.now() - started) / 1000).toFixed(1) })
    console.log(`${ok ? 'ok  ' : 'FAIL'} ${status || 'no reply'}  ${rows.at(-1).seconds}s  ${p.name}/${model}`)
  }
}

const dead = rows.filter(r => !r.ok)
const table = ['| model | result | time |', '| --- | --- | --- |', ...rows.map(r => `| ${r.route} | ${r.ok ? 'ok' : 'failed (' + (r.status || 'no reply') + ')'} | ${r.seconds}s |`)].join('\n')
writeFileSync('check-report.md', `${dead.length} of ${rows.length} free models failed this week's check.\n\n${table}\n\nA model that failed (busy, 429) can be temporary. One that fails two weeks in a row (404) should be removed from router/providers.json.\n`)
process.exitCode = dead.length ? 1 : 0
