# Contributing

## Build

Build the CLI with Docker:

```bash
docker run --rm -v $(pwd)/cli:/src -w /src golang:1.27 go build -o p .
```

## Tests

Run the test suite:

```bash
docker run --rm -v $(pwd)/cli:/src -w /src golang:1.27 go test ./...
```

## Router tests

Run router tests with Docker:

```bash
docker run --rm -v $(pwd)/router:/r -w /r node:24-alpine node --test server.test.mjs
```

## Benchmarks

Run benchmarks:

```bash
bench/run.sh <label>
```

## Adding a skill

Skills are defined in cli/skills/<name>.md with front matter name matching the filename, a one-line description under 120 chars, then imperative steps. Alternatively, use /skills new <name> inside Prism.

## Adding a provider

Add an entry to router/providers.json with shape {name, baseURL, models: [], requires?: ENV_VAR, auth?: false, quick?: {}}. Providers must be free with no credit card.

## Pull requests

Keep PRs small and focused. Include a clear description of what changed and why. Reference any related issues.
