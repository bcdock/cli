# Changelog

All notable changes to the `bcdock` CLI are recorded here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the
project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/bcdock/cli/compare/v0.1.3...HEAD
[0.1.3]: https://github.com/bcdock/cli/releases/tag/v0.1.3
