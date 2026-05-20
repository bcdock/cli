# Contributing

> ⚠️ **Beta software.** This CLI is pre-`v1.0.0`. Command names, flags, JSON output shapes, and exit codes can change between releases without deprecation cycles. See [README.md § Beta status](README.md#beta-status-read-this-before-scripting-against-it) for what's at risk.

## tl;dr - we don't accept PRs here (yet)

This repository is a **read-only public mirror** of an internal codebase. The primary copy lives in a private monorepo where it co-evolves with the BCDock Platform API. The mirror is updated on every tagged CLI release.

We chose this model for the same reason `gh`, `aws`, and `stripe` do: the CLI is a thin client over a hosted service we operate, and keeping the two in lockstep is easier when they live in the same tree. The mirror is here so that:

- Customers and auditors can read the client that handles their credentials.
- Anyone can `go install` or `brew install` a stable, versioned binary.
- AI coding assistants helping their users with BCDock have a working reference for the CLI surface, rather than guessing at command names.

Day-to-day development stays in the private monorepo. Concretely:

- We don't review Pull Requests opened against this repo. They'll be closed with a friendly pointer back to this file. (Thank you for the intent - please don't take the close as a signal we don't value the contribution.)
- We don't actively triage GitHub Issues. Please email **support@bcdock.io** instead - the same engineers see it, and it gets a real response timeline.
- We don't ask for CLA / DCO signatures - there's no path for external code to flow back today, so there's nothing to assign.

We may open up to community PRs in a later phase. The current beta is moving too quickly (API + CLI surface still hardening pre-v1) for a stable contribution flow to be fair to anyone.

## What you *can* do

- **Report bugs**: email support@bcdock.io with reproduction steps, CLI version (`bcdock version`), and the relevant command + flags. Include `--verbose` output if available.
- **Request features**: same channel. Include the use case - "I want `bcdock env clone`" is easier to weigh than "support cloning".
- **Report security issues**: see [SECURITY.md](SECURITY.md). Do **not** use public channels.
- **Fork**: MIT licensed. You're free to fork, vendor, modify, or build derivative tools. The HTTP API the fork talks to remains internal; please don't depend on undocumented endpoint behaviour without expecting it to change.
- **Build from source**: instructions below.

## Building from source

```bash
git clone https://github.com/bcdock/cli.git
cd cli
make build              # produces bin/bcdock
./bin/bcdock version
```

Requirements: Go 1.25 or later. No other tooling needed.

Run the tests:

```bash
make test
```

Regenerate the CLI reference docs:

```bash
make docs               # writes to ../../docs-site/public/docs/cli/reference (no-op outside the monorepo)
```

## Code style

- Standard `gofmt` / `goimports`.
- Cobra command files live in `cmd/public/`. Each top-level verb has its own file (`env.go`, `me.go`, `al.go`, …).
- API client traffic goes through `internal/client` - don't hand-roll `http.Client` calls in verb files.
- Output rendering goes through `internal/output.Printer` - every verb's last line is `r.Printer.Print(resp)`, never `fmt.Println`.

## Versioning + release cadence

- Pre-`v1.0.0`: minor releases may include CLI surface changes; CHANGELOG entries flag breakage.
- Tags follow SemVer (`v0.MINOR.PATCH`).
- Each tag triggers the public-repo mirror push from the upstream monorepo.

---

If you reached this file from a closed PR, sorry for the cycle - and thank you for the contribution intent. The fastest way to land a fix is **support@bcdock.io**.
