# Connecting MCP servers to Prism

MCP (Model Context Protocol) is a standard way to plug extra tools into an AI
assistant. An MCP server is a small program that offers tools, for example
"read a file", "fetch a web page" or "list my GitHub issues". Prism connects
to these servers and hands their tools to the AI.

This guide shows you how to add servers, with copy-paste examples.

## Where the config lives

Prism reads two config files:

- `~/.config/prism/mcp.json` applies to every project.
- `.prism/mcp.json` inside a project folder applies only to that project. If
  both files define a server with the same name, the project one wins.

Inside the Docker container, home is `/home/prism` and your project folder is
mounted at `/workspace`, so the files live at
`/home/prism/.config/prism/mcp.json` and `/workspace/.prism/mcp.json`.

## Config format

The file holds one object with an `mcpServers` key. Each entry names a server
and says how to start it.

A local server runs as a command:

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"],
      "env": {}
    }
  }
}
```

A remote server is reached over HTTP:

```json
{
  "mcpServers": {
    "github": {
      "url": "https://api.githubcopilot.com/mcp/",
      "headers": {
        "Authorization": "Bearer ${GITHUB_TOKEN}"
      }
    }
  }
}
```

Notes:

- `env` sets environment variables for a local server, `headers` sets HTTP
  headers for a remote one.
- Values may contain `${VAR}`, which Prism replaces with the value of the
  environment variable `VAR`. This is the right way to pass tokens instead of
  writing them into the file.
- Add `"disabled": true` to a server to switch it off without deleting it.
- The Docker image has nodejs, npm and python3, so servers started with `npx`
  work out of the box. There is no `uv`, so Python based servers that need it
  will not run.

## Seeing what is connected

Type `/mcp` inside Prism. It lists every server with its state and its tools.
States are `starting`, `ready`, `off` and `error`. A broken config file shows
up as a server named `config` in the `error` state, with the parse problem as
the message.

Tools appear to the AI as `<server>__<tool>`. For example the filesystem
server above offers `filesystem__read_file`.

## Approving tool calls

In `prism fix` mode every MCP tool call is shown to you first, and the AI only
proceeds after you approve it. In normal chat mode MCP tools run without
asking.

## Examples

All of these are free to use. Paste them into `~/.config/prism/mcp.json` (or a
project's `.prism/mcp.json`) and merge them under one `mcpServers` key.

### 1. Filesystem

Lets the AI read and write files in a folder you choose. Give it `/workspace`
so it can see your project inside the container.

```json
"filesystem": {
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
}
```

### 2. Fetch web pages

Lets the AI pull down a URL and read the page as text. Its tool is called `imageFetch`, but it fetches normal web pages too, not just images.

```json
"fetch": {
  "command": "npx",
  "args": ["-y", "@kazuph/mcp-fetch"]
}
```

### 3. Git

Gives the AI proper git tools (log, diff, commit search) instead of running
shell commands. After it connects, run its `git_set_working_dir` tool with
`/workspace` first (or just ask Prism to set the git folder to `/workspace`),
otherwise the tools look in the wrong folder.

```json
"git": {
  "command": "npx",
  "args": ["-y", "@cyanheads/git-mcp-server"]
}
```

### 4. Browser automation

Drives a real browser: open pages, click, fill forms, take screenshots. Useful
for checking the website you are building.

```json
"browser": {
  "command": "npx",
  "args": ["-y", "@playwright/mcp@latest"]
}
```

The first start downloads Playwright's browser build, so it takes a while.

### 5. GitHub (free account needed)

Reads issues, pull requests and code from GitHub. Uses GitHub's hosted remote
server, so there is nothing to install.

Create a free personal access token at
https://github.com/settings/tokens (a classic token with `repo` scope covers
most uses). Export it before starting Prism:

```sh
export GITHUB_TOKEN=ghp_yourtoken
```

Then add:

```json
"github": {
  "url": "https://api.githubcopilot.com/mcp/",
  "headers": {
    "Authorization": "Bearer ${GITHUB_TOKEN}"
  }
}
```

Never commit the token or the config file with the token written in it. Keep
the token in the environment variable only, and check `.prism/mcp.json` into
git only when it references `${VAR}` names, not real secrets.

## Troubleshooting

| Problem | Likely cause | Fix |
| --- | --- | --- |
| Server shows `error` in `/mcp` | Command missing, bad args, or the server crashed during startup | Read the error message in `/mcp`. Try running the same `npx ...` command yourself in the container. Check for typos in `command` and `args`. |
| A server named `config` shows `error` | A `mcp.json` file is not valid JSON | The message names the file. Fix the syntax, usually a missing comma or quote. |
| First `npx` start is slow | npm downloads the package on first run | Wait. Later starts use the cache and are fast. The GitHub example avoids this by being remote. |
| Server is `ready` but the AI never uses the tool | The AI does not see a need, or the name is unclear | Ask directly, for example "use the fetch tool to read this page". Check `/mcp` to see the exact tool names and mention one. |
| Tool call keeps asking for approval | You are in `prism fix` mode | Expected behaviour: every MCP call is approved by you in fix mode. Use normal chat mode if you do not want prompts. |
| `${VAR}` comes through empty | The variable is not set in the environment Prism runs in | Export it before starting Prism, or pass it into the container with `docker run -e VAR=...`. |
