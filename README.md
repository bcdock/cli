# bcdock - BCDock CLI

[![Status](https://img.shields.io/badge/status-beta-yellow)]() [![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

> ⚠️ **Beta - expect change.** This CLI is in active beta. The command surface, flag names, output JSON shapes, and the underlying HTTP API will change before `v1.0.0` and there is **no deprecation guarantee** in beta. Pin a specific version in scripts and CI, and read [CHANGELOG.md](CHANGELOG.md) before upgrading. Do not build production tooling against this surface yet.

The official command-line interface for [BCDock](https://www.bcdock.io) - a managed platform for Microsoft Dynamics 365 Business Central environments on Azure.

`bcdock` is the primary tool for humans **and AI agents** to:

- Create, hibernate, resume, and delete BC environments
- Discover available BC versions, countries, and Azure regions before provisioning
- Compile and publish AL extensions against a live environment
- Inspect billing, usage, account state, and audit history

If you're an AI coding agent landing on this repository, start with [**AGENTS.md**](AGENTS.md).

---

## Beta status (read this before scripting against it)

**Nothing on this CLI is a stability contract yet.** During beta:

- Command names, subcommand layout, and flag names can change.
- Output JSON field names can change, be added, or be removed.
- Exit codes can change.
- The underlying HTTP API the CLI dials can change without notice.

| Surface | Status in beta | Status at `v1.0.0` and after |
|---|---|---|
| `bcdock <verb>` command tree | **May change.** Deprecation notices are best-effort, not guaranteed. | Stable. Breaking changes only with one full release of `--deprecated` warning. |
| `--output json` field names | **May change.** | Additive-only within a major; removals/renames flagged in CHANGELOG. |
| Exit codes | **May change.** | Stable. |
| `https://api.bcdock.io/api/v1/*` HTTP endpoints | **Not a public contract, in beta or after.** Observable via `--verbose`, but internal. Use the CLI; do not write tooling that hits the API directly. |

If you build a script today, expect to fix it at least once before `v1.0.0`. The shape of this trade-off is intentional and matches what `gh`, `aws`, `stripe`, and other vendor CLIs do once they reach `v1` - the CLI is the public surface; the backend it talks to is not. We are not there yet.

---

## Install

```bash
# Go users (works once the public repo is published)
go install github.com/bcdock/cli/cmd/bcdock@latest

# Other install paths (Homebrew tap, install script, GitHub release binaries) - coming with the v0.x release pipeline.
```

Verify:

```bash
bcdock version
# bcdock 0.x.y (commit abc1234, api v1)
```

---

## 30-second quickstart

```bash
# 1. Authenticate via emailed OTP (one-time, ~30s)
bcdock auth login --email you@example.com

# 2. Discover what BC versions + countries are cached in your region
#    (skip this and you may trigger a 78-minute image build)
bcdock artifacts list --fast-only

# 3. Create an environment from a cached version
bcdock env create --name dev --version 26 --country au

# 4. Wait until it's running, then print its WebClient URL + creds
bcdock env wait dev
bcdock env get dev

# 5. Open the WebClient URL in a browser, log in as the printed user.
```

For richer flows (publishing AL extensions, hibernation, billing), see [AGENTS.md](AGENTS.md) and [docs.bcdock.io](https://docs.bcdock.io).

---

## Documentation

- **Full reference**: [docs.bcdock.io/cli](https://docs.bcdock.io/cli) - generated from the same cobra command tree
- **Per-command help**: `bcdock <verb> --help` (no network round-trip)
- **Agent-oriented guide**: [AGENTS.md](AGENTS.md) - concrete commands, error handling, machine-readable output
- **Architecture**: [bcdock.io/architecture](https://www.bcdock.io/architecture)

---

## How this repository works

This is a **read-only public mirror** of the BCDock CLI source. Primary development happens privately; this mirror is updated on every tagged release.

- Bug reports and feature requests: email **support@bcdock.io** or use the in-app feedback (GitHub Issues are not actively triaged during the beta phase).
- Security issues: see [SECURITY.md](SECURITY.md) - please do **not** file via public Issues.
- Pull requests: not accepted today. See [CONTRIBUTING.md](CONTRIBUTING.md) for the rationale and what to do instead.

The mirror is here so that:

1. Customers and auditors can read the client that handles their credentials.
2. CI workflows can pin to a stable, versioned binary.
3. AI coding assistants helping their users with BCDock have a working reference for `bcdock <verb>` instead of guessing at command names.

---

## License

[MIT](LICENSE).
