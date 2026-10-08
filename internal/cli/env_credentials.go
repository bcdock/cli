package cli

import (
	"context"
	"fmt"
	"net/http"
	"text/tabwriter"

	"github.com/bcdock/cli/internal/client"
	"github.com/bcdock/cli/internal/output"
	"github.com/spf13/cobra"
)

// environmentCredentials is the response of the owner reveal, GET
// /api/v1/environments/{id}/credentials (SEC-039).
//
// It is a separate type from `environment` on purpose. The detail response used to carry
// these two secrets on every read; deleting them from `environment` rather than leaving
// them nil-valued means every consumer that still expects them fails to COMPILE instead of
// silently receiving nothing. There were four of them, and one was found only because the
// compiler refused it.
type environmentCredentials struct {
	EnvironmentID       string  `json:"environmentId"`
	Username            *string `json:"username"`
	Password            *string `json:"password"`
	WebServiceAccessKey *string `json:"webServiceAccessKey"`
}

// revealEnvCredentials fetches an environment's BC credentials from the reveal endpoint.
//
// One call per invocation. Every call writes an audit row server-side, so a caller that
// loops over this turns an auditable event into noise - fetch once and pass the values
// down, which is what each of the publish paths does.
func revealEnvCredentials(ctx context.Context, c *client.Client, envID string) (*environmentCredentials, error) {
	var creds environmentCredentials
	if err := c.Do(ctx, http.MethodGet, "/api/v1/environments/"+envID+"/credentials", nil, &creds); err != nil {
		return nil, fmt.Errorf("revealing environment credentials: %w", err)
	}
	return &creds, nil
}

// requireBcAdmin returns the username and password, or an error naming what to do about it.
// The status is included because "no credentials" is nearly always "not running yet".
func requireBcAdmin(creds *environmentCredentials, envArg, status string) (string, string, error) {
	if creds.Username == nil || creds.Password == nil {
		return "", "", fmt.Errorf("environment %q has no admin credentials yet (status: %s)", envArg, status)
	}
	return *creds.Username, *creds.Password, nil
}

var envCredentialsCmd = &cobra.Command{
	Use:   "credentials <env>",
	Short: "Reveal an environment's BC admin credentials",
	Long: `Reveal the BC admin password and web-service access key for an environment.

These are no longer returned by 'bcdock env get'. A plain read of an environment used to
carry its live credentials in the response - into every log of that response, every proxy
in front of it, and every browser tab polling it. They now come only from here, on an
explicit request, and every reveal is recorded in the audit trail.

The username is not a secret and stays on 'bcdock env get'.

  bcdock env credentials my-env
  bcdock env credentials my-env -o json

The value is printed to stdout. It is never accepted as a flag or an argument anywhere in
this CLI, so it does not reach your shell history or the process list.

Output formats:
  -o table  (default) vertical key/value layout, one field per line
  -o json   the reveal record (use for scripting)

Exit codes:
  0   ok
  3   auth failure (missing or invalid token)
  4   rate-limited
  5   environment not found`,
	Example: `  bcdock env credentials my-env
  bcdock env credentials my-env -o json
  bcdock env credentials a1b2c3d4`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		id, err := resolveEnvID(cmd.Context(), r.Client, args[0])
		if err != nil {
			return err
		}

		creds, err := revealEnvCredentials(cmd.Context(), r.Client, id)
		if err != nil {
			return err
		}

		if r.Printer.Format == output.FormatJSON {
			return r.Printer.Print(creds)
		}
		return printCredentials(r.Printer, creds)
	},
}

// printCredentials renders the reveal as the same vertical key/value layout 'env get' uses,
// so the rows read as the continuation of that output rather than a different tool.
func printCredentials(p *output.Printer, c *environmentCredentials) error {
	w := tabwriter.NewWriter(p.W, 0, 0, 2, ' ', 0)
	row := func(k, v string) {
		if v == "" {
			return
		}
		fmt.Fprintf(w, "%s\t%s\n", k+":", v)
	}
	row("USERNAME", derefStr(c.Username))
	row("PASSWORD", derefStr(c.Password))
	row("WS ACCESS KEY", derefStr(c.WebServiceAccessKey))
	return w.Flush()
}
