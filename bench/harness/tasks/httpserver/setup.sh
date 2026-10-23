cat > server.js <<'EOF'
'use strict';
/*
 * Tiny in-memory items API.
 *
 * Existing:
 *   GET  /items        -> 200 JSON array of all items
 *   POST /items        -> 201 JSON item; body {"name": string, "tags": string[] (optional)}
 *                         400 {"error": ...} for invalid JSON, missing/empty name or bad tags
 *   GET  /items/:id    -> 200 item | 404 {"error":"not found"}
 *   anything else      -> 404 {"error":"not found"}
 *
 * TO ADD:
 *   GET  /items?tag=T&limit=L&offset=O
 *        tag    keeps only items whose tags include T (exact match)
 *        limit  default 20, integer 1..100      offset  default 0, integer >= 0
 *        items stay ordered by id; response header X-Total-Count = number of items
 *        matching the tag filter BEFORE limit/offset are applied.
 *        a malformed limit/offset (not a plain base-10 integer, or out of range)
 *        -> 400 {"error": ...}
 *   DELETE /items/:id -> 204 with an empty body | 404 {"error":"not found"}
 *        ids are never reused after a delete.
 *   GET  /stats       -> 200 {"count": <number of items>, "tags": {"<tag>": <items having it>}}
 *   A known path used with a method it does not support (e.g. PUT /items,
 *   POST /stats, POST /items/1) -> 405 {"error":"method not allowed"} with an
 *   Allow header listing the supported methods, comma separated, e.g. "GET, POST".
 *   (/items allows GET, POST; /items/:id allows GET, DELETE; /stats allows GET.)
 */
const http = require('http');

function createServer() {
  const items = [];
  let nextId = 1;

  function send(res, status, body, headers) {
    const payload = body === undefined ? '' : JSON.stringify(body);
    res.writeHead(status, Object.assign({ 'Content-Type': 'application/json' }, headers || {}));
    res.end(payload);
  }

  function readBody(req) {
    return new Promise((resolve) => {
      let data = '';
      req.on('data', (c) => { data += c; });
      req.on('end', () => resolve(data));
    });
  }

  const server = http.createServer(async (req, res) => {
    const url = new URL(req.url, 'http://localhost');
    const path = url.pathname;

    if (path === '/items' && req.method === 'GET') {
      return send(res, 200, items);
    }

    if (path === '/items' && req.method === 'POST') {
      let body;
      try {
        body = JSON.parse(await readBody(req));
      } catch (e) {
        return send(res, 400, { error: 'invalid JSON' });
      }
      if (!body || typeof body.name !== 'string' || body.name.trim() === '') {
        return send(res, 400, { error: 'name is required' });
      }
      const tags = body.tags === undefined ? [] : body.tags;
      if (!Array.isArray(tags) || !tags.every((t) => typeof t === 'string')) {
        return send(res, 400, { error: 'tags must be an array of strings' });
      }
      const item = { id: nextId++, name: body.name.trim(), tags };
      items.push(item);
      return send(res, 201, item);
    }

    const m = path.match(/^\/items\/(\d+)$/);
    if (m && req.method === 'GET') {
      const item = items.find((i) => i.id === Number(m[1]));
      return item ? send(res, 200, item) : send(res, 404, { error: 'not found' });
    }

    return send(res, 404, { error: 'not found' });
  });
  return server;
}

module.exports = { createServer };

if (require.main === module) {
  const port = Number(process.env.PORT || 3000);
  createServer().listen(port, () => console.log('listening on ' + port));
}
EOF
cat > test.js <<'EOF'
'use strict';
const assert = require('assert');
const http = require('http');
const { createServer } = require('./server');

let base;
function call(method, path, body) {
  return new Promise((resolve, reject) => {
    const data = body === undefined ? undefined : (typeof body === 'string' ? body : JSON.stringify(body));
    const req = http.request(base + path, { method, headers: data ? { 'Content-Type': 'application/json' } : {} }, (res) => {
      let text = '';
      res.on('data', (c) => { text += c; });
      res.on('end', () => {
        let json;
        try { json = JSON.parse(text); } catch (e) { json = undefined; }
        resolve({ status: res.statusCode, headers: res.headers, text, json });
      });
    });
    req.on('error', reject);
    if (data) req.write(data);
    req.end();
  });
}

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

async function seed() {
  // ids 1..6 (a=1 ... f=6)
  const rows = [['a', ['x']], ['b', ['y']], ['c', ['x', 'y']], ['d', []], ['e', ['x']], ['f', ['z']]];
  for (const [name, tags] of rows) await call('POST', '/items', { name, tags });
}

test('create and fetch', async () => {
  const r = await call('POST', '/items', { name: '  widget ', tags: ['t'] });
  assert.strictEqual(r.status, 201);
  assert.deepStrictEqual(r.json, { id: 1, name: 'widget', tags: ['t'] });
  const g = await call('GET', '/items/1');
  assert.strictEqual(g.status, 200);
  assert.deepStrictEqual(g.json, r.json);
  assert.strictEqual((await call('GET', '/items/99')).status, 404);
});

test('validation errors are 400', async () => {
  assert.strictEqual((await call('POST', '/items', '{oops')).status, 400);
  assert.strictEqual((await call('POST', '/items', { name: '  ' })).status, 400);
  assert.strictEqual((await call('POST', '/items', { name: 'a', tags: 'x' })).status, 400);
  assert.strictEqual((await call('GET', '/nope')).status, 404);
});

test('list: default shows everything (small data) with total header', async () => {
  await seed();
  const r = await call('GET', '/items');
  assert.strictEqual(r.status, 200);
  assert.strictEqual(r.json.length, 6);
  assert.strictEqual(r.headers['x-total-count'], '6');
});

test('list: tag filter', async () => {
  await seed();
  const r = await call('GET', '/items?tag=x');
  assert.deepStrictEqual(r.json.map((i) => i.name), ['a', 'c', 'e']);
  assert.strictEqual(r.headers['x-total-count'], '3');
  const none = await call('GET', '/items?tag=nope');
  assert.deepStrictEqual(none.json, []);
  assert.strictEqual(none.headers['x-total-count'], '0');
});

test('list: limit/offset', async () => {
  await seed();
  const r = await call('GET', '/items?limit=2&offset=1');
  assert.deepStrictEqual(r.json.map((i) => i.name), ['b', 'c']);
  assert.strictEqual(r.headers['x-total-count'], '6');
  const tail = await call('GET', '/items?limit=10&offset=5');
  assert.deepStrictEqual(tail.json.map((i) => i.name), ['f']);
  const past = await call('GET', '/items?offset=50');
  assert.deepStrictEqual(past.json, []);
  assert.strictEqual(past.headers['x-total-count'], '6');
});

test('list: tag + pagination, total counts the filtered set', async () => {
  await seed();
  const r = await call('GET', '/items?tag=x&limit=1&offset=1');
  assert.deepStrictEqual(r.json.map((i) => i.name), ['c']);
  assert.strictEqual(r.headers['x-total-count'], '3');
});

test('list: bad limit/offset -> 400', async () => {
  await seed();
  for (const q of ['limit=0', 'limit=101', 'limit=-1', 'limit=abc', 'limit=1.5', 'limit=', 'limit=1e1', 'limit=%2B5',
                   'offset=-1', 'offset=x', 'offset=0x10', 'offset=2.0']) {
    const r = await call('GET', '/items?' + q);
    assert.strictEqual(r.status, 400, q);
    assert.ok(r.json && typeof r.json.error === 'string', q);
  }
  assert.strictEqual((await call('GET', '/items?limit=100')).status, 200);
  assert.strictEqual((await call('GET', '/items?limit=1&offset=0')).status, 200);
});

test('delete', async () => {
  await seed();
  const d = await call('DELETE', '/items/2');
  assert.strictEqual(d.status, 204);
  assert.strictEqual(d.text, '');
  assert.strictEqual((await call('GET', '/items/2')).status, 404);
  const again = await call('DELETE', '/items/2');
  assert.strictEqual(again.status, 404);
  assert.deepStrictEqual(again.json, { error: 'not found' });
  assert.strictEqual((await call('DELETE', '/items/999')).status, 404);
  assert.strictEqual((await call('GET', '/items')).headers['x-total-count'], '5');
  assert.deepStrictEqual((await call('GET', '/items')).json.map((i) => i.id), [1, 3, 4, 5, 6]);
});

test('ids are never reused', async () => {
  await seed();
  assert.strictEqual((await call('DELETE', '/items/6')).status, 204);
  const r = await call('POST', '/items', { name: 'g' });
  assert.strictEqual(r.json.id, 7);
});

test('stats', async () => {
  await seed();
  await call('DELETE', '/items/2');
  const s = await call('GET', '/stats');
  assert.strictEqual(s.status, 200);
  assert.deepStrictEqual(s.json, { count: 5, tags: { x: 3, y: 1, z: 1 } });
});

test('405 with Allow header', async () => {
  await seed();
  const cases = [['PUT', '/items', 'GET, POST'], ['DELETE', '/items', 'GET, POST'],
                 ['POST', '/items/1', 'GET, DELETE'], ['PUT', '/items/1', 'GET, DELETE'],
                 ['POST', '/stats', 'GET'], ['DELETE', '/stats', 'GET']];
  for (const [m, p, allow] of cases) {
    const r = await call(m, p);
    assert.strictEqual(r.status, 405, m + ' ' + p);
    assert.strictEqual(r.headers.allow, allow, m + ' ' + p);
    assert.deepStrictEqual(r.json, { error: 'method not allowed' }, m + ' ' + p);
  }
  assert.strictEqual((await call('GET', '/stats/extra')).status, 404);
  assert.strictEqual((await call('PUT', '/nothing')).status, 404);
});

(async () => {
  let failed = 0;
  for (const [name, fn] of tests) {   // every test gets a fresh server
    const server = createServer();
    await new Promise((r) => server.listen(0, '127.0.0.1', r));
    base = 'http://127.0.0.1:' + server.address().port;
    try { await fn(); console.log('ok   ' + name); } catch (e) { failed++; console.log('FAIL ' + name + ': ' + e.message.split('\n')[0]); }
    server.close();
  }
  console.log(failed ? failed + ' FAILED' : 'ALL PASSED');
  process.exit(failed ? 1 : 0);
})();
EOF
