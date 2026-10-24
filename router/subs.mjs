// Subscriptions: sign in to a plan you already pay for, with a device code or a
// browser sign-in, the same way the vendor's own tools do. Tokens live in
// subscriptions.json. Client ids are the vendors' own public CLI clients.
import { readFileSync, writeFileSync, statSync, chmodSync } from 'node:fs'
import { createHash, randomUUID } from 'node:crypto'
import http from 'node:http'
import { grokFetch, grokRequestHeaders } from './wire.mjs'

const here = new URL('.', import.meta.url)

// Defeat DNS rebinding and cross-site browser requests, including callbacks.
export const localOnly = (req, res, port) => {
  const hosts = ['127.0.0.1', 'localhost', '[::1]', 'router', 'host.docker.internal'].map(host => `${host}:${port}`)
  const origins = [`http://127.0.0.1:${port}`, `http://localhost:${port}`]
  const message = !hosts.includes((req.headers.host ?? '').toLowerCase())
    ? 'prism only answers to local hosts'
    : req.headers.origin !== undefined && !origins.includes(req.headers.origin.toLowerCase())
      ? 'cross-origin requests are not allowed' : null
  if (message) {
    res.writeHead(403, { 'content-type': 'text/plain' })
    res.end(message + '\n')
    return false
  }
  res.setHeader('X-Content-Type-Options', 'nosniff')
  return true
}

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
    models: ['claude-sonnet-5-5', 'claude-opus-5-5', 'claude-haiku-5-5'],
  },
  {
    id: 'grok',
    name: 'Grok (xAI)',
    kind: 'device',
    discoveryURL: 'https://auth.x.ai/.well-known/openid-configuration',
    authorizeURL: 'https://auth.x.ai/oauth2/authorize',
    tokenURL: 'https://auth.x.ai/oauth2/token',
    deviceURL: 'https://auth.x.ai/oauth2/device/code',
    clientID: 'b1a00492-073a-47ea-816f-4c329264a828',
    redirectPort: 56121,
    redirectPath: '/callback',
    redirectHost: '127.0.0.1',
    // adapted from router-for-me/CLIProxyAPI (MIT): scope, CLI proxy and chat models.
    scopes: 'openid profile email offline_access grok-cli:access api:access',
    apiBase: 'https://cli-chat-proxy.grok.com/v1',
    wire: 'grok',
    models: ['grok-4.7', 'grok-4.7-build-fast', 'grok-4.6', 'grok-build-0.1', 'grok-4.5', 'grok-4.3', 'grok-4.20-0309-reasoning', 'grok-4.20-0309-non-reasoning', 'grok-4.20-multi-agent-0309', 'grok-3-mini', 'grok-3-mini-fast', 'grok-composer-2.5-fast'],
  },
  {
    id: 'antigravity',
    name: 'Antigravity (Google)',
    kind: 'browser',
    authorizeURL: 'https://accounts.google.com/o/oauth2/v2/auth',
    tokenURL: 'https://oauth2.googleapis.com/token',
    // Antigravity's own public IDE client (constants.go), written directly in
    // the code the way the IDE ships it.
    clientID: '1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com',
    clientSecret: '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',
    redirectPort: 51121,
    redirectHost: 'localhost',
    redirectPath: '/oauth-callback',
    scopes:
      'https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/cclog https://www.googleapis.com/auth/experimentsandconfigs',
    extraAuth: { access_type: 'offline', prompt: 'consent' },
    // The daily endpoint is the one the Antigravity IDE talks to; the prod one
    // answers subscription accounts with RESOURCE_EXHAUSTED (429).
    apiBase: 'https://daily-cloudcode-pa.googleapis.com',
    wire: 'antigravity',
    onboard: true,
    apiUserAgent: 'antigravity/hub/2.24.0 (aidev_client; os_type=darwin; arch=arm64; cl=1000335682)',
    models: ['gemini-3.8-flash', 'gemini-4-argon', 'claude-sonnet-5-5', 'claude-opus-5-5'],
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
  const generation = loginGenerations.get(sub.id)
  const stored = loadSubs()[sub.id]
  if (!stored?.refreshToken) throw new Error(`${sub.id} sign-in expired; run /login ${sub.id} again`)
  const body = new URLSearchParams({ grant_type: 'refresh_token', refresh_token: stored.refreshToken, client_id: sub.clientID })
  if (sub.clientSecret) body.set('client_secret', sub.clientSecret)
  const res = await fetch(stored.tokenURL ?? sub.tokenURL, {
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
  if (loginGenerations.get(sub.id) !== generation) throw new Error('the sign-in was cancelled')
  saveSubs(file, { ...loadSubs(file), [sub.id]: fresh })
  return fresh
}

export const subToken = async sub => {
  const stored = loadSubs()[sub.id]
  if (!stored?.accessToken) throw new Error(`not signed in to ${sub.id}`)
  if (needsGrokConsent(sub.id)) throw new Error(GROK_CONSENT_ERROR)
  // Older signed-in Grok accounts predate the persisted CLI session identity.
  if (sub.id === 'grok' && !stored.sessionId) {
    stored.sessionId = randomUUID()
    const file = subsPath()
    saveSubs(file, { ...loadSubs(file), [sub.id]: stored })
  }
  if (sub.kind === 'browser' || sub.id === 'grok') {
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
const finishingAccounts = new Map()
const loginGenerations = new Map()
const cancelLogin = id => {
  loginGenerations.set(id, (loginGenerations.get(id) ?? 0) + 1)
  for (const entry of browserPending.values()) {
    if (entry.sub.id === id) closeBrowserLogin(entry)
  }
  pending.delete(id)
  finishingAccounts.delete(id)
  apiTokens.delete(id)
  return loginGenerations.get(id)
}

// loginErrors remembers the last sign-in failure per account, so /login can
// say what went wrong instead of failing silently.
const loginErrors = new Map()
const GROK_CONSENT_ERROR = 'sign in to grok again to give Prism access to your Grok plan (/login grok)'
const needsGrokConsent = id => {
  const account = loadSubs()[id]
  const scopes = account?.scopes
  return id === 'grok' && Boolean(account?.accessToken) && !(Array.isArray(scopes) ? scopes : String(scopes ?? '').split(/\s+/)).includes('grok-cli:access')
}
export const loginError = id => needsGrokConsent(id) ? GROK_CONSENT_ERROR : loginErrors.get(id) ?? loadSubs()[id]?.loginError ?? null
export const loginStatus = sub => {
  for (const entry of browserPending.values()) {
    if (Date.now() >= entry.until) expireBrowserLogin(entry)
  }
  if (pending.has(sub.id) && Date.now() >= pending.get(sub.id).until) {
    pending.delete(sub.id)
    loginErrors.set(sub.id, 'the code expired; run /login again')
  }
  const waiting = pending.has(sub.id) || [...browserPending.values()].some(entry => entry.sub.id === sub.id)
  return {
    id: sub.id, name: sub.name,
    signedIn: !waiting && !finishingAccounts.has(sub.id) && !needsGrokConsent(sub.id) && Boolean(loadSubs()[sub.id]?.accessToken),
    pending: waiting,
    finishing: finishingAccounts.has(sub.id),
    models: visibleModels(sub.id, loadSubs()[sub.id]?.models).length,
    error: loginError(sub.id),
  }
}

const escapeHTML = value => String(value).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

const loginPage = (ok, message) =>
  `<html><body style="font-family:sans-serif;background:#1c1c28;color:#eee;display:flex;align-items:center;justify-content:center;height:100vh"><p>${ok ? 'signed in — you can close this tab' : `sign-in failed: ${escapeHTML(message)}`}</p></body></html>`

// startBrowserLogin builds an authorize URL (PKCE) and waits for the vendor's
// redirect. Fixed-port providers (ChatGPT 1455, Grok 56121) get their own
// listener; the rest ride on the router's own port via handleOAuthCallback.
export const startBrowserLogin = async sub => {
  const generation = cancelLogin(sub.id)
  loginErrors.delete(sub.id)
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
  const entry = { sub, generation, verifier, state, redirectURI, url, until: Date.now() + 10 * 60_000, server: null, cancelled: false }
  browserPending.set(state, entry)
  if (sub.redirectPort) {
    const listen = process.env.HOST || '127.0.0.1'
    entry.server = http.createServer(async (req, res) => {
      if (!localOnly(req, res, port)) return
      const url = new URL(req.url, 'http://router')
      if (url.pathname !== sub.redirectPath) {
        res.writeHead(404)
        return res.end()
      }
      const result = await finishSafely(sub, url)
      res.writeHead(200, { 'content-type': 'text/html' })
      res.end(loginPage(!result?.error, result?.error ?? ''))
    })
    try {
      await new Promise((resolve, reject) => {
        entry.server.once('error', reject)
        entry.server.listen(sub.redirectPort, listen, resolve)
      })
    } catch (error) {
      closeBrowserLogin(entry)
      const message = error.code === 'EADDRINUSE' ? `port ${sub.redirectPort} is busy (another sign-in or app is using it); close it and try again` : `could not start the sign-in: ${error.message}`
      loginErrors.set(sub.id, message)
      return { error: message }
    }
    entry.until = Date.now() + 10 * 60_000
    setTimeout(() => expireBrowserLogin(entry), 10 * 60_000).unref?.()
  }
  return { url, expires: 600 }
}

const expireBrowserLogin = entry => {
  // A completed or superseded sign-in's timer must not poison a newer attempt.
  if (browserPending.get(entry.state) !== entry) return
  loginErrors.set(entry.sub.id, 'the sign-in timed out; run /login again')
  closeBrowserLogin(entry)
}

const closeBrowserLogin = entry => {
  entry.cancelled = true
  if (entry.server) {
    entry.server.close()
    entry.server = null
  }
  if (browserPending.get(entry.state) === entry) browserPending.delete(entry.state)
}

// handleOAuthCallback serves the redirect for providers that ride on the
// router's own port (Claude). Returns true when it handled the path.
export const handleOAuthCallback = async (req, res, pathname) => {
  const url = new URL(req.url, 'http://router')
  for (const entry of browserPending.values()) {
    if (entry.sub.redirectPort || url.pathname !== entry.sub.redirectPath) continue
    const result = await finishSafely(entry.sub, url)
    res.writeHead(200, { 'content-type': 'text/html' })
    res.end(loginPage(!result?.error, result?.error ?? ''))
    return true
  }
  return false
}

// finishSafely never throws: a failed save or a bad reply becomes a login error
// the user can read, instead of an unhandled rejection that kills the router.
const finishSafely = async (sub, url) => {
  try {
    return await finishBrowserLogin(url.searchParams.get('code'), url.searchParams.get('state'))
  } catch (error) {
    const message = `sign-in could not be saved: ${error.message}`
    loginErrors.set(sub.id, message)
    return { error: message }
  }
}

// pendingLoginURL tells the launcher which page to open in the user's browser.
export const pendingLoginURL = () => [...browserPending.values()][0]?.url ?? null

// Manual paste and loopback share the same PKCE exchange and re-entry guard.
export const submitLoginCode = async (sub, input) => {
  const invalid = 'that code did not work; copy it again'
  const entry = [...browserPending.values()].find(entry => entry.sub.id === sub.id)
  if (!entry) {
    return { error: loginError(sub.id) === 'the sign-in timed out; run /login again'
      ? loginError(sub.id)
      : loadSubs()[sub.id]?.accessToken ? `already signed in to ${sub.id}`
        : `no sign-in is waiting for ${sub.id}; run /login ${sub.id} first` }
  }
  if (Date.now() >= entry.until) {
    expireBrowserLogin(entry)
    return { error: loginError(sub.id) }
  }
  if (typeof input !== 'string') return { error: invalid }
  const text = input.trim()
  let code = text, state = null, url
  try { url = new URL(text) } catch {}
  if (url) {
    if (url.searchParams.has('error') || url.searchParams.has('error_description')) {
      return { error: url.searchParams.get('error_description') || url.searchParams.get('error') || invalid }
    }
    code = url.searchParams.get('code')
    state = url.searchParams.get('state')
  } else if (text.includes('#')) {
    const split = text.indexOf('#')
    code = text.slice(0, split)
    state = text.slice(split + 1)
  }
  if (!code || (state !== null && state !== entry.state)) return { error: invalid }
  try {
    const result = await finishBrowserLogin(code, entry.state, true)
    if (result.error && !/already finishing|timed out|cancelled|could not reach/.test(result.error)) {
      loginErrors.set(sub.id, invalid)
      return { error: invalid }
    }
    return { ...result, ...(result.ok && { note: result.note ?? '' }) }
  } catch (error) {
    const message = `sign-in could not be saved: ${error.message}`
    loginErrors.set(sub.id, message)
    return { error: message }
  }
}

const finishBrowserLogin = async (code, state, retry = false) => {
  const entry = browserPending.get(state)
  if (entry && finishingAccounts.get(entry.sub.id) === entry) {
    return { error: 'the sign-in is already finishing' }
  }
  if (entry && Date.now() >= entry.until) {
    expireBrowserLogin(entry)
    return { error: loginError(entry.sub.id) }
  }
  if (!entry || !code || entry.cancelled) {
    if (entry) closeBrowserLogin(entry)
    loginErrors.set(entry?.sub.id ?? 'unknown', 'the sign-in was cancelled')
    return { error: 'the sign-in was cancelled' }
  }
  const sub = entry.sub
  const current = () => !entry.cancelled && Date.now() < entry.until && loginGenerations.get(sub.id) === entry.generation
  const cancelled = { error: 'the sign-in was cancelled' }
  finishingAccounts.set(sub.id, entry)
  let completed = false
  try {
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
    if (data.error || !data.access_token) {
      const message = data.error_description ?? data.error ?? `${sub.name} returned no access token`
      loginErrors.set(sub.id, message)
      return { error: message }
    }
    if (!current()) return cancelled
    loginErrors.delete(sub.id)
    const account = {
      accessToken: data.access_token,
      scopes: data.scope ?? sub.scopes,
      tokenURL: sub.tokenURL,
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
    // Antigravity binds the account to a cloud project right after sign-in.
    const projectId = sub.onboard ? await onboardAntigravity(sub, account, current) : undefined
    if (!current()) return cancelled
    const models = await probeSubModels(sub)
    if (!current()) return cancelled
    const list = models.models?.length ? models.models : (sub.models ?? [])
    const fresh = { ...loadSubs(file), [sub.id]: { ...account, ...(projectId && { projectId }), models: list } }
    saveSubs(file, fresh)
    completed = true
    return { ok: true, models: visibleModels(sub.id, list), note: models.error }
  } finally {
    if (!retry || completed || !current()) closeBrowserLogin(entry)
    if (finishingAccounts.get(sub.id) === entry) finishingAccounts.delete(sub.id)
  }
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

// projectOf reads the cloudaicompanion project id out of a loadCodeAssist or
// onboardUser response: three accepted keys, as a string or an {id} object.
const projectOf = data => {
  for (const key of ['cloudaicompanionProject', 'projectId', 'project']) {
    const value = data?.[key]
    if (typeof value === 'string' && value.trim()) return value.trim()
    if (value && typeof value === 'object' && typeof value.id === 'string' && value.id.trim()) return value.id.trim()
  }
  return ''
}

// onboardAntigravity does the post-sign-in onboarding the Antigravity IDE does:
// loadCodeAssist for an existing project, then onboardUser (polled) when there
// is none yet. Returns the project id, or undefined when it could not be found;
// failures land in loginErrors so /login can say what went wrong.
const onboardAntigravity = async (sub, account, current) => {
  try {
    const load = await fetch(`${sub.apiBase}/v1internal:loadCodeAssist`, {
      method: 'POST',
      headers: { authorization: `Bearer ${account.accessToken}`, 'content-type': 'application/json', 'user-agent': sub.apiUserAgent ?? 'prism-router' },
      body: JSON.stringify({ metadata: { ideType: 'ANTIGRAVITY' } }),
      signal: AbortSignal.timeout(15_000),
    })
    const data = await load.json().catch(() => ({}))
    let project = projectOf(data)
    if (!project) {
      const tier = (Array.isArray(data.allowedTiers) ? data.allowedTiers : []).find(t => t?.isDefault)?.id ?? data.currentTier?.id ?? 'free-tier'
      const body = JSON.stringify({ tier_id: tier, metadata: { ide_type: 'ANTIGRAVITY', ide_version: '2.24.0', ide_name: 'antigravity' } })
      for (let attempt = 0; attempt < 5 && !project; attempt++) {
        const res = await fetch(`${sub.apiBase}/v1internal:onboardUser`, {
          method: 'POST',
          headers: {
            authorization: `Bearer ${account.accessToken}`,
            accept: '*/*',
            'content-type': 'application/json',
            'user-agent': `${sub.apiUserAgent ?? 'prism-router'} google-api-nodejs-client/10.3.0`,
            'x-goog-api-client': 'gl-node/22.21.1',
          },
          body,
          signal: AbortSignal.timeout(30_000),
        })
        if (!res.ok) throw new Error(`onboardUser answered ${res.status}`)
        const polled = await res.json().catch(() => ({}))
        if (polled.done) {
          project = projectOf(polled.response)
          break
        }
        await new Promise(resolve => setTimeout(resolve, 2000))
      }
    }
    if (!project) throw new Error('no project id in loadCodeAssist or onboardUser response')
    const file = subsPath()
    if (!current()) return undefined
    saveSubs(file, { ...loadSubs(file), [sub.id]: { ...loadSubs(file)[sub.id], projectId: project } })
    return project
  } catch (error) {
    loginErrors.set(sub.id, `antigravity onboarding failed: ${error.message}`)
    return undefined
  }
}

// Models a plan lists that are not worth picking: internal helpers, autocomplete
// and preview slots, and retired generations. Lower current tiers stay visible.
const HIDDEN = {
  all: [/embed|whisper|tts|transcri|rerank|moderat|dall-e|image|voice|guard|audio|realtime|search-preview/i],
  chatgpt: [/^codex-auto-review$/, /^gpt-reserve$/, /^gpt-[1-4]/, /^gpt-5(\.[0-5])?-/, /^o\d/],
  claude: [/-20\d{6}$/, /^claude-[0-3]-/, /^claude-(opus|sonnet|haiku)-4-[0-6]$/, /^claude-haiku-4/],
  antigravity: [/^gpt-oss/, /^tab_/, /^chat_\d+/, /lite/, /^gemini-[0-2]\./, /^gemini-3-/, /^gemini-3\.[0-5]-/, /-tiered$/, /-extra-low$/, /-agent$/, /-thinking$/],
  copilot: [/^gpt-3/, /^gpt-4(?!\.1)/, /^o1/, /^text-/, /^copilot-/],
  grok: [/^grok-[0-2]/, /video/i],
}

// visibleModels keeps the models worth showing for an account.
export const visibleModels = (id, list) => {
  const kept = (list ?? []).filter(name => ![...HIDDEN.all, ...(HIDDEN[id] ?? [])].some(rule => rule.test(name)))
  // Antigravity lists its models in no particular order; group them by name.
  return id === 'antigravity' ? kept.sort((a, b) => a.localeCompare(b, 'en', { numeric: true })) : kept
}

// subscriptionProviders turns signed-in accounts into router providers. Their
// routes are paid-marked: they answer when picked by name, never for auto.
export const subscriptionProviders = () =>
  SUBSCRIPTIONS.filter(sub => loadSubs()[sub.id]?.accessToken && !needsGrokConsent(sub.id) && visibleModels(sub.id, loadSubs()[sub.id]?.models).length).map(sub => ({
    name: sub.id,
    baseURL: loadSubs()[sub.id].apiBase ?? sub.apiBase,
    models: visibleModels(sub.id, loadSubs()[sub.id].models).map(name => ({ name, family: name.split('/').pop().split(/[-_.]/)[0], roles: [], vision: false })),
    mark: { subscription: sub.id, paid: true, source: 'subscription' },
  }))

// --- device-code sign-in ---------------------------------------------------------------------

const pending = new Map()

export const startLogin = async sub => {
  const generation = cancelLogin(sub.id)
  loginErrors.delete(sub.id)
  // OIDC discovery and form-encoded device grants:
  // adapted from router-for-me/CLIProxyAPI (MIT)
  let deviceURL = sub.deviceURL, tokenURL = sub.tokenURL
  if (sub.id === 'grok' && sub.discoveryURL) {
    try {
      const discovery = await fetch(sub.discoveryURL, { signal: AbortSignal.timeout(10_000) })
      if (discovery.ok) {
        const data = await discovery.json()
        deviceURL = data.device_authorization_endpoint ?? deviceURL
        tokenURL = data.token_endpoint ?? tokenURL
      }
    } catch { /* Configured endpoints remain a fallback if discovery is down. */ }
  }
  const params = { client_id: sub.clientID, ...(sub.scopes && { scope: sub.scopes }) }
  let res
  try {
    res = await fetch(deviceURL, {
      method: 'POST',
      headers: { 'content-type': sub.id === 'grok' ? 'application/x-www-form-urlencoded' : 'application/json', accept: 'application/json', 'user-agent': 'prism-router' },
      body: sub.id === 'grok' ? new URLSearchParams(params) : JSON.stringify(params),
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
  if (sub.id === 'grok' && (!data.device_code || !data.user_code || !(data.verification_uri_complete ?? data.verification_uri))) return { error: `${sub.name} returned an incomplete device code` }
  if (loginGenerations.get(sub.id) !== generation) return { error: 'the sign-in was cancelled' }
  const expires = sub.id === 'grok' ? Math.min(data.expires_in ?? 1800, 1800) : data.expires_in ?? 900
  pending.set(sub.id, { generation, tokenURL, deviceCode: data.device_code, interval: data.interval ?? 5, until: Date.now() + expires * 1000, timer: null })
  return { userCode: data.user_code, verificationURI: data.verification_uri_complete ?? data.verification_uri, expires }
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
  const params = { client_id: sub.clientID, device_code: state.deviceCode, grant_type: 'urn:ietf:params:oauth:grant-type:device_code' }
  let res
  try {
    res = await fetch(state.tokenURL ?? sub.tokenURL, {
      method: 'POST',
      headers: { 'content-type': sub.id === 'grok' ? 'application/x-www-form-urlencoded' : 'application/json', accept: 'application/json', 'user-agent': 'prism-router' },
      body: sub.id === 'grok' ? new URLSearchParams(params) : JSON.stringify(params),
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
  if (loginGenerations.get(sub.id) !== state.generation) return { status: 'error', message: 'the sign-in was cancelled' }
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
  subs[sub.id] = {
    accessToken: data.access_token, scopes: data.scope ?? sub.scopes, models: [],
    ...(sub.id === 'grok' && { refreshToken: data.refresh_token, expiresAt: Date.now() + (data.expires_in ?? 3600) * 1000, tokenURL: state.tokenURL, sessionId: randomUUID() }),
  }
  saveSubs(file, subs)
  const models = await probeSubModels(sub)
  if (loginGenerations.get(sub.id) !== state.generation) return { status: 'error', message: 'the sign-in was cancelled' }
  const list = models.models?.length ? models.models : (sub.models ?? [])
  const visible = visibleModels(sub.id, list)
  // Re-read before saving: a sign-in or sign-out for another account during the
  // probe must not be lost.
  const note = !visible.length
    ? `signed in, but ${sub.name} did not list its models; run /login ${sub.id} again`
    : models.error
  if (note && !visible.length) loginErrors.set(sub.id, note)
  else loginErrors.delete(sub.id)
  const fresh = { ...loadSubs(file), [sub.id]: { ...loadSubs(file)[sub.id], models: list, ...(note && !visible.length && { loginError: note }) } }
  saveSubs(file, fresh)
  return { status: 'ok', models: visibleModels(sub.id, list), note }
}

const probeSubModels = async sub => {
  try {
    const { token } = await subToken(sub)
    const stored = loadSubs()[sub.id] ?? {}
    // Antigravity lists models the way its IDE does: a POST to fetchAvailableModels
    // scoped to the onboarding project, with the model ids as the keys of `models`.
    if (sub.wire === 'antigravity') {
      const res = await fetch(`${sub.apiBase}/v1internal:fetchAvailableModels`, {
        method: 'POST',
        headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json', 'user-agent': sub.apiUserAgent ?? 'prism-router' },
        body: JSON.stringify({ project: stored.projectId ?? '' }),
        signal: AbortSignal.timeout(10_000),
      })
      if (!res.ok) return { error: `the service answered ${res.status}` }
      const data = await res.json()
      const ids = Object.keys(data.models ?? {})
        .map(id => id.trim())
        .filter(Boolean)
      const chat = ids.filter(id => !/embed|whisper|tts|transcri|rerank|moderat|dall-e|image|voice|guard/i.test(id))
      return chat.length ? { models: [...new Set(chat)].slice(0, 300) } : { error: 'the service listed no chat models' }
    }
    const res = await (sub.id === 'grok' ? grokFetch : fetch)(`${sub.apiBase}/models${sub.modelsQuery ? `?${new URLSearchParams(sub.modelsQuery)}` : ''}`, {
      headers: {
        authorization: `Bearer ${token}`,
        accept: 'application/json',
        'user-agent': sub.apiUserAgent ?? 'prism-router',
        ...(sub.apiHeaders ?? {}),
        ...(sub.id === 'grok' && { ...grokRequestHeaders({}, stored.sessionId), accept: 'application/json' }),
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
    const chat = ids.filter(id => !/embed|whisper|tts|transcri|rerank|moderat|dall-e|image|video|voice|guard/i.test(id))
    return chat.length ? { models: [...new Set(chat)].slice(0, 300) } : { error: 'the service listed no chat models' }
  } catch (error) {
    return { error: error.message }
  }
}

export const signOut = sub => {
  cancelLogin(sub.id)
  const file = subsPath()
  const subs = { ...loadSubs(file) }
  delete subs[sub.id]
  apiTokens.delete(sub.id)
  loginErrors.delete(sub.id)
  saveSubs(file, subs)
}
