// Wire adapters: Prism's agent speaks OpenAI chat completions, but subscription
// backends speak Responses (ChatGPT/Codex), Anthropic Messages, or Gemini
// generateContent. Each adapter converts a chat request one way and its SSE
// stream back into chat.completion.chunk events.
import { randomUUID, createHash } from 'node:crypto'

const decode = new TextDecoder()

const text = value => String(value ?? '')

// Gemini signs its tool calls; the signature has to come back with the call or
// the next turn is refused. Prism carries it inside the tool call id as
// "<id>~<signature>", and every other wire sends just the id part.
const callId = id => text(id).split('~')[0]
const callSignature = id => text(id).split('~').slice(1).join('~')

// --- ChatGPT (Codex / Responses API) ----------------------------------------------

export const toCodex = body => {
  let instructions
  const input = []
  for (const m of body.messages ?? []) {
    if (m.role === 'system') {
      instructions = [instructions, text(m.content)].filter(Boolean).join('\n\n')
      continue
    }
    if (m.role === 'user') {
      input.push({ role: 'user', content: [{ type: 'input_text', text: text(m.content) }] })
    } else if (m.role === 'assistant') {
      if (text(m.content)) input.push({ role: 'assistant', content: [{ type: 'output_text', text: text(m.content) }] })
      for (const call of m.tool_calls ?? []) input.push({ type: 'function_call', call_id: callId(call.id), name: call.function?.name, arguments: text(call.function?.arguments) })
    } else if (m.role === 'tool') {
      input.push({ type: 'function_call_output', call_id: callId(m.tool_call_id), output: text(m.content) })
    }
  }
  // Codex models always reason: without a reasoning block the backend returns
  // 200 with an empty stream instead of an error.
  const out = {
    model: body.model,
    input,
    stream: true,
    store: false,
    include: ['reasoning.encrypted_content'],
    reasoning: { effort: 'medium', summary: 'auto' },
  }
  if (instructions) out.instructions = instructions
  if (body.tools?.length) out.tools = body.tools.map(t => ({ type: 'function', name: t.function?.name, description: t.function?.description, parameters: t.function?.parameters, strict: false }))
  if (body.parallel_tool_calls !== undefined) out.parallel_tool_calls = body.parallel_tool_calls
  return out
}

// Codex SSE events become chat.completion.chunk SSE lines.
export class CodexStream {
  constructor() {
    this.buffer = ''
    this.calls = 0
    this.done = false
  }

  write(chunk) {
    this.buffer += typeof chunk === 'string' ? chunk : decode.decode(chunk)
    const out = []
    let index
    while ((index = this.buffer.indexOf('\n')) >= 0) {
      const line = this.buffer.slice(0, index).trim()
      this.buffer = this.buffer.slice(index + 1)
      if (!line.startsWith('data:')) continue
      const payload = line.slice(5).trim()
      if (!payload || payload === '[DONE]') continue
      let event
      try {
        event = JSON.parse(payload)
      } catch {
        continue
      }
      const push = delta => out.push(`data: ${JSON.stringify({ id: 'chatcmpl-codex', object: 'chat.completion.chunk', choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`)
      if (event.type === 'response.output_text.delta' && event.delta) push({ content: event.delta })
      else if (event.type === 'response.output_item.done' && event.item?.type === 'function_call') {
        push({ tool_calls: [{ index: this.calls++, id: event.item.call_id, type: 'function', function: { name: event.item.name, arguments: event.item.arguments ?? '' } }] })
      } else if (event.type === 'response.completed' || event.type === 'response.failed' || event.type === 'error') {
        const failed = event.type !== 'response.completed'
        const usage = event.response?.usage
          ? { prompt_tokens: event.response.usage.input_tokens ?? 0, completion_tokens: event.response.usage.output_tokens ?? 0 }
          : undefined
        out.push(`data: ${JSON.stringify({ id: 'chatcmpl-codex', object: 'chat.completion.chunk', choices: [{ index: 0, delta: {}, finish_reason: failed ? 'stop' : this.calls ? 'tool_calls' : 'stop' }], ...(usage && { usage }) })}\n\n`)
        out.push('data: [DONE]\n\n')
        this.done = true
      }
    }
    return out.join('')
  }
}

// --- Claude (Anthropic Messages API) ----------------------------------------------

export const toAnthropic = body => {
  const system = []
  const messages = []
  for (const m of body.messages ?? []) {
    if (m.role === 'system') {
      system.push(text(m.content))
      continue
    }
    if (m.role === 'user') {
      messages.push({ role: 'user', content: text(m.content) })
    } else if (m.role === 'assistant') {
      const content = []
      if (text(m.content)) content.push({ type: 'text', text: text(m.content) })
      for (const call of m.tool_calls ?? []) {
        let input = {}
        try {
          input = JSON.parse(text(call.function?.arguments) || '{}')
        } catch {}
        content.push({ type: 'tool_use', id: callId(call.id), name: call.function?.name, input })
      }
      messages.push({ role: 'assistant', content })
    } else if (m.role === 'tool') {
      messages.push({ role: 'user', content: [{ type: 'tool_result', tool_use_id: callId(m.tool_call_id), content: text(m.content) }] })
    }
  }
  const out = { model: body.model, max_tokens: body.max_tokens ?? 16384, messages, stream: true }
  // The Claude Code identity block first: Anthropic rejects OAuth requests to
  // the Messages API that do not carry it (a bare 429 with a valid token).
  out.system = [{ type: 'text', text: "You are Claude Code, Anthropic's official CLI for Claude." }, ...system.map(text => ({ type: 'text', text }))]
  if (!system.length) out.system = [out.system[0]]
  if (body.tools?.length) out.tools = body.tools.map(t => ({ name: t.function?.name, description: t.function?.description, input_schema: t.function?.parameters }))
  return out
}

export class AnthropicStream {
  constructor() {
    this.buffer = ''
    this.calls = 0
    this.done = false
  }

  write(chunk) {
    this.buffer += typeof chunk === 'string' ? chunk : decode.decode(chunk)
    const out = []
    let index
    while ((index = this.buffer.indexOf('\n')) >= 0) {
      const line = this.buffer.slice(0, index).trim()
      this.buffer = this.buffer.slice(index + 1)
      if (!line.startsWith('data:')) continue
      const payload = line.slice(5).trim()
      if (!payload || payload === '[DONE]') continue
      let event
      try {
        event = JSON.parse(payload)
      } catch {
        continue
      }
      const push = delta => out.push(`data: ${JSON.stringify({ id: 'chatcmpl-claude', object: 'chat.completion.chunk', choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`)
      if (event.type === 'content_block_delta' && event.delta?.type === 'text_delta') push({ content: event.delta.text })
      else if (event.type === 'content_block_start' && event.content_block?.type === 'tool_use') {
        push({ tool_calls: [{ index: this.calls++, id: event.content_block.id, type: 'function', function: { name: event.content_block.name, arguments: '' } }] })
      } else if (event.type === 'content_block_delta' && event.delta?.type === 'input_json_delta') {
        push({ tool_calls: [{ index: max(0, this.calls - 1), function: { arguments: event.delta.partial_json ?? '' } }] })
      } else if (event.type === 'message_delta') {
        const reason = event.delta?.stop_reason === 'tool_use' ? 'tool_calls' : 'stop'
        const usage = event.usage ? { prompt_tokens: event.usage.input_tokens ?? 0, completion_tokens: event.usage.output_tokens ?? 0 } : undefined
        out.push(`data: ${JSON.stringify({ id: 'chatcmpl-claude', object: 'chat.completion.chunk', choices: [{ index: 0, delta: {}, finish_reason: reason }], ...(usage && { usage }) })}\n\n`)
        out.push('data: [DONE]\n\n')
        this.done = true
      }
    }
    return out.join('')
  }
}

// --- Gemini (Code Assist API) ----------------------------------------------------

export const toGemini = body => {
  const contents = []
  let system
  const names = new Map()
  for (const m of body.messages ?? []) {
    if (m.role === 'system') {
      system = [system, text(m.content)].filter(Boolean).join('\n\n')
      continue
    }
    if (m.role === 'user') {
      contents.push({ role: 'user', parts: [{ text: text(m.content) }] })
    } else if (m.role === 'assistant') {
      const parts = []
      if (text(m.content)) parts.push({ text: text(m.content) })
      for (const call of m.tool_calls ?? []) {
        names.set(call.id, call.function?.name)
        let args = {}
        try {
          args = JSON.parse(text(call.function?.arguments) || '{}')
        } catch {}
        parts.push({ functionCall: { id: callId(call.id), name: call.function?.name, args }, thoughtSignature: callSignature(call.id) || 'skip_thought_signature_validator' })
      }
      contents.push({ role: 'model', parts })
    } else if (m.role === 'tool') {
      const part = { functionResponse: { id: callId(m.tool_call_id), name: names.get(m.tool_call_id) ?? callId(m.tool_call_id), response: { result: text(m.content) } } }
      // Answers to parallel calls travel together in one turn.
      const last = contents[contents.length - 1]
      if (last?.role === 'user' && last.parts.every(p => p.functionResponse)) last.parts.push(part)
      else contents.push({ role: 'user', parts: [part] })
    }
  }
  const out = { contents }
  if (system) out.systemInstruction = { parts: [{ text: system }] }
  if (body.tools?.length) {
    const declarations = body.tools.map(t => ({ name: t.function?.name, description: t.function?.description, parameters: t.function?.parameters }))
    out.tools = [{ functionDeclarations: declarations }]
  }
  return out
}

export class GeminiStream {
  constructor() {
    this.buffer = ''
    this.calls = 0
    this.done = false
  }

  write(chunk) {
    this.buffer += typeof chunk === 'string' ? chunk : decode.decode(chunk)
    const out = []
    let index
    while ((index = this.buffer.indexOf('\n')) >= 0) {
      const line = this.buffer.slice(0, index).trim()
      this.buffer = this.buffer.slice(index + 1)
      if (!line.startsWith('data:')) continue
      const payload = line.slice(5).trim()
      if (!payload || payload === '[DONE]') continue
      let event
      try {
        event = JSON.parse(payload)
      } catch {
        continue
      }
      const push = delta => out.push(`data: ${JSON.stringify({ id: 'chatcmpl-gemini', object: 'chat.completion.chunk', choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`)
      const candidate = event.candidates?.[0] ?? {}
      for (const part of candidate.content?.parts ?? []) {
        // Thinking parts are the model's private reasoning, not the reply.
        if (part.text && part.thought) push({ reasoning_content: part.text })
        else if (part.text) push({ content: part.text })
        if (part.functionCall) {
          const id = part.functionCall.id || `call_${randomUUID().slice(0, 8)}`
          push({ tool_calls: [{ index: this.calls++, id: part.thoughtSignature ? `${id}~${part.thoughtSignature}` : id, type: 'function', function: { name: part.functionCall.name, arguments: JSON.stringify(part.functionCall.args ?? {}) } }] })
        }
      }
      if (event.usageMetadata) this.usage = event.usageMetadata
      // Usage rides on every chunk; only finishReason marks the end of the reply.
      if (candidate.finishReason) out.push(this.finish())
    }
    return out.join('')
  }

  finish() {
    if (this.done) return ''
    const usage = this.usage ? { prompt_tokens: this.usage.promptTokenCount ?? 0, completion_tokens: this.usage.candidatesTokenCount ?? 0 } : undefined
    this.done = true
    return `data: ${JSON.stringify({ id: 'chatcmpl-gemini', object: 'chat.completion.chunk', choices: [{ index: 0, delta: {}, finish_reason: this.calls ? 'tool_calls' : 'stop' }], ...(usage && { usage }) })}\n\ndata: [DONE]\n\n`
  }
}

// --- Antigravity (Google Code Assist, Gemini envelope) ---------------------------

// Antigravity wraps the Gemini body in the project-scoped envelope its IDE sends
// (request.go geminiToAntigravity): {model, project, requestType, requestId,
// userAgent, request: <gemini body>}, with a stable session id on the request.
export const toAntigravity = (body, project) => {
  const request = toGemini(body)
  request.sessionId = stableSessionId(request)
  return {
    model: body.model,
    ...(project && { project }),
    requestType: 'agent',
    requestId: `agent-${randomUUID()}`,
    userAgent: 'antigravity',
    request,
  }
}

// stableSessionId derives the conversation session from the first user turn, the
// way the reference does: a hash of that text, as a negative decimal string.
const stableSessionId = request => {
  for (const content of request.contents ?? []) {
    if (content.role !== 'user') continue
    const text = content.parts?.[0]?.text
    if (!text) continue
    const hash = createHash('sha256').update(text).digest()
    return `-${BigInt(`0x${hash.subarray(0, 8).toString('hex')}`) & 0x7fffffffffffffffn}`
  }
  return `-${Math.floor(Math.random() * 9_000_000_000_000_000_000)}`
}

// Antigravity SSE chunks wrap the Gemini payload in a `response` field (with a
// traceId beside it); unwrap each chunk and hand it to the Gemini stream logic.
export class AntigravityStream {
  constructor() {
    this.inner = new GeminiStream()
    this.buffer = ''
  }

  get done() {
    return this.inner.done
  }

  finish() {
    return this.inner.finish()
  }

  write(chunk) {
    this.buffer += typeof chunk === 'string' ? chunk : decode.decode(chunk)
    const out = []
    let index
    while ((index = this.buffer.indexOf('\n')) >= 0) {
      const line = this.buffer.slice(0, index).trim()
      this.buffer = this.buffer.slice(index + 1)
      if (!line.startsWith('data:')) continue
      const payload = line.slice(5).trim()
      if (!payload || payload === '[DONE]') continue
      let event
      try {
        event = JSON.parse(payload)
      } catch {
        continue
      }
      // An error chunk ends the reply as an error the agent can show, not as an
      // empty answer.
      if (event?.error) {
        this.inner.done = true
        out.push(`data: ${JSON.stringify({ error: { message: `antigravity: ${event.error.message ?? 'request failed'}` } })}\n\ndata: [DONE]\n\n`)
        continue
      }
      const inner = event && typeof event.response === 'object' && event.response ? event.response : event
      out.push(this.inner.write(`data: ${JSON.stringify(inner)}\n\n`))
    }
    return out.join('')
  }
}

const max = (a, b) => (a > b ? a : b)

export const wireFor = name => ({ codex: { to: toCodex, stream: CodexStream }, anthropic: { to: toAnthropic, stream: AnthropicStream }, gemini: { to: toGemini, stream: GeminiStream }, antigravity: { to: toAntigravity, stream: AntigravityStream } }[name] ?? null)
