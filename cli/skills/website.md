---
name: website
description: Build a polished single-page website with modern responsive CSS, checked by clicking through it with browse.
---

# Skill: build a website

Use this when the user wants a landing page, portfolio, or small site.

## Plan
- Decide the structure: separate `index.html`, `style.css`, `script.js` (easier to maintain) unless the user asks for one file.
- Keep everything local. No CDN links, no external fonts, no build tools.

## Build the HTML
- Start from `<!DOCTYPE html>`, `<html lang="en">`, and a `<meta charset="utf-8">` plus `<meta name="viewport" content="width=device-width, initial-scale=1">` in the head.
- Use semantic tags: header, nav, main, section, footer. Not divs everywhere.
- Give every image an `alt`. Give form fields a `<label>`.
- One `<h1>` per page. Heading levels go down in order, never skip.
- Put enough content in for a real site, not lorem ipsum. Match the user's topic.

## Build the CSS
- Mobile-first: write base styles for small screens, then `@media (min-width: 700px)` to widen.
- Use CSS custom properties in `:root` for colors and spacing so the theme is easy to tweak.
- Use flexbox or grid for layout. No floats, no absolute-positioning hacks.
- Add a visible `:focus-visible` outline so keyboard users can see where they are.
- Respect motion: wrap animations in `@media (prefers-reduced-motion: no-preference)`.
- Keep line length readable: `max-width: 70ch` on paragraphs.

## Build the JS
- Keep it small. Only add JS for real interaction (menu toggle, smooth scroll, form handling).
- Wrap code in an event listener for `DOMContentLoaded`. Never assume elements exist before the DOM loads.
- No `alert()` in finished code. Show messages in the page.

## Verify before you finish
- Run `bash` with a python check to confirm the HTML parses and every referenced local file exists:

  ```bash
  python3 - <<'EOF'
  from html.parser import HTMLParser
  import os
  class P(HTMLParser):
      def __init__(self):
          super().__init__(); self.refs = []; self.ids = set()
      def handle_starttag(self, tag, attrs):
          d = dict(attrs)
          if 'id' in d: self.ids.add(d['id'])
          for a in ('src', 'href'):
              v = d.get(a)
              if v and not v.startswith(('http', '#', 'mailto:', 'tel:')):
                  self.refs.append(v)
  p = P(); p.feed(open('index.html').read())
  missing = [r for r in p.refs if not os.path.exists(r)]
  print('missing files:', missing or 'none')
  assert not missing
  EOF
  ```

- If there is a `script.js`, run `node --check script.js` to catch syntax errors.
- Use the `browse` tool on `index.html` (`file`) to check it works like a person would use it. List the main user actions as `steps` from a fresh page: click the nav link or button, type into a form field, press Enter, then `expect` the visible result (new text, opened menu, success message). Use `expect_not` for things that must disappear, and `reload` for anything meant to persist.
  - Name targets as the returned outline shows them, e.g. `button "Send"` or `textbox`. If the outline shows a control with no name, fix the page (add a `<label>` or text).
  - Page errors in the result are bugs. If any step fails, fix the page (or the step, if it named the wrong thing) and run `browse` again until every step passes and there are no page errors.
- Check anchors: every `href="#x"` should match an `id="x"` in the page.
- Report to the user which files you made and how to open the site (open `index.html` in a browser).
