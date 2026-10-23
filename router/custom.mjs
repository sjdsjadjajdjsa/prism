// Your own AI providers, added with "prism keys add".
// The list lives in custom-providers.json (next to keys.conf); the secret keys stay in keys.conf.
import { readFileSync, statSync } from 'node:fs'

const here = new URL('.', import.meta.url)

// Services most people use. Each one speaks the OpenAI chat format, so only the key is needed.
// paid: true means every request uses the user's own credit.
export const PRESETS = [
  { id: 'openai', name: 'OpenAI', baseURL: 'https://api.openai.com/v1', keyVar: 'OPENAI_API_KEY', paid: true, keyUrl: 'https://platform.openai.com/api-keys', models: [] },
  { id: 'anthropic', name: 'Anthropic (Claude)', baseURL: 'https://api.anthropic.com/v1', keyVar: 'ANTHROPIC_API_KEY', paid: true, keyUrl: 'https://console.anthropic.com/settings/keys', models: [] },
  { id: 'deepseek', name: 'DeepSeek', baseURL: 'https://api.deepseek.com/v1', keyVar: 'DEEPSEEK_API_KEY', paid: true, keyUrl: 'https://platform.deepseek.com/api_keys', models: ['deepseek-chat'] },
  { id: 'xai', name: 'xAI (Grok)', baseURL: 'https://api.x.ai/v1', keyVar: 'XAI_API_KEY', paid: true, keyUrl: 'https://console.x.ai', models: [] },
  { id: 'together', name: 'Together AI', baseURL: 'https://api.together.xyz/v1', keyVar: 'TOGETHER_API_KEY', paid: true, keyUrl: 'https://api.together.ai/settings/api-keys', models: [] },
  { id: 'fireworks', name: 'Fireworks AI', baseURL: 'https://api.fireworks.ai/inference/v1', keyVar: 'FIREWORKS_API_KEY', paid: true, keyUrl: 'https://app.fireworks.ai/settings/users/api-keys', models: [] },
  { id: 'cerebras', name: 'Cerebras (has a free tier)', baseURL: 'https://api.cerebras.ai/v1', keyVar: 'CEREBRAS_API_KEY', paid: false, keyUrl: 'https://cloud.cerebras.ai', models: [] },
  { id: 'sambanova', name: 'SambaNova (has a free tier)', baseURL: 'https://api.sambanova.ai/v1', keyVar: 'SAMBANOVA_API_KEY', paid: false, keyUrl: 'https://cloud.sambanova.ai/apis', models: [] },
  { id: 'openrouter-paid', name: 'OpenRouter (paid models)', baseURL: 'https://openrouter.ai/api/v1', keyVar: 'OPENROUTER_API_KEY', paid: true, keyUrl: 'https://openrouter.ai/keys', models: [] },
  { id: 'huggingface', name: 'Hugging Face router', baseURL: 'https://router.huggingface.co/v1', keyVar: 'HF_TOKEN', paid: true, keyUrl: 'https://huggingface.co/settings/tokens', models: [] },
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

const defaultFile = () => process.env.PRISM_CUSTOM_FILE || new URL('custom-providers.json', here).pathname
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
