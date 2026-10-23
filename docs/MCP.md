# Connecting MCP servers to Prism

MCP (Model Context Protocol) is a standard way to plug extra tools into an AI
assistant. An MCP server is a small program that offers tools, for example
"read a web page", "look up library docs" or "list my GitHub issues". Prism
connects to these servers and hands their tools to the AI.

## The easy way: 3 steps

Type these inside Prism (the terminal and `prism web` work the same):

1. **`/mcp add`** shows a numbered list of free, ready-made servers.
2. **`/mcp add 1`** (or `/mcp add fetch`) adds one to this project. Prism writes
   it into `.prism/mcp.json` for you, starts it, and tells you `ready` or what
   to fix, plus an example of what to ask.
3. **Only if it needs a key**, Prism names the key and the page where you get it
   for free. Paste it with `/mcp key NAME value`, for example
   `/mcp key GITHUB_TOKEN ghp_abc123`. Prism saves it, reconnects and tells you
   if it works. The value must be one word with no spaces.

Then just ask for what you want, for example "read https://go.dev/doc/effective_go
and list the five main rules". The AI picks the tool by itself.

Other commands:

| Command | What it does |
| --- | --- |
| `/mcp` | Shows every server, its state and its tools (`/mcp list` and `/mcp status` do the same) |
| `/mcp add` | Lists the servers you can add |
| `/mcp add <name or number>` | Adds one to this project and connects it |
| `/mcp key NAME value` | Saves a key a server needs, then reconnects the servers that use it |
| `/mcp test <name>` | Re-reads the config, reconnects one server and shows `ready` or the problem in plain words (`/mcp reconnect` is the same) |
| `/mcp remove <name>` | Removes it from this project's `.prism/mcp.json` and disconnects it (`rm` and `delete` work too) |

`/mcp add` never overwrites an entry that is already in `.prism/mcp.json`: it
leaves it as you wrote it and just reconnects.

### The built-in list

All of these are free and were checked to start inside Prism's container
(Node.js is included; there is no `uv`, so Python servers are not offered).
The numbers are the order used by `/mcp add <number>`.

| Name | What it does | Needs |
| --- | --- | --- |
| `fetch` | Reads any web page or JSON URL as clean text | nothing |
| `context7` | Current docs and code examples for popular libraries | nothing |
| `deepwiki` | Explains how any public GitHub repo is built | nothing |
| `github` | Issues, pull requests and code on GitHub | free `GITHUB_TOKEN` from https://github.com/settings/tokens |
| `tavily` | Web search (free plan, 1000 searches a month, no card) | free `TAVILY_API_KEY` from https://app.tavily.com |
| `git` | Proper git tools: log, diff, blame, commits | nothing |
| `memory` | A small notebook of facts that Prism recalls later | nothing |
| `thinking` | A scratchpad for breaking hard problems into steps | nothing |
| `files` | Directory trees, search and bulk reads (Prism already reads and edits files, so this is optional) | nothing |

The list lives in `cli/mcp_catalog.json`.

### Where keys are saved

Run `/mcp key NAME value` and Prism writes `NAME=value` to `mcp.env` in its
settings folder. Inside the Docker container that is
`/home/prism/.config/prism/mcp.env`, a volume that survives restarts. In
`prism fix` mode (Prism running directly on your computer) the settings folder
is `~/.config/prism` on Linux (or `$XDG_CONFIG_HOME/prism` if you set it) and
`~/Library/Application Support/prism` on macOS. The file is readable only by
you. The key is never put into the project, never sent to the AI and never
committed to git: `.prism/mcp.json` only contains a `${NAME}` reference. The
key name may only use letters, numbers and underscores.

`keys.conf` is a different thing. It holds the keys for the AI models and goes
to the router only; the container that runs MCP servers does not see it, so
putting an MCP key there does nothing. If you prefer, you can also export the
variable in the shell before starting Prism on the host (`prism fix`); a real
environment variable wins over `mcp.env`. (The Docker container does not
receive your shell variables, so inside Docker use `/mcp key`.)

### When something goes wrong

Prism explains problems in words and tells you what to do:

- **needs the key X** – the server wants a key you haven't saved. Follow the
  link it prints, then `/mcp key X <the key>`.
- **npx isn't installed** – Node.js is missing. Inside Docker this doesn't
  happen; on your own computer install it from https://nodejs.org. (For
  `uvx`, `uv`, `pipx` or `python` Prism tells you to pick a server that starts
  with `npx` instead.)
- **took too long to start** – the first run downloads the server, and Prism
  waits up to 90 seconds. Check your internet and run `/mcp test <name>`.
- **crashed right after starting** – usually a wrong key or no internet. The
  last line the server printed is shown in parentheses.
- **refused the key** – the server answered HTTP 401 or 403: the key is wrong
  or expired. Save a new one with `/mcp key`.
- **the address wasn't found** – HTTP 404: check the `url` in `.prism/mcp.json`.
- **can't reach it** – no internet connection, or the host name is wrong.
- **has no "command" or "url"** – the entry is incomplete. Fix it in
  `.prism/mcp.json` or `/mcp remove` it.

The rest of this page is for people who want to write the config by hand.

## Where the config lives

Prism reads two config files when it starts:

- `mcp.json` in Prism's settings folder applies to every project. On Linux
  that is `~/.config/prism/mcp.json`; on macOS (`prism fix`) it is
  `~/Library/Application Support/prism/mcp.json`.
- `.prism/mcp.json` inside a project folder applies only to that project. If
  both files define a server with the same name, the project entry replaces the
  global one as a whole.

Inside the Docker container, home is `/home/prism` and your project folder is
mounted at `/workspace`, so the files live at
`/home/prism/.config/prism/mcp.json` and `/workspace/.prism/mcp.json`.
Beginners should prefer `.prism/mcp.json` in the project root: it is on your
computer in the project folder (so you can edit it with any editor), and it can
be committed to git (using `${VAR}` references instead of real secrets). The
global file inside Docker also survives restarts, but it lives in a Docker
volume that is awkward to reach from the host.

After editing a file by hand, run `/mcp test <name>` to load the change for that
server, or restart Prism. `/mcp add`, `/mcp key` and `/mcp remove` take effect
right away.

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

- An entry needs either `command` (local) or `url` (remote). If it has both,
  `url` is used.
- `env` sets extra environment variables for a local server (it also receives
  Prism's own environment), `headers` sets HTTP headers for a remote one.
- Values in `env` and `headers` may contain `${VAR}`, which Prism replaces with
  the value of the environment variable `VAR`, or with the value saved by
  `/mcp key VAR ...` (a real environment variable wins). This is the right way
  to pass tokens instead of writing them into the file. A server whose `${VAR}`
  has no value shows `error` with a note on how to set it. `${VAR}` is **not**
  expanded in `command`, `args` or `url`.
- Add `"disabled": true` to a server to switch it off without deleting it. It
  shows as `off`.
- The Docker image has Node.js (with npm and npx), git and python3, so servers
  started with `npx` work out of the box. There is no `uv`, so Python based
  servers that need it will not run.

## Seeing what is connected

Type `/mcp` inside Prism. It lists every server with its state and its tools.
States are `starting`, `ready`, `off` and `error`. An `error` line says in plain
words what is wrong and how to fix it. A broken config file shows
up as a server named `config` in the `error` state, with the file name and the
parse problem as the message.

Tools appear to the AI as `<server>__<tool>`. For example the filesystem
server above offers `filesystem__read_file`. Characters other than letters,
numbers, `_` and `-` in a name become `_`, long names are shortened (server to
24 characters, tool to 36), and if two servers end up with the same tool name
the later one gets `_2`, `_3`... added.

Limits: a server may offer at most 128 tools, a tool call is stopped after 3
minutes, and a reply longer than 30000 characters is cut. Only text replies are
shown to the AI; images and other binary results appear as a "not shown" note.
Crew workers cannot use MCP tools.

## Approving tool calls

In `prism fix` mode (computer help, `prism -system`) every MCP tool call is
shown to you first, and the AI only proceeds after you approve it. In normal
chat mode and in `prism web`, MCP tools run without asking, so only add servers
you trust.

## Examples

All of these are free to use. Each block below is a complete, pasteable
`mcp.json` file. Save it as `.prism/mcp.json` inside your project (recommended
for beginners -- it stays with the project and works inside the container
without any extra volume setup) or as `~/.config/prism/mcp.json` to apply it
to every project.

To combine several servers, merge their entries under one `mcpServers` key.

### 1. Filesystem

Lets the AI read and write files in a folder you choose. Give it `/workspace`
so it can see your project inside the container. (Prism already reads and edits
files itself, so this is only useful for the extras such as directory trees.
In `prism fix` mode there is no `/workspace`; use a real folder path instead.)

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
    }
  }
}
```

### 2. Fetch web pages

Lets the AI pull down a URL and read the page as text or Markdown.
`mcp-fetch-server` is a pure-Node package with no native dependencies, so it
works on Linux and inside the Docker container.

```json
{
  "mcpServers": {
    "fetch": {
      "command": "npx",
      "args": ["-y", "mcp-fetch-server"]
    }
  }
}
```

### 3. Git

Gives the AI proper git tools (log, diff, commit search) instead of running
shell commands. After it connects, run its `git_set_working_dir` tool with
`/workspace` first (or just ask Prism to set the git folder to `/workspace`),
otherwise the tools look in the wrong folder.

```json
{
  "mcpServers": {
    "git": {
      "command": "npx",
      "args": ["-y", "@cyanheads/git-mcp-server"]
    }
  }
}
```

### 4. Browser automation

Drives a real browser: open pages, click, fill forms, take screenshots. Useful
for checking the website you are building.

```json
{
  "mcpServers": {
    "browser": {
      "command": "npx",
      "args": ["-y", "@playwright/mcp@latest"]
    }
  }
}
```

Note: this one is not in Prism's built-in list and has not been tested inside
the container. The Docker image has the system libraries a browser needs
(libnss3, libatk, libgbm and others) but no display, so a window cannot open,
and the browser itself is not bundled. If it fails inside Docker, try adding
`"--headless"` to `args`, and ask the AI to install the browser when it
reports one is missing. On a host machine with a desktop it works as written.

### 5. GitHub (free account needed)

Reads issues, pull requests and code from GitHub. Uses GitHub's hosted remote
server, so there is nothing to install.

Create a free personal access token at
https://github.com/settings/tokens (a classic token with `repo` scope covers
most uses). Save it inside Prism with `/mcp key GITHUB_TOKEN ghp_yourtoken`
(or `export GITHUB_TOKEN=...` in the shell before starting Prism on the host).

Then add it with `/mcp add github`, or paste this into `.prism/mcp.json`:

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

Never commit the token or the config file with the token written in it. Keep
the token in the environment variable only, and check `.prism/mcp.json` into
git only when it references `${VAR}` names, not real secrets.

## Troubleshooting

| Problem | Likely cause | Fix |
| --- | --- | --- |
| Server shows `error` in `/mcp` | Command missing, bad args, or the server crashed during startup | Read the message in `/mcp`, or run `/mcp test <name>`. Try running the same `npx ...` command yourself in the container. Check for typos in `command` and `args`. |
| A server named `config` shows `error` | A `mcp.json` file is not valid JSON | The message names the file. Fix the syntax, usually a missing comma or quote. |
| First `npx` start is slow or says `took too long to start` | npm downloads the package on first run; Prism waits up to 90 seconds | Run `/mcp test <name>` again. Later starts use the cache and are fast. The GitHub example avoids this by being remote. |
| Server is `ready` but the AI never uses the tool | The AI does not see a need, or the name is unclear | Ask directly, for example "use the fetch tool to read this page". Check `/mcp` to see the exact tool names and mention one. |
| Tool call keeps asking for approval | You are in `prism fix` mode | Expected behaviour: every MCP call is approved by you in fix mode. Use normal chat mode if you do not want prompts. |
| `${VAR}` in `args` or `url` is passed through unchanged | Only `env` and `headers` are expanded | Move the value into `env` (local server) or `headers` (remote server). |
| `${VAR}` comes through empty | The variable is not set in the environment Prism runs in | Run `/mcp key VAR value` inside Prism. It is saved in the settings folder and the server reconnects. (`keys.conf` does not reach MCP servers.) |
