package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bcdock/cli/internal/client"
	"github.com/bcdock/cli/internal/output"
	"github.com/spf13/cobra"
)

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Manage Business Central environments",
	Long: `Create, list, inspect, and delete BC environments.

Config discovery: BC versions with a pre-built VM image (~7-15 min provisioning
on a warm pool) are called "fast configs". Versions without one trigger a ~78
min image build on first use - usually not what you want. Always prefer fast
configs unless you explicitly need a specific version.

  bcdock artifacts list --region <r> --fast-only   # browse fast configs in a region
  bcdock env create                                 # interactive picker (fast configs only)

Exit codes:
  0   ok
  1   general error`,
	Example: `  bcdock env create --name my-env --version 25.5 --country au --region westus2 --wait
  bcdock env list -o json
  bcdock env get my-env
  bcdock env delete my-env --force`,
}

var envCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new BC environment",
	Long: `Create a new BC environment.

When --version / --country / --region are all omitted and stdin is a terminal,
the CLI fetches fast configs (versions with pre-built VM images) across all
regions and prompts you to pick one - avoiding the ~78 min image build that
happens when no pre-built image exists.

Pass the flags explicitly to skip the picker (scripts, CI, agent use).

Exit codes:
  0   ok
  1   general error
  3   auth failure (missing or invalid token)
  4   rate-limited
  10  provisioning failed (only with --wait and env status=failed)`,
	Example: `  bcdock env create
  bcdock env create --name my-env --version 25.5 --country au --region westus2
  bcdock env create --name my-env --version 25.5 --country au --region westus2 --wait`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		version, _ := cmd.Flags().GetString("version")
		country, _ := cmd.Flags().GetString("country")
		name, _ := cmd.Flags().GetString("name")
		envType, _ := cmd.Flags().GetString("type")
		multiTenant, _ := cmd.Flags().GetBool("multi-tenant")
		region, _ := cmd.Flags().GetString("region")
		wait, _ := cmd.Flags().GetBool("wait")
		waitTimeout, _ := cmd.Flags().GetDuration("wait-timeout")

		// Interactive picker when no config was provided and stdin is a TTY.
		if version == "" && country == "" && region == "" && isTTY(os.Stdin) {
			picked, err := pickFastConfig(cmd.Context(), r.Client)
			if err != nil {
				return err
			}
			version = picked.VersionFull
			country = picked.Country
			region = picked.Region
			if strings.EqualFold(picked.ArtifactType, "OnPrem") {
				envType = "onprem"
			}
		}

		if version == "" {
			return fmt.Errorf("--version is required (e.g. --version 25.5). Run 'bcdock env create' without flags for an interactive picker, or 'bcdock artifacts list --region <r> --fast-only' to browse")
		}
		if country == "" {
			return fmt.Errorf("--country is required (e.g. --country au)")
		}

		imageType := "Sandbox"
		if strings.EqualFold(envType, "onprem") {
			imageType = "OnPrem"
		}

		if err := validateEnvCreateInput(cmd.Context(), r.Client, region, version, country, imageType); err != nil {
			return err
		}

		req := createEnvRequest{
			Name:        name,
			Version:     version,
			Country:     country,
			ImageType:   imageType,
			Location:    region,
			MultiTenant: multiTenant,
		}

		r.Printer.Info("Creating environment...")

		var created createEnvResponse
		if err := r.Client.Do(cmd.Context(), http.MethodPost, "/api/v1/environments", req, &created); err != nil {
			return err
		}

		if !wait {
			return r.Printer.Print(created)
		}

		if waitTimeout == 0 {
			waitTimeout = 30 * time.Minute
		}
		r.Printer.Info("Waiting for environment %s to be ready (timeout: %s)...", created.ShortID, waitTimeout)

		env, err := pollEnv(cmd.Context(), r.Client, created.ID, r.Printer, waitTimeout)
		if err != nil {
			return err
		}

		if env.Status == "failed" {
			msg := derefStr(env.ErrorMessage)
			if msg == "" {
				msg = "provisioning failed"
			}
			return fmt.Errorf("%s", msg)
		}

		return r.Printer.Print(toEnvRow(*env))
	},
}

var envListCmd = &cobra.Command{
	Use:   "list",
	Short: "List environments",
	Long: `List all environments for the active company. Excludes deleted environments.

Use --status to filter by environment state (running, creating, hibernated,
failed, etc.). Use --version to filter by BC version. Use --region to filter
by Azure region.

Exit codes:
  0   ok
  3   auth failure (missing or invalid token)
  4   rate-limited`,
	Example: `  bcdock env list
  bcdock env list -o json
  bcdock env list --status running
  bcdock env list --version 25.5 --region australiaeast`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		statusFilter, _ := cmd.Flags().GetString("status")
		versionFilter, _ := cmd.Flags().GetString("version")
		regionFilter, _ := cmd.Flags().GetString("region")

		path := "/api/v1/environments"
		if regionFilter != "" {
			path += "?location=" + regionFilter
		}

		var envs []environment
		if err := r.Client.Do(cmd.Context(), http.MethodGet, path, nil, &envs); err != nil {
			return err
		}

		filtered := make([]environment, 0, len(envs))
		for _, e := range envs {
			if e.DeletedAt != nil {
				continue
			}
			if statusFilter != "" && !strings.EqualFold(e.Status, statusFilter) {
				continue
			}
			if versionFilter != "" && e.BcVersion != versionFilter {
				continue
			}
			filtered = append(filtered, e)
		}

		if r.Printer.Format == output.FormatJSON {
			return r.Printer.Print(filtered)
		}

		rows := make([]envRow, len(filtered))
		for i, e := range filtered {
			rows[i] = toEnvRow(e)
		}
		return r.Printer.Print(rows)
	},
}

var envGetCmd = &cobra.Command{
	Use:   "get <name|shortId>",
	Short: "Get details of an environment",
	Long: `Show full details of one environment - connection endpoints (web client,
SOAP, OData v4, dev endpoint, downloads), BC admin credentials, status,
and timestamps.

Output formats:
  -o table  (default) vertical key/value layout, one field per line
  -o json   the full env record from the API (use for scripting)
  -o csv    the same compact row as 'env list' (one entry)

Exit codes:
  0   ok
  3   auth failure (missing or invalid token)
  4   rate-limited
  5   environment not found`,
	Example: `  bcdock env get my-env
  bcdock env get my-env -o json
  bcdock env get a1b2c3d4`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		id, err := resolveEnvID(cmd.Context(), r.Client, args[0])
		if err != nil {
			return err
		}

		var env environment
		if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/environments/"+id, nil, &env); err != nil {
			return err
		}

		switch r.Printer.Format {
		case output.FormatJSON:
			return r.Printer.Print(env)
		case output.FormatCSV:
			return r.Printer.Print(toEnvRow(env))
		default:
			return printEnvDetails(r.Printer, env)
		}
	},
}

var envDeleteCmd = &cobra.Command{
	Use:   "delete <name|shortId>",
	Short: "Delete an environment",
	Long: `Delete an environment and free its pool slot. Prompts for confirmation
unless --force is passed.

Use --wait to block until the deletion is fully complete. With --wait, the
command polls until the status is 'deleted'; without --wait, deletion is
started asynchronously.

Exit codes:
  0   ok
  3   auth failure (missing or invalid token)
  4   rate-limited
  5   environment not found`,
	Example: `  bcdock env delete my-env
  bcdock env delete my-env --force
  bcdock env delete my-env --force --wait`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		force, _ := cmd.Flags().GetBool("force")
		wait, _ := cmd.Flags().GetBool("wait")
		waitTimeout, _ := cmd.Flags().GetDuration("wait-timeout")

		id, err := resolveEnvID(cmd.Context(), r.Client, args[0])
		if err != nil {
			return err
		}

		if !force {
			confirm, err := prompt(fmt.Sprintf("Delete environment %q? [y/N]: ", args[0]))
			if err != nil {
				return err
			}
			if !strings.EqualFold(strings.TrimSpace(confirm), "y") {
				r.Printer.Info("Cancelled.")
				return nil
			}
		}

		if err := r.Client.Do(cmd.Context(), http.MethodDelete, "/api/v1/environments/"+id, nil, nil); err != nil {
			return err
		}

		if !wait {
			r.Printer.Info("Deletion started.")
			return nil
		}

		if waitTimeout == 0 {
			waitTimeout = 5 * time.Minute
		}
		r.Printer.Info("Waiting for environment to be deleted (timeout: %s)...", waitTimeout)

		env, err := pollEnv(cmd.Context(), r.Client, id, r.Printer, waitTimeout)
		if err != nil {
			return err
		}
		if env.Status == "deleted" || env.DeletedAt != nil {
			r.Printer.Info("Deleted.")
		}
		return nil
	},
}

var envHibernateCmd = &cobra.Command{
	Use:   "hibernate <name|shortId>",
	Short: "Hibernate an environment (saves to blob storage, frees pool slot)",
	Long: `Save the environment state to blob storage and free its pool VM. The
environment transitions from 'running' to 'hibernated'. While hibernated, only
the base-fee is billed (no active-rate charges).

Use 'bcdock env resume' to bring it back. Use --wait to block until hibernation
completes instead of returning immediately.

Exit codes:
  0   ok
  3   auth failure (missing or invalid token)
  4   rate-limited
  5   environment not found
  10  hibernation failed`,
	Example: `  bcdock env hibernate my-env
  bcdock env hibernate my-env --wait`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		wait, _ := cmd.Flags().GetBool("wait")
		waitTimeout, _ := cmd.Flags().GetDuration("wait-timeout")

		id, err := resolveEnvID(cmd.Context(), r.Client, args[0])
		if err != nil {
			return err
		}

		if err := r.Client.Do(cmd.Context(), http.MethodPost, "/api/v1/environments/"+id+"/hibernate", nil, nil); err != nil {
			return err
		}

		if !wait {
			r.Printer.Info("Hibernate started.")
			return nil
		}

		if waitTimeout == 0 {
			waitTimeout = 10 * time.Minute
		}
		r.Printer.Info("Waiting for environment to hibernate (timeout: %s)...", waitTimeout)

		env, err := pollEnv(cmd.Context(), r.Client, id, r.Printer, waitTimeout)
		if err != nil {
			return err
		}
		if env.Status != "hibernated" {
			return fmt.Errorf("hibernate failed (status: %s)", env.Status)
		}
		r.Printer.Info("Hibernated.")
		return nil
	},
}

var envResumeCmd = &cobra.Command{
	Use:   "resume <name|shortId>",
	Short: "Resume a hibernated environment",
	Long: `Restore a hibernated environment from blob storage. The environment
transitions from 'hibernated' back to 'running'.

Use --version to upgrade the BC platform version during resume (optional - the
version must be compatible with the saved state). Use --wait to block until
running or failed instead of returning immediately.

Exit codes:
  0   ok
  3   auth failure (missing or invalid token)
  4   rate-limited
  5   environment not found
  10  resume failed (provisioning error)`,
	Example: `  bcdock env resume my-env
  bcdock env resume my-env --wait
  bcdock env resume my-env --version 25.5 --wait`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		targetVersion, _ := cmd.Flags().GetString("version")
		wait, _ := cmd.Flags().GetBool("wait")
		waitTimeout, _ := cmd.Flags().GetDuration("wait-timeout")

		id, err := resolveEnvID(cmd.Context(), r.Client, args[0])
		if err != nil {
			return err
		}

		var body interface{}
		if targetVersion != "" {
			body = struct {
				TargetVersion string `json:"targetVersion"`
			}{TargetVersion: targetVersion}
		}

		if err := r.Client.Do(cmd.Context(), http.MethodPost, "/api/v1/environments/"+id+"/resume", body, nil); err != nil {
			return err
		}

		if !wait {
			r.Printer.Info("Resume started.")
			return nil
		}

		if waitTimeout == 0 {
			waitTimeout = 30 * time.Minute
		}
		r.Printer.Info("Waiting for environment to be ready (timeout: %s)...", waitTimeout)

		env, err := pollEnv(cmd.Context(), r.Client, id, r.Printer, waitTimeout)
		if err != nil {
			return err
		}
		if env.Status == "failed" {
			msg := derefStr(env.ErrorMessage)
			if msg == "" {
				msg = "resume failed"
			}
			return fmt.Errorf("%s", msg)
		}
		return r.Printer.Print(toEnvRow(*env))
	},
}

var envLogsCmd = &cobra.Command{
	Use:   "logs <name|shortId>",
	Short: "Show container logs (default) or provisioning log history",
	Long: `Fetch logs for a BC environment.

Default: last N container log lines (stdout/stderr from the BC process).
--provisioning: provisioning log history from Loki (queued/creating/failed
  stage messages, useful for diagnosing why an env failed to start).
--follow: stream live container logs via SSE until ctrl+c.

--tail controls line count for non-follow modes (default: 100).

Exit codes:
  0   ok
  3   auth failure (missing or invalid token)
  5   environment not found`,
	Example: `  bcdock env logs my-env
  bcdock env logs my-env --tail 200
  bcdock env logs my-env --provisioning
  bcdock env logs my-env --follow`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		provisioning, _ := cmd.Flags().GetBool("provisioning")
		follow, _ := cmd.Flags().GetBool("follow")
		tail, _ := cmd.Flags().GetInt("tail")

		id, err := resolveEnvID(cmd.Context(), r.Client, args[0])
		if err != nil {
			return err
		}

		if provisioning {
			return envLogsProvisioning(cmd.Context(), r, id, tail)
		}
		if follow {
			return envLogsFollow(cmd.Context(), r, id)
		}
		return envLogsContainer(cmd.Context(), r, id, tail)
	},
}

var envUsageCmd = &cobra.Command{
	Use:   "usage <name|shortId>",
	Short: "Show billing usage for an environment",
	Long: `Show the billing usage timeline for one environment: total running hours,
total billed amount, and the per-day segment breakdown.

For company-wide usage across all environments, use 'bcdock usage'.

Exit codes:
  0   ok
  3   auth failure (missing or invalid token)
  4   rate-limited
  5   environment not found`,
	Example: `  bcdock env usage my-env
  bcdock env usage my-env -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		id, err := resolveEnvID(cmd.Context(), r.Client, args[0])
		if err != nil {
			return err
		}

		var timeline envBillingTimeline
		if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/environments/"+id+"/billing", nil, &timeline); err != nil {
			return err
		}

		if r.Printer.Format == output.FormatJSON {
			return r.Printer.Print(timeline)
		}

		// Summary row
		hours := float64(timeline.TotalRunningSeconds) / 3600.0
		summary := envUsageSummary{
			Environment: timeline.EnvironmentName,
			TotalHours:  fmt.Sprintf("%.1fh", hours),
			TotalAmount: fmt.Sprintf("%.2f %s", timeline.TotalAmount, timeline.Currency),
			Segments:    len(timeline.Segments),
		}
		return r.Printer.Print(summary)
	},
}

// timeoutError carries exit code 124 (GNU coreutils convention for timeout(1))
// up through main's exitCodeFor without leaking command-specific knowledge into it.
type timeoutError struct{ msg string }

func (e *timeoutError) Error() string { return e.msg }
func (e *timeoutError) ExitCode() int { return 124 }

var envWaitCmd = &cobra.Command{
	Use:   "wait <name|shortId>",
	Short: "Block until an environment reaches one of the requested states",
	Long: `Wait for an environment to reach a desired status, polling every 3 seconds.

Multiple --status values are OR-ed: the command exits 0 as soon as the env
reaches any one of them.

Exit codes:
  0   one of --status values reached
  5   environment not found
  124 timeout elapsed without reaching any requested state`,
	Example: `  bcdock env wait my-env --status running --timeout 30m
  bcdock env wait my-env --status running --status failed --timeout 30m`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		statuses, _ := cmd.Flags().GetStringArray("status")
		timeout, _ := cmd.Flags().GetDuration("timeout")
		if len(statuses) == 0 {
			return fmt.Errorf("at least one --status is required (e.g. --status running)")
		}
		if timeout <= 0 {
			timeout = 30 * time.Minute
		}

		id, err := resolveEnvID(cmd.Context(), r.Client, args[0])
		if err != nil {
			return err
		}

		r.Printer.Info("Waiting for environment %s to reach %v (timeout: %s)...", args[0], statuses, timeout)

		// pollEnv returns once the env is in any terminal state. We then check whether
		// the user's --status set was hit; if not, surface a 124 timeout.
		env, pollErr := pollEnv(cmd.Context(), r.Client, id, r.Printer, timeout)
		if env != nil {
			for _, want := range statuses {
				if strings.EqualFold(env.Status, want) {
					return nil
				}
			}
		}
		if pollErr != nil {
			// pollEnv embeds "timed out after ..." for deadline expiry; promote to exit 124.
			if strings.Contains(pollErr.Error(), "timed out") {
				return &timeoutError{msg: pollErr.Error()}
			}
			return pollErr
		}
		current := "unknown"
		if env != nil {
			current = env.Status
		}
		return &timeoutError{msg: fmt.Sprintf("environment reached terminal status %q without matching any of %v", current, statuses)}
	},
}

var envOpenCmd = &cobra.Command{
	Use:   "open <name|shortId>",
	Short: "Open the BC Web Client in a browser",
	Long: `Open the BC Web Client URL for the given environment in the default browser.
This command is not yet implemented.

To get the URL today:
  bcdock env get my-env -o json | jq -r .webClientUrl

Exit codes:
  1   general error (not yet implemented)`,
	Example: `  bcdock env open my-env`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return errNotImplemented()
	},
}

var envLaunchJsonCmd = &cobra.Command{
	Use:   "launch-json <env>",
	Short: "Emit a VS Code launch.json config for publishing to a BC environment",
	Long: `Generate the launch.json configuration block VS Code's AL extension needs
to publish (Ctrl+F5) to a BCDock environment, derived from the env's DTO.

By default emits a complete launch.json (single configuration in the array)
to stdout. With --out, writes the file (creating parent dirs).

The values are derived from 'bcdock env get':
  server         = origin of devEndpointUrl
  serverInstance = first path segment of devEndpointUrl (BC-dev / {name}-dev)
  authentication = "UserPassword"  (BCDock containers don't ship Entra auth)
  tenant         = "default"       (BCDock ships single-tenant=default for MT)

Credentials are NOT written to launch.json - VS Code prompts on first
publish and caches in the OS credential store.

Exit codes:
  0   ok
  1   general error (env not ready, no devEndpointUrl yet)
  3   auth failure (missing or invalid token)
  5   environment not found`,
	Example: `  bcdock env launch-json my-env
  bcdock env launch-json my-env --out .vscode/launch.json
  bcdock env launch-json my-env --config-name "BC sandbox"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		envArg := args[0]

		out, _ := cmd.Flags().GetString("out")
		configName, _ := cmd.Flags().GetString("config-name")
		launchBrowser, _ := cmd.Flags().GetBool("launch-browser")

		id, err := resolveEnvID(cmd.Context(), r.Client, envArg)
		if err != nil {
			return err
		}
		var env environment
		if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/environments/"+id, nil, &env); err != nil {
			return err
		}

		cfg, err := buildLaunchJsonConfig(env, configName, launchBrowser)
		if err != nil {
			return err
		}

		doc := map[string]any{
			"version":        "0.2.0",
			"configurations": []any{cfg},
		}
		buf, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		buf = append(buf, '\n')

		if out == "" {
			_, err = r.Printer.W.Write(buf)
			return err
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(out, buf, 0o644); err != nil {
			return err
		}
		r.Printer.Info("Wrote %s", out)
		return nil
	},
}

// buildLaunchJsonConfig derives the AL launch.json fields from an env DTO.
// server / serverInstance derivation handles both routing modes uniformly:
// the first path segment of devEndpointUrl is BC-dev (subdomain mode) or
// {name}-dev (path mode) - exactly what serverInstance needs to be for VS
// Code's POST {server}/{serverInstance}/dev/apps to land on the right Traefik
// route.
func buildLaunchJsonConfig(env environment, configName string, launchBrowser bool) (map[string]any, error) {
	if env.DevEndpointUrl == nil || *env.DevEndpointUrl == "" {
		return nil, fmt.Errorf("environment %q has no devEndpointUrl yet (status: %s)", env.DisplayName, env.Status)
	}
	devURL, err := url.Parse(*env.DevEndpointUrl)
	if err != nil {
		return nil, fmt.Errorf("parse devEndpointUrl: %w", err)
	}
	server := devURL.Scheme + "://" + devURL.Host
	// serverInstance is the FIRST non-empty path segment. Modern envs serve
	// devEndpointUrl as {server}/{serverInstance}/ - single segment. Older
	// envs (pre-DEV-026 Traefik change) had /BC/dev/ - taking only the first
	// segment yields "BC", which won't actually publish (no /BC-dev/ route on
	// those containers), but at least produces a valid AL launch.json shape
	// rather than the invalid "BC/dev".
	segments := strings.Split(strings.Trim(devURL.Path, "/"), "/")
	serverInstance := ""
	for _, s := range segments {
		if s != "" {
			serverInstance = s
			break
		}
	}
	if serverInstance == "" {
		return nil, fmt.Errorf("devEndpointUrl %q has no path segment to use as serverInstance", *env.DevEndpointUrl)
	}
	if configName == "" {
		configName = "BCDock: " + env.DisplayName
	}
	return map[string]any{
		"name":                              configName,
		"type":                              "al",
		"request":                           "launch",
		"environmentType":                   "OnPrem",
		"server":                            server,
		"serverInstance":                    serverInstance,
		"authentication":                    "UserPassword",
		"tenant":                            "default",
		"schemaUpdateMode":                  "Synchronize",
		"breakOnError":                      "All",
		"breakOnRecordWrite":                "None",
		"launchBrowser":                     launchBrowser,
		"enableSqlInformationDebugger":      true,
		"enableLongRunningSqlStatements":    true,
		"longRunningSqlStatementsThreshold": 500,
		"numberOfSqlStatements":             10,
	}, nil
}

var envDownloadSymbolsCmd = &cobra.Command{
	Use:     "download-symbols <env>",
	Aliases: []string{"symbols"},
	Short:   "Download AL symbol packages from a BC environment's dev endpoint",
	Long: `Fetch the symbol packages an AL project depends on from the connected BC
environment and write them to .alpackages/. Equivalent to VS Code's
"AL: Download symbols" - same wire protocol, no IDE required.

Reads app.json (default ./app.json) for:
  - "application" version  → Microsoft_Application_*.app   (System + Base App combined)
  - "platform" version     → Microsoft_System_*.app        (platform symbols)
  - "dependencies"         → one .app per entry            (per-tenant or 3rd-party deps)

Wire protocol (per Microsoft.Dynamics.Nav.Deployment.dll):
  GET {devEndpointUrl}dev/packages?publisher=<P>&appName=<N>&versionText=<V>[&tenant=<T>]
  Authorization: Basic base64(user:password)
  Accept: application/octet-stream, */*
  → binary .app file in response body
(devEndpointUrl is {server}/{serverInstance}/ - launch.json-shaped - so we
append BC's dev-API path "dev/packages" to it.)

By default, packages already present in --out-dir are skipped (incremental).
Use --force to re-download.

Exit codes:
  0   ok
  1   general error (download failed, env not running, no dev endpoint)
  3   auth failure (missing or invalid token)
  5   environment not found`,
	Example: `  bcdock env download-symbols my-env
  bcdock env download-symbols my-env --app-json apps/MyExt/app.json --out-dir apps/MyExt/.alpackages
  bcdock env download-symbols my-env --force --tenant default`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		envArg := args[0]

		appJsonPath, _ := cmd.Flags().GetString("app-json")
		outDir, _ := cmd.Flags().GetString("out-dir")
		tenant, _ := cmd.Flags().GetString("tenant")
		force, _ := cmd.Flags().GetBool("force")
		insecure, _ := cmd.Flags().GetBool("insecure")
		timeout, _ := cmd.Flags().GetDuration("timeout")

		manifest, err := readAppJson(appJsonPath)
		if err != nil {
			return err
		}

		id, err := resolveEnvID(cmd.Context(), r.Client, envArg)
		if err != nil {
			return err
		}
		var env environment
		if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/environments/"+id, nil, &env); err != nil {
			return err
		}
		if env.DevEndpointUrl == nil || *env.DevEndpointUrl == "" {
			return fmt.Errorf("environment %q has no dev endpoint URL (status: %s)", envArg, env.Status)
		}
		if env.Username == nil || env.Password == nil {
			return fmt.Errorf("environment %q has no admin credentials yet (status: %s)", envArg, env.Status)
		}

		targets := manifest.symbolTargets()
		if len(targets) == 0 {
			r.Printer.Info("No symbol packages declared in %s - nothing to download.", appJsonPath)
			return nil
		}
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return fmt.Errorf("create out-dir: %w", err)
		}

		return downloadSymbols(cmd.Context(), r.Printer, *env.DevEndpointUrl,
			*env.Username, *env.Password, tenant, targets, outDir, force, timeout, insecure)
	},
}

var envPublishCmd = &cobra.Command{
	Use:   "publish <env> <app-path>",
	Short: "Publish (deploy) an AL .app to a BC environment",
	Long: `Upload a compiled .app file to the BC dev service endpoint and run install
or upgrade codeunits server-side. Equivalent to VS Code's "AL: Publish without
debugging" - same wire protocol, no IDE required.

Wire protocol (full spec in the al-cli skill):
  POST {devEndpointUrl}apps?SchemaUpdateMode=…[&tenant=…][&DependencyPublishingOption=…]
  Authorization: Basic base64(user:password)   # from env credentials
  Content-Type: multipart/form-data; one file part = .app bytes

Schema update modes:
  synchronize  (default) - additive schema changes; preserves data
  forcesync              - recreate columns when types changed; data loss possible
  recreate               - drop and recreate tables; ALL data lost - use with care

The call is synchronous: returns only after install/upgrade codeunits finish.
Set --timeout high for large apps (default 10m).

Exit codes:
  0   ok
  1   general error (publish failed, env not running, no dev endpoint)
  3   auth failure (missing or invalid token)
  5   environment not found`,
	Example: `  bcdock env publish my-env build/MyApp_1.0.0.0.app
  bcdock env publish my-env app.app --schema-update-mode forcesync
  bcdock env publish my-env app.app --tenant default --timeout 20m`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		envArg, appPath := args[0], args[1]

		schemaMode, _ := cmd.Flags().GetString("schema-update-mode")
		tenant, _ := cmd.Flags().GetString("tenant")
		depPub, _ := cmd.Flags().GetString("dependency-publishing")
		timeout, _ := cmd.Flags().GetDuration("timeout")
		insecure, _ := cmd.Flags().GetBool("insecure")

		appBytes, err := os.ReadFile(appPath)
		if err != nil {
			return fmt.Errorf("read app file: %w", err)
		}

		id, err := resolveEnvID(cmd.Context(), r.Client, envArg)
		if err != nil {
			return err
		}
		var env environment
		if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/environments/"+id, nil, &env); err != nil {
			return err
		}

		if env.DevEndpointUrl == nil || *env.DevEndpointUrl == "" {
			return fmt.Errorf("environment %q has no dev endpoint URL (status: %s)", envArg, env.Status)
		}
		if env.Username == nil || env.Password == nil {
			return fmt.Errorf("environment %q has no admin credentials yet (status: %s)", envArg, env.Status)
		}

		// devEndpointUrl is the launch.json-shaped root: {server}/{serverInstance}/.
		// BC's dev API spec is /{serverInstance}/dev/{resource}, so we append "dev/apps".
		publishURL := *env.DevEndpointUrl + "dev/apps"
		q := url.Values{}
		q.Set("SchemaUpdateMode", strings.ToLower(schemaMode))
		if tenant != "" {
			q.Set("tenant", tenant)
		}
		if depPub != "" {
			q.Set("DependencyPublishingOption", depPub)
		}
		publishURL += "?" + q.Encode()

		return publishApp(cmd.Context(), r.Printer, publishURL,
			*env.Username, *env.Password, filepath.Base(appPath), appBytes,
			timeout, insecure)
	},
}

// appJSON is the subset of app.json we need to drive symbol downloads.
type appJSON struct {
	Name         string   `json:"name"`
	Publisher    string   `json:"publisher"`
	Version      string   `json:"version"`
	Application  string   `json:"application"`
	Platform     string   `json:"platform"`
	Dependencies []appDep `json:"dependencies"`
}

type appDep struct {
	Name      string `json:"name"`
	Publisher string `json:"publisher"`
	Version   string `json:"version"`
}

// symbolTarget is one symbol package to fetch from /dev/packages.
//
// Optional targets are synthesized from app.json's `application` / `platform`
// shorthand, where multiple package names map to the same field for cross-BC
// compatibility (modern BC 23+ split, legacy BC ≤22 single). 404 on an
// optional target is "this BC version doesn't ship that name" - skip silently.
// User-declared dependencies are never optional: 404 is a real error.
type symbolTarget struct {
	Publisher string
	Name      string
	Version   string
	Optional  bool
}

func (t symbolTarget) filename() string {
	// Match BcContainerHelper / VS Code convention: {Publisher}_{Name}_{Version}.app
	return fmt.Sprintf("%s_%s_%s.app", t.Publisher, t.Name, t.Version)
}

func (m appJSON) symbolTargets() []symbolTarget {
	var out []symbolTarget
	if m.Application != "" {
		// Modern BC (23+) splits the old "Application" bundle into three packages.
		// alc requires the split set by name. Older BC (≤22) still ships
		// "Application" as a single bundle. We fetch all four with Optional=true
		// so 404s on the names this BC version doesn't ship are silent skips.
		for _, name := range []string{"System Application", "Base Application", "Business Foundation", "Application"} {
			out = append(out, symbolTarget{Publisher: "Microsoft", Name: name, Version: m.Application, Optional: true})
		}
	}
	if m.Platform != "" {
		out = append(out, symbolTarget{Publisher: "Microsoft", Name: "System", Version: m.Platform})
	}
	for _, d := range m.Dependencies {
		if d.Name == "" || d.Publisher == "" || d.Version == "" {
			continue
		}
		out = append(out, symbolTarget{Publisher: d.Publisher, Name: d.Name, Version: d.Version})
	}
	return out
}

func readAppJson(path string) (*appJSON, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var m appJSON
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &m, nil
}

// downloadSymbols fetches each target sequentially. Sequential keeps progress
// readable; the BC dev service is the bottleneck either way.
func downloadSymbols(ctx context.Context, p *output.Printer, devURL, user, pass, tenant string,
	targets []symbolTarget, outDir string, force bool, timeout time.Duration, insecure bool) error {

	tr := &http.Transport{}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // gated by --insecure
	}
	httpClient := &http.Client{Transport: tr, Timeout: timeout}

	skipped, downloaded := 0, 0
	for _, t := range targets {
		dest := filepath.Join(outDir, t.filename())
		if !force {
			if _, err := os.Stat(dest); err == nil {
				p.Info("  skip %s (already present, --force to refetch)", t.filename())
				skipped++
				continue
			}
		}

		q := url.Values{}
		q.Set("publisher", t.Publisher)
		q.Set("appName", t.Name)
		q.Set("versionText", t.Version)
		if tenant != "" {
			q.Set("tenant", tenant)
		}
		// devURL is {server}/{serverInstance}/ - append BC's dev-API path.
		u := devURL + "dev/packages?" + q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.SetBasicAuth(user, pass)
		req.Header.Set("Accept", "application/octet-stream, */*")

		p.Info("  fetch %s_%s %s", t.Publisher, t.Name, t.Version)
		resp, err := httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("download %s: %w", t.filename(), err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			if t.Optional {
				p.Info("  skip %s_%s %s (404 - not present on this BC version)", t.Publisher, t.Name, t.Version)
				skipped++
				continue
			}
			return fmt.Errorf("symbol not found on env: %s_%s %s - confirm the dependency is installed in the env, or pin to a version present there",
				t.Publisher, t.Name, t.Version)
		}
		if resp.StatusCode >= 400 {
			msg := strings.TrimSpace(string(body))
			if msg == "" {
				msg = resp.Status
			}
			return fmt.Errorf("download %s failed (%d): %s", t.filename(), resp.StatusCode, msg)
		}

		if err := os.WriteFile(dest, body, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dest, err)
		}
		downloaded++
	}

	p.Info("Symbols → %s (%d downloaded, %d skipped)", outDir, downloaded, skipped)
	return nil
}

// publishApp does the multipart POST to the BC dev endpoint. Pure HTTP - no
// BcContainerHelper, no PowerShell. Mirrors what VS Code's AL extension does
// internally for "Publish without debugging".
func publishApp(ctx context.Context, p *output.Printer, publishURL, user, pass, appName string, appBytes []byte, timeout time.Duration, insecure bool) error {
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, err := mw.CreateFormFile(appName, appName)
	if err != nil {
		return fmt.Errorf("multipart: %w", err)
	}
	if _, err := part.Write(appBytes); err != nil {
		return fmt.Errorf("multipart write: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("multipart close: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, publishURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.SetBasicAuth(user, pass)

	tr := &http.Transport{}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // gated by --insecure
	}
	httpClient := &http.Client{Transport: tr, Timeout: timeout}

	p.Info("Publishing %s (%d bytes) → %s", appName, len(appBytes), publishURL)
	start := time.Now()
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("publish request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		// BC error responses carry useful detail in the body - surface it verbatim.
		msg := strings.TrimSpace(string(respBody))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("publish failed (%d): %s", resp.StatusCode, msg)
	}

	p.Info("Published in %s.", time.Since(start).Round(time.Millisecond))
	if len(respBody) > 0 {
		_, _ = p.W.Write(respBody)
		_, _ = p.W.Write([]byte("\n"))
	}
	return nil
}

// resolveEnvID returns the GUID or shortId to pass to the API.
// The API resolves GUID and shortId natively. For names, we list all and match.
func resolveEnvID(ctx context.Context, c *client.Client, nameOrID string) (string, error) {
	if IsGUID(nameOrID) || IsShortID(nameOrID) {
		return nameOrID, nil
	}
	var envs []environment
	if err := c.Do(ctx, http.MethodGet, "/api/v1/environments", nil, &envs); err != nil {
		return "", err
	}
	for _, e := range envs {
		if strings.EqualFold(e.DisplayName, nameOrID) || strings.EqualFold(e.Name, nameOrID) {
			return e.ID, nil
		}
	}
	return "", &client.APIError{
		Message: fmt.Sprintf("environment %q not found", nameOrID),
		Status:  http.StatusNotFound,
	}
}

// pollEnv polls GET /api/environments/{id} every 3 seconds until a terminal status is reached.
func pollEnv(ctx context.Context, c *client.Client, id string, printer *output.Printer, timeout time.Duration) (*environment, error) {
	deadline := time.Now().Add(timeout)
	lastStatus := ""
	lastStage := ""
	lastPct := -1
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		var env environment
		if err := c.Do(ctx, http.MethodGet, "/api/v1/environments/"+id, nil, &env); err != nil {
			return nil, err
		}

		stage := derefStr(env.ProvisioningStage)
		pct := 0
		if env.ProvisioningProgressPercent != nil {
			pct = *env.ProvisioningProgressPercent
		}
		// Print on any change in (status, stage, percent) so the user sees
		// queued → resuming → RestoringDB → StartingServiceTier → running
		// instead of silence between stage transitions.
		if env.Status != lastStatus || stage != lastStage || pct != lastPct {
			extra := ""
			if stage != "" {
				extra = " " + stage
			}
			if pct > 0 {
				extra += fmt.Sprintf(" (%d%%)", pct)
			}
			printer.Info("  [%s]%s", env.Status, extra)
			lastStatus = env.Status
			lastStage = stage
			lastPct = pct
		}

		switch env.Status {
		case "running", "failed", "stopped", "deleted", "suspended", "hibernated":
			return &env, nil
		}

		if time.Now().After(deadline) {
			return &env, fmt.Errorf("timed out after %s (current status: %s)", timeout, env.Status)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func toEnvRow(e environment) envRow {
	progress := ""
	switch e.Status {
	case "creating", "queued", "pending-pool", "hibernating", "resuming":
		if e.ProvisioningProgressPercent != nil && *e.ProvisioningProgressPercent > 0 {
			progress = fmt.Sprintf("%d%%", *e.ProvisioningProgressPercent)
			if e.ProvisioningEstimatedMinutes != nil {
				elapsed := int(time.Since(e.CreatedAt).Minutes())
				if remaining := *e.ProvisioningEstimatedMinutes - elapsed; remaining > 0 {
					progress += fmt.Sprintf(" ~%dm", remaining)
				}
			}
		}
	}
	return envRow{
		Name:     e.DisplayName,
		ShortID:  e.ShortID,
		Version:  e.BcVersion,
		Country:  e.Country,
		Status:   e.Status,
		Progress: progress,
		Region:   e.Location,
		Created:  e.CreatedAt.Format("2006-01-02"),
	}
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// printEnvDetails renders one environment as a vertical "key: value" table.
// Empty / unset fields are skipped so the layout stays clean for prewarmed,
// pending, or hibernated envs that don't have URLs or credentials yet.
func printEnvDetails(p *output.Printer, e environment) error {
	w := tabwriter.NewWriter(p.W, 0, 0, 2, ' ', 0)

	row := func(k, v string) {
		if v == "" {
			return
		}
		fmt.Fprintf(w, "%s\t%s\n", k+":", v)
	}

	row("NAME", e.DisplayName)
	row("SHORT ID", e.ShortID)
	row("STATUS", e.Status)
	if e.ProvisioningStage != nil {
		stage := *e.ProvisioningStage
		if e.ProvisioningProgressPercent != nil {
			stage = fmt.Sprintf("%s (%d%%)", stage, *e.ProvisioningProgressPercent)
		}
		row("STAGE", stage)
	}
	row("BC VERSION", e.BcVersion)
	row("COUNTRY", e.Country)
	row("REGION", e.Location)
	row("ARTIFACT", e.ArtifactType)
	if e.MultiTenant {
		row("TENANCY", "multi-tenant")
	}

	row("WEB CLIENT", derefStr(e.WebClientUrl))
	row("DEV ENDPOINT", derefStr(e.DevEndpointUrl))
	row("SOAP", derefStr(e.SoapUrl))
	row("ODATA V4", derefStr(e.ODataUrl))
	row("DOWNLOADS", derefStr(e.DownloadsUrl))

	row("USERNAME", derefStr(e.Username))
	row("PASSWORD", derefStr(e.Password))
	row("WS ACCESS KEY", derefStr(e.WebServiceAccessKey))

	row("CREATED", e.CreatedAt.Format(time.RFC3339))
	if e.HibernatedAt != nil {
		row("HIBERNATED", e.HibernatedAt.Format(time.RFC3339))
	}
	if e.SuspendedAt != nil {
		row("SUSPENDED", e.SuspendedAt.Format(time.RFC3339))
	}
	if e.DeletedAt != nil {
		row("DELETED", e.DeletedAt.Format(time.RFC3339))
	}
	if e.ErrorMessage != nil {
		row("ERROR", *e.ErrorMessage)
	}

	return w.Flush()
}

func IsGUID(s string) bool {
	clean := strings.ReplaceAll(s, "-", "")
	if len(clean) != 32 {
		return false
	}
	_, err := hex.DecodeString(clean)
	return err == nil
}

func IsShortID(s string) bool {
	if len(s) != 8 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// isTTY reports whether f is a terminal (for interactive prompts).
func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// fastConfigChoice is one pickable row for the env-create interactive picker.
type fastConfigChoice struct {
	Region       string
	VersionFull  string
	Country      string
	CountryName  string
	ArtifactType string
}

// pickFastConfig queries fast configs (pre-built VM images) across all regions
// and prompts the user to pick one. Returns the chosen config.
func pickFastConfig(ctx context.Context, c *client.Client) (*fastConfigChoice, error) {
	var regions []publicRegion
	if err := c.Do(ctx, http.MethodGet, "/api/v1/public/regions", nil, &regions); err != nil {
		return nil, fmt.Errorf("list regions: %w", err)
	}

	var choices []fastConfigChoice
	for _, reg := range regions {
		var catalog artifactVersionCatalog
		// includePreview=false; fast filter applied client-side (mirrors 'artifacts list --fast-only')
		if err := c.Do(ctx, http.MethodGet, "/api/v1/public/artifact-versions?region="+reg.ID, nil, &catalog); err != nil {
			// Soft-fail per region - some may not have a synced catalog yet.
			continue
		}
		for _, v := range catalog.Versions {
			if !v.HasVmImage || v.IsPreview {
				continue
			}
			choices = append(choices, fastConfigChoice{
				Region:       reg.ID,
				VersionFull:  v.VersionFull,
				Country:      strings.ToLower(v.Country),
				CountryName:  v.CountryName,
				ArtifactType: v.ArtifactType,
			})
		}
	}

	if len(choices) == 0 {
		return nil, fmt.Errorf("no fast configs available. Run 'bcdock artifacts list --region <r>' to browse all versions, or ask an admin to pre-build an image")
	}

	fmt.Fprintln(os.Stderr, "Fast configs (pre-built images, ~7-15 min provisioning on a warm pool):")
	fmt.Fprintln(os.Stderr)
	for i, ch := range choices {
		fmt.Fprintf(os.Stderr, "  [%2d]  %-20s  BC %-18s  %-3s %s (%s)\n",
			i+1, ch.Region, ch.VersionFull, ch.Country, ch.CountryName, ch.ArtifactType)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "Pick a config [1-%d]: ", len(choices))

	in := bufio.NewScanner(os.Stdin)
	if !in.Scan() {
		return nil, fmt.Errorf("aborted: no input")
	}
	raw := strings.TrimSpace(in.Text())
	var idx int
	if _, err := fmt.Sscanf(raw, "%d", &idx); err != nil || idx < 1 || idx > len(choices) {
		return nil, fmt.Errorf("aborted: %q is not a valid selection", raw)
	}
	return &choices[idx-1], nil
}

// envLogsContainer fetches plain-text container logs from the pool agent proxy.
func envLogsContainer(ctx context.Context, r *Resolved, id string, tail int) error {
	path := fmt.Sprintf("/api/v1/environments/%s/logs/container?tail=%d", id, tail)
	body, err := r.Client.Stream(ctx, path)
	if err != nil {
		return err
	}
	defer body.Close()
	_, err = io.Copy(os.Stdout, body)
	return err
}

// envLogsFollow streams live log lines via SSE from the Loki tail endpoint.
func envLogsFollow(ctx context.Context, r *Resolved, id string) error {
	body, err := r.Client.Stream(ctx, "/api/v1/environments/"+id+"/logs/stream")
	if err != nil {
		return err
	}
	defer body.Close()

	r.Printer.Info("Streaming logs (ctrl+c to stop)...")
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			fmt.Println(strings.TrimPrefix(line, "data: "))
		}
	}
	return scanner.Err()
}

// envLogsProvisioning fetches log history from the Loki query endpoint and prints log lines.
func envLogsProvisioning(ctx context.Context, r *Resolved, id string, limit int) error {
	path := fmt.Sprintf("/api/v1/environments/%s/logs/query?limit=%d", id, limit)
	var lokiResp struct {
		Data struct {
			Result []struct {
				Values [][]string `json:"values"` // [["ts_ns", "line"], ...]
			} `json:"result"`
		} `json:"data"`
	}
	if err := r.Client.Do(ctx, http.MethodGet, path, nil, &lokiResp); err != nil {
		return err
	}
	for _, stream := range lokiResp.Data.Result {
		for _, v := range stream.Values {
			if len(v) >= 2 {
				fmt.Println(v[1])
			}
		}
	}
	return nil
}

// envBillingTimeline mirrors EnvironmentBillingTimeline from the platform.
type envBillingTimeline struct {
	EnvironmentID       string           `json:"environmentId"`
	EnvironmentName     string           `json:"environmentName"`
	TotalRunningSeconds int              `json:"totalRunningSeconds"`
	TotalAmount         float64          `json:"totalAmount"`
	Currency            string           `json:"currency"`
	Segments            []billingSegment `json:"segments"`
}

type billingSegment struct {
	RecordDate       string  `json:"recordDate"`
	SubscriptionPlan string  `json:"subscriptionPlan"`
	HourlyRate       float64 `json:"hourlyRate"`
	RunningSeconds   int     `json:"runningSeconds"`
	Amount           float64 `json:"amount"`
}

type envUsageSummary struct {
	Environment string `header:"ENVIRONMENT"`
	TotalHours  string `header:"TOTAL_HOURS"`
	TotalAmount string `header:"AMOUNT"`
	Segments    int    `header:"DAYS"`
}

func init() {
	// Create flags
	envCreateCmd.Flags().String("name", "", "Environment name")
	envCreateCmd.Flags().String("version", "", "BC version (e.g. 25.5)")
	envCreateCmd.Flags().String("country", "", "Country localisation (e.g. au, us, gb)")
	envCreateCmd.Flags().String("type", "sandbox", "Environment type: sandbox or onprem")
	envCreateCmd.Flags().Bool("multi-tenant", true, "Multi-tenant mode")
	envCreateCmd.Flags().String("region", "", "Azure region")
	envCreateCmd.Flags().Bool("wait", false, "Block until running or failed")
	envCreateCmd.Flags().Duration("wait-timeout", 0, "Max time to wait (default: 30m)")

	// Delete flags
	envDeleteCmd.Flags().Bool("force", false, "Skip confirmation prompt")
	envDeleteCmd.Flags().Bool("wait", false, "Block until fully cleaned up")
	envDeleteCmd.Flags().Duration("wait-timeout", 0, "Max time to wait (default: 5m)")

	// List flags
	envListCmd.Flags().String("status", "", "Filter by status")
	envListCmd.Flags().String("version", "", "Filter by BC version")
	envListCmd.Flags().String("region", "", "Filter by region")

	// Logs flags
	envLogsCmd.Flags().Bool("provisioning", false, "Show provisioning logs")
	envLogsCmd.Flags().Int("tail", 100, "Number of lines to show")
	envLogsCmd.Flags().Bool("follow", false, "Stream logs in real-time")

	// Symbols flags
	envDownloadSymbolsCmd.Flags().String("app-json", "app.json", "Path to the app.json driving symbol selection")
	envDownloadSymbolsCmd.Flags().String("out-dir", ".alpackages", "Directory to write downloaded .app symbol packages into")
	envDownloadSymbolsCmd.Flags().String("tenant", "default", "BC tenant the symbols belong to")
	envDownloadSymbolsCmd.Flags().Bool("force", false, "Re-download even if a matching .app already exists in --out-dir")
	envDownloadSymbolsCmd.Flags().Bool("insecure", false, "Skip TLS verification (use for self-signed local certs only)")
	envDownloadSymbolsCmd.Flags().Duration("timeout", 5*time.Minute, "Per-package download timeout")

	// Publish flags
	envPublishCmd.Flags().String("schema-update-mode", "synchronize",
		"Schema update mode: synchronize | forcesync | recreate (recreate drops data)")
	envPublishCmd.Flags().String("tenant", "default", "BC tenant to publish into")
	envPublishCmd.Flags().String("dependency-publishing", "",
		"Dependency handling: Default | Ignore | Strict (omitted = server default)")
	envPublishCmd.Flags().Duration("timeout", 10*time.Minute,
		"Max time to wait for the publish call (install/upgrade codeunits run synchronously)")
	envPublishCmd.Flags().Bool("insecure", false,
		"Skip TLS verification for the dev endpoint (use for self-signed local certs only)")

	// Hibernate flags
	envHibernateCmd.Flags().Bool("wait", false, "Block until hibernated or failed")
	envHibernateCmd.Flags().Duration("wait-timeout", 0, "Max time to wait (default: 10m)")

	// Resume flags
	envResumeCmd.Flags().String("version", "", "Confirm upgrade to this BC version (required when upgrading)")
	envResumeCmd.Flags().Bool("wait", false, "Block until running or failed")
	envResumeCmd.Flags().Duration("wait-timeout", 0, "Max time to wait (default: 30m)")

	// Wait flags
	envWaitCmd.Flags().StringArray("status", nil, "Status to wait for (repeatable: running, failed, hibernated, deleted, ...)")
	envWaitCmd.Flags().Duration("timeout", 30*time.Minute, "Max time to wait")

	envLaunchJsonCmd.Flags().String("out", "", "Write launch.json to this path instead of stdout")
	envLaunchJsonCmd.Flags().String("config-name", "", "Name field for the configuration (default: 'BCDock: <displayName>')")
	envLaunchJsonCmd.Flags().Bool("launch-browser", false, "Set launchBrowser=true (VS Code opens browser after publish; URL may not match the env's serverInstance routing)")

	envCmd.AddCommand(envCreateCmd, envListCmd, envGetCmd, envDeleteCmd, envLogsCmd, envUsageCmd, envOpenCmd, envDownloadSymbolsCmd, envPublishCmd, envLaunchJsonCmd, envHibernateCmd, envResumeCmd, envWaitCmd)
	RootCmd.AddCommand(envCmd)
}
