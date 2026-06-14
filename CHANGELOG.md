# Changelog

All notable changes to the `bcdock` CLI are recorded here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the
project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/bcdock/cli/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/bcdock/cli/releases/tag/v0.1.0
