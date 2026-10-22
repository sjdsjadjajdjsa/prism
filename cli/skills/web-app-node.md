---
name: web-app-node
description: Set up a small Node.js app with package.json, npm scripts, and script-based checks instead of a server.
---

# Skill: small Node.js app

Use this when the user wants a Node tool, script, or small app.

## Setup
- Create `package.json` by hand or with `npm init -y`, then fix the fields: name, version, description, `"main": "index.js"`.
- Add npm scripts so the user has simple commands:
  - `"start": "node index.js"`
  - `"test": "node test.js"` (or the real test command)
- Install dependencies locally with `npm install <pkg>`. Never use `npm install -g` (global installs).
- Add a `.gitignore` with `node_modules/` in it.

## Code
- One entry file `index.js` to start. Split into modules with `require`/`module.exports` (CommonJS) only when it grows.
- Keep everything self-contained. This environment cannot keep a server running, so prefer apps that do work and exit: process input, print output, exit 0.
- If the user asks for a web server, write it with the built-in `http` module, but structure the code so the request-handling logic is a separate exported function that can be tested without listening on a port.
- Handle bad input with clear error messages to stderr and `process.exit(1)`. Beginners should see why it failed.

## Test without a server
- Do not start a long-running server to verify. Write a `test.js` that imports your functions, calls them with sample input, and asserts the output with `node:assert`:

  ```js
  const assert = require('node:assert');
  const { formatName } = require('./index');
  assert.strictEqual(formatName('ada'), 'Ada');
  console.log('all tests passed');
  ```

- For HTTP handlers, call the handler function with fake request/response objects instead of binding a port.

## Verify
- Run `node --check index.js` (and on every other `.js` file) to catch syntax errors without executing.
- Run `npm test`. It must print success and exit 0.
- Run `npm start` once with sample input to show real output.
- If anything listens on a port, make sure your verification kills it or never starts it, nothing should keep running after you finish.

## Report
- List the files created and the exact commands: `npm install`, `npm start`, `npm test`.
