# AGENTS.md - guidance for AI agents using BCDock

> ⚠️ **Beta CLI - surface may change.** Both `bcdock <verb>` commands and `--output json` field names can change without deprecation cycles before `v1.0.0`. If a flag or field this file documents doesn't match what `bcdock <verb> --help` says today, **trust the binary, not this file**. File an issue (via support@bcdock.io) so we can update the docs.

> If you are a coding agent (Claude, GPT, Copilot, Cursor, Aider, an autonomous workflow) and a user asked you to help them with BCDock, **start here**. Reading this end-to-end will save you 5-10 wrong tool calls.

## What BCDock is, in one paragraph

BCDock is a hosted platform that gives Microsoft Dynamics 365 Business Central developers a working BC environment in minutes instead of hours. Each environment is a Docker container running BC on a managed Azure VM. Users get a Web Client URL, credentials, and the ability to publish AL extensions - without managing VMs, Docker, or the BC artefact catalogue themselves.

## What `bcdock` is, in one paragraph

`bcdock` is the primary client. It wraps the BCDock Platform API as a stable command-line surface. Humans use it; AI agents use it; CI workflows pin to it. Every customer-facing operation is reachable via `bcdock <verb>` - there is no portal-only feature in the supported workflow. If you can't do it with `bcdock`, it's either a CLI gap (please report) or an admin operation that requires support contact.

## Rule of thumb for agents

**One CLI call per intent. Read the output. Decide the next step.** Do not chain six commands speculatively. Do not invent flags. Do not assume an endpoint exists because it would be convenient. When in doubt: `bcdock <verb> --help`.

---

## 0. Before you run anything: discover, then act

The single most common agent mistake with BCDock is picking a BC version without checking what's cached. Picking a stale or unavailable version triggers a **78-minute image build** before the environment can even start provisioning. The user will not be happy.

**Always run this before `env create`:**

```bash
bcdock artifacts list --fast-only --output json
```

The `--fast-only` flag restricts results to versions whose Docker image is already cached on a warm pool - those create in 5-10 minutes. Pick from this list. If the user explicitly requests a version not on the list, **tell them it'll take ~78 minutes** before proceeding.

The same logic applies to country code and region:

```bash
bcdock config regions --output json    # which Azure regions are enabled
bcdock artifacts list --fast-only --output json | jq '[.[] | {version, country, location}] | unique_by(.version+.country+.location)'
```

Pull the `version`, `country`, and `location` triple from a single row of the fast-only list. Do not hardcode `version: "26.0.123456"` based on prior knowledge - the catalogue rotates.

---

## 1. Authenticate

Three paths, in order of agent preference:

### a. Already-set environment variable (preferred for CI / non-interactive agents)

```bash
export BCDOCK_TOKEN=bdk_xxxxxxxxxxxxxxxxxxxx
bcdock auth whoami      # verify
```

If `BCDOCK_TOKEN` is set in the environment when you start, the user is already authenticated. Don't re-run `auth login`.

### b. Token file (interactive humans, persisted)

```bash
bcdock auth login --email you@example.com
# → CLI sends a 6-digit code to the email
# → CLI prompts "Code:" and writes the token to ~/.bcdock/credentials.json (mode 0600)
```

If you're driving an interactive terminal, this is the right path. For non-interactive automation (`--otp` flag is available if the agent has out-of-band access to the email):

```bash
bcdock auth login --email you@example.com --otp 123456
```

### c. Per-command flag (one-off operations)

```bash
bcdock --token bdk_xxxx env list
```

Avoid this in scripts - it leaks the token into shell history.

### Confirm who you are

```bash
bcdock auth whoami --output json
# { "email": "...", "displayName": "...", "platformRole": null, "companyName": "..." }
```

If `platformRole` is `super_admin` or `support`, you're a staff account - most agent flows assume customer-account perspective. Behave accordingly.

---

## 2. Five workflows you'll actually use

### Workflow A - create an environment

```bash
# 1. Discover (see § 0)
bcdock artifacts list --fast-only --output json

# 2. Pick a row, then create
bcdock env create --name dev --version 26 --country au --location australiaeast

# 3. Watch until it's ready (or fail-fast)
bcdock env wait dev --timeout 15m

# 4. Print connection info
bcdock env get dev --output json
# → { "name": "dev", "status": "running", "webClientUrl": "https://...", "username": "...", "password": "..." }
```

The default state machine: `Queued → Pending-Pool → Provisioning → Running` (5-10 min on a fast version, 78+ min on a cold one). If `env create` returns immediately with a UUID, the work continues server-side; `env wait` is non-blocking until the env reaches `running` or `failed`.

### Workflow B - publish an AL extension

```bash
# Compile against the target environment's symbol set
bcdock al compile --env dev

# Publish the .app to the running container
bcdock env publish dev --app ./output/MyExtension_1.0.0.0.app --schema-update-mode Synchronize
```

`bcdock al compile --env dev` downloads symbols from the live environment to `./.alpackages/`, runs `alc.exe`, and writes the `.app` to `./output/`. No PowerShell, no BcContainerHelper.

### Workflow C - hibernate when idle, resume when needed

```bash
bcdock env hibernate dev      # stops container, releases compute; storage charges continue
bcdock env resume dev         # restarts container; ~30s
bcdock env list --output json | jq '.[] | {name, status}'
```

Hibernation is the primary cost-control lever. If the user is going home for the day, hibernate. If billing is a concern, suggest it.

### Workflow D - get credentials for an existing environment

```bash
bcdock env get my-env --output json | jq '{webClientUrl, username, password}'
```

Credentials rotate per-environment; they are not the user's portal login.

### Workflow E - clean up

```bash
bcdock env delete my-env --confirm my-env
```

The `--confirm <name>` flag is required for destructive operations. Pass the environment name back as confirmation - the CLI refuses if it doesn't match. Don't try to bypass.

---

## 3. Output formats

Every command supports:

```bash
-o table     # default, human-readable, NOT machine-parseable
-o json      # canonical machine output - use this when scripting
-o csv       # rare; useful for spreadsheet handoff
```

**Always use `-o json` when an agent will parse the output.** Table format prioritises readability - column widths, truncation, ANSI colour - and is not a stable contract. JSON output schemas are stable additive within a major CLI version (additions OK, removals/renames flagged in CHANGELOG).

Combine with `jq` for downstream extraction:

```bash
bcdock env list --output json | jq -r '.[] | select(.status == "running") | .name'
```

---

## 4. Errors and exit codes

Exit codes are deterministic:

| Code | Meaning | Agent response |
|---|---|---|
| `0` | Success | Continue |
| `1` | Generic error (network, parse, unknown) | Read stderr; surface to user |
| `2` | Usage error (bad flag, missing arg) | You typed the command wrong - fix it |
| `3` | Not authenticated | Run `bcdock auth whoami` to confirm; re-auth if needed |
| `4` | Authorisation failure (token valid but lacks scope) | Surface to user; you may need a different role or API key scope |
| `5` | Resource not found (404) | Confirm the name/id; list to see what's there |
| `6` | Conflict (409) - resource already exists, or operation invalid in current state | Read the error; the resource may already be in the desired state |
| `124` | Timeout (e.g. `env wait` ran out) | Increase `--timeout`; or treat as transient |

The error format is canonical: every API-side error returns `{ "error": "...", "code": "...", "details": {...} }`. The CLI prints `error: <message>` to stderr by default; pass `--output json` to get the structured shape back.

Examples worth recognising:

```bash
$ bcdock env create --name dev --version 99
error: VERSION_NOT_AVAILABLE: BC version 99 is not in the catalogue.
       Run 'bcdock artifacts list' to see available versions.
$ echo $?
1
```

```bash
$ bcdock env delete dev
error: CONFIRMATION_REQUIRED: pass --confirm dev to delete this environment.
$ echo $?
2
```

---

## 5. Hints that save you turns

- **Don't hardcode versions.** Always discover. Memory: a `--version 26.0.99999` you've seen in a prior conversation may have rotated out of the catalogue.
- **Don't poll faster than 3 seconds.** The API rate-limits at 600 req/min/token authenticated; tighter loops will get you `429 Too Many Requests`. `bcdock env wait` already picks the right cadence.
- **`--quiet` for CI logs.** Cuts the progress noise; the final result still prints.
- **`--no-color` for log parsing.** ANSI codes break grep/jq downstream.
- **Prefer `--output json` over screen-scraping table output.** Table widths change between versions.
- **`bcdock --help` is fast.** No network round-trip, no auth required. Cheaper than guessing.
- **Idempotency.** Most read operations (`env get`, `env list`, `auth whoami`) are safe to retry freely. Mutations (`env create`, `env delete`, `env hibernate`) are not idempotent today - repeated calls may return 409 conflicts.

---

## 6. What's intentionally not in this file

The full per-command reference: see [docs.bcdock.io/cli](https://docs.bcdock.io/cli) or run `bcdock <verb> --help`.

The HTTP API: not documented for end users. The CLI's `--verbose` mode prints requests, but the API is internal and may change without deprecation cycles. Don't write tooling that hits `api.bcdock.io/api/v1/*` directly - use the CLI, which absorbs the churn.

Admin / staff verbs: not in this binary. The internal `bcdockadm` binary (private) covers them. Customer-account agents will never need them.

---

## TL;DR for an agent on its first turn

```bash
bcdock auth whoami                            # confirm auth
bcdock artifacts list --fast-only -o json     # discover fast versions
bcdock env list -o json                       # what already exists
```

Three commands, three reads of the world. Then plan with the user.
