---
name: clean-data
description: Clean up a messy CSV, JSON, or Excel file, keeping the original.
---

# Skill: clean data

## Inspect before changing anything
- Ask for the desired cleaning rules, input format, and treatment of missing or duplicate rows; do not silently invent deletion rules.
- Inspect the first few records and total row count without printing sensitive values: for CSV use `csv.DictReader`, for JSON use `json.load` and check whether it contains a list or a known record key.
- List columns/keys, inferred types, missing values, inconsistent formats, and suspicious examples; distinguish empty strings from missing fields.
- For Excel, use `pandas` only if installed (`python3 -c 'import pandas'`); reading `.xlsx` may also require `openpyxl`. If unavailable, explain what to install or request CSV instead.
- Keep dates, identifiers, leading zeros, and text encoding intact unless the user explicitly requests conversion.

## Write a safe cleaning script
- Prefer Python standard-library `csv`, `json`, `argparse`, `pathlib`, and `collections` for CSV/JSON; use pandas only when already installed and needed for Excel or complex tables.
- Accept an input and a distinct output path; default to `--dry-run`, and require `--apply` before writing any cleaned file.
- Reject identical paths (including resolved aliases), and reject an output that already exists; never overwrite or modify the original.
- Read rows, apply explicit deterministic rules, and keep a counter per reason for changed and dropped rows; count each affected row once in totals.
- Preserve CSV headers and encoding where possible; write JSON with clear formatting. For Excel, preserve sheets or warn before a conversion that loses formatting/formulas.
- In dry-run, print proposed counts and a few masked before/after examples without creating the output file.
- In apply mode, write a NEW file only after validation; report the output path, input rows, output rows, rows changed, rows dropped, and reasons.

## Check the result
- Create a tiny temporary CSV/JSON fixture with valid, invalid, missing, and duplicate cases; run dry-run and confirm no file was written.
- Run `python3 -m py_compile script.py`, then `python3 script.py input.csv output.csv --apply` on a disposable fixture.
- Reopen the new file and recount records: `input rows - dropped rows == output rows`; check that changed and untouched rows match the declared rules.
- Confirm the original file is byte-for-byte unchanged (for example, compare a `sha256sum` taken before and after), and verify existing output paths fail safely.
- Report counts and reasons, any ambiguous records left untouched, and the commands needed to inspect or apply to the user's real file.
