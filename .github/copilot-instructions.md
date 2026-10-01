# Copilot instructions for the BCDock CLI

This repository is the official command-line interface for [BCDock](https://www.bcdock.io),
a managed platform for Microsoft Dynamics 365 Business Central environments on Azure.

**Before running any `bcdock` command, read [AGENTS.md](../AGENTS.md).** It is the
authoritative guidance for AI agents: discovery-first rule, exit-code recovery,
one-call-per-intent loop, and version-pinning notes.

If a flag or output field this file documents conflicts with `bcdock <verb> --help`,
trust the binary. Beta surface; both can change before `v1.0.0`.
