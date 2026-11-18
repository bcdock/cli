package cli

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// ── bcdock waitlist join ────────────────────────────────────────────────────
//
// SEC-023: a fully NON-INTERACTIVE waitlist submit, so the CI smoke suite can drive
// the gated dev mirror unattended. Unlike `bcdock auth join-waitlist` (interactive,
// always the SEC-012 deferred browser-confirm flow), this never prompts and submits
// INLINE when the dev Turnstile bypass is active.
//
// Turnstile: when BCDOCK_DEV_TURNSTILE_BYPASS is set, the resolved client sends the
// X-Dev-Turnstile-Bypass header (see root.go) which the API honours only on
// BCDOCK_ENV=dev - so the submit clears Turnstile inline (Deferred=false). Without it
// (a real human running this verb), it falls back to the deferred flow so the human
// clears Turnstile in a browser. The substring allowlist (SEC-016a) still applies: the
// email must carry an allowlisted token to actually create a row on dev.

var waitlistCmd = &cobra.Command{
	Use:   "waitlist",
	Short: "Join the BCDock waitlist",
	Long: `Join the BCDock waitlist.

BCDock is in early access. Submit a waitlist entry and an admin reviews it, then
emails you an invite code (typically within 48 hours). Once you have the code,
run 'bcdock auth signup'.

Exit codes:
  0   ok
  1   general error
  4   rate-limited`,
	Example: `  bcdock waitlist join --name "Jane Smith" --email jane@example.com`,
}

var waitlistJoinCmd = &cobra.Command{
	Use:   "join",
	Short: "Submit a waitlist entry non-interactively (CI-friendly)",
	Long: `Submit a waitlist entry without any prompts.

--name and --email are required; everything else is optional. Unlike
'bcdock auth join-waitlist' (interactive), this verb never prompts - it is the
form the smoke suite and other automation drive.

On the dev mirror with BCDOCK_DEV_TURNSTILE_BYPASS set, the submit clears the
Turnstile gate inline; otherwise it requests the deferred browser-confirm flow
and prints a confirm URL.

Exit codes:
  0   ok
  1   general error (missing required flag, validation)
  4   rate-limited`,
	Example: `  bcdock waitlist join --name "Jane Smith" --email jane@example.com
  bcdock waitlist join --name CI --email "ci+TOKEN.noemail@example.com"`,
	RunE: runWaitlistJoin,
}

func runWaitlistJoin(cmd *cobra.Command, _ []string) error {
	r := GetResolved(cmd)

	name := strings.TrimSpace(mustFlag(cmd, "name"))
	email := strings.TrimSpace(mustFlag(cmd, "email"))
	if name == "" {
		return fmt.Errorf("--name is required")
	}
	if email == "" {
		return fmt.Errorf("--email is required")
	}

	multiTenantFlag := mustFlag(cmd, "multi-tenant")
	var multiTenant *bool
	if multiTenantFlag != "" {
		v := strings.EqualFold(multiTenantFlag, "true") || strings.EqualFold(multiTenantFlag, "yes")
		multiTenant = &v
	}

	// SEC-023: when the dev Turnstile bypass is set, attach the X-Dev-Turnstile-Bypass header to
	// THIS POST only (scoped to the waitlist submit, not every request) and submit inline; the API
	// honours it solely on BCDOCK_ENV=dev. Without it, request the deferred flow so a real human
	// clears Turnstile in a browser.
	bypass := os.Getenv("BCDOCK_DEV_TURNSTILE_BYPASS")

	req := waitlistFullRequest{
		Name:            name,
		Email:           email,
		UseCase:         mustFlag(cmd, "use-case"),
		BcVersion:       mustFlag(cmd, "bc-version"),
		Country:         mustFlag(cmd, "country"),
		ArtifactType:    mustFlag(cmd, "artifact-type"),
		MultiTenant:     multiTenant,
		PreferredRegion: mustFlag(cmd, "region"),
		SourcePage:      "cli",
		Deferred:        bypass == "",
	}

	var headers map[string]string
	if bypass != "" {
		headers = map[string]string{"X-Dev-Turnstile-Bypass": bypass}
	}

	var resp struct {
		Message    string `json:"message"`
		ConfirmUrl string `json:"confirmUrl"`
	}
	if err := r.Client.DoWithHeaders(cmd.Context(), http.MethodPost, "/api/v1/public/waitlist", req, &resp, headers); err != nil {
		return err
	}

	r.Printer.Info("%s", resp.Message)
	if resp.ConfirmUrl != "" {
		r.Printer.Info("Confirm (clear Turnstile in a browser): %s", resp.ConfirmUrl)
	}
	return nil
}

// mustFlag returns the string flag value (empty string if unset); helper to keep
// the request assembly above flat and readable.
func mustFlag(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

func init() {
	waitlistJoinCmd.Flags().String("name", "", "Your name (required)")
	waitlistJoinCmd.Flags().String("email", "", "Email address (required)")
	waitlistJoinCmd.Flags().String("use-case", "", "What you'd like BCDock to help you with (optional)")
	waitlistJoinCmd.Flags().String("bc-version", "", "Default BC version (optional)")
	waitlistJoinCmd.Flags().String("country", "", "Default BC country code (optional)")
	waitlistJoinCmd.Flags().String("artifact-type", "", "Default artifact type: Sandbox or OnPrem (optional)")
	waitlistJoinCmd.Flags().String("multi-tenant", "", "true|false - preferred multi-tenant default (optional)")
	waitlistJoinCmd.Flags().String("region", "", "Preferred Azure region (optional)")

	waitlistCmd.AddCommand(waitlistJoinCmd)
	RootCmd.AddCommand(waitlistCmd)
}
