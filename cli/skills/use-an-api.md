---
name: use-an-api
description: Call a web service from a small script to get live data.
---

# Skill: use a web API

## Find the contract
- Ask which API, endpoint, desired fields, and allowed scope if the request is unclear.
- Find the provider's current official documentation for authentication, endpoint URL, parameters, pagination, errors, and rate limits; record the relevant links.
- Check terms and quotas before choosing a free tier; do not guess endpoint names or response shapes.
- Prefer Python's standard library: `urllib.request`, `urllib.parse`, `json`, `os`, and `time`; install a client only if necessary.

## Keep credentials private
- Put the actual key in a local `.env` file, never in source, command arguments, logs, screenshots, or example output.
- Add `.env` to the project's ignore rules if needed, and check `git check-ignore .env` before proceeding; do not commit credentials.
- Read the named variable from the environment or parse the simple `KEY=value` entry in `.env`; fail clearly when absent. Do not print its value.
- Use the authentication header or parameter specified by the docs; never embed the key in a committed URL.
- Provide a `.env.example` with a placeholder only if the user needs setup instructions.

## Write the smallest script
- Accept safe input parameters and an output path via `argparse`; use `urllib.parse.urlencode` for query parameters and a timeout on every request.
- Decode JSON with `json.load`/`json.loads`; validate the expected fields before using them, and distinguish malformed data from an empty result.
- Catch `urllib.error.HTTPError`, `URLError`, timeouts, and JSON decoding errors; show the status and useful message without leaking secrets.
- On HTTP 429, honor `Retry-After` when present; otherwise back off with a bounded delay and limited retries. Do not spin forever.
- Follow the documented next-page URL, page number, or cursor until done or a configurable page limit; detect repeated cursors and stay on the documented API host.
- Avoid logging response headers or bodies that might contain tokens or personal data.

## Prove it works
- Run `python3 -m py_compile script.py` and test missing-key and invalid-input failures without making requests.
- With the user's permission and a real key in `.env`, make one real request using a small page size; check the status and expected fields, not just that the command exits zero.
- Save a small sample response as JSON in a new file, after removing tokens and personal or sensitive fields; do not overwrite an existing sample.
- If a real request cannot be made, say exactly why and do not claim live verification.
- Report the documentation link, invocation, rate/pagination behavior, sample path, and actual test result without exposing the key.
