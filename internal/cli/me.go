package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// User-self LAW-004 surface (GDPR Art. 15 / 17 + APP 12 / 13). Mirrors the
// portal /profile cards so an agent can run the same flow on the user's
// behalf without going through the UI.

// fullMeResponse mirrors GET /api/auth/me - superset of meResponse in auth.go
// adds the LAW-004 status + deletionScheduledAt fields.
type fullMeResponse struct {
	ID                  string `json:"id"                  header:"ID"`
	Email               string `json:"email"               header:"EMAIL"`
	DisplayName         string `json:"displayName"         header:"NAME"`
	PlatformRole        string `json:"platformRole"        header:"ROLE"`
	CompanyID           string `json:"companyId"           header:"COMPANY_ID"`
	CompanyName         string `json:"companyName"         header:"COMPANY"`
	AzureRegion         string `json:"azureRegion"         header:"REGION"`
	TimeZone            string `json:"timeZone"            header:"TIMEZONE"`
	Status              string `json:"status"              header:"STATUS"`
	DeletionScheduledAt string `json:"deletionScheduledAt" header:"DELETION_AT"`
}

type dataExportSummary struct {
	ID            string `json:"id"            header:"ID"`
	Status        string `json:"status"        header:"STATUS"`
	RequestedAt   string `json:"requestedAt"   header:"REQUESTED"`
	CompletedAt   string `json:"completedAt"   header:"COMPLETED"`
	ExpiresAt     string `json:"expiresAt"     header:"EXPIRES"`
	DownloadURL   string `json:"downloadUrl"   header:"DOWNLOAD_URL"`
	ErrorMessage  string `json:"errorMessage"  header:"ERROR"`
}

type deletionRequestResult struct {
	UserID                              string `json:"userId"                              header:"USER_ID"`
	ScheduledAnonymiseAt                string `json:"scheduledAnonymiseAt"                header:"ANONYMISE_AT"`
	CompaniesToBeDeleted                int    `json:"companiesToBeDeleted"                header:"DELETE"`
	CompaniesOwnershipTransferred       int    `json:"companiesOwnershipTransferred"       header:"TRANSFER"`
	MembershipsRemoved                  int    `json:"membershipsRemoved"                  header:"MEMBERSHIPS"`
	EnvironmentsScheduledForHibernation int    `json:"environmentsScheduledForHibernation" header:"HIBERNATE"`
}

type CancelDeletionResult struct {
	Cancelled bool `json:"cancelled" header:"CANCELLED"`
}

// ── me root ────────────────────────────────────────────────────────────────

var meCmd = &cobra.Command{
	Use:     "me",
	GroupID: "account",
	Short: "Manage your own account (export data, request deletion, cancel)",
	Long: `User-self surface for GDPR Art. 15 / 17 and APP 12 / 13.

  bcdock me show              → identity + status + deletion schedule (if any)
  bcdock me export            → request a ZIP of all data we hold for you
  bcdock me delete            → schedule account deletion in 30 days
  bcdock me cancel-deletion   → reverse a pending deletion

Auth: requires a JWT or API key bound to your account.

Exit codes:
  0   ok
  1   general error`,
	Example: `  bcdock me show
  bcdock me export --wait
  bcdock me delete --confirm you@example.com`,
}

// ── me show ────────────────────────────────────────────────────────────────

var meShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show your account profile (id, email, role, company, deletion status)",
	Long: `Print the full profile for the authenticated user: id, email, display name,
platform role, active company, region, time zone, account status, and deletion
schedule (if a deletion request is pending).

Exit codes:
  0   ok
  1   general error (not authenticated)
  3   auth failure (invalid token)
  4   rate-limited`,
	Example: `  bcdock me show
  bcdock me show -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		if r.Token == "" {
			return fmt.Errorf("not authenticated - run 'bcdock auth set-token <token>' or set BCDOCK_TOKEN")
		}
		var me fullMeResponse
		if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/auth/me", nil, &me); err != nil {
			return err
		}
		return r.Printer.Print(me)
	},
}

// ── me export ──────────────────────────────────────────────────────────────

var meExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Request a ZIP of all data we hold for your account and company",
	Long: `Request a GDPR-style data export. The platform builds a ZIP of CSVs
covering your environments, usage history, audit log, billing history, email
log, etc., uploads it to encrypted blob storage, and emails you a 24-hour
SAS download link.

Idempotent: if a request is already pending or processing, the existing one is
returned instead of creating a duplicate.

Exit codes:
  0   ok
  1   general error
  3   auth failure (missing or invalid token)
  4   rate-limited
  124 timeout (--wait only: export not ready within --wait-timeout)`,
	Example: `  bcdock me export
  bcdock me export --wait
  bcdock me export --wait --out export.zip
  bcdock me export -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		if r.Token == "" {
			return fmt.Errorf("not authenticated - run 'bcdock auth set-token <token>' or set BCDOCK_TOKEN")
		}

		wait, _ := cmd.Flags().GetBool("wait")
		waitTimeout, _ := cmd.Flags().GetDuration("wait-timeout")
		outPath, _ := cmd.Flags().GetString("out")

		var summary dataExportSummary
		if err := r.Client.Do(cmd.Context(), http.MethodPost, "/api/v1/me/export", nil, &summary); err != nil {
			return err
		}

		if !wait {
			if outPath != "" {
				return fmt.Errorf("--out requires --wait (export must be ready before downloading)")
			}
			return r.Printer.Print(summary)
		}

		// Poll until ready/failed/expired or timeout.
		final, err := pollExportRequest(cmd.Context(), r, summary.ID, waitTimeout)
		if err != nil {
			return err
		}

		if outPath != "" {
			if final.Status != "ready" || final.DownloadURL == "" {
				return fmt.Errorf("cannot download - export status=%s", final.Status)
			}
			if err := downloadExport(cmd.Context(), final.DownloadURL, outPath); err != nil {
				return fmt.Errorf("download: %w", err)
			}
			r.Printer.Info("Wrote %s", outPath)
		}
		return r.Printer.Print(final)
	},
}

func pollExportRequest(ctx context.Context, r *Resolved, id string, timeout time.Duration) (dataExportSummary, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	interval := 2 * time.Second
	r.Printer.Info("Waiting for export to be ready (timeout %s)...", timeout)
	for {
		var s dataExportSummary
		if err := r.Client.Do(ctx, http.MethodGet, "/api/v1/me/export/"+id, nil, &s); err != nil {
			return s, err
		}
		switch s.Status {
		case "ready", "failed", "expired":
			return s, nil
		}
		if time.Now().After(deadline) {
			// timeoutError (defined in env.go) carries exit code 124.
			return s, &timeoutError{msg: fmt.Sprintf("export still %s after %s", s.Status, timeout)}
		}
		select {
		case <-ctx.Done():
			return s, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// downloadExport fetches a SAS URL and writes the body to a local file.
// Distinct from al.go's downloadFile, which deals with vsix-specific concerns
// (insecure cert quirks for self-hosted, custom timeouts).
func downloadExport(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

// ── me delete ──────────────────────────────────────────────────────────────

var meDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Schedule account deletion in 30 days (reversible until then)",
	Long: `Schedule anonymisation of your account in 30 days. Within that window:
  - your running envs are hibernated to stop active-rate billing
  - companies you sole-own are flagged for anonymisation
  - companies you co-own transfer ownership to the oldest co-member
  - you can sign back in any time to cancel ('me cancel-deletion' or just re-signin)

Confirm by passing --confirm with your account email. The CLI never deletes
based on prompts; --confirm is mandatory for unattended / agent use.

Exit codes:
  0   ok
  1   general error (wrong --confirm email)
  3   auth failure (missing or invalid token)`,
	Example: `  bcdock me delete --confirm you@example.com
  bcdock me delete --confirm you@example.com -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		if r.Token == "" {
			return fmt.Errorf("not authenticated - run 'bcdock auth set-token <token>' or set BCDOCK_TOKEN")
		}
		confirm, _ := cmd.Flags().GetString("confirm")
		if strings.TrimSpace(confirm) == "" {
			return fmt.Errorf("--confirm is required (must equal your account email)")
		}
		body := map[string]string{"confirm": confirm}
		var result deletionRequestResult
		if err := r.Client.Do(cmd.Context(), http.MethodPost, "/api/v1/me/delete", body, &result); err != nil {
			return err
		}
		return r.Printer.Print(result)
	},
}

// ── me cancel-deletion ─────────────────────────────────────────────────────

var meCancelDeletionCmd = &cobra.Command{
	Use:   "cancel-deletion",
	Short: "Cancel a pending deletion (idempotent - no-op if nothing pending)",
	Long: `Reverse a pending account deletion. Returns cancelled=true if there was
a pending request, cancelled=false otherwise - safe to call unconditionally.

Exit codes:
  0   ok
  3   auth failure (missing or invalid token)`,
	Example: `  bcdock me cancel-deletion
  bcdock me cancel-deletion -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		if r.Token == "" {
			return fmt.Errorf("not authenticated - run 'bcdock auth set-token <token>' or set BCDOCK_TOKEN")
		}
		var result CancelDeletionResult
		if err := r.Client.Do(cmd.Context(), http.MethodPost, "/api/v1/me/cancel-deletion", nil, &result); err != nil {
			return err
		}
		return r.Printer.Print(result)
	},
}

func init() {
	meExportCmd.Flags().Bool("wait", false, "Poll until status flips to ready/failed/expired")
	meExportCmd.Flags().Duration("wait-timeout", 5*time.Minute, "Maximum time to poll when --wait is set (exit 124 if exceeded)")
	meExportCmd.Flags().String("out", "", "When --wait succeeds, download the ZIP to this path")
	meDeleteCmd.Flags().String("confirm", "", "Must equal your account email (required)")
	_ = meDeleteCmd.MarkFlagRequired("confirm")

	meCmd.AddCommand(meShowCmd, meExportCmd, meDeleteCmd, meCancelDeletionCmd)
	RootCmd.AddCommand(meCmd)
}
