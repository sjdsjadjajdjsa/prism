---
name: browser-game
description: Build a small canvas game in one index.html with a game loop, input, score, and restart.
---

# Skill: build a browser game

Use this when the user wants a small 2D game like snake, pong, breakout, or a dodging game.

## Plan
- Pick one clear mechanic. A game that works is better than a big game that does not.
- Put everything in one `index.html`: markup, style in a `<style>` block, game code in a `<script>` block.
- No libraries, no CDNs, no build step. Plain canvas and keyboard input.

## Build the page
- Start from `<!DOCTYPE html>`, `<meta charset="utf-8">`, and a viewport meta tag.
- Add one `<canvas>` with a fixed size (for example 480x320) and a score line above it.
- Center the canvas with simple CSS. Dark background makes the game look decent by default.
- Show controls under the canvas ("arrow keys to move, R to restart").

## Build the game code
- Keep state in plain variables: player position, enemies, score, and a `gameOver` flag.
- Use one game loop with `requestAnimationFrame`. Track time with the timestamp argument so speed is stable.
- Read input with `keydown` and `keyup` into a keys object. Never move the player inside the key handler.
- Update then draw each frame: move things, check collisions, then clear the canvas and redraw.
- On collision or win: set `gameOver`, draw a "Game over, press R" message, and stop updating.
- Restart resets all state and score to zero. Do not reload the page.
- Keep magic numbers named as constants at the top (speed, sizes, spawn rate).

## Verify before you finish
- Run a python check that the HTML parses and the script block extracts cleanly:

  ```bash
  python3 - <<'EOF'
  from html.parser import HTMLParser
  class P(HTMLParser):
      def __init__(self):
          super().__init__(); self.scripts = []; self.cur = None; self.canvas = 0
      def handle_starttag(self, tag, attrs):
          if tag == 'canvas': self.canvas += 1
          if tag == 'script': self.cur = ''
      def handle_data(self, data):
          if self.cur is not None: self.cur += data
      def handle_endtag(self, tag):
          if tag == 'script' and self.cur is not None:
              self.scripts.append(self.cur); self.cur = None
  p = P(); p.feed(open('index.html').read())
  open('/tmp/game.js', 'w').write('\n'.join(p.scripts))
  print('canvas tags:', p.canvas, 'script blocks:', len(p.scripts))
  assert p.canvas == 1 and p.scripts
  EOF
  ```

- If node exists, run `node --check /tmp/game.js` to catch JS syntax errors.
- Play it with the `browse` tool on `{"file":"index.html"}`. Press the start key (Space, Enter, or an arrow key) or click the start button, then `expect` the score text. If the game can end, hold a direction with repeated `press` steps and `expect` the "Game over" text. The result lists page errors: any error is a bug, so fix it and run the steps again until they pass. `press` cannot send letter keys, so make restart also work with Enter or Space.
- Tell the user to open `index.html` in a browser and describe the controls.
