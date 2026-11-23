# bcdock - BCDock CLI

[![Status](https://img.shields.io/badge/status-beta-yellow)]() [![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

> ⚠️ **Beta - expect change.** This CLI is in active beta. The command surface, flag names, output JSON shapes, and the underlying HTTP API will change before `v1.0.0` and there is **no deprecation guarantee** in beta. Pin a version in scripts and CI, and read [CHANGELOG.md](CHANGELOG.md) before upgrading.

The official command-line interface for [BCDock](https://bcdock.io) - a managed platform for Microsoft Dynamics 365 Business Central environments on Azure.

`bcdock` is the primary tool for humans **and AI agents** to:

- Create, hibernate, resume, and delete BC environments
- Discover available BC versions, countries, and Azure regions before provisioning
- Compile and publish AL extensions against a live environment
- Inspect billing, usage, account state, and audit history

If you're an AI coding agent landing on this repository, start with [**AGENTS.md**](AGENTS.md).

**Pricing:** [bcdock.io/pricing](https://bcdock.io/pricing). Environments are metered per second while they run, and a hibernated one bills at a much lower stored rate.

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

On macOS or Linux:

```bash
curl -fsSL https://cli.bcdock.io/install.sh | sh
```

On Windows, in PowerShell:

```powershell
irm https://cli.bcdock.io/install.ps1 | iex
```

With Homebrew:

```bash
brew install bcdock/tap/bcdock
```

Other paths (npm for CI runners, release archives, building from source) are on the [install page](https://docs.bcdock.io/cli/install/).

Verify:

```bash
bcdock version
```

---

## Quickstart

Access is by invitation for now. Join the waitlist, sign up with the code from your invite email, then log in:

```bash
# 1. Request access (once)
bcdock auth join-waitlist

# 2. When the invite email arrives, activate your account
bcdock auth signup --invite-code CODE --email you@example.com

# 3. Log in: we email you a one-time code
bcdock auth login --email you@example.com
```

If signup printed `Your first BC environment (<id>) is being provisioned`, run `bcdock env wait <id> --status running` instead of creating another.

Then create an environment:

```bash
# 4. See which BC versions and countries are ready in your region
bcdock artifacts list --region australiaeast --fast-only

# 5. Create one (about 15 minutes) and wait until it is running.
#    Or run `bcdock env create --name dev --wait` in a terminal and pick from a list.
bcdock env create --name dev --version 28.5 --country au --region australiaeast --wait

# 6. Print its web client URL, then reveal its sign-in password
bcdock env get dev
bcdock env credentials dev
```

The step-by-step version is the [CLI quickstart](https://docs.bcdock.io/quickstart/cli/). For richer flows (publishing AL extensions, hibernation, billing), see [AGENTS.md](AGENTS.md) and [docs.bcdock.io](https://docs.bcdock.io).

---

## Documentation

- **Full reference**: [docs.bcdock.io/cli/reference](https://docs.bcdock.io/cli/reference/bcdock/) - generated from the same cobra command tree
- **Per-command help**: `bcdock <verb> --help` (no network round-trip)
- **Agent-oriented guide**: [AGENTS.md](AGENTS.md) - concrete commands, error handling, machine-readable output
- **Architecture**: [docs.bcdock.io/architecture](https://docs.bcdock.io/architecture/overview/)
- **Pricing**: [bcdock.io/pricing](https://bcdock.io/pricing)

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
