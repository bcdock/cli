# Changelog

All notable changes to the `bcdock` CLI are recorded here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the
project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Dates are **Melbourne** (the project's timezone), not UTC. They can differ: `v0.3.0`
published at `2026-09-05T23:51:40Z`, which is `2026-09-06` locally. Every entry before it
happened to fall on a day where the two agreed.

## [0.5.7] - 2026-10-09

### Added
- **The rest of the portal, from the command line (#715).** With these, every customer action in
  the portal has a `bcdock` command, except creating an API key (it needs a signed-in person) and
  switching plan (it opens with subscriptions).
  - `bcdock companies set-location <country-code> [--region <azure-region>]` sets the active
    company's default country and region for new environments. Owners and admins only.
  - `bcdock me set-timezone <iana-timezone>` sets the time zone the portal shows your times in. The
    id is checked before it is sent, so a typo fails here.
  - `bcdock me billing export [--from --to] [--out file]` downloads per-environment usage and cost
    as CSV, streamed to stdout or a file.
  - `bcdock me billing cycle [--from --to]` shows the company's totals for a period and how much of
    the plan's included time is used (the trial).
  - `bcdock me export latest [--out file]` shows your most recent data export and downloads it
    when ready, without starting a new one.
  - `bcdock plans list` lists the subscription plans and their active rates.

## [0.5.6] - 2026-10-09

### Fixed
- **No prompts when there is no terminal.** The CLI took `/dev/null` for a terminal (it is a
  character device), which is how agents and CI jobs often run. So `bcdock env create` with no
  flags opened the interactive picker and failed on end of input, and `bcdock auth keys revoke`
  printed its prompt before refusing. Both now check for a real terminal: without one,
  `env create` says to pass `--version`, `--country` and `--region`, and `revoke` says to pass
  `--yes`. At a terminal nothing changes.

## [0.5.5] - 2026-10-09

### Added
- **`bcdock env create --accept-insider-eula`** creates a preview (insider) BC version. Passing
  the flag accepts the Business Central Insider EULA for that environment; it is never accepted
  without it. When the flag is missing, the error now says so instead of stopping at
  `INSIDER_EULA_REQUIRED`.
- **`bcdock auth keys list`** shows your company's API keys: id, name, prefix, scopes, and when
  each was created, last used and expires. Secrets are never shown.
- **`bcdock auth keys revoke <id>`** revokes one, after a prompt or with `--yes`. Without a
  terminal (scripts, agents) `--yes` is required. Keys are still created in the portal or by
  `bcdock auth login`.

### Fixed
- **`bcdock env create --region` no longer calls a preview version invalid.** Its catalog check
  left preview versions out, so a preview version was refused as an invalid `--version` before
  the platform could say what it needed.

## [0.5.4] - 2026-10-09

### Added
- **A Claude skill for `bcdock`**, in `skills/bcdock/SKILL.md`. Put it in `.claude/skills/bcdock/`
  and Claude Code loads it when a task involves Business Central or `bcdock`: it finds commands
  with `--help` instead of recalling them, reads `-o json`, waits with `--wait`, and hibernates
  rather than deletes. The [agent guide](https://docs.bcdock.io/guides/claude-code/#project-setup)
  also has an `AGENTS.md` block for other coding agents.
- **`bcdock artifacts request <version> --country <c>`** asks BCDock to build a BC version that has
  no ready image yet, the same as the portal's "Request this version". When `bcdock env create`
  refuses a version for having no ready image, its error now prints this command with the
  version filled in, so an agent can follow it instead of stopping.
- **`bcdock env retry <env>`** retries provisioning an environment that failed (the portal's
  Retry); `--wait` blocks until it is running or has failed again.
- **`bcdock env rename <env> <display-name>`** changes the name an environment shows in the
  portal and in `env list`. Its URLs and credentials stay the same.

### Changed
- **The help no longer quotes timings it can't back.** `env`, `env create`, `artifacts`,
  `artifacts list` and the `help` topic now say a new environment takes **about 15 minutes**
  (it said ~7-15 on a warm pool), and give no resume time (it said ~7-15). They also stop
  promising a "~78 minute image build on first use": the platform refuses a version with no
  ready image, so the help now says such a version must be requested from the portal first.

## [0.5.3] - 2026-10-08

### Changed
- **`bcdock version` also prints the date of the commit it was built from** (`COMMIT DATE`, and
  `commitDate` in `-o json`), so you can tell how old an installed build is without looking it
  up. A build made with a plain `go build` prints `unknown` for the commit and its date.

### Fixed
- **Tables no longer print memory addresses.** In table and CSV output, a field that can be empty
  (the environment cap and the subscription in `bcdock me billing show`, for example) printed as
  a memory address such as `0x37bc76fa3870`. It now prints its value, and `-` when it is empty.
  `-o json` is unchanged.

## [0.5.2] - 2026-10-01

### Changed
- **`bcdock auth set-token` reads the key from stdin**, so it stays out of your shell history
  and the process list: `pbpaste | bcdock auth set-token`, or run it and paste the key when
  asked. Passing the key as an argument still works but prints a warning, because the key is
  then saved in your shell history.

### Fixed
- **`env hibernate --wait` says why a hibernation failed.** When a hibernation fails and your
  environment is still running, the environment records the reason. `--wait` used to exit 1
  with only `hibernate failed (status: running)`; it now prints that reason, for example
  `Hibernation failed: <why>; your environment is still running (status: running)`.
  `env wait` adds the same reason when an environment settles in a status you did not wait
  for (it still exits 124).

## [0.5.1] - 2026-10-01

### Fixed
- **`auth signup` says when it did not create your first environment, and why.** When the
  invite carries a BC configuration (from the waitlist) or you pass one, signup creates your
  first environment. If that configuration can no longer be created - most often a version
  with no ready image any more - the account is still activated and signup prints
  `No environment was created: <reason>`, with how to create one (`bcdock env create`).
  It used to report success and queue an environment that could not start.

### Changed
- **`bcdock auth login` keys carry two more scopes: `billing:write` and `account:write`.** `me export`,
  `me delete` and `me cancel-deletion` now need `account:write`, and `billing checkout` / `billing portal`
  need `billing:write`, so a key minted for CI with only `env:read` can no longer delete the account,
  export its data or change the plan. A key from an earlier `bcdock auth login` lacks the new scopes:
  those commands answer with a 403 that says to **run `bcdock auth login` to refresh your key**.
- **An API key with only `billing:write` can no longer read environment data** (environments,
  logs, stats, health). Environment reads need `env:read` or `env:write`; billing and usage reads
  accept `env:read`, `env:write` or `billing:write`. Keys from `bcdock auth login` carry `env:read`,
  so every `bcdock` command that reads environments, billing or usage works as before.
- The `--bc-version` help and the `auth signup` / `auth join-waitlist` examples use a full
  version (or `<version>` from `bcdock artifacts list --region <r> --fast-only`) instead of
  `25.5`, which the API refuses.

## [0.5.0] - 2026-09-30

### Fixed
- **A failed environment stops a `--wait` at once, with the reason, and exits `1`.** The
  API reports a failure as status `error` (or `failed-debug`); the CLI waited for
  `failed`, which the API never sends. So `env create --wait` and `env resume --wait` on a
  failed environment ran to the full timeout (30 minutes) and printed
  `timed out (current status: error)` without saying why. They now stop at the first poll
  that sees the failure, print the environment's error message, and exit `1`.
  `env hibernate --wait` and `env delete --wait` do the same; a delete that ends in
  `error` is no longer reported as a success.
- **A refused create says why and what to do.** `env create` with a short version
  (`--version 27`) printed only `error: invalid_input`; the API's sentence ("No BC artifact
  for 27 ... Run 'bcdock artifacts list ...'") was dropped. Any error in that shape now
  prints its message.
- **A version with no ready image says so.** The API refuses it with
  `request_version_required` and no message, and the CLI printed that code and nothing
  else. It now names the version and the region and says to pick from
  `bcdock artifacts list --region <region> --fast-only`, or to run `bcdock env create`
  with no version for the picker. With `-o json` the `error` code is unchanged.
- **Confirming a platform upgrade on resume gives the CLI command.** The API's message
  said "Resubmit with targetVersion"; the CLI now ends it with
  `bcdock env resume <env> --version <version> --wait`.

### Changed
- **`env wait` exits `1`, not `124`, when the environment fails.** Old: an environment
  that reached a failure status you did not ask for was documented as exit `124`, and in
  practice the wait ran to `--timeout` and then exited `124`. New: it exits `1` at once,
  with the environment's error message. `124` still means the wait gave up: `--timeout`
  elapsed, or the environment settled in another status you did not ask for (such as
  `hibernated`). A wait that asks for `--status failed` now exits `1` with the reason too,
  since the API never reports `failed`. To treat a failure as a success, ask for
  `--status error`.
- Help text and docs name the real statuses: `error` and `failed-debug`, not `failed`.
- Help examples and the `--version` flag text use full versions (or `<version>` from
  `bcdock artifacts list --region <r> --fast-only`) instead of `25.5`, which the API
  refuses. The `help` topics list the `error` status and give resume as ~7-15 minutes.

## [0.4.2] - 2026-09-27

### Fixed
- **`--help` no longer lists exit codes the CLI never returns.** `env create`,
  `env hibernate`, `env resume`, `bcdock --help` and `bcdock help` listed `10`
  ("provisioning failed") and `2` ("still provisioning"). A failed provisioning,
  hibernate or resume, and a `--wait` that times out on those commands, exit `1`.
  `124` is returned when a wait gives up: `env wait`, or `me export --wait`. The
  behaviour is unchanged; the help now describes it, matching the exit-codes page.
- **`env delete --wait` exits `0` when the delete succeeds.** It used to print
  `[deleting] complete (100%)`, then `error: not_found: Environment not found`, and exit
  `5`: once an environment is deleted it is no longer found, and the wait read that as a
  failure. It now prints `Deleted.` and exits `0`. An environment that did not exist
  still exits `5`, before anything is deleted.

## [0.4.1] - 2026-09-26

### Changed
- **`bcdock auth login` says where a missing code went.** Before the `Code:` prompt it
  prints `No code? Check your Junk or Spam folder.`, and `bcdock auth join-waitlist` ends
  with `No email? Check your Junk or Spam folder.` Some mail providers file BCDock's
  email as junk. Both lines go to stderr; `--otp` (no one waiting at a prompt) prints
  nothing extra.

## [0.4.0] - 2026-09-26

### Added
- **With `-o json`, a failed command writes one JSON object to stderr** instead of
  the `error: ...` line, so a script or an agent can act on the failure without
  parsing text:

  ```
  {"error":"invalid_state","message":"invalid_state: Only running environments can be hibernated (current status: hibernated).","exitCode":1,"status":400}
  ```

  `error` is the API's own code when it sent one (`not_found`, `invalid_state`,
  `invalid_input`, ...), otherwise one of a small documented set (`timeout`,
  `network`, `cli_error`, and the API's status names). `message` is the text the
  line would have shown, and `status` appears only for an API error. The exit code
  is unchanged, stdout stays empty, and table and CSV output keep the text line.
  See the exit-codes page for the full list.

## [0.3.1] - 2026-09-26

### Fixed
- **`bcdock env create --wait -o json` and `bcdock env resume -o json` now print
  the environment record**, the same camelCase object `env get -o json` returns,
  including `id`. Before, they printed the table row encoded as JSON: Go field
  names (`Name`, `ShortID`, `Version`, `Country`, `Status`, `Progress`, `Region`,
  `Created`) and no `id`, so a script could not take the environment it had just
  created or resumed and pass it to the next command.

  The same applies to `env create --manifest` when an app fails to publish: the
  environment exists and is running, and the JSON it prints now carries the `id`
  you need to re-publish into it or delete it.

  **If a script parses those PascalCase keys, update it** to the record's keys
  (`name`, `shortId`, `status`, ...), for example `jq -r .id`. Table and CSV output
  are unchanged.

## [0.3.0] - 2026-09-06

### Added
- **`bcdock env query <env> <odata-path>`** reads business data out of an
  environment over its OData v4 endpoint, so answering a question from the data
  no longer means holding a credential to do it:

  ```
  bcdock env query my-env Company -o json
  bcdock env query my-env --company "CRONUS AU" Chart_of_Accounts --select "No,Name" --top 3
  bcdock env query my-env '$metadata'
  ```

  OData v4 serves the entity sets your environment publishes as web services, so which
  nouns exist varies by environment. `Company` (capital, singular) is a BC system entity
  set and is always present; for everything else, `$metadata` lists what this environment
  actually serves. A name BC does not serve answers 404, not an empty list.

  The CLI fetches the environment's web-service access key once per invocation
  from the same audited reveal `env credentials` uses, and never prints it. It is
  not accepted as a flag or an argument anywhere, so it stays out of your shell
  history and the process list.

  Read-only is a property of the code rather than a promise: the verb issues GET
  and there is no flag that changes it. A path that is an absolute URL, escapes
  the OData root, or names `$batch` is refused before any call is made - the
  first of those because the verb authenticates with your access key and will not
  send it to a host you named in an argument.

  Options: `--company`, `--tenant`, `--filter`, `--select`, `--top`, `--timeout`,
  `--insecure`. Paging, caching and writes are deliberately out of scope.

## [0.2.0] - 2026-09-05

### Changed

- **`bcdock env get` no longer returns the BC admin password or the web-service
  access key.** Reading an environment happens constantly - on every portal page
  load, in every `env wait` poll, in every script - and a credential that rides
  along on all of it ends up in far more places than you chose to put it. The
  username stays; it is not secret.

  Ask for the secrets explicitly instead, and each reveal is recorded in your
  audit trail:

  ```
  bcdock env credentials <env>
  bcdock env credentials <env> -o json
  ```

  `env publish`, `env download-symbols` and `env create --manifest` do this for
  you - one reveal per invocation, no flags to pass.

  **v0.1.3's `publish` and `download-symbols` stop working against this
  platform; upgrade to v0.2.0.** They read the password from the environment
  response, which no longer carries one, and the error they print
  (`has no admin credentials yet`) names the wrong cause.

### Added
- `bcdock env create --manifest <path>` builds an environment from a
  `bcdock.manifest.yaml` recipe and publishes its apps in manifest order. The
  manifest is parsed and fully validated **before any API call**, so a recipe
  naming an `.app` that is not on disk fails with nothing created. `--manifest`
  implies `--wait`, because apps can only be published into a running
  environment. Run `bcdock env create --help` for the full flag contract.

  Flag precedence is **an explicitly passed flag > the manifest > the built-in
  default**. "Explicitly passed" means you typed the flag, not that it has a
  value: `--type` defaults to `sandbox` and `--multi-tenant` to `true`, so a
  manifest saying `artifactType: OnPrem` is honoured unless you actually pass
  `--type`.

  An unknown `schema` version and any unknown key are both hard errors, so a
  manifest written for a later CLI is refused rather than silently
  half-applied.

  `github:` apps pin by **tag, not by digest**. A tag can be moved, so the same
  manifest at two different times can install different bytes with nothing
  reporting it. Repeatable, not reproducible; a lockfile is deliberately out of
  v1 scope.

- `bcdock env credentials <env>` reveals an environment's BC admin password and
  web-service access key. This is the only path that returns them, and every
  reveal writes an audit-trail entry. See the `Changed` note above.

- `bcdock env create --insecure` skips TLS verification when publishing the
  manifest's apps to the BC dev endpoint, for a stack with a self-signed
  certificate. It scopes to the publish leg only - the create call itself is
  unaffected - and matches the flag `env publish` and `env download-symbols`
  already carry.

## [0.1.3] - 2026-06-14

Released without a changelog entry. The tag and its generated notes are the
only record: [`v0.1.3`](https://github.com/bcdock/cli/releases/tag/v0.1.3).

## [0.1.2] - 2026-06-14

Released without a changelog entry, and never mirrored to the public repo - the
monorepo tag `cli/v0.1.2` is the only record.

## [0.1.0] - 2026-05-13

### Added
- Initial public release of the `bcdock` CLI.
- `auth` - sign in via OTP, manage tokens, generate scoped API keys.
- `env` - create, list, get, hibernate, resume, delete, wait, logs, publish AL extensions, fetch symbols, generate `launch.json`.
- `al compile --env` - compile AL projects with the `alc` matching the target BC version.
- `me` - export account data, request account deletion, cancel deletion.
- `companies` - list and switch billing context.
- `usage`, `billing` - usage and billing visibility.
- `config`, `artifacts` - discover available regions, BC versions, countries, artifact types.
- `version` - emit CLI + API major version (`v1`) for skew detection.
- Output formats: `--output table|json|csv`.
- Exit codes follow a documented schema (see `docs/cli/exit-codes.md`).

> No `v0.1.0` or `v0.1.1` tag exists in either repo, so this entry has no
> release to link to. The earlier link here pointed at
> `releases/tag/v0.1.0` and returned 404.

[Unreleased]: https://github.com/bcdock/cli/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/bcdock/cli/releases/tag/v0.3.0
[0.2.0]: https://github.com/bcdock/cli/releases/tag/v0.2.0
[0.1.3]: https://github.com/bcdock/cli/releases/tag/v0.1.3
