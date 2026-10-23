---
name: portfolio-site
description: Build a personal resume or portfolio page you can print.
---

# Skill: build a portfolio or resume page

Use this when the user wants a personal site: resume, portfolio, or about-me page.

## Content and layout
- Ask for user info (name, role, projects) or use clear placeholders like `YOUR NAME` and `PROJECT TITLE HERE`.
- Separate files: `index.html` and `style.css`. Minimal JS is fine for small extras.
- Semantic structure: header, main sections (about, experience, projects, skills, contact), small footer.
- Add `<meta name="description">` bio and contact section with a `mailto:` link placeholder.

## CSS and styles
- Single-column layout, `max-width: 70ch`, system fonts, CSS variables in `:root` for accent colors.
- HTML class names must match CSS selectors.
- Print styles for PDF export:
  ```css
  @media print {
    nav, .no-print { display: none; }
    body { color: #000; background: #fff; font-size: 11pt; }
    a { color: #000; text-decoration: none; }
    a[href^="http"]::after { content: " (" attr(href) ")"; font-size: 9pt; }
  }
  ```

## Verify before you finish
- Check placeholders: `grep -n "YOUR\|HERE" index.html`.
- Run a python check that every class in `style.css` exists in `index.html` and vice versa:
  ```python
  import re, html.parser
  class P(html.parser.HTMLParser):
      def __init__(self): super().__init__(); self.classes = set()
      def handle_starttag(self, tag, attrs):
          for k, v in attrs:
              if k == 'class': self.classes.update(v.split())
  p = P(); p.feed(open('index.html').read())
  css_cls = set(re.findall(r'\.([a-zA-Z0-9_-]+)', re.sub(r'/\*.*?\*/', '', open('style.css').read(), flags=re.S))) - {'no-print'}
  assert not (css_cls - p.classes), f"CSS classes missing in HTML: {css_cls - p.classes}"
  assert not (p.classes - css_cls), f"HTML classes missing in CSS: {p.classes - css_cls}"
  ```
- Tell the user which placeholders to edit and how to print to PDF (Ctrl+P).
