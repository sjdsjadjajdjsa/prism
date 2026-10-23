---
name: accessibility-review
description: Check a web page or app for WCAG issues (semantics, keyboard, contrast, motion) and report exact fixes.
---

# Skill: accessibility review

Use this when the user wants a page or app checked for accessibility. Read only. Report issues with `file:line` and the exact fix. Do not edit unless the user asks.

## Find the pages

- List HTML, CSS, and JS: `rg --files -g '*.html' -g '*.css' -g '*.js'`.
- Note the page language and title in each HTML file.

## Semantic HTML and headings

- Require `<html lang="...">` and a non-empty `<title>`.
- One `<h1>`. Headings step down in order (`h1` then `h2`, never skip).
- Prefer `header`, `nav`, `main`, `section`, `footer`, `button` over clickable `div`s.
- Flag `role` that duplicates a native element, or `aria-*` on an element that does not support it.

## Images, forms, keyboard

- Every `<img>` needs `alt` (empty `alt=""` only for decorative images).
- Every input, select, and textarea needs a `<label for>` or `aria-label`.
- Check focus order follows the DOM. Require a visible `:focus-visible` outline.
- Flag `tabindex` greater than 0, and handlers that trap Tab or remove focus with no escape.

## Colour contrast (WCAG AA 4.5:1)

- Read colour pairs from CSS. Compute the ratio with python3:

```bash
python3 - <<'EOF'
def lin(c):
    c = c/255
    return c/12.92 if c <= 0.04045 else ((c+0.055)/1.055)**2.4
def L(hex):
    h = hex.lstrip('#')
    r,g,b = [lin(int(h[i:i+2],16)) for i in (0,2,4)]
    return 0.2126*r + 0.7152*g + 0.0722*b
def ratio(a,b):
    la,lb = sorted((L(a),L(b)))
    return (lb+0.05)/(la+0.05)
print(round(ratio('#767676','#ffffff'), 2))
EOF
```

- Text under 4.5:1 fails AA (large text, 18pt or 14pt bold, needs 3:1). Name the selector and both colours.

## Motion and a look at the page

- Animations and transitions must sit inside `@media (prefers-reduced-motion: no-preference)`, or honour `prefers-reduced-motion: reduce`.
- If a `screenshot` tool is in your tool list, capture the page and look for clipped text, tiny targets, and low-contrast regions.

## Report

- List each issue as `file:line`, what fails, and the smallest exact fix. Do not apply fixes until asked.
