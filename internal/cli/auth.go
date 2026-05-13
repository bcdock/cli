package cli

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/bcdock/cli/internal/config"
	"github.com/spf13/cobra"
)

// meResponse mirrors GET /api/auth/me
type meResponse struct {
	Email        string `json:"email"        header:"EMAIL"`
	DisplayName  string `json:"displayName"  header:"NAME"`
	PlatformRole string `json:"platformRole" header:"ROLE"`
	CompanyName  string `json:"companyName"  header:"COMPANY"`
}

// waitlistRequest mirrors POST /api/public/waitlist
type waitlistRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	UseCase string `json:"useCase,omitempty"`
}

// sendCodeRequest mirrors POST /api/auth/email/send-code
type sendCodeRequest struct {
	Email string `json:"email"`
}

// exchangeRequest mirrors POST /api/auth/email/exchange
type exchangeRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
	Label string `json:"label,omitempty"`
}

// exchangeResponse mirrors the response from POST /api/auth/email/exchange
type exchangeResponse struct {
	Key       string `json:"key"`
	KeyPrefix string `json:"keyPrefix"`
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authenticate with the BCDock platform",
	Long: `Manage authentication credentials.

BCDock access is currently invitation-only. New users follow this path:

  1. bcdock auth waitlist --name "Your Name" --email you@example.com
  2. Wait for your invite (reviewed within 48 hours)
  3. bcdock auth login --email you@example.com  (creates account + logs in)

Returning users:
  bcdock auth login --email you@example.com

For CI/CD or agent use, store an API key directly:
  bcdock auth set-token <key>`,
}

var authWaitlistCmd = &cobra.Command{
	Use:   "waitlist",
	Short: "Request access to BCDock (currently invitation-only)",
	Long: `Submit your name and email to join the BCDock waitlist.

BCDock access is currently gated by invitation. Once your request is
reviewed and approved (within 48 hours) you will receive an invite code
by email. Then run:

  bcdock auth login --email you@example.com

This command will be removed once BCDock is open to all users.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		name, _ := cmd.Flags().GetString("name")
		email, _ := cmd.Flags().GetString("email")
		useCase, _ := cmd.Flags().GetString("use-case")

		if name == "" {
			var err error
			name, err = prompt("Name: ")
			if err != nil {
				return err
			}
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("name is required")
		}

		if email == "" {
			var err error
			email, err = prompt("Email: ")
			if err != nil {
				return err
			}
		}
		email = strings.TrimSpace(email)
		if email == "" {
			return fmt.Errorf("email is required")
		}

		var resp struct {
			Message string `json:"message"`
		}
		if err := r.Client.Do(cmd.Context(), http.MethodPost, "/api/v1/public/waitlist",
			waitlistRequest{Name: name, Email: email, UseCase: useCase}, &resp); err != nil {
			return err
		}

		r.Printer.Info("%s", resp.Message)
		r.Printer.Info("Next step - once you receive your invite, run: bcdock auth login --email %s", email)
		return nil
	},
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in via email OTP - also handles first-time account creation",
	Long: `Authenticate using the email one-time password flow.

Works for both first-time signup and returning users. If your account
does not exist yet it will be created automatically - no separate signup
step required. (Access is currently invitation-only; use 'bcdock auth
waitlist' to request an invite before running this.)

You will be prompted for your email address, then a 6-digit code sent
to that address. A long-lived API key (bdk_...) is minted and stored in
~/.bcdock/credentials.json - no silent expiry.

For non-interactive use (CI, smoke tests, agents): pass --email and --otp
together to skip both prompts. The OTP must already be known to the
caller (Development mode accepts "000000" - see local-e2e.sh notes).

For CI/CD and agent use, prefer API keys: bcdock auth set-token <key>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		email, _ := cmd.Flags().GetString("email")
		otp, _ := cmd.Flags().GetString("otp")
		nonInteractive := otp != ""

		if email == "" {
			if nonInteractive {
				return fmt.Errorf("--email is required when --otp is provided")
			}
			var err error
			email, err = prompt("Email: ")
			if err != nil {
				return err
			}
		}
		email = strings.TrimSpace(email)
		if email == "" {
			return fmt.Errorf("email is required")
		}

		r.Printer.Info("Sending code to %s...", email)
		if err := r.Client.Do(cmd.Context(), http.MethodPost, "/api/v1/auth/email/send-code",
			sendCodeRequest{Email: email}, nil); err != nil {
			return err
		}

		var code string
		if nonInteractive {
			code = strings.TrimSpace(otp)
		} else {
			c, err := prompt("Code: ")
			if err != nil {
				return err
			}
			code = strings.TrimSpace(c)
		}
		if len(code) != 6 {
			return fmt.Errorf("code must be 6 digits")
		}

		hostname, _ := os.Hostname()
		label := "CLI key"
		if hostname != "" {
			label = "CLI: " + hostname
		}

		var resp exchangeResponse
		if err := r.Client.Do(cmd.Context(), http.MethodPost, "/api/v1/auth/email/exchange",
			exchangeRequest{Email: email, Code: code, Label: label}, &resp); err != nil {
			return err
		}

		if err := config.SaveCredentials(&config.Credentials{
			Token: resp.Key,
		}); err != nil {
			return fmt.Errorf("save credentials: %w", err)
		}

		r.Printer.Info("Logged in as %s. API key (%s) stored in %s/credentials.json",
			email, resp.KeyPrefix, config.ConfigDir())
		return nil
	},
}

var authSetTokenCmd = &cobra.Command{
	Use:   "set-token <token>",
	Short: "Store an API token persistently",
	Long: `Store an API key in ~/.bcdock/credentials.json for all future commands.

Generate API keys at: https://app.bcdock.io/profile/api-keys

The token is used in order of precedence:
  1. --token flag
  2. BCDOCK_TOKEN environment variable
  3. ~/.bcdock/credentials.json (set by this command)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		token := args[0]
		if err := config.SaveCredentials(&config.Credentials{Token: token}); err != nil {
			return fmt.Errorf("save credentials: %w", err)
		}
		r.Printer.Info("Token stored in %s/credentials.json", config.ConfigDir())
		return nil
	},
}

var authWhoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show the currently authenticated user",
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		if r.Token == "" {
			return fmt.Errorf("not authenticated - run 'bcdock auth set-token <token>' or set BCDOCK_TOKEN")
		}
		var me meResponse
		if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/auth/me", nil, &me); err != nil {
			return err
		}
		return r.Printer.Print(me)
	},
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Clear stored credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		// Best-effort server-side logout (invalidates refresh token)
		if r.Token != "" {
			_ = r.Client.Do(context.Background(), http.MethodPost, "/api/v1/auth/logout", nil, nil)
		}

		if err := config.SaveCredentials(&config.Credentials{}); err != nil {
			return fmt.Errorf("clear credentials: %w", err)
		}
		r.Printer.Info("Logged out.")
		return nil
	},
}

func init() {
	authWaitlistCmd.Flags().String("name", "", "Your name (skips prompt)")
	authWaitlistCmd.Flags().String("email", "", "Email address (skips prompt)")
	authWaitlistCmd.Flags().String("use-case", "", "Brief description of your use case (optional)")
	authLoginCmd.Flags().String("email", "", "Email address (skips prompt)")
	authLoginCmd.Flags().String("otp", "", "Pre-known OTP code - skips the interactive Code: prompt. Requires --email. Intended for smoke tests and agent flows; humans should leave this unset.")
	authCmd.AddCommand(authWaitlistCmd, authLoginCmd, authSetTokenCmd, authWhoamiCmd, authLogoutCmd)
	RootCmd.AddCommand(authCmd)
}

func prompt(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
