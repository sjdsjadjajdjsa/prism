// Subscriptions: sign in to a plan you already pay for, with a device code or a
// browser sign-in, the same way the vendor's own tools do. Tokens live in
// subscriptions.json. Client ids are the vendors' own public CLI clients.
import { readFileSync, writeFileSync, statSync, chmodSync } from 'node:fs'
import { createHash, randomUUID } from 'node:crypto'
import http from 'node:http'

const here = new URL('.', import.meta.url)

// Each account: where to get a device code, how to turn it into a token, and
// where chat requests go. Models are probed after sign-in and stored per account.
export const SUBSCRIPTIONS = [
  {
    id: 'copilot',
    name: 'GitHub Copilot',
    deviceURL: 'https://github.com/login/device/code',
    tokenURL: 'https://github.com/login/oauth/access_token',
    clientID: 'Iv1.b507a08c87ecfe98',
    exchangeURL: 'https://api.github.com/copilot_internal/v2/token',
    apiBase: 'https://api.githubcopilot.com',
    models: [],
  },
  {
    id: 'chatgpt',
    name: 'ChatGPT (Codex)',
    kind: 'browser',
    authorizeURL: 'https://auth.openai.com/oauth/authorize',
    tokenURL: 'https://auth.openai.com/oauth/token',
    clientID: 'app_EMoamEEZ73f0CkXaXp7hrann',
    redirectPort: 1455,
    redirectPath: '/auth/callback',
    scopes: 'openid profile email offline_access',
    extraAuth: { id_token_add_organizations: 'true', codex_cli_simplified_flow: 'true' },
    apiBase: 'https://chatgpt.com/backend-api/codex',
    wire: 'codex',
    apiHeaders: { 'OpenAI-Beta': 'responses=experimental', originator: 'codex_cli_rs' },
    apiUserAgent: 'codex_cli_rs/0.175.0',
    // The backend gates both the model list and requests on this query param.
    modelsQuery: { client_version: '0.175.0' },
    models: ['gpt-6.1-sol', 'gpt-6-luna'],
  },
  {
    id: 'claude',
    name: 'Claude',
    kind: 'browser',
    authorizeURL: 'https://claude.ai/oauth/authorize',
    tokenURL: 'https://platform.claude.com/v1/oauth/token',
    clientID: '9d1c250a-e61b-44d9-88ed-5944d1962f5e',
    redirectPort: 0,
    redirectPath: '/callback',
    scopes: 'user:inference user:profile user:sessions:claude_code',
    // Claude's token exchange takes the state back (most providers reject it).
    stateInExchange: true,
    apiBase: 'https://api.anthropic.com/v1',
    wire: 'anthropic',
    // The full Claude Code fingerprint: Anthropic throttles OAuth requests that
    // do not look like their own CLI (a bare 429 with a valid token).
    apiHeaders: {
      'anthropic-version': '2023-06-01',
      'anthropic-beta': 'oauth-2025-04-20,claude-code-20250219',
      'anthropic-dangerous-direct-browser-access': 'true',
      'x-app': 'cli',
      'x-stainless-lang': 'js',
      'x-stainless-runtime': 'node',
      'x-stainless-retry-count': '0',
      'x-stainless-timeout': '600',
      'x-stainless-package-version': '0.112.1',
      'x-stainless-runtime-version': 'v26.3.0',
      'x-stainless-os': 'Linux',
      'x-stainless-arch': 'x64',
    },
    apiUserAgent: 'claude-cli/2.1.280 (external, cli)',
    models: ['claude-sonnet-5.5', 'claude-opus-5.5', 'claude-haiku-5.5'],
  },
  {
    id: 'grok',
    name: 'Grok (xAI)',
    kind: 'browser',
    authorizeURL: 'https://auth.x.ai/oauth2/authorize',
    tokenURL: 'https://auth.x.ai/oauth2/token',
    deviceURL: 'https://auth.x.ai/oauth2/device/code',
    clientID: 'b1a00492-073a-47ea-816f-4c329264a828',
    redirectPort: 56121,
    redirectPath: '/callback',
    redirectHost: '127.0.0.1',
    scopes: 'openid profile email offline_access api:access',
    apiBase: 'https://api.x.ai/v1',
    apiUserAgent: 'grok-cli/1.2.0',
    models: ['grok-4.7'],
  },
  {
    id: 'gemini',
    name: 'Gemini (Code Assist)',
    kind: 'browser',
    authorizeURL: 'https://accounts.google.com/o/oauth2/v2/auth',
    tokenURL: 'https://oauth2.googleapis.com/token',
    // Google's push protection blocks committing the gemini-cli's public client,
    // so it comes from keys.conf (GEMINI_OAUTH_CLIENT_ID / _SECRET), read live.
    clientIDEnv: 'GEMINI_OAUTH_CLIENT_ID',
    get clientID() {
      return process.env.GEMINI_OAUTH_CLIENT_ID ?? ''
    },
    get clientSecret() {
      return process.env.GEMINI_OAUTH_CLIENT_SECRET ?? ''
    },
    redirectPort: 0,
    redirectPath: '/oauth2callback',
    scopes: 'https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile',
    extraAuth: { access_type: 'offline', prompt: 'consent' },
    apiBase: 'https://cloudcode-pa.googleapis.com',
    wire: 'gemini',
    register: true,
    apiUserAgent: 'gemini-cli/1.4.0 (oauth)',
    models: ['gemini-3.8-flash'],
  },
]

export const subsPath = () => {
  if (process.env.PRISM_SUBS_FILE) return process.env.PRISM_SUBS_FILE
  for (const candidate of [new URL('subscriptions.json', here).pathname, new URL('../subscriptions.json', here).pathname]) {
    try {
      statSync(candidate)
      return candidate
    } catch {}
  }
  return new URL('../subscriptions.json', here).pathname
}

let subsCache = { file: '', stamp: '', data: {} }

// loadSubs reads subscriptions.json (mtime-cached). Never throws: a missing
// or broken file just means nobody is signed in.
export const loadSubs = (file = subsPath()) => {
  let stamp = 'missing'
  try {
    const info = statSync(file)
    stamp = `${info.mtimeMs}:${info.size}`
  } catch {}
  if (subsCache.file !== file || subsCache.stamp !== stamp) {
    let data = {}
    if (stamp !== 'missing') {
      try {
        const parsed = JSON.parse(readFileSync(file, 'utf8'))
        if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) data = parsed
      } catch {}
    }
    subsCache = { file, stamp, data }
  }
  return subsCache.data
}

const saveSubs = (file, data) => {
  writeFileSync(file, JSON.stringify(data, null, 2) + '\n', { mode: 0o600 })
  try {
    chmodSync(file, 0o600)
  } catch {}
  subsCache = { file, stamp: '', data }
}

// The short-lived API token for an account, refreshed through the vendor's
// exchange endpoint when it is close to expiring.
const apiTokens = new Map()

// refreshOAuth renews a browser sign-in with its refresh token.
const refreshOAuth = async sub => {
  const stored = loadSubs()[sub.id]
  if (!stored?.refreshToken) throw new Error(`${sub.id} sign-in expired; run /login ${sub.id} again`)
  const body = new URLSearchParams({ grant_type: 'refresh_token', refresh_token: stored.refreshToken, client_id: sub.clientID })
  if (sub.clientSecret) body.set('client_secret', sub.clientSecret)
  const res = await fetch(sub.tokenURL, {
    method: 'POST',
    headers: { 'content-type': 'application/x-www-form-urlencoded', accept: 'application/json' },
    body,
    signal: AbortSignal.timeout(10_000),
  })
  if (!res.ok) throw new Error(`${sub.id} sign-in expired (${res.status}); run /login ${sub.id} again`)
  const data = await res.json()
  if (!data?.access_token) throw new Error(`${sub.id} did not return a token`)
  const fresh = {
    ...stored,
    accessToken: data.access_token,
    refreshToken: data.refresh_token ?? stored.refreshToken,
    expiresAt: Date.now() + (data.expires_in ?? 3600) * 1000,
  }
  const file = subsPath()
  saveSubs(file, { ...loadSubs(file), [sub.id]: fresh })
  return fresh
}

export const subToken = async sub => {
  const stored = loadSubs()[sub.id]
  if (!stored?.accessToken) throw new Error(`not signed in to ${sub.id}`)
  if (sub.kind === 'browser') {
    if (!stored.expiresAt || stored.expiresAt > Date.now() + 120_000) return { token: stored.accessToken }
    const fresh = await refreshOAuth(sub)
    return { token: fresh.accessToken }
  }
  const cached = apiTokens.get(sub.id)
  if (cached && cached.expiresAt > Date.now() + 120_000) return cached
  const res = await fetch(sub.exchangeURL, {
    headers: {
      authorization: `Bearer ${stored.accessToken}`,
      accept: 'application/json',
      'user-agent': sub.apiUserAgent ?? 'prism-router',
      'editor-version': 'prism/1.0',
      'copilot-integration-id': 'prism',
    },
    signal: AbortSignal.timeout(10_000),
  })
  if (!res.ok) throw new Error(`${sub.id} sign-in expired (${res.status}); run /login ${sub.id} again`)
  const data = await res.json()
  if (!data?.token) throw new Error(`${sub.id} did not return an API token`)
  const fresh = { token: data.token, expiresAt: (data.expires_at ?? Date.now() / 1000 + 60) * 1000 }
  apiTokens.set(sub.id, fresh)
  return fresh
}

// --- browser sign-in ------------------------------------------------------------------------

const browserPending = new Map()

// loginErrors remembers the last sign-in failure per account, so /login can
// say what went wrong instead of failing silently.
const loginErrors = new Map()
export const loginError = id => loginErrors.get(id) ?? null

const loginPage = (ok, message) =>
  `<html><body style="font-family:sans-serif;background:#1c1c28;color:#eee;display:flex;align-items:center;justify-content:center;height:100vh"><p>${ok ? 'signed in — you can close this tab' : `sign-in failed: ${message}`}</p></body></html>`

// startBrowserLogin builds an authorize URL (PKCE) and waits for the vendor's
// redirect. Fixed-port providers (ChatGPT 1455, Grok 56121) get their own
// listener; the rest ride on the router's own port via handleOAuthCallback.
export const startBrowserLogin = async sub => {
  if (!sub.clientID) return { error: `${sub.name} needs its sign-in client: add ${sub.clientIDEnv} and ${sub.clientIDEnv.replace('_ID', '_SECRET')} to keys.conf (the gemini-cli's public Desktop client, or your own)` }
  const verifier = randomUUID() + randomUUID()
  const challenge = createHash('sha256').update(verifier).digest('base64url')
  const state = randomUUID()
  const host = sub.redirectHost ?? 'localhost'
  const port = sub.redirectPort || Number(process.env.PORT) || 4848
  const redirectURI = `http://${host}:${port}${sub.redirectPath}`
  const params = new URLSearchParams({
    response_type: 'code',
    client_id: sub.clientID,
    redirect_uri: redirectURI,
    scope: sub.scopes,
    state,
    code_challenge: challenge,
    code_challenge_method: 'S256',
    ...(sub.extraAuth ?? {}),
  })
  const url = `${sub.authorizeURL}?${params}`
  const entry = { sub, verifier, state, redirectURI, url, until: Date.now() + 10 * 60_000, server: null }
  browserPending.set(state, entry)
  if (sub.redirectPort) {
    const listen = process.env.HOST || '0.0.0.0'
    entry.server = http.createServer(async (req, res) => {
      const url = new URL(req.url, 'http://router')
      if (url.pathname !== sub.redirectPath) {
        res.writeHead(404)
        return res.end()
      }
      const result = await finishBrowserLogin(url.searchParams.get('code'), url.searchParams.get('state'))
      res.writeHead(200, { 'content-type': 'text/html' })
      res.end(loginPage(!result?.error, result?.error ?? ''))
    })
    await new Promise((resolve, reject) => {
      entry.server.once('error', reject)
      entry.server.listen(sub.redirectPort, listen, resolve)
    })
    entry.until = Date.now() + 10 * 60_000
    setTimeout(() => closeBrowserLogin(entry), 10 * 60_000).unref?.()
  }
  return { url }
}

const closeBrowserLogin = entry => {
  if (entry.server) {
    entry.server.close()
    entry.server = null
  }
  if (browserPending.get(entry.state) === entry) browserPending.delete(entry.state)
}

// handleOAuthCallback serves the redirect for providers that ride on the
// router's own port (Claude, Gemini). Returns true when it handled the path.
export const handleOAuthCallback = async (req, res, pathname) => {
  const url = new URL(req.url, 'http://router')
  for (const entry of browserPending.values()) {
    if (entry.sub.redirectPort || url.pathname !== entry.sub.redirectPath) continue
    const result = await finishBrowserLogin(url.searchParams.get('code'), url.searchParams.get('state'))
    res.writeHead(200, { 'content-type': 'text/html' })
    res.end(loginPage(!result?.error, result?.error ?? ''))
    return true
  }
  return false
}

// pendingLoginURL tells the launcher which page to open in the user's browser.
export const pendingLoginURL = () => [...browserPending.values()][0]?.url ?? null

const finishBrowserLogin = async (code, state) => {
  const entry = browserPending.get(state)
  if (!entry || !code) {
    loginErrors.set(entry?.sub.id ?? 'unknown', 'the sign-in was cancelled')
    return { error: 'the sign-in was cancelled' }
  }
  const sub = entry.sub
  const body = new URLSearchParams({
    grant_type: 'authorization_code',
    code,
    redirect_uri: entry.redirectURI,
    client_id: sub.clientID,
    code_verifier: entry.verifier,
  })
  if (sub.clientSecret) body.set('client_secret', sub.clientSecret)
  if (sub.stateInExchange) body.set('state', state)
  let data
  try {
    const res = await fetch(sub.tokenURL, {
      method: 'POST',
      headers: { 'content-type': 'application/x-www-form-urlencoded', accept: 'application/json', ...(sub.apiUserAgent && { 'user-agent': sub.apiUserAgent }) },
      body,
      signal: AbortSignal.timeout(15_000),
    })
    if (!res.ok) {
      const detail = await res.text().catch(() => '')
      const message = `${sub.name} refused the sign-in (${res.status})${detail ? ': ' + detail.slice(0, 200) : ''}`
      loginErrors.set(sub.id, message)
      return { error: message }
    }
    data = await res.json()
  } catch (error) {
    const message = `could not reach ${sub.name}: ${error.message}`
    loginErrors.set(sub.id, message)
    return { error: message }
  }
  if (data.error) {
    const message = data.error_description ?? data.error
    loginErrors.set(sub.id, message)
    return { error: message }
  }
  closeBrowserLogin(entry)
  loginErrors.delete(sub.id)
  const account = {
    accessToken: data.access_token,
    refreshToken: data.refresh_token,
    expiresAt: Date.now() + (data.expires_in ?? 3600) * 1000,
    accountId: accountIdOf(data.id_token),
    // A stable device and session identity, the way the CLI has one.
    deviceId: randomUUID(),
    accountUuid: accountUuidOf(data.id_token) ?? randomUUID(),
    sessionId: randomUUID(),
    models: [],
  }
  const file = subsPath()
  saveSubs(file, { ...loadSubs(file), [sub.id]: account })
  if (sub.register) await registerGemini(sub, account)
  const models = await probeSubModels(sub)
  const list = models.models?.length ? models.models : sub.models
  const fresh = { ...loadSubs(file), [sub.id]: { ...account, models: list } }
  saveSubs(file, fresh)
  return { ok: true, models: list, note: models.error }
}

// accountIdOf pulls the ChatGPT account id out of the id_token JWT claims.
const accountIdOf = idToken => {
  if (!idToken) return undefined
  try {
    const claims = JSON.parse(Buffer.from(idToken.split('.')[1] ?? '', 'base64url').toString('utf8'))
    return claims['https://api.openai.com/auth']?.chatgpt_account_id ?? claims.chatgpt_account_id
  } catch {
    return undefined
  }
}

// accountUuidOf pulls the Claude account uuid out of the id_token JWT claims.
const accountUuidOf = idToken => {
  if (!idToken) return undefined
  try {
    const claims = JSON.parse(Buffer.from(idToken.split('.')[1] ?? '', 'base64url').toString('utf8'))
    return claims['https://api.anthropic.com/auth']?.account_uuid ?? claims.account_uuid
  } catch {
    return undefined
  }
}

// registerGemini binds the Google account to the Code Assist surface, the same
// call the gemini-cli makes after sign-in.
const registerGemini = async (sub, account) => {
  try {
    const res = await fetch(`${sub.apiBase}/v1internal:register`, {
      method: 'POST',
      headers: { authorization: `Bearer ${account.accessToken}`, 'content-type': 'application/json' },
      body: JSON.stringify({ toolId: 'gemini-cli', apiKey: '', useMutualTLS: false }),
      signal: AbortSignal.timeout(10_000),
    })
    const data = await res.json().catch(() => ({}))
    if (data.apiKey) {
      const file = subsPath()
      saveSubs(file, { ...loadSubs(file), [sub.id]: { ...loadSubs(file)[sub.id], apiKey: data.apiKey } })
    }
  } catch {}
}

// subscriptionProviders turns signed-in accounts into router providers. Their
// routes are paid-marked: they answer when picked by name, never for auto.
export const subscriptionProviders = () =>
  SUBSCRIPTIONS.filter(sub => (loadSubs()[sub.id]?.models ?? []).length).map(sub => ({
    name: sub.id,
    baseURL: loadSubs()[sub.id].apiBase ?? sub.apiBase,
    models: loadSubs()[sub.id].models.map(name => ({ name, family: name.split('/').pop().split(/[-_.]/)[0], roles: [], vision: false })),
    mark: { subscription: sub.id, paid: true, source: 'subscription' },
  }))

// --- device-code sign-in ---------------------------------------------------------------------

const pending = new Map()

export const startLogin = async sub => {
  let res
  try {
    res = await fetch(sub.deviceURL, {
      method: 'POST',
      headers: { 'content-type': 'application/json', accept: 'application/json', 'user-agent': 'prism-router' },
      body: JSON.stringify({ client_id: sub.clientID }),
      signal: AbortSignal.timeout(10_000),
    })
  } catch {
    return { error: `could not reach ${sub.name}` }
  }
  if (!res.ok) return { error: `could not reach ${sub.name} (${res.status})` }
  let data
  try {
    data = await res.json()
  } catch {
    return { error: `${sub.name} answered something Prism could not read` }
  }
  if (data.error) return { error: data.error_description ?? data.error }
  pending.set(sub.id, { deviceCode: data.device_code, interval: data.interval ?? 5, until: Date.now() + (data.expires_in ?? 900) * 1000, timer: null })
  return { userCode: data.user_code, verificationURI: data.verification_uri, expires: data.expires_in ?? 900 }
}

// pollLogin does one token poll. On success it stores the account, probes its
// models, and saves both. The caller drives the timing.
export const pollLogin = async sub => {
  const state = pending.get(sub.id)
  if (!state) return { status: 'idle' }
  if (Date.now() > state.until) {
    pending.delete(sub.id)
    return { status: 'error', message: 'the code expired; run /login again' }
  }
  let res
  try {
    res = await fetch(sub.tokenURL, {
      method: 'POST',
      headers: { 'content-type': 'application/json', accept: 'application/json', 'user-agent': 'prism-router' },
      body: JSON.stringify({ client_id: sub.clientID, device_code: state.deviceCode, grant_type: 'urn:ietf:params:oauth:grant-type:device_code' }),
      signal: AbortSignal.timeout(10_000),
    })
  } catch {
    return { status: 'error', message: `could not reach ${sub.name}` }
  }
  let data
  try {
    data = await res.json()
  } catch {
    return { status: 'error', message: `${sub.name} answered something Prism could not read` }
  }
  if (data.error === 'authorization_pending') return { status: 'pending', interval: state.interval }
  if (data.error === 'slow_down') {
    state.interval += 5
    return { status: 'pending', interval: state.interval }
  }
  if (data.error || !data.access_token) {
    pending.delete(sub.id)
    return { status: 'error', message: data.error_description ?? data.error ?? 'no token returned' }
  }
  pending.delete(sub.id)
  const file = subsPath()
  const subs = { ...loadSubs(file) }
  subs[sub.id] = { accessToken: data.access_token, models: [] }
  saveSubs(file, subs)
  const models = await probeSubModels(sub)
  const list = models.models?.length ? models.models : sub.models
  // Re-read before saving: a sign-in or sign-out for another account during the
  // probe must not be lost.
  const fresh = { ...loadSubs(file), [sub.id]: { ...subs[sub.id], models: list } }
  saveSubs(file, fresh)
  return { status: 'ok', models: list, note: models.error }
}

const probeSubModels = async sub => {
  try {
    const { token } = await subToken(sub)
    const stored = loadSubs()[sub.id] ?? {}
    const res = await fetch(`${sub.apiBase}/models${sub.modelsQuery ? `?${new URLSearchParams(sub.modelsQuery)}` : ''}`, {
      headers: {
        authorization: `Bearer ${token}`,
        accept: 'application/json',
        'user-agent': sub.apiUserAgent ?? 'prism-router',
        ...(sub.apiHeaders ?? {}),
        ...(stored.accountId && { 'chatgpt-account-id': stored.accountId }),
        ...(stored.apiKey && { 'x-goog-api-key': stored.apiKey }),
        ...(sub.id === 'copilot' && { 'copilot-integration-id': 'prism' }),
      },
      signal: AbortSignal.timeout(10_000),
    })
    if (!res.ok) return { error: `the service answered ${res.status}` }
    const data = await res.json()
    // The list has come back as a bare array, or under models/data/items, with
    // entries that are strings or carry slug/id/name.
    const lists = Array.isArray(data) ? [data] : [data.models, data.data, data.items].filter(Array.isArray)
    const ids = []
    for (const list of lists) {
      for (const item of list) {
        const id = typeof item === 'string' ? item : String(item?.slug ?? item?.id ?? item?.name ?? '')
        if (id.trim()) ids.push(id.trim())
      }
    }
    const chat = ids.filter(id => !/embed|whisper|tts|transcri|rerank|moderat|dall-e|image|voice|guard/i.test(id))
    return chat.length ? { models: [...new Set(chat)].slice(0, 300) } : { error: 'the service listed no chat models' }
  } catch (error) {
    return { error: error.message }
  }
}

export const signOut = sub => {
  const file = subsPath()
  const subs = { ...loadSubs(file) }
  delete subs[sub.id]
  apiTokens.delete(sub.id)
  saveSubs(file, subs)
}
