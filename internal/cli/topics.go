package cli

import (
	"github.com/spf13/cobra"
)

// Help topics are registered under the "helpTopics" group so they appear in
// a dedicated "Help Topics:" section in `bcdock --help` output rather than
// mixing with actionable commands.
//
// They have no RunE - cobra shows their Long when invoked directly, and
// `bcdock help <topic>` works identically.

var authenticationTopicCmd = &cobra.Command{
	Use:     "authentication",
	GroupID: "helpTopics",
	Short:   "Authentication: token sources, login flows, CI/CD guidance",
	Long: `BCDock CLI authenticates using a token (bdk_...) resolved in this order:

  1. --token flag on the current command
  2. BCDOCK_TOKEN environment variable
  3. ~/.bcdock/credentials.json  (written by 'auth login' and 'auth set-token')

For humans (interactive terminal):
  bcdock auth login --email you@example.com
  # Enter the 6-digit OTP sent to your email. A long-lived API key is minted
  # and stored in ~/.bcdock/credentials.json (mode 0600).

For CI/CD and agents (non-interactive):
  export BCDOCK_TOKEN=bdk_xxxxxxxxxxxxxxxxxxxx
  bcdock auth whoami    # verify the token

  Or store it with:
  bcdock auth set-token bdk_xxxxxxxxxxxxxxxxxxxx

  Generate API keys at: https://app.bcdock.io/profile/api-keys

Non-interactive OTP flow (smoke tests, if out-of-band OTP access is available):
  bcdock auth login --email you@example.com --otp 123456

New user flow (invitation-only):
  1. bcdock auth join-waitlist  (request access)
  2. Wait for invite email (~48h)
  3. bcdock auth signup --invite-code CODE --email you@example.com
  4. bcdock auth login --email you@example.com

Exit codes:
  0   ok (help shown)`,
	Example: `  bcdock help authentication`,
}

var poolsVsEnvsTopicCmd = &cobra.Command{
	Use:     "pools-vs-envs",
	GroupID: "helpTopics",
	Short:   "Pools and environments: what each is, lifecycle, and hibernation",
	Long: `BCDock has two layers: pools and environments.

POOLS
  A pool is a set of pre-warmed Azure VMs in a region. BCDock manages pools
  automatically. You do not create or delete pools.

  A "fast config" (FAST=yes in 'artifacts list') means a VM image for that
  BC version + country combination is already cached on the pool. Provisioning
  then takes 7-15 minutes (restore the image, start BC).

  A "cold config" has no cached image. The first environment on that config
  triggers a full image build: ~78 minutes. Subsequent environments reuse
  the cached image.

ENVIRONMENTS
  An environment is a BC container running on a pool VM. Each environment:
  - Has a unique GUID and a short 8-hex-char ID
  - Exposes a Web Client URL, dev endpoint, OData, SOAP, and downloads URL
  - Has a set of BC admin credentials (username / password / WebServiceAccessKey)
  - Can be in one of these states:
      creating   → provisioning in progress
      running    → live and accepting connections
      hibernated → saved to blob storage; no compute charges but base fee applies
      failed     → provisioning or resume failed; see 'env logs --provisioning'
      deleted    → permanently removed

LIFECYCLE
  Create:    bcdock env create --name my-env --version 25.5 --country au --region westus2
  Hibernate: bcdock env hibernate my-env  (save to blob, free pool slot)
  Resume:    bcdock env resume my-env     (~30s from hibernated to running)
  Delete:    bcdock env delete my-env     (permanent)

HIBERNATION AS COST CONTROL
  Active-rate billing (hourly per-env charge) stops when an environment is
  hibernated. The base fee (monthly) continues. If you're not using an
  environment for more than a few hours, hibernate it.

Exit codes:
  0   ok (help shown)`,
	Example: `  bcdock help pools-vs-envs`,
}

var agentWorkflowsTopicCmd = &cobra.Command{
	Use:     "agent-workflows",
	GroupID: "helpTopics",
	Short:   "Recipes for Claude, Copilot, and other AI agent workflows",
	Long: `BCDock CLI is designed as the primary API surface for AI agents.
Every customer-facing operation is reachable via 'bcdock <verb>' - no
portal-only features in the supported workflow.

RULE OF THUMB
  One CLI call per intent. Read the output. Decide next step.
  Never chain six commands speculatively. Never invent flags.
  When uncertain: bcdock <verb> --help

DISCOVER BEFORE CREATING
  The most common agent mistake is picking a BC version without checking
  what's cached. A non-fast version triggers a 78-minute image build.

  Always run this first:
    bcdock artifacts list --region <r> --fast-only -o json

  Pick version + country + region from the same row of that output.

WORKFLOW: CREATE AND WAIT
  bcdock artifacts list --region australiaeast --fast-only -o json
  bcdock env create --name my-env --version 25.5 --country au --region australiaeast
  bcdock env wait my-env --status running --timeout 30m
  bcdock env get my-env -o json

WORKFLOW: PUBLISH AN AL EXTENSION
  bcdock env download-symbols my-env
  bcdock al compile --env my-env
  bcdock env publish my-env build/MyApp_1.0.0.0.app

WORKFLOW: HIBERNATE WHEN IDLE
  bcdock env hibernate my-env
  bcdock env resume my-env --wait

SCRIPTING GUIDANCE
  - Use -o json for machine-readable output; field names are stable within a
    major API version.
  - Exit codes are machine-readable; see each command's help for the full set.
  - Set BCDOCK_TOKEN in the environment rather than passing --token.
  - The CLI emits version-skew warnings to stderr; capture stdout only for
    structured output.

See AGENTS.md in the bcdock-cli source for the full reference used by
Claude, GitHub Copilot, and other AI systems.

Exit codes:
  0   ok (help shown)`,
	Example: `  bcdock help agent-workflows`,
}

var ciIntegrationTopicCmd = &cobra.Command{
	Use:     "ci-integration",
	GroupID: "helpTopics",
	Short:   "CI/CD: non-TTY behaviour, exit codes, --output json contract",
	Long: `Using bcdock in CI pipelines (GitHub Actions, Azure DevOps, Jenkins, etc.).

AUTHENTICATION
  Set BCDOCK_TOKEN as a CI secret. Do not use 'auth login' in CI.
  Example (GitHub Actions):
    env:
      BCDOCK_TOKEN: ${{ secrets.BCDOCK_TOKEN }}

NON-TTY BEHAVIOUR
  When stdin is not a terminal (CI always), interactive pickers are disabled.
  Commands that require interactive input in TTY mode require explicit flags in
  non-TTY mode:
    bcdock env create --name my-env --version 25.5 --country au --region westus2

MACHINE-READABLE OUTPUT
  Pass -o json to get structured JSON output on stdout. Field names are stable
  within a major API version (the 'api' field from 'bcdock version').
  stderr carries human-readable progress (--quiet suppresses most of it).

  Parse with jq:
    bcdock env get my-env -o json | jq -r .webClientUrl

EXIT CODES (machine-readable, never rely on stderr text)
  0   ok
  1   general error
  2   still provisioning (environment not yet ready)
  3   authentication failure
  4   rate-limited (retry after a delay)
  5   not found
  10  provisioning failed
  124 timeout (--wait or --timeout exceeded)

WAIT PATTERN (non-blocking create + explicit wait)
  bcdock env create --name my-env --version 25.5 --country au --region westus2
  bcdock env wait my-env --status running --timeout 30m
  if [ $? -ne 0 ]; then bcdock env logs my-env --provisioning; exit 1; fi

RATE LIMITING
  If you receive exit code 4, wait and retry. The rate limit window is short
  for most endpoints. For heavy automation, space out create calls.

Exit codes:
  0   ok (help shown)`,
	Example: `  bcdock help ci-integration`,
}

var versionPolicyTopicCmd = &cobra.Command{
	Use:     "version-policy",
	GroupID: "helpTopics",
	Short:   "CLI/platform-API version skew rules and upgrade guidance",
	Long: `BCDock CLI has a built-in version skew probe that runs on every command.

HOW IT WORKS
  On each command invocation (excluding 'version' itself), the CLI makes a
  bounded 1.5s probe to GET /api/version on the platform. If the platform
  advertises a minimumCliVersion that is newer than the installed CLI, the
  CLI prints a warning to stderr:

    warning: bcdock CLI 1.2.0 is older than the server's minimum (1.3.0)
    - upgrade to avoid surprise errors on new flags/endpoints.

  The probe is informational only - it never blocks the command.
  Network failures and "dev" builds skip the probe silently.

SKEW POLICY
  - The CLI and platform API are independently versioned.
  - The platform guarantees backward compatibility within a major API version.
    (Check 'bcdock version -o json' for the "api" field.)
  - When the platform ships a breaking change (new major), minimumCliVersion
    is bumped to a CLI release that understands the new API shape.
  - CLI releases are forward-compatible: a new CLI will work against an older
    platform that has not yet upgraded, as long as the API major matches.

UPGRADE
  Replace your CLI binary with the latest release from the BCDock download
  page or your package manager. The platform never forces an immediate upgrade;
  the warning is advisory until minimumCliVersion exceeds your installed version
  and new commands start returning errors.

Exit codes:
  0   ok (help shown)`,
	Example: `  bcdock help version-policy`,
}

func init() {
	RootCmd.AddCommand(
		authenticationTopicCmd,
		poolsVsEnvsTopicCmd,
		agentWorkflowsTopicCmd,
		ciIntegrationTopicCmd,
		versionPolicyTopicCmd,
	)
}
