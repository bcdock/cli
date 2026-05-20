# Security policy

## Reporting a vulnerability

Email **security@bcdock.io** with:

- A clear description of the issue
- Steps to reproduce
- The CLI version (`bcdock version`) and operating system
- Any proof-of-concept code

Please do **not** report security issues via public GitHub Issues, public chat, or social media.

We acknowledge reports within **5 business days** during the beta phase and aim to confirm a fix timeline within **15 business days**. Critical issues (credential exposure, remote code execution, authentication bypass) jump the queue.

## Scope

In scope:

- The `bcdock` binary itself - credential handling, token storage, output sanitisation, terminal-injection resistance.
- Anything the CLI writes to disk: `~/.bcdock/credentials.json`, log files, downloaded artefacts.
- Build-time supply chain (`go.mod` / `go.sum` integrity).

Out of scope:

- **The HTTP API endpoints the CLI dials.** `https://api.bcdock.io/api/v1/*` is an internal API. It is observable from the CLI but not a public stability contract - see [README.md § Beta status](README.md#beta-status). Vulnerabilities discovered by packet capture against the live API should still be reported to security@bcdock.io, but we treat them as backend findings, not CLI findings.
- Issues in third-party dependencies that don't affect the CLI's behaviour.
- Behaviour requiring root access, physical access to the user's machine, or compromised credentials already in the attacker's possession.

## Public disclosure

We follow coordinated disclosure: we publish details (release notes, advisory) **after** a fix has shipped to all supported CLI versions, typically 30-90 days from the initial report. Reporter credit is offered on request.

## Beta caveat

This is a beta product. We don't yet operate a paid bug-bounty programme; we are very interested in responsible reports and will credit researchers in release notes once disclosure is complete.
