cat > README.md <<'EOF'
# taskbook

A tiny to-do list CLI. One file, `taskbook.py`, python3 standard library only,
built with `argparse`. State lives in a JSON database file.

    python3 taskbook.py [--db PATH] [--today YYYY-MM-DD] COMMAND ...

* `--db PATH`   database file, default `tasks.json` in the current directory.
  A missing file means "no tasks yet"; it is created by the first command that
  changes something. A file that is not valid JSON makes every command print
  `error: cannot read database` to **stderr** and exit with status 1.
* `--today DATE`  treat DATE as the current day (default: the real date). It
  only matters for "overdue" below.

Global options come before the command. Exit status: `0` success, `1` runtime
error (unknown task id, unreadable database), `2` usage error (anything argparse
rejects: bad choice, bad date, missing argument, no command, ...). All
`error: ...` messages go to stderr; normal output goes to stdout.

## Tasks

Every task has: `id` (int, 1, 2, 3, ... assigned in creation order and never
reused, even after `rm`), `title` (str), `priority` (`low`, `normal` or `high`),
`due` (`YYYY-MM-DD` string or null), `tags` (list of lowercase str) and `done`
(bool). A task is **overdue** when it is not done, has a due date and that date
is strictly before today.

## Commands

### add TITLE [--priority {low,normal,high}] [--due YYYY-MM-DD] [--tag TAG]...

Adds a task (`--priority` defaults to `normal`; `--tag` may be repeated). The
title is stored with surrounding whitespace stripped; an empty title, or an
invalid or impossible date such as `2024-02-30`, is a usage error (status 2).
Tags are lower-cased and duplicates dropped, keeping first-seen order.
Prints `added #<id>: <title>`.

### list [--status {open,done,all}] [--tag TAG] [--sort {id,priority,due}] [--json]

Lists tasks; `--status` defaults to `open`, `--sort` to `id`. `--tag` keeps tasks
carrying that tag (compared case-insensitively). Sort orders:

* `id`        ascending id
* `priority`  high, then normal, then low; ties by id
* `due`       earliest due date first, tasks without a due date last; ties by id

Text output is one line per task:

    #<id> [<mark>] <title> (<priority>)[ due <date>][ tags: <t1>,<t2>][ OVERDUE]

`<mark>` is `x` for done tasks and a space otherwise. The ` due`, ` tags:` and
` OVERDUE` parts only appear when they apply, in that order. If nothing matches
print `no tasks`. With `--json` print instead the matching tasks as a JSON array
produced by `json.dumps(tasks, indent=2, sort_keys=True)` (just `[]` when
empty), each task an object with exactly the keys described above.

### done ID

Marks task ID done and prints `done #<id>`. If it was already done print
`already done #<id>` (status 0). Unknown id: stderr `error: no task #<id>`,
status 1. A non-integer id is a usage error.

### rm ID

Deletes task ID and prints `removed #<id>`. Unknown id: stderr
`error: no task #<id>`, status 1.

### stats

Prints exactly these five lines (all tasks counted, except the last line which
counts open tasks only):

    total: <n>
    open: <n>
    done: <n>
    overdue: <n>
    open by priority: high=<n> normal=<n> low=<n>
EOF
