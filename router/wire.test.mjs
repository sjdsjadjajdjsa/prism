import { test } from 'node:test'
import assert from 'node:assert/strict'
import { toCodex, CodexStream, toAnthropic, AnthropicStream, toGemini, GeminiStream, wireFor } from './wire.mjs'

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
  const streamed = s.write('data: {"candidates":[{"content":{"parts":[{"text":"hey"},{"functionCall":{"name":"read","args":{"path":"a.go"}}}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":6}}\n\n')
  const lines = streamed.split('\n\n').filter(l => l && l !== 'data: [DONE]')
  const deltas = lines.map(l => JSON.parse(l.slice(6)).choices[0].delta)
  assert.equal(deltas[0].content, 'hey')
  assert.equal(deltas[1].tool_calls[0].function.name, 'read')
  assert.equal(JSON.parse(lines[2].slice(6)).choices[0].finish_reason, 'tool_calls')
  assert.equal(s.done, true)
})

test('wireFor maps the names', () => {
  assert.equal(wireFor('codex').to, toCodex)
  assert.equal(wireFor('anthropic').to, toAnthropic)
  assert.equal(wireFor('gemini').to, toGemini)
  assert.equal(wireFor('nope'), null)
})
