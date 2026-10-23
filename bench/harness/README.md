# Harness comparison: Prism vs other open-source coding agents

> Two separate comparisons live in this repo. This file's main body documents the
> **router-based** run (`bench/harness/run.sh`, model id `auto`, four harnesses).
> The **pinned single-model head-to-head** (`bench/compare.sh`, Prism vs Aider) is documented
> in [the last section](#pinned-single-model-head-to-head-benchcomparesh).

The question: **with the same free models, is Prism a better harness?**  
Every harness talks to the same Prism router (`prism-router-1`, OpenAI-compatible,
`http://127.0.0.1:4848/v1`) with the same model id (`auto`), on the same tasks,
with the same time budget. Only the harness differs.

Results: [`results.json`](results.json) (every run, failures, routes used).
Raw logs, request logs and the final workspace of every run: `out/<batch>/<harness>-<task>-r<rep>/`.

## Harnesses (pinned)

| harness | version | how it runs headless | why picked |
|---|---|---|---|
| Prism | `prism-agent:latest` image (repo HEAD in `results.json`) | `prism -api <url> run "<prompt>"`, as `bench/run.sh` does | the subject |
| Aider | `aider-chat==0.86.2` (latest on PyPI, Oct 2026) | `aider --model openai/auto --openai-api-base <url> --yes-always --no-git --message "<prompt>" <starting files>` | most used terminal pair-programmer; one-shot `--message` mode |
| mini-swe-agent | `mini-swe-agent==2.4.6` (latest) | `mini -y --exit-immediately -l 0 -m openai/auto -c mini.yaml -c model.model_kwargs.api_base=<url> -t "<prompt>"` | the SWE-bench team's minimal bash-only agent loop |
| opencode | `opencode-ai@1.19.4` (latest on npm) | `opencode run --model router/auto "<prompt>"` with an `@ai-sdk/openai-compatible` provider pointing at `<url>`, all permissions `allow` | popular full-featured open-source terminal agent |

Considered and not used: OpenHands (`openhands-ai` 1.11, latest on PyPI) — its agent normally
executes inside its own sandbox runtime container, which does not fit "one fresh container
per run, same budget" without docker-in-docker; it was left out to keep the setup equal and
the run time bounded, not because it failed. The router only serves `/v1/chat/completions`,
so any harness must speak the Chat Completions API.

The third-party harnesses live in one image, `prism-harness:20261022`
([`Dockerfile`](Dockerfile)), built **on top of `prism-agent:latest`**, so every harness
gets the same OS, python3, node, git and ripgrep. Settings are each harness's defaults
plus only what is needed to run unattended (auto-approve, no git, no update checks,
no telemetry) — see [`entry.py`](entry.py). One fix was needed: mini-swe-agent 2.4.6
resolves to litellm 1.106.2, which crashes on every tool-calling request without the
undeclared `mcp` package, so the image adds `mcp==2.3.0`.

## Tasks

The five tasks of `bench/run.sh`, as fixed folders in [`tasks/`](tasks):
`prompt.txt` (identical text for every harness), `setup.sh` (creates the starting files),
`check.sh` (automatic pass/fail, run on the host after the harness exits):

| task | start | pass when |
|---|---|---|
| bugfix | `shop.py` with two bugs | `python3 shop.py` prints exactly `3.83` |
| website | empty | `index.html` mentions localStorage and parses as HTML |
| refactor | `report.py` with repeated code | same output as before and `upper()` appears exactly once |
| explain | `m1.py..m3.py` | `answer.txt` holds `18` |
| multifile | empty | `calc` package imports, `add`/`sub` work, `python3 test_calc.py` passes |

All five checks fail on the untouched starting folders.

## Method

`run.sh` loops repetition × task × harness, one run at a time (no two harnesses share the
router at once). Harness order is rotated per task and repetition so no harness always goes
first. For each run:

1. a fresh temp folder is created and `setup.sh` runs in it;
2. a private recording proxy ([`proxy.py`](proxy.py)) is started in front of the router; the
   harness is pointed at it, so every request of that run — and the upstream route the router
   picked for it (`x-free-route` response header) — is logged to `requests.jsonl`;
3. the harness runs in a fresh `docker run --rm --network host` container as the host user,
   with the folder mounted at `/workspace`, killed at **360 s**;
4. `check.sh` decides pass/fail; wall time, exit code, timeout, request count and routes are
   saved; `summarize.py` rebuilds `results.json` from every batch under `out/`.

Prism runs from its own `prism-agent:latest` image with the same command line `bench/run.sh`
uses, but in a fresh container without the shared `prism-config` volume, so no memory carries
over between runs (the same is true for the other harnesses).

## Reproduce

```bash
docker ps | grep prism-router-1 || docker compose -f docker-compose.yml -f docker-compose.host.yml up -d router
docker compose --profile agent build agent         # prism-agent:latest, if missing
bench/harness/run.sh 3                             # N=3 → 60 runs; builds prism-harness:20261022 if missing
HARNESSES="prism aider" TASKS=bugfix bench/harness/run.sh 1   # subset
python3 bench/harness/summarize.py bench/harness/out bench/harness/results.json
```

## Results

Run 2026-10-22 16:28–17:05 PDT, batch `out/20261022-162758`, N=3 (60 runs), model `auto`, budget 360 s.

| harness | passed | median s | mean s | timeouts | model requests | KB sent |
|---|---|---|---|---|---|---|
| opencode 1.19.4 | 15/15 | **16.3** | 20.9 | 0 | 80 | 2115 |
| Aider 0.86.2 | 14/15 | 22.8 | 24.5 | 0 | 22 | 79 |
| **Prism** (4dfe61891519) | 15/15 | 27.8 | 33.6 | 0 | 97 | 727 |
| mini-swe-agent 2.4.6 | 15/15 | 33.3 | 36.2 | 0 | 119 | 941 |

Median seconds per task:

| harness | bugfix | website | refactor | explain | multifile |
|---|---|---|---|---|---|
| opencode | 18.4 | 21.0 | 30.8 | 11.2 | 16.0 |
| Aider | 23.9 | 41.8 | 15.5 | 12.4 | 20.2 |
| Prism | 51.4 | 33.3 | 22.5 | 29.0 | 24.6 |
| mini | 35.9 | 46.8 | 26.1 | 18.9 | 29.9 |

Reading it honestly:

- **Pass rate does not separate the harnesses.** These five tasks are too easy for today's free
  models: three harnesses went 15/15. The only failure is Aider on `refactor` r1: the model's
  reply made Aider's default "whole file" edit format write the code into a file literally named
  `print(a(" hi "), b("yo"), c("ok"))` and leave `report.py` empty.
- **Prism is not the fastest.** opencode was faster on 4 of 5 tasks (median 16 s vs 28 s), and
  single-shot Aider was faster too. Prism beat only mini-swe-agent. Prism was slowest of all on
  `bugfix` (it runs the script and checks, often over several turns).
- **Prism is economical with context:** ~7.5 KB per request vs ~26 KB for opencode (opencode
  sends a large system prompt and tool list every turn).
- **Routes differed.** No upstream request failed at the proxy (the router absorbed upstream
  429s by switching routes). The share of requests served by the big Nemotron 3 Ultra 550B was
  opencode 41 %, mini 43 %, Aider 27 %, Prism 13 % — Prism's `prism_quick` hint steers many of
  its turns to the small Nemotron 3.5 Lightning 30B routes. Full per-harness counts are in
  `results.json` → `summary.<harness>.routes_used`.

## Limitations

- **Free models change.** The router's upstream list (NVIDIA NIM, Kilo, BlockRun, LLM7 free
  tiers) and their quality/speed/rate limits drift by the hour; these numbers are a snapshot of
  one afternoon and are not reproducible exactly.
- **`auto` is not one model.** The router picks, races and hedges per request, so two harnesses
  (or two requests of one run) can be served by different models. `results.json` records which
  route served each run's requests; check `routes_used` before reading too much into a gap.
  Harnesses also send different request shapes, and Prism alone sends router hints
  (`prism_quick`, `prism_avoid`, see `cli/agent.go`) that the router honours — that is part of
  what Prism is, so it was left on, but it means the model mix is not identical.
- **Small samples.** 5 tiny tasks × 3 repetitions per harness. A difference of one or two
  passes is noise. The tasks are Prism's own benchmark tasks, chosen before this comparison, and
  are easy; they say little about large real-world repos.
- **Checks are coarse.** `website` only checks for localStorage and parseable HTML, not that
  the page works.
- **Defaults only.** No harness was tuned (e.g. Aider's edit format for an unknown model, mini's
  prompt templates). Aider is single-shot by design (`--message`), gets the starting files on its
  command line (its normal usage) and does not iterate on test output.
- **Shared router.** Other processes on this machine may use the same router during the run,
  which affects its learned speeds and rate limits (but not the per-run route attribution).
- Wall time includes container start-up and, for opencode, downloading its provider package into
  a fresh home folder.

## Pinned single-model head-to-head (`bench/compare.sh`)

Prism vs Aider on the five tasks of `bench/run.sh`, with **one model pinned for both**, so the
router's racing/hedging does not blur the comparison. Data: the `compare` section of
[`../history.json`](../history.json); raw rows in
`bench/results-compare-<harness>-<model>.{txt,jsonl}`.

### What is pinned

| item | value |
|---|---|
| model | route `nvidia/nvidia/nemotron-3-ultra-550b-a55b` (provider `nvidia`, model `nvidia/nemotron-3-ultra-550b-a55b`, from `router/providers.json`) |
| Prism | image `prism-agent:latest` = ID `4dfe61891519` (same ID as `prism-agent:bench-before`), repo HEAD `ae023e4` (2026-10-22) |
| Aider | `aider-chat==0.86.2`, from the `prism-harness:20261022` image (`/opt/aider/bin/aider`), or a local `aider` on PATH if present |
| tasks | prompts, setup and checks are `eval`'d straight out of `bench/run.sh` (`task bugfix` … `multifile`), so they are identical |
| budget | 900 s per task (`BUDGET`) |
| key | `NVIDIA_API_KEY` in the environment or in `keys.conf` at the repo root (the script exits if missing) |

Both harnesses talk to a small pinning proxy that `compare.sh` starts for each task. It forwards
straight to that one provider+model (no router), strips `prism_*` request fields, merges the
provider's `quick` params when `prism_quick` is set, asks for usage, and sums the
provider-reported prompt tokens (the `tokens` column). Each run uses a fresh temp dir; the check
runs on the host afterwards.

### Reproduce

```bash
# prerequisites: docker, python3, NVIDIA_API_KEY (env or keys.conf)
docker compose --profile agent build agent                  # prism-agent:latest, if missing
docker build --network host -f bench/harness/Dockerfile -t prism-harness:20261022 bench/harness   # Aider image, if missing
export NVIDIA_API_KEY=...                                    # or put it in keys.conf

bench/compare.sh prism      # -> bench/results-compare-prism-nvidia_nvidia_nemotron-3-ultra-550b-a55b.{txt,jsonl}
bench/compare.sh aider      # -> bench/results-compare-aider-nvidia_nvidia_nemotron-3-ultra-550b-a55b.{txt,jsonl}

TASKS="bugfix" bench/compare.sh aider          # subset
bench/compare.sh prism <provider>/<model>      # another route id from router/providers.json
```

Each run prints one line per task (`name  secs  pass|fail  tokens  served-by-model`) and a
`total:` line. The published rows in `history.json` were copied from those `.jsonl` files.

### Result (one run each, N=1)

| | Prism | Aider |
|---|---|---|
| passed | 3/5 | 4/5 |
| total wall time | 307 s | 199 s |
| prompt tokens (provider-reported) | 20 578 (2 tasks unreported) | 3 655 |

Per task, seconds / result: bugfix 40 pass vs 80 pass; website 218 pass vs 85 fail;
refactor 23 pass vs 10 pass; explain 0 fail vs 10 pass; multifile 26 fail vs 14 pass
(Prism vs Aider). Prism was faster only on `bugfix`; Aider was faster on the other four
(on `explain` Prism failed after 0 s with no request served, i.e. it never reached the model; the
cause was not investigated, so treat it as a failed run rather than a model-quality result).
Prism's token count is higher partly by design: it is a multi-turn agent, Aider is single-shot.

### Known limitations

- **claude-code, codex, gemini and openhands were not installed** on the machine that produced
  these numbers (none of `claude`, `codex`, `gemini`, `openhands` was on PATH). `compare.sh` has
  adapters for them, but they were **never run and no result exists**: the script prints
  `SKIP <harness>: not installed` and exits 0. Their adapters are also untested, and those
  harnesses use their own vendor models (not the pinned NVIDIA route unless `--model` happens to
  accept it), so they would not be an equal-model comparison anyway. Do not read their absence as
  a loss or a win.
- **N=1.** Each cell is a single run of a free-tier hosted model; a one- or two-task gap is noise.
- **Not the router.** This pins one model and bypasses the router's racing, so it says nothing
  about Prism's normal `auto` behaviour (see the router-based run above for that).
- **Aider gets the starting files on its command line and is single-shot** (`--message`); it
  does not iterate on test output. Prism runs its full agent loop.
- Prism's `website` token count and `explain` row are `null` in `history.json` (no usage reported).
- The Prism image was built before some later commits may have landed; the HEAD above is the
  one recorded when the harness run was made, not a guarantee the image matches HEAD.
