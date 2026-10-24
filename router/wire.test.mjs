import { test } from 'node:test'
import assert from 'node:assert/strict'
import { toCodex, CodexStream, toGrok, createGrokFetch, toAnthropic, AnthropicStream, toGemini, GeminiStream, toAntigravity, AntigravityStream, wireFor } from './wire.mjs'

const chat = {
  model: 'm',
  max_tokens: 1000,
  messages: [
    { role: 'system', content: 'be nice' },
    { role: 'user', content: 'hello' },
    { role: 'assistant', content: 'hi', tool_calls: [{ id: 'c1', type: 'function', function: { name: 'read', arguments: '{"path":"a.go"}' } }] },
    { role: 'tool', tool_call_id: 'c1', content: 'the file' },
  ],
  tools: [{ type: 'function', function: { name: 'read', description: 'read a file', parameters: { type: 'object' } } }],
}

test('chat becomes a Codex responses request', () => {
  const out = toCodex(chat)
  assert.equal(out.store, false)
  assert.deepEqual(out.reasoning, { effort: 'medium', summary: 'auto' })
  assert.equal(out.instructions, 'be nice')
  assert.ok(out.include.includes('reasoning.encrypted_content'))
  assert.deepEqual(out.tools, [{ type: 'function', name: 'read', description: 'read a file', parameters: { type: 'object' }, strict: false }])
  const types = out.input.map(i => i.type ?? `${i.role}:${i.content?.[0]?.type}`)
  assert.deepEqual(types, ['user:input_text', 'assistant:output_text', 'function_call', 'function_call_output'])
  assert.equal(out.input[2].call_id, 'c1')
  assert.equal(out.input[3].output, 'the file')
})

test('codex stream becomes chat chunks', () => {
  const s = new CodexStream()
  const out = s.write('data: {"type":"response.output_text.delta","delta":"hi"}\n\ndata: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"c1","name":"read","arguments":"{}"}}\n\ndata: {"type":"response.completed","response":{"usage":{"input_tokens":3,"output_tokens":4}}}\n\n')
  const lines = out.split('\n\n').filter(l => l && l !== 'data: [DONE]')
  assert.equal(lines.length, 3)
  const deltas = lines.map(l => JSON.parse(l.slice(6)).choices[0].delta)
  assert.equal(deltas[0].content, 'hi')
  assert.equal(deltas[1].tool_calls[0].id, 'c1')
  assert.equal(JSON.parse(lines[2].slice(6)).choices[0].finish_reason, 'tool_calls')
  assert.ok(out.includes('data: [DONE]'))
  assert.equal(s.done, true)
})

test('Grok reuses Responses input and tool calls without Codex-only fields', () => {
  const out = toGrok({ ...chat, temperature: 0.4, tool_choice: { type: 'function', function: { name: 'read' } } })
  assert.deepEqual(out.input, toCodex(chat).input)
  assert.deepEqual(out.tools, toCodex(chat).tools)
  assert.equal(out.instructions, 'be nice')
  assert.equal(out.max_output_tokens, 1000)
  assert.equal(out.temperature, 0.4)
  assert.deepEqual(out.tool_choice, { type: 'function', name: 'read' })
  assert.equal(out.reasoning, undefined)
  assert.equal(out.include, undefined)
  const s = new (wireFor('grok').stream)()
  const outStream = s.write('data: {"type":"response.output_text.delta","delta":"hi"}\n\ndata: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"c1","name":"read","arguments":"{}"}}\n\ndata: {"type":"response.incomplete","response":{"usage":{"input_tokens":3,"output_tokens":4}}}\n\n')
  const chunks = outStream.split('\n\n').filter(l => l && l !== 'data: [DONE]').map(l => JSON.parse(l.slice(6)))
  assert.equal(chunks[0].choices[0].delta.content, 'hi')
  assert.deepEqual(chunks[1].choices[0].delta.tool_calls[0].function, { name: 'read', arguments: '{}' })
  assert.equal(chunks[1].choices[0].delta.tool_calls[0].id, 'c1')
  assert.equal(chunks[2].choices[0].finish_reason, 'length')
  assert.deepEqual(chunks[2].usage, { prompt_tokens: 3, completion_tokens: 4 })
  assert.equal(s.done, true)
})

test('Grok version lookup caches for 3h, coalesces lookups and refreshes once on 426', async () => {
  let now = 0, lookups = 0, posts = 0
  const versions = []
  const grokFetch = createGrokFetch({ now: () => now, fetcher: async (url, options) => {
    if (url === 'https://registry.npmjs.org/@xai-official/grok/latest') {
      lookups++
      return Response.json({ version: `1.0.${46 + lookups}` })
    }
    versions.push(new Headers(options.headers).get('x-grok-client-version'))
    assert.equal(new Headers(options.headers).get('user-agent'), `xai-grok-workspace/${versions.at(-1)}`)
    return new Response('', { status: ++posts === 3 ? 426 : 200 })
  } })
  await Promise.all([grokFetch('https://fake/responses', {}), grokFetch('https://fake/models', {})])
  assert.equal(lookups, 1)
  await grokFetch('https://fake/responses', {})
  assert.equal(lookups, 2)
  assert.deepEqual(versions, ['1.0.47', '1.0.47', '1.0.47', '1.0.48'])
  now += 3 * 60 * 60_000
  await grokFetch('https://fake/models', {})
  assert.equal(lookups, 3)
})

test('Grok version resolution falls back and never retries a 426 more than once', async () => {
  let lookups = 0, posts = 0
  const grokFetch = createGrokFetch({ fetcher: async (url, options) => {
    if (url.includes('registry.npmjs.org')) { lookups++; throw new Error('offline') }
    assert.equal(new Headers(options.headers).get('x-grok-client-version'), '1.0.46')
    posts++
    return new Response('', { status: 426 })
  } })
  assert.equal((await grokFetch('https://fake/responses', {})).status, 426)
  assert.equal(posts, 2)
  assert.equal(lookups, 2)
})

test('chat becomes an Anthropic messages request', () => {
  const out = toAnthropic(chat)
  assert.equal(out.system[0].text, "You are Claude Code, Anthropic's official CLI for Claude.")
  assert.equal(out.system[1].text, 'be nice')
  assert.equal(out.max_tokens, 1000)
  assert.deepEqual(out.tools, [{ name: 'read', description: 'read a file', input_schema: { type: 'object' } }])
  assert.equal(out.messages[0].role, 'user')
  assert.equal(out.messages[1].content[1].type, 'tool_use')
  assert.equal(out.messages[1].content[1].input.path, 'a.go')
  assert.equal(out.messages[2].content[0].tool_use_id, 'c1')
})

test('anthropic stream becomes chat chunks', () => {
  const s = new AnthropicStream()
  const out = s.write('data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"yo"}}\n\ndata: {"type":"content_block_start","content_block":{"type":"tool_use","id":"t1","name":"read"}}\n\ndata: {"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\\"a\\":"}}\n\ndata: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":1,"output_tokens":2}}\n\n')
  const lines = out.split('\n\n').filter(l => l && l !== 'data: [DONE]')
  const deltas = lines.map(l => JSON.parse(l.slice(6)).choices[0].delta)
  assert.equal(deltas[0].content, 'yo')
  assert.equal(deltas[1].tool_calls[0].id, 't1')
  assert.equal(deltas[2].tool_calls[0].function.arguments, '{"a":')
  assert.equal(JSON.parse(lines[3].slice(6)).choices[0].finish_reason, 'tool_calls')
  assert.equal(s.done, true)
})

test('chat becomes a Gemini request and back', () => {
  const out = toGemini(chat)
  assert.deepEqual(out.systemInstruction, { parts: [{ text: 'be nice' }] })
  assert.equal(out.tools[0].functionDeclarations[0].name, 'read')
  assert.equal(out.contents[1].parts[1].functionCall.name, 'read')
  assert.equal(out.contents[2].parts[0].functionResponse.name, 'read')
  const s = new GeminiStream()
  const streamed = s.write('data: {"candidates":[{"content":{"parts":[{"text":"hey"},{"functionCall":{"name":"read","args":{"path":"a.go"}},"thoughtSignature":"SIG/1+x="}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":6}}\n\n')
  const lines = streamed.split('\n\n').filter(l => l && l !== 'data: [DONE]')
  const deltas = lines.map(l => JSON.parse(l.slice(6)).choices[0].delta)
  assert.equal(deltas[0].content, 'hey')
  assert.equal(deltas[1].tool_calls[0].function.name, 'read')
  assert.match(deltas[1].tool_calls[0].id, /~SIG\/1\+x=$/)
  assert.equal(JSON.parse(lines[2].slice(6)).choices[0].finish_reason, 'tool_calls')
  assert.equal(s.done, true)
})

test('a Gemini tool call signature survives the round trip and stays off other wires', () => {
  const id = 'call_ab12~SIG/1+x='
  const history = {
    model: 'm',
    messages: [
      { role: 'user', content: 'go' },
      { role: 'assistant', content: '', tool_calls: [{ id, type: 'function', function: { name: 'read', arguments: '{}' } }, { id: 'call_cd34', type: 'function', function: { name: 'grep', arguments: '{}' } }] },
      { role: 'tool', tool_call_id: id, content: 'a' },
      { role: 'tool', tool_call_id: 'call_cd34', content: 'b' },
    ],
  }
  const g = toGemini(history)
  assert.deepEqual(g.contents[1].parts[0], { functionCall: { id: 'call_ab12', name: 'read', args: {} }, thoughtSignature: 'SIG/1+x=' })
  assert.equal(g.contents[1].parts[1].thoughtSignature, 'skip_thought_signature_validator')
  assert.equal(g.contents.length, 3, 'parallel answers share one turn')
  assert.deepEqual(g.contents[2].parts.map(p => p.functionResponse.id), ['call_ab12', 'call_cd34'])
  assert.equal(toAnthropic(history).messages[1].content[0].id, 'call_ab12')
  assert.equal(toAnthropic(history).messages[2].content[0].tool_use_id, 'call_ab12')
  assert.equal(toCodex(history).input.find(i => i.type === 'function_call').call_id, 'call_ab12')
})

test('Grok OAuth headers keep the session and conversation stable, not request ids', async () => {
  const { grokRequestHeaders } = await import('./wire.mjs')
  const first = grokRequestHeaders(chat, 'account-session')
  const next = grokRequestHeaders({ ...chat, messages: [...chat.messages, { role: 'user', content: 'next' }] }, 'account-session')
  assert.equal(first.accept, 'text/event-stream')
  assert.equal(first['x-grok-session-id'], 'account-session')
  assert.equal(first['x-grok-model-override'], 'm')
  assert.ok(first['x-grok-agent-id'])
  assert.match(first['x-grok-req-id'], /^[0-9a-f-]{36}$/)
  assert.match(first['x-grok-conv-id'], /^[0-9a-f-]{36}$/)
  assert.notEqual(first['x-grok-req-id'], next['x-grok-req-id'])
  assert.equal(first['x-grok-conv-id'], next['x-grok-conv-id'])
  assert.notEqual(first['x-grok-conv-id'], grokRequestHeaders(chat, 'another-account')['x-grok-conv-id'])
  assert.notEqual(first['x-grok-conv-id'], grokRequestHeaders({ messages: [{ role: 'user', content: 'different' }] }, 'account-session')['x-grok-conv-id'])
})

test('wireFor maps the names', () => {
  assert.equal(wireFor('codex').to, toCodex)
  assert.equal(wireFor('anthropic').to, toAnthropic)
  assert.equal(wireFor('gemini').to, toGemini)
  assert.equal(wireFor('antigravity').to, toAntigravity)
  assert.equal(wireFor('nope'), null)
})

test('chat becomes an Antigravity envelope around the Gemini body', () => {
  const out = toAntigravity(chat, 'proj-9')
  assert.equal(out.model, 'm')
  assert.equal(out.project, 'proj-9')
  assert.equal(out.requestType, 'agent')
  assert.equal(out.userAgent, 'antigravity')
  assert.match(out.requestId, /^agent-/)
  // The Gemini body rides under `request`, with a stable session id.
  assert.deepEqual(out.request.systemInstruction, { parts: [{ text: 'be nice' }] })
  assert.equal(out.request.tools[0].functionDeclarations[0].name, 'read')
  assert.equal(out.request.contents[1].parts[1].functionCall.name, 'read')
  assert.match(out.request.sessionId, /^-\d+$/)
  // The same first user turn derives the same session id.
  assert.equal(out.request.sessionId, toAntigravity(chat, 'proj-9').request.sessionId)
  // Without a project the field is omitted entirely.
  assert.equal('project' in toAntigravity(chat), false)
})

test('antigravity stream unwraps the response field around each chunk', () => {
  const s = new AntigravityStream()
  const out = s.write(
    'data: {"response":{"candidates":[{"content":{"parts":[{"text":"hey"}]}}]},"traceId":"t1"}\n\n' +
      'data: {"response":{"candidates":[{"content":{"parts":[{"functionCall":{"name":"read","args":{"path":"a.go"}}}]}}]},"traceId":"t2"}\n\n' +
      'data: {"response":{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":6}},"traceId":"t3"}\n\n',
  )
  const lines = out.split('\n\n').filter(l => l && l !== 'data: [DONE]')
  const deltas = lines.map(l => JSON.parse(l.slice(6)).choices[0].delta)
  assert.equal(deltas[0].content, 'hey')
  assert.equal(deltas[1].tool_calls[0].function.name, 'read')
  assert.equal(JSON.parse(lines[2].slice(6)).choices[0].finish_reason, 'tool_calls')
  assert.ok(out.includes('data: [DONE]'))
  assert.equal(s.done, true)
  // A bare Gemini chunk (no wrapper) still parses.
  const bare = new AntigravityStream()
  const plain = bare.write('data: {"candidates":[{"content":{"parts":[{"text":"raw"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}\n\n')
  assert.match(plain, /"content":"raw"/)
  assert.equal(bare.done, true)
})
