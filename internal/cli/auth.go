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

  1. bcdock auth join-waitlist                                    (request access)
  2. Wait for your invite (reviewed within 48 hours)
  3. bcdock auth signup --invite-code CODE --email you@example.com (activate account)
  4. bcdock auth login --email you@example.com                    (OTP login)

Returning users:
  bcdock auth login --email you@example.com

For CI/CD or agent use, store an API key directly:
  bcdock auth set-token <key>

See 'bcdock help authentication' for detailed token-source precedence and
CI/CD best practices.

Exit codes:
  0   ok
  1   general error`,
	Example: `  bcdock auth login --email you@example.com
  bcdock auth whoami
  bcdock auth set-token bdk_xxxxxxxxxxxxxxxxxxxx
  bcdock auth logout`,
}

var authWaitlistCmd = &cobra.Command{
	Use:   "waitlist",
	Short: "Request access to BCDock (currently invitation-only)",
	Long: `Submit your name and email to join the BCDock waitlist.

BCDock access is currently gated by invitation. Once your request is
reviewed and approved (within 48 hours) you will receive an invite code
by email. Then run:

  bcdock auth login --email you@example.com

This command will be removed once BCDock is open to all users.

Exit codes:
  0   ok
  1   general error`,
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
	Short: "Log in via email OTP (returning users; account must already exist)",
	Long: `Authenticate using the email one-time password flow.

Use this for returning users. Login does NOT create new accounts - brand-new
emails get an explicit error pointing at 'bcdock auth signup'. New users:

  1. bcdock auth join-waitlist
  2. (wait for invite email)
  3. bcdock auth signup --invite-code CODE --email you@example.com
  4. bcdock auth login --email you@example.com

You will be prompted for your email address, then a 6-digit code sent
to that address. A long-lived API key (bdk_...) is minted and stored in
~/.bcdock/credentials.json - no silent expiry.

For non-interactive use (CI, smoke tests, agents): pass --email and --otp
together to skip both prompts. The OTP must already be known to the
caller (Development mode accepts "000000" - see local-e2e.sh notes).

For CI/CD and agent use, prefer API keys: bcdock auth set-token <key>

Exit codes:
  0   ok
  1   general error (wrong email, wrong code, email not registered)
  3   auth failure
  4   rate-limited`,
	Example: `  bcdock auth login --email you@example.com
  bcdock auth login --email you@example.com --otp 123456`,
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
  2. BCDOCK_TOKEN environment variable  (env: BCDOCK_TOKEN)
  3. ~/.bcdock/credentials.json (set by this command)

Exit codes:
  0   ok
  1   general error (cannot write credentials file)`,
	Example: `  bcdock auth set-token bdk_xxxxxxxxxxxxxxxxxxxx`,
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
	Long: `Print the email, display name, role, and company for the authenticated user.

Useful as a quick auth check in scripts and agent flows. Returns a non-zero
exit code if the token is missing or invalid.

Exit codes:
  0   ok
  1   general error (not authenticated)
  3   auth failure (invalid token)
  4   rate-limited`,
	Example: `  bcdock auth whoami
  bcdock auth whoami -o json`,
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
	Long: `Invalidate the current session server-side and clear the token from
~/.bcdock/credentials.json. Safe to run even if not authenticated.

Does not revoke API keys created via the portal; use the portal API-keys page
to revoke long-lived keys.

Exit codes:
  0   ok
  1   general error (cannot clear credentials file)`,
	Example: `  bcdock auth logout`,
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
