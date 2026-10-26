// Your own AI providers, added with "prism keys add".
// The list lives in custom-providers.json (next to keys.conf); the secret keys stay in keys.conf.
import { readFileSync, writeFileSync, statSync, chmodSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const here = new URL('.', import.meta.url)

// Services most people use. Each one speaks the OpenAI chat format, so only the key is needed.
// paid: true means every request uses the user's own credit.
// models is the fallback list when the service does not answer a model probe.
export const PRESETS = [
  { id: 'openai', name: 'OpenAI', baseURL: 'https://api.openai.com/v1', keyVar: 'OPENAI_API_KEY', paid: true, keyUrl: 'https://platform.openai.com/api-keys', models: ['gpt-4o-mini'] },
  { id: 'anthropic', name: 'Anthropic (Claude)', baseURL: 'https://api.anthropic.com/v1', keyVar: 'ANTHROPIC_API_KEY', paid: true, keyUrl: 'https://console.anthropic.com/settings/keys', models: ['claude-sonnet-4-5'], probeKeyHeader: 'x-api-key' },
  { id: 'deepseek', name: 'DeepSeek', baseURL: 'https://api.deepseek.com/v1', keyVar: 'DEEPSEEK_API_KEY', paid: true, keyUrl: 'https://platform.deepseek.com/api_keys', models: ['deepseek-chat'] },
  { id: 'xai', name: 'xAI (Grok)', baseURL: 'https://api.x.ai/v1', keyVar: 'XAI_API_KEY', paid: true, keyUrl: 'https://console.x.ai', models: [] },
  { id: 'mistral-paid', name: 'Mistral (paid models)', baseURL: 'https://api.mistral.ai/v1', keyVar: 'MISTRAL_API_KEY', paid: true, keyUrl: 'https://console.mistral.ai/api-keys', models: ['mistral-large-4'] },
  { id: 'gemini-paid', name: 'Google Gemini (paid models)', baseURL: 'https://generativelanguage.googleapis.com/v1beta/openai', keyVar: 'GEMINI_API_KEY', paid: true, keyUrl: 'https://aistudio.google.com/apikey', models: [] },
  { id: 'together', name: 'Together AI', baseURL: 'https://api.together.xyz/v1', keyVar: 'TOGETHER_API_KEY', paid: true, keyUrl: 'https://api.together.ai/settings/api-keys', models: [] },
  { id: 'fireworks', name: 'Fireworks AI', baseURL: 'https://api.fireworks.ai/inference/v1', keyVar: 'FIREWORKS_API_KEY', paid: true, keyUrl: 'https://app.fireworks.ai/settings/users/api-keys', models: [] },
  { id: 'cerebras', name: 'Cerebras (has a free tier)', baseURL: 'https://api.cerebras.ai/v1', keyVar: 'CEREBRAS_API_KEY', paid: false, keyUrl: 'https://cloud.cerebras.ai', models: [] },
  { id: 'sambanova', name: 'SambaNova (has a free tier)', baseURL: 'https://api.sambanova.ai/v1', keyVar: 'SAMBANOVA_API_KEY', paid: false, keyUrl: 'https://cloud.sambanova.ai/apis', models: [] },
  { id: 'openrouter-paid', name: 'OpenRouter (paid models)', baseURL: 'https://openrouter.ai/api/v1', keyVar: 'OPENROUTER_API_KEY', paid: true, keyUrl: 'https://openrouter.ai/keys', models: ['openai/gpt-4o-mini'] },
  { id: 'huggingface', name: 'Hugging Face router', baseURL: 'https://router.huggingface.co/v1', keyVar: 'HF_TOKEN', paid: true, keyUrl: 'https://huggingface.co/settings/tokens', models: [] },
  { id: 'perplexity', name: 'Perplexity', baseURL: 'https://api.perplexity.ai', keyVar: 'PERPLEXITY_API_KEY', paid: true, keyUrl: 'https://www.perplexity.ai/settings/api', models: [] },
  { id: 'cohere', name: 'Cohere', baseURL: 'https://api.cohere.com/compatibility/v1', keyVar: 'COHERE_API_KEY', paid: true, keyUrl: 'https://dashboard.cohere.com/api-keys', models: [] },
  { id: 'moonshot', name: 'Moonshot (Kimi)', baseURL: 'https://api.moonshot.ai/v1', keyVar: 'MOONSHOT_API_KEY', paid: true, keyUrl: 'https://platform.moonshot.ai/console/api-keys', models: [] },
  { id: 'dashscope', name: 'Alibaba (Qwen)', baseURL: 'https://dashscope.aliyuncs.com/compatible-mode/v1', keyVar: 'DASHSCOPE_API_KEY', paid: true, keyUrl: 'https://dashscope.console.aliyun.com/apiKey', models: [] },
  { id: 'zhipu', name: 'Z.AI (GLM)', baseURL: 'https://open.bigmodel.cn/api/paas/v4', keyVar: 'ZHIPU_API_KEY', paid: true, keyUrl: 'https://z.ai/manage-apikey/apikey-list', models: [] },
  { id: 'deepinfra', name: 'DeepInfra', baseURL: 'https://api.deepinfra.com/v1/openai', keyVar: 'DEEPINFRA_API_KEY', paid: true, keyUrl: 'https://deepinfra.com/dash/api_keys', models: [] },
  { id: 'novita', name: 'Novita', baseURL: 'https://api.novita.ai/v3/openai', keyVar: 'NOVITA_API_KEY', paid: true, keyUrl: 'https://novita.ai/dashboard/keys', models: [] },
  { id: 'lambda', name: 'Lambda', baseURL: 'https://api.lambdalabs.com/v1', keyVar: 'LAMBDA_API_KEY', paid: true, keyUrl: 'https://cloud.lambdalabs.com/api-keys', models: [] },
  { id: 'venice', name: 'Venice (private AI)', baseURL: 'https://api.venice.ai/api/v1', keyVar: 'VENICE_API_KEY', paid: true, keyUrl: 'https://venice.ai/settings/api', models: [], keyCheck: '/api_keys/rate_limits' },
  { id: 'ollama-local', name: 'Ollama on this computer (no key)', baseURL: 'http://host.docker.internal:11434/v1', keyless: true, paid: false, keyUrl: 'https://ollama.com', models: [] },
  { id: 'lmstudio', name: 'LM Studio on this computer (no key)', baseURL: 'http://host.docker.internal:1234/v1', keyless: true, paid: false, keyUrl: 'https://lmstudio.ai', models: [] },
]

const idPattern = /^[a-z0-9][a-z0-9-]{0,31}$/
const keyVarPattern = /^[A-Z][A-Z0-9_]*_(KEY|TOKEN|SECRET)$/

let builtIn
const builtInNames = () => {
  if (!builtIn) {
    try {
      builtIn = JSON.parse(readFileSync(new URL('providers.json', here), 'utf8')).providers.map(provider => provider.name)
    } catch {
      builtIn = []
    }
  }
  return builtIn
}

export const cleanURL = value => String(value ?? '').trim().replace(/\/+$/, '').replace(/\/chat\/completions$/, '').replace(/\/+$/, '')

// "claude-sonnet-5.5" -> "claude", "meta-llama/Llama-3.3-70B" -> "llama", "gpt-oss-120b" -> "gpt-oss"
export const familyOf = model => {
  const base = String(model).split('/').pop().split(':')[0].toLowerCase()
  if (base.startsWith('gpt-oss')) return 'gpt-oss'
  return base.split(/[-_.\s]/)[0].replace(/\d+$/, '') || base
}

export const maskKey = value => {
  const text = String(value ?? '')
  if (!text) return '(not set)'
  if (text.length <= 8) return '****'
  return `${text.slice(0, 3)}...${text.slice(-4)}`
}

// Returns { entry } with a cleaned copy, or { error } saying what to fix in plain words.
export const validateEntry = (raw, { reserved = builtInNames(), seen = new Set() } = {}) => {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return { error: 'not an object' }
  const id = String(raw.id ?? '').trim()
  if (!idPattern.test(id)) return { error: `"${id}" is not a good id (use lowercase letters, numbers and dashes)` }
  if (reserved.includes(id)) return { error: `"${id}" is already a built-in provider name` }
  if (seen.has(id)) return { error: `"${id}" is listed twice` }
  const baseURL = cleanURL(raw.baseURL)
  let url
  try {
    url = new URL(baseURL)
  } catch {
    return { error: `${id}: "${raw.baseURL ?? ''}" is not a web address` }
  }
  if (!['http:', 'https:'].includes(url.protocol)) return { error: `${id}: the address must start with http:// or https://` }
  if (url.username || url.password || /\s|\$\{/.test(baseURL)) return { error: `${id}: the address has characters Prism can't use` }
  const keyless = raw.keyless === true
  const keyVar = String(raw.keyVar ?? '').trim()
  if (!keyless && !keyVarPattern.test(keyVar)) return { error: `${id}: needs a key name ending in _KEY (or set keyless for local models)` }
  const models = Array.isArray(raw.models) ? raw.models.map(model => String(model).trim()) : []
  if (!models.length) return { error: `${id}: add at least one model name` }
  if (models.some(model => !model || /\s|\$\{/.test(model))) return { error: `${id}: model names can't have spaces` }
  if (new Set(models).size !== models.length) return { error: `${id}: a model name is listed twice` }
  const entry = {
    id,
    name: String(raw.name ?? id).trim() || id,
    baseURL,
    ...(keyless ? { keyless: true } : { keyVar }),
    models,
    paid: raw.paid !== false,
    ...(raw.vision === true && { vision: true }),
  }
  return { entry }
}

// Reads the file. Bad entries are skipped and described in problems, so one typo never breaks the router.
export const loadCustom = (file, options = {}) => {
  let text
  try {
    text = readFileSync(file, 'utf8')
  } catch (error) {
    return { entries: [], problems: error.code === 'ENOENT' ? [] : [`could not read ${file}: ${error.message}`] }
  }
  if (!text.trim()) return { entries: [], problems: [] }
  let list
  try {
    list = JSON.parse(text)
  } catch {
    return { entries: [], problems: ['custom-providers.json is not valid JSON'] }
  }
  if (!Array.isArray(list)) return { entries: [], problems: ['custom-providers.json must be a list'] }
  const seen = new Set()
  const entries = []
  const problems = []
  for (const raw of list) {
    const { entry, error } = validateEntry(raw, { ...options, seen })
    if (error) problems.push(error)
    else {
      seen.add(entry.id)
      entries.push(entry)
    }
  }
  return { entries, problems }
}

// Shape of one providers.json provider, plus mark = { paid, source } for the router to carry along.
export const toProvider = entry => ({
  name: entry.id,
  ...(entry.keyVar && { requires: entry.keyVar }),
  ...(entry.keyless && { auth: false }),
  baseURL: entry.baseURL,
  models: entry.models.map(name => ({ name, family: familyOf(name), roles: [], vision: entry.vision === true })),
  mark: { paid: entry.paid, source: 'yours' },
})

const defaultFile = () => process.env.PRISM_CUSTOM_FILE || fileURLToPath(new URL('custom-providers.json', here))
export const customFilePath = defaultFile
let cache = { file: '', stamp: '', providers: [] }
let reported = ''

export const customProviders = (file = defaultFile()) => {
  let stamp = 'missing'
  try {
    const info = statSync(file)
    stamp = `${info.mtimeMs}:${info.size}`
  } catch {}
  if (cache.file !== file || cache.stamp !== stamp) {
    const { entries, problems } = stamp === 'missing' ? { entries: [], problems: [] } : loadCustom(file)
    const note = problems.join(' | ')
    if (note && note !== reported) console.log(`custom providers skipped: ${note}`)
    reported = note
    cache = { file, stamp, providers: entries.map(toProvider) }
  }
  return cache.providers
}

// Paid routes only take part in "auto" when the user turned that on. Pinning a model always works.
export const eligible = (route, env = process.env) => route.paid !== true || env.PRISM_USE_PAID === '1'

// keys.conf holds the AI model keys. Docker passes it to the router as env_file at
// start and also mounts it read-write, so a key added from inside prism (POST /keys)
// works without a restart.

export const keysPath = () => {
  if (process.env.PRISM_KEYS_FILE) return process.env.PRISM_KEYS_FILE
  for (const candidate of [fileURLToPath(new URL('keys.conf', here)), fileURLToPath(new URL('../keys.conf', here))]) {
    try {
      statSync(candidate)
      return candidate
    } catch {}
  }
  return fileURLToPath(new URL('../keys.conf', here))
}

let keysCache = { file: '', stamp: '', vars: [] }

// Re-reads keys.conf when it changes. Vars removed from the file are deleted from
// process.env, so a removed key stops working at once.
export const syncKeys = (file = keysPath(), force = false) => {
  let stamp = 'missing'
  try {
    const info = statSync(file)
    stamp = `${info.mtimeMs}:${info.size}`
  } catch {}
  if (!force && keysCache.file === file && keysCache.stamp === stamp) return keysCache.vars
  const vars = []
  if (stamp !== 'missing') {
    for (const line of readFileSync(file, 'utf8').split('\n')) {
      const clean = line.trim()
      if (!clean || clean.startsWith('#')) continue
      const cut = clean.indexOf('=')
      if (cut < 1) continue
      const name = clean.slice(0, cut).trim()
      const value = clean.slice(cut + 1).trim().replace(/^["']|["']$/g, '')
      if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name) || !value) continue
      process.env[name] = value
      vars.push(name)
    }
  }
  for (const name of keysCache.vars) if (!vars.includes(name)) delete process.env[name]
  keysCache = { file, stamp, vars }
  return vars
}

// setKeyVar writes KEY=value into keys.conf in place (a single-file bind mount
// breaks on rename) and refreshes the environment. An empty value removes the key.
export const setKeyVar = (file, name, value) => {
  const keep = []
  try {
    for (const line of readFileSync(file, 'utf8').split('\n')) {
      if (line.trim().startsWith(`${name}=`)) continue
      keep.push(line)
    }
  } catch {}
  while (keep.length && keep[keep.length - 1] === '') keep.pop()
  if (value) keep.push(`${name}=${value}`)
  writeFileSync(file, `${keep.join('\n')}\n`, { mode: 0o600 })
  try {
    chmodSync(file, 0o600)
  } catch {}
  syncKeys(file, true)
}

// upsertPresetEntry adds or updates the custom-providers.json entry for a preset,
// in place. Existing models are kept when the probe adds nothing new.
export const upsertPresetEntry = (file, preset, models) => {
  if (!models.length) return { error: 'no models to list' }
  let list = []
  try {
    const parsed = JSON.parse(readFileSync(file, 'utf8'))
    if (Array.isArray(parsed)) list = parsed
  } catch {}
  const entry = {
    id: preset.id,
    name: preset.name,
    baseURL: preset.baseURL,
    ...(preset.keyless ? { keyless: true } : { keyVar: preset.keyVar }),
    models,
    paid: preset.paid !== false,
  }
  const { error } = validateEntry(entry)
  if (error) return { error }
  const next = []
  let placed = false
  for (const raw of list) {
    if (raw && typeof raw === 'object' && raw.id === preset.id) {
      next.push(entry)
      placed = true
    } else next.push(raw)
  }
  if (!placed) next.push(entry)
  writeFileSync(file, JSON.stringify(next, null, 2) + '\n', { mode: 0o600 })
  try {
    chmodSync(file, 0o600)
  } catch {}
  return { models }
}

// Prism always sends tools, so models a service marks as unable to call them, or offline, are left out.
const usable = m => {
  const spec = m?.model_spec
  if (spec?.offline === true || spec?.capabilities?.supportsFunctionCalling === false) return false
  return !Array.isArray(m?.supported_parameters) || m.supported_parameters.includes('tools')
}

// probeModels asks the provider which models the key can use. A key the service
// refuses comes back as rejected; any other failed probe never blocks saving the
// key, and the preset's fallback models are used.
export const probeModels = async (preset, key) => {
  const headers = {}
  if (preset.probeKeyHeader === 'x-api-key') {
    headers['x-api-key'] = key
    headers['anthropic-version'] = '2023-06-01'
  } else {
    headers.authorization = `Bearer ${key}`
  }
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), Number(process.env.PRISM_PROBE_MS) || 10_000)
  try {
    if (preset.keyCheck) {
      const check = await fetch(`${preset.baseURL}${preset.keyCheck}`, { headers, signal: controller.signal })
      if (check.status === 401 || check.status === 403) return { rejected: true, error: 'the service rejected this key' }
    }
    const res = await fetch(`${preset.baseURL}/models`, { headers, signal: controller.signal })
    if (res.status === 401 || res.status === 403) return { rejected: true, error: 'the service rejected this key' }
    if (!res.ok) return { error: `the service answered ${res.status}` }
    const data = await res.json()
    const ids = [...(data.data ?? data.models ?? [])].filter(usable).map(m => String(m?.id ?? m?.name ?? '')).filter(Boolean)
    const chat = ids.filter(id => !/embed|whisper|tts|transcri|rerank|moderat|dall-e|image|voice|guard/i.test(id))
    return chat.length ? { models: chat.slice(0, 300) } : { error: 'the service listed no chat models' }
  } catch {
    return { error: 'could not reach the service' }
  } finally {
    clearTimeout(timer)
  }
}
