---
name: portfolio-site
description: Build a personal resume or portfolio page with placeholders and print-friendly CSS.
---

# Skill: build a portfolio or resume page

Use this when the user wants a personal site: resume, portfolio, or about-me page.

## Gather content
- Ask the user for their name, role, and 2 or 3 projects or jobs. If they do not answer, use clearly marked placeholders like `YOUR NAME` and `PROJECT TITLE HERE` so they are easy to find and replace.
- Keep it one page. A portfolio is a summary, not a blog.

## Build the HTML
- Separate files: `index.html` and `style.css`.
- Semantic structure: header with name and role, main with sections (about, experience, projects, skills, contact), small footer.
- Every project gets a title, one-line description, and a link placeholder.
- Add `<meta name="description">` with a one-line bio.
- Add a contact section with `mailto:` link placeholder.

## Build the CSS
- Clean single-column layout, `max-width: 70ch`, generous spacing. This is a resume; readable beats flashy.
- Use CSS custom properties in `:root` for the accent color and fonts so restyling is one-line edits.
- System font stack only. No external fonts, no CDNs, no JS needed.
- Add print styles so the page doubles as a PDF resume:

  ```css
  @media print {
    nav, .no-print { display: none; }
    body { color: #000; background: #fff; font-size: 11pt; }
    a { color: #000; text-decoration: none; }
    a[href^="http"]::after { content: " (" attr(href) ")"; font-size: 9pt; }
  }
  ```

- Test the print layout idea mentally: no dark backgrounds, links show their URLs.

## Verify before you finish
- Run the same python HTML check as the website skill: parse `index.html` and confirm `style.css` exists.
- Check every placeholder is marked: `grep -n "YOUR\|HERE" index.html` should list them all for the user.
- Tell the user which placeholders to edit and that they can print to PDF from the browser (Ctrl+P).
