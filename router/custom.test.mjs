import { test } from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { mkdtempSync, writeFileSync, readFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { PRESETS, validateEntry, loadCustom, customProviders, toProvider, eligible, maskKey, familyOf, cleanURL, syncKeys, setKeyVar, upsertPresetEntry, probeModels } from './custom.mjs'

const reserved = ['nvidia', 'groq', 'kilo']
const good = { id: 'mine', name: 'Mine', baseURL: 'https://example.com/v1', keyVar: 'MINE_API_KEY', models: ['big-model'] }
const write = value => {
  const file = join(mkdtempSync(join(tmpdir(), 'custom-')), 'custom-providers.json')
  writeFileSync(file, typeof value === 'string' ? value : JSON.stringify(value))
  return file
}

test('a good entry is accepted and paid defaults to true', () => {
  const { entry, error } = validateEntry(good, { reserved })
  assert.equal(error, undefined)
  assert.equal(entry.paid, true)
  assert.equal(entry.baseURL, 'https://example.com/v1')
})

test('bad addresses are rejected', () => {
  for (const baseURL of ['', 'not a url', 'ftp://example.com', 'https://user:pw@example.com/v1', 'https://example.com/${HOME}']) {
    assert.ok(validateEntry({ ...good, baseURL }, { reserved }).error, baseURL)
  }
})

test('trailing slash and /chat/completions are tidied', () => {
  assert.equal(cleanURL('https://example.com/v1/chat/completions/'), 'https://example.com/v1')
  assert.equal(validateEntry({ ...good, baseURL: 'https://example.com/v1/' }, { reserved }).entry.baseURL, 'https://example.com/v1')
})

test('a missing key name is rejected unless the model is local', () => {
  assert.match(validateEntry({ ...good, keyVar: undefined }, { reserved }).error, /key name/)
  assert.match(validateEntry({ ...good, keyVar: 'path' }, { reserved }).error, /key name/)
  assert.match(validateEntry({ ...good, keyVar: 'PATH' }, { reserved }).error, /key name/)
  const local = validateEntry({ ...good, keyVar: undefined, keyless: true, paid: false }, { reserved })
  assert.equal(local.error, undefined)
  assert.equal(toProvider(local.entry).requires, undefined)
  assert.equal(toProvider(local.entry).auth, false)
})

test('ids: built-in names, bad characters and duplicates are rejected', () => {
  assert.match(validateEntry({ ...good, id: 'groq' }, { reserved }).error, /built-in/)
  assert.ok(validateEntry({ ...good, id: 'Has Space' }, { reserved }).error)
  const { entries, problems } = loadCustom(write([good, { ...good, name: 'again' }, { ...good, id: 'other' }]), { reserved })
  assert.deepEqual(entries.map(entry => entry.id), ['mine', 'other'])
  assert.match(problems[0], /listed twice/)
})

test('models: needed, no spaces, no env lookups, no repeats', () => {
  for (const models of [undefined, [], [''], ['a b'], ['${SECRET}'], ['x', 'x']]) assert.ok(validateEntry({ ...good, models }, { reserved }).error, JSON.stringify(models))
})

test('a broken file never throws', () => {
  assert.deepEqual(loadCustom('/nonexistent/custom.json', { reserved }), { entries: [], problems: [] })
  assert.match(loadCustom(write('{oops'), { reserved }).problems[0], /not valid JSON/)
  assert.match(loadCustom(write('{}'), { reserved }).problems[0], /must be a list/)
  assert.deepEqual(loadCustom(write(''), { reserved }), { entries: [], problems: [] })
})

test('providers come out in the shape server.mjs reads', () => {
  const file = write([{ ...good, models: ['claude-sonnet-5.5', 'gpt-oss-120b'], vision: true }])
  const [provider] = customProviders(file)
  assert.equal(provider.name, 'mine')
  assert.equal(provider.requires, 'MINE_API_KEY')
  assert.equal(provider.baseURL, 'https://example.com/v1')
  assert.deepEqual(provider.models[0], { name: 'claude-sonnet-5.5', family: 'claude', roles: [], vision: true })
  assert.equal(provider.models[1].family, 'gpt-oss')
  assert.deepEqual(provider.mark, { paid: true, source: 'yours' })
})

test('customProviders notices when the file changes', async () => {
  const file = write([good])
  assert.equal(customProviders(file).length, 1)
  await new Promise(resolve => setTimeout(resolve, 20))
  writeFileSync(file, JSON.stringify([good, { ...good, id: 'second' }]))
  assert.equal(customProviders(file).length, 2)
})

test('paid routes are left out of auto unless PRISM_USE_PAID=1', () => {
  const paid = { paid: true }
  const free = { paid: false }
  assert.equal(eligible(paid, {}), false)
  assert.equal(eligible(paid, { PRISM_USE_PAID: '0' }), false)
  assert.equal(eligible(paid, { PRISM_USE_PAID: '1' }), true)
  assert.equal(eligible(free, {}), true)
  assert.equal(eligible({}, {}), true)
  // A route built from a file entry is paid unless the entry says otherwise.
  assert.equal(eligible({ paid: toProvider(validateEntry(good, { reserved }).entry).mark.paid }, {}), false)
})

test('preset list is sane', () => {
  const ids = new Set()
  for (const preset of PRESETS) {
    assert.ok(!ids.has(preset.id), `duplicate ${preset.id}`)
    ids.add(preset.id)
    assert.ok(preset.keyless || preset.keyVar, preset.id)
    assert.equal(typeof preset.paid, 'boolean', preset.id)
    // Every preset must pass the same checks as a hand-written entry once models are filled in.
    const { error } = validateEntry({ ...preset, models: preset.models.length ? preset.models : ['m'] })
    assert.equal(error, undefined, `${preset.id}: ${error}`)
    assert.match(preset.baseURL, /^https?:\/\//)
  }
  for (const id of ['openai', 'anthropic', 'deepseek', 'xai', 'together', 'fireworks', 'cerebras', 'sambanova', 'openrouter-paid', 'huggingface', 'ollama-local', 'lmstudio']) assert.ok(ids.has(id), id)
  assert.equal(PRESETS.find(p => p.id === 'ollama-local').keyless, true)
  assert.equal(PRESETS.find(p => p.id === 'openai').paid, true)
})

test('keys are masked for display', () => {
  assert.equal(maskKey(''), '(not set)')
  assert.equal(maskKey('short'), '****')
  assert.equal(maskKey('sk-abcdefghijklmnop1234'), 'sk-...1234')
  assert.ok(!maskKey('sk-abcdefghijklmnop1234').includes('abcdefgh'))
})

test('family comes from the model name', () => {
  assert.equal(familyOf('meta-llama/Llama-3.3-70B-Instruct'), 'llama')
  assert.equal(familyOf('deepseek-ai/DeepSeek-V4.1-Flash'), 'deepseek')
  assert.equal(familyOf('nvidia/nemotron-3-ultra'), 'nemotron')
  assert.equal(familyOf('gpt-4o'), 'gpt')
})

test('non-object entries are rejected without throwing', () => {
  for (const raw of [null, undefined, 'text', 42, [], [good]]) assert.equal(validateEntry(raw, { reserved }).error, 'not an object')
  const { entries, problems } = loadCustom(write([null, 'x', good]), { reserved })
  assert.equal(entries.length, 1)
  assert.equal(problems.length, 2)
})

test('id rules: length, case, leading dash and edge lengths', () => {
  for (const id of ['', 'UPPER', '-lead', 'under_score', 'a'.repeat(33), 'dot.ted', undefined]) {
    assert.match(validateEntry({ ...good, id }, { reserved }).error, /not a good id/, String(id))
  }
  for (const id of ['a', '9lives', 'my-provider-2', 'a'.repeat(32)]) assert.equal(validateEntry({ ...good, id }, { reserved }).error, undefined, id)
  assert.equal(validateEntry({ ...good, id: '  padded  ' }, { reserved }).entry.id, 'padded')
})

test('every real built-in provider name is reserved by default', () => {
  const real = JSON.parse(readFileSync(new URL('./providers.json', import.meta.url), 'utf8')).providers.map(provider => provider.name)
  assert.ok(real.length > 0)
  for (const name of real) {
    if (!/^[a-z0-9][a-z0-9-]{0,31}$/.test(name)) continue
    assert.match(validateEntry({ ...good, id: name }).error, /built-in/, name)
  }
})

test('reserved names win over the duplicate check and custom reserved lists are honoured', () => {
  const seen = new Set(['groq'])
  assert.match(validateEntry({ ...good, id: 'groq' }, { reserved, seen }).error, /built-in/)
  assert.equal(validateEntry({ ...good, id: 'groq' }, { reserved: [] }).error, undefined)
  assert.match(validateEntry({ ...good, id: 'mine' }, { reserved: ['mine'] }).error, /built-in/)
})

test('a provider cannot reuse a name already seen', () => {
  assert.match(validateEntry(good, { reserved, seen: new Set(['mine']) }).error, /listed twice/)
})

test('loadCustom passes reserved names through and keeps going after bad entries', () => {
  const { entries, problems } = loadCustom(write([{ ...good, id: 'groq' }, { ...good, id: 'ok' }, { ...good, id: 'kilo' }]), { reserved })
  assert.deepEqual(entries.map(entry => entry.id), ['ok'])
  assert.equal(problems.length, 2)
  assert.ok(problems.every(problem => /built-in/.test(problem)))
})

test('addresses with credentials, spaces or env lookups are rejected', () => {
  for (const baseURL of ['https://user:pw@example.com/v1', 'https://user@example.com/v1', 'https://example.com/a b', 'https://${HOST}/v1']) {
    assert.ok(validateEntry({ ...good, baseURL }, { reserved }).error, baseURL)
  }
  assert.match(validateEntry({ ...good, baseURL: 'ftp://example.com' }, { reserved }).error, /http:\/\/ or https:\/\//)
  assert.match(validateEntry({ ...good, baseURL: undefined }, { reserved }).error, /not a web address/)
  assert.equal(validateEntry({ ...good, baseURL: 'http://localhost:11434/v1' }, { reserved }).error, undefined)
})

test('cleanURL handles odd input', () => {
  assert.equal(cleanURL(undefined), '')
  assert.equal(cleanURL(null), '')
  assert.equal(cleanURL('  https://example.com/v1///  '), 'https://example.com/v1')
  assert.equal(cleanURL('https://example.com/v1/chat/completions'), 'https://example.com/v1')
})

test('key variable names must end in _KEY, _TOKEN or _SECRET', () => {
  for (const keyVar of ['MINE_API_KEY', 'HF_TOKEN', 'MY_SECRET', 'A1_KEY']) assert.equal(validateEntry({ ...good, keyVar }, { reserved }).error, undefined, keyVar)
  for (const keyVar of ['KEY', 'mine_key', 'MINE', '1_KEY', 'MINE-KEY', 'MINE KEY', 'HOME', '']) assert.match(validateEntry({ ...good, keyVar }, { reserved }).error, /key name/, keyVar)
})

test('keyless is only honoured when exactly true and drops any key name', () => {
  const local = validateEntry({ ...good, keyless: true }, { reserved }).entry
  assert.equal(local.keyless, true)
  assert.equal('keyVar' in local, false)
  assert.match(validateEntry({ ...good, keyless: 'true', keyVar: undefined }, { reserved }).error, /key name/)
  assert.equal('keyless' in validateEntry(good, { reserved }).entry, false)
})

test('entry is cleaned: name default, paid and vision flags, trimmed models', () => {
  const plain = validateEntry({ ...good, name: undefined }, { reserved }).entry
  assert.equal(plain.name, 'mine')
  assert.equal(validateEntry({ ...good, name: '   ' }, { reserved }).entry.name, 'mine')
  assert.equal(validateEntry({ ...good, paid: false }, { reserved }).entry.paid, false)
  assert.equal(validateEntry({ ...good, paid: 'no' }, { reserved }).entry.paid, true)
  assert.equal('vision' in plain, false)
  assert.equal('vision' in validateEntry({ ...good, vision: 'yes' }, { reserved }).entry, false)
  assert.equal(validateEntry({ ...good, vision: true }, { reserved }).entry.vision, true)
  assert.deepEqual(validateEntry({ ...good, models: [' a ', 'b'] }, { reserved }).entry.models, ['a', 'b'])
  assert.match(validateEntry({ ...good, models: 'big-model' }, { reserved }).error, /at least one model/)
  assert.match(validateEntry({ ...good, models: [' x ', 'x'] }, { reserved }).error, /twice/)
})

test('validateEntry does not mutate its input', () => {
  const raw = { ...good, baseURL: 'https://example.com/v1/', models: [' a '] }
  const copy = JSON.parse(JSON.stringify(raw))
  validateEntry(raw, { reserved })
  assert.deepEqual(raw, copy)
})

test('maskKey edge cases never reveal short keys', () => {
  assert.equal(maskKey(undefined), '(not set)')
  assert.equal(maskKey(null), '(not set)')
  assert.equal(maskKey('12345678'), '****')
  assert.equal(maskKey('123456789'), '123...6789')
  assert.equal(maskKey(12345), '****')
  const secret = 'sk-live-SUPERSECRETVALUE-9876'
  const masked = maskKey(secret)
  assert.equal(masked, 'sk-...9876')
  assert.ok(masked.length < secret.length)
  assert.ok(!masked.includes('SUPERSECRET'))
})

test('a bad key name in the file is not echoed back in the problem text', () => {
  const secret = 'sk-this-is-a-real-secret-1234'
  const { entries, problems } = loadCustom(write([{ ...good, keyVar: secret }]), { reserved })
  assert.equal(entries.length, 0)
  assert.equal(problems.length, 1)
  assert.match(problems[0], /key name/)
  assert.ok(!problems[0].includes(secret))
})

test('keys.conf is mirrored into the environment and removals are deleted', () => {
  const file = write('# a comment\nA_KEY=one\nB_KEY="two"\n\nBROKEN\n')
  delete process.env.A_KEY
  delete process.env.B_KEY
  process.env.OUTSIDE_KEY = 'keep'
  assert.deepEqual(syncKeys(file, true).sort(), ['A_KEY', 'B_KEY'])
  assert.equal(process.env.A_KEY, 'one')
  assert.equal(process.env.B_KEY, 'two')
  setKeyVar(file, 'A_KEY', 'next')
  assert.match(readFileSync(file, 'utf8'), /^A_KEY=next$/m)
  assert.equal(process.env.A_KEY, 'next')
  setKeyVar(file, 'A_KEY', '')
  assert.equal(process.env.A_KEY, undefined)
  assert.equal(process.env.B_KEY, 'two')
  assert.equal(process.env.OUTSIDE_KEY, 'keep')
  delete process.env.OUTSIDE_KEY
})

test('a preset entry is upserted into custom-providers.json', () => {
  const file = write('[]')
  const preset = PRESETS.find(p => p.id === 'openai')
  const first = upsertPresetEntry(file, preset, ['gpt-test'])
  assert.equal(first.error, undefined)
  let saved = JSON.parse(readFileSync(file, 'utf8'))
  assert.equal(saved.length, 1)
  assert.equal(saved[0].id, 'openai')
  assert.deepEqual(saved[0].models, ['gpt-test'])
  upsertPresetEntry(file, preset, ['gpt-other'])
  saved = JSON.parse(readFileSync(file, 'utf8'))
  assert.equal(saved.length, 1)
  assert.deepEqual(saved[0].models, ['gpt-other'])
  assert.equal(upsertPresetEntry(file, preset, []).error, 'no models to list')
})

test('probeModels lists chat models and drops the rest', async () => {
  const seen = []
  const srv = await new Promise(resolve => {
    const s = http.createServer((req, res) => {
      seen.push(req.headers.authorization)
      res.writeHead(200, { 'content-type': 'application/json' })
      res.end(JSON.stringify({ data: [{ id: 'chat-big' }, { id: 'chat-embed-small' }, { id: 'whisper-1' }, { name: 'named-model' }] }))
    }).listen(0, '127.0.0.1', () => resolve(s))
  })
  const base = `http://127.0.0.1:${srv.address().port}`
  const probed = await probeModels({ id: 'x', baseURL: base, keyVar: 'X_KEY' }, 'sk-test-123456')
  assert.deepEqual(probed.models, ['chat-big', 'named-model'])
  assert.deepEqual(seen, ['Bearer sk-test-123456'])
  const anthropic = await probeModels({ id: 'a', baseURL: base, keyVar: 'A_KEY', probeKeyHeader: 'x-api-key' }, 'sk-test-123456')
  assert.equal(anthropic.error, undefined)
  srv.close()
})
