package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/bcdock/cli/internal/client"
	"github.com/bcdock/cli/internal/config"
	"github.com/bcdock/cli/internal/exitcode"
	"github.com/bcdock/cli/internal/output"
	"github.com/spf13/cobra"
)

// Resolved holds values after global flag resolution in PersistentPreRunE.
type Resolved struct {
	Token   string
	APIURL  string
	Printer *output.Printer
	Client  *client.Client
}

type contextKey int

const resolvedKey contextKey = 0

// Global flag vars (read by PersistentPreRunE)
var (
	flagToken   string
	flagAPIURL  string
	flagOutput  string
	flagQuiet   bool
	flagNoColor bool
	flagTimeout time.Duration
)

var RootCmd = &cobra.Command{
	Use:   "bcdock",
	Short: "BCDock CLI - manage Business Central environments",
	Long: `BCDock CLI wraps the BCDock Platform API for use in terminals,
scripts, and AI agent workflows.

Set BCDOCK_TOKEN to authenticate without --token.
Set BCDOCK_API_URL to target a different API endpoint.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip resolution for version and help - they don't need auth
		if cmd.Name() == "version" || cmd.Name() == "help" {
			return nil
		}

		token := flagToken
		if token == "" {
			token = os.Getenv("BCDOCK_TOKEN")
		}
		if token == "" {
			creds, err := config.LoadCredentials()
			if err == nil {
				token = creds.Token
			}
		}

		apiURL := flagAPIURL
		if apiURL == "" {
			apiURL = os.Getenv("BCDOCK_API_URL")
		}
		if apiURL == "" {
			cfg, err := config.LoadConfig()
			if err == nil && cfg.ApiUrl != "" {
				apiURL = cfg.ApiUrl
			}
		}
		if apiURL == "" {
			apiURL = "https://api.bcdock.io"
		}

		r := &Resolved{
			Token:   token,
			APIURL:  apiURL,
			Printer: output.NewWithErr(flagOutput, flagQuiet, flagNoColor, cmd.OutOrStdout(), cmd.ErrOrStderr()),
			Client:  client.New(apiURL, token, flagTimeout),
		}

		// Best-effort skew check; bounded to 1.5s. "version" command opts out
		// because it exists precisely to report build state without side-effects.
		probeVersionSkew(cmd.Context(), r.Client, version, versionProbeWriter)

		cmd.SetContext(context.WithValue(cmd.Context(), resolvedKey, r))
		return nil
	},
}

func GetResolved(cmd *cobra.Command) *Resolved {
	r, _ := cmd.Context().Value(resolvedKey).(*Resolved)
	if r == nil {
		// Fallback for commands that skip PersistentPreRunE (version, help)
		return &Resolved{
			Printer: output.New(flagOutput, flagQuiet, flagNoColor, cmd.OutOrStdout()),
		}
	}
	return r
}

func errNotImplemented() error {
	return fmt.Errorf("not yet implemented - coming in a future CLI release")
}

func exitWithError(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	if apiErr, ok := err.(*client.APIError); ok {
		os.Exit(apiErr.ExitCode())
	}
	os.Exit(exitcode.GeneralError)
}

func init() {
	RootCmd.PersistentFlags().StringVarP(&flagOutput, "output", "o", "table", "Output format: table, json, csv")
	RootCmd.PersistentFlags().StringVar(&flagToken, "token", "", "API token (overrides BCDOCK_TOKEN env var)")
	RootCmd.PersistentFlags().StringVar(&flagAPIURL, "api-url", "", "API base URL (overrides BCDOCK_API_URL env var)")
	RootCmd.PersistentFlags().BoolVarP(&flagQuiet, "quiet", "q", false, "Suppress non-essential output")
	RootCmd.PersistentFlags().BoolVar(&flagNoColor, "no-color", false, "Disable colored output")
	RootCmd.PersistentFlags().DurationVar(&flagTimeout, "timeout", 30*time.Second, "Request timeout")
}
