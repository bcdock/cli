package cli

import (
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

// #682: two portal buttons the CLI lacked - "Retry" on a failed environment and "Rename".

type envRetryResponse struct {
	ID      string `json:"id"      header:"ID"`
	ShortID string `json:"shortId" header:"SHORT_ID"`
	Status  string `json:"status"  header:"STATUS"`
	JobID   string `json:"jobId"   header:"JOB"`
}

var envRetryCmd = &cobra.Command{
	Use:   "retry <name|shortId>",
	Short: "Retry provisioning an environment that failed",
	Long: `Retry provisioning an environment in the 'error' status, the same as the
portal's Retry button. Only a failed environment can be retried; for any other
status the API refuses and nothing changes.

Use --wait to block until the environment is running or has failed again.

Needs the env:write scope (keys from 'bcdock auth login' have it).

Exit codes:
  0   ok
  1   general error, including the environment not being in 'error', provisioning
      failing again (its error message is printed), or a --wait timeout
  3   auth failure (missing or invalid token)
  4   rate-limited
  5   environment not found`,
	Example: `  bcdock env retry my-env
  bcdock env retry my-env --wait
  bcdock env retry my-env --wait -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		wait, _ := cmd.Flags().GetBool("wait")
		waitTimeout, _ := cmd.Flags().GetDuration("wait-timeout")

		id, err := resolveEnvID(cmd.Context(), r.Client, args[0])
		if err != nil {
			return err
		}

		var resp envRetryResponse
		if err := r.Client.Do(cmd.Context(), http.MethodPost, "/api/v1/environments/"+id+"/retry", nil, &resp); err != nil {
			return err
		}

		if !wait {
			r.Printer.Info("Retry started (status: %s).", resp.Status)
			return r.Printer.Print(resp)
		}

		if waitTimeout == 0 {
			waitTimeout = 30 * time.Minute
		}
		r.Printer.Info("Waiting for environment to be ready (timeout: %s)...", waitTimeout)

		env, err := pollEnv(cmd.Context(), r.Client, id, r.Printer, waitTimeout)
		if err != nil {
			return err
		}
		if ferr := envFailure(env, "provisioning failed"); ferr != nil {
			return ferr
		}
		return printEnvResult(r.Printer, *env)
	},
}

type envRenameResponse struct {
	ID          string `json:"id"          header:"ID"`
	DisplayName string `json:"displayName" header:"DISPLAY_NAME"`
}

var envRenameCmd = &cobra.Command{
	Use:   "rename <name|shortId> <display-name>",
	Short: "Change an environment's display name",
	Long: `Change the name an environment shows in the portal and in 'env list', the
same as the portal's Rename. Only the display name changes: the environment's
own name (its container name, used in its URLs) never changes, so its URLs and
credentials stay the same.

The display name is 1 to 60 characters.

Needs the env:write scope (keys from 'bcdock auth login' have it).

Exit codes:
  0   ok
  1   general error (for example, a name over 60 characters, or a deleted environment)
  3   auth failure (missing or invalid token)
  4   rate-limited
  5   environment not found`,
	Example: `  bcdock env rename my-env "Credit hold demo"
  bcdock env rename a1b2c3d4 "UAT - client X" -o json`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		id, err := resolveEnvID(cmd.Context(), r.Client, args[0])
		if err != nil {
			return err
		}
		body := struct {
			DisplayName string `json:"displayName"`
		}{DisplayName: args[1]}

		var resp envRenameResponse
		if err := r.Client.Do(cmd.Context(), http.MethodPatch, "/api/v1/environments/"+id, body, &resp); err != nil {
			return err
		}
		r.Printer.Info("Renamed %s to %q.", args[0], resp.DisplayName)
		return r.Printer.Print(resp)
	},
}

func init() {
	envRetryCmd.Flags().Bool("wait", false, "Block until running or failed")
	envRetryCmd.Flags().Duration("wait-timeout", 0, "Max time to wait (default: 30m)")
	envCmd.AddCommand(envRetryCmd, envRenameCmd)
}
