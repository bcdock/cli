---
name: bcdock
description: Use before running any `bcdock` command, and whenever a task involves BCDock or Business Central sandbox environments - listing, creating, publishing AL apps to, hibernating or deleting them. Explains how to find commands with --help, read -o json output, wait for long operations, and act on exit codes.
---

# BCDock (the `bcdock` CLI)

BCDock runs Business Central sandbox environments. The `bcdock` CLI calls the same public Platform
API the portal uses. Not every portal action has a command yet, and creating API keys or adding
team members needs a signed-in person. If `--help` shows no command for what you need, say so
rather than guessing.

## Discover commands, don't recall them

The CLI gains commands and flags between releases, so commands you remember may be wrong. Ask the
binary instead:

- `bcdock --help` - the command groups
- `bcdock <group> --help` - the commands in a group, for example `bcdock env --help`
- `bcdock <group> <command> --help` - flags and examples for one command

The reference at https://docs.bcdock.io/cli/ is generated from the same help text.

## Conventions every command shares

- Use `-o json` for anything you parse, and read fields from the JSON, not the table. For
  example, list environments with `bcdock env list -o json`.
- Use `--wait` (with `--wait-timeout`) to block until a long operation finishes, instead of
  polling. To wait for a state, use `bcdock env wait <env> --status running`.
- Exit codes: `0` ok, `1` error, `3` auth, `4` rate-limited, `5` not found. A failed provisioning
  exits `1`, and so does a `--wait` that times out on create, resume, hibernate or delete. `124`
  comes only when `bcdock env wait` or `bcdock me export --wait` gives up waiting. On a timeout,
  stop and ask the human rather than looping.

## Authentication

The token is in the `BCDOCK_TOKEN` environment variable. Don't run `bcdock auth login` (it is
interactive), don't pass `--token` on the command line (it shows in process listings), and don't
write tokens to disk.

## Safe defaults

- When you have finished with an environment, hibernate it: `bcdock env hibernate <env> --wait`.
  It keeps the work and costs far less than leaving it running.
- Delete an environment only when the human explicitly asks. Deletion is destructive.
- If a step fails, show the error and ask before retrying.
