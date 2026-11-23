package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/bcdock/cli/internal/output"
	"github.com/spf13/cobra"
)

// End-user Stripe billing surface (`bcdock me billing …`). Mirrors
// BillingController - see docs/STRIPE_BILLING_PLAN.md. Admin billing verbs
// live in cmd/admin/billing.go.

// ── DTOs (mirror C# BCDock.Platform.API.Models.BillingResponse) ────────────

type billingPlanDTO struct {
	Tier              string  `json:"tier"              header:"TIER"`
	DisplayName       string  `json:"displayName"       header:"PLAN"`
	Currency          string  `json:"currency"          header:"CURRENCY"`
	BaseMonthlyAmount float64 `json:"baseMonthlyAmount" header:"BASE/MO"`
	HourlyRate        float64 `json:"hourlyRate"        header:"ACTIVE/H"`
	StoredHourlyRate  float64 `json:"storedHourlyRate"  header:"STORED/H"`
	MaxEnvironments   *int    `json:"maxEnvironments"   header:"ENV CAP"`
}

type billingSubscriptionDTO struct {
	StripeSubscriptionID *string `json:"stripeSubscriptionId" header:"SUBSCRIPTION"`
	StripeStatus         *string `json:"stripeStatus"         header:"STATUS"`
	CurrentPeriodStart   *string `json:"currentPeriodStart"   header:"PERIOD START"`
	CurrentPeriodEnd     *string `json:"currentPeriodEnd"     header:"PERIOD END"`
	CancelAtPeriodEnd    bool    `json:"cancelAtPeriodEnd"    header:"CANCELING"`
	CanceledAt           *string `json:"canceledAt"           header:"CANCELED AT"`
}

type billingPaymentMethodDTO struct {
	StripePaymentMethodID string `json:"stripePaymentMethodId" header:"PM"`
}

type billingInvoiceDTO struct {
	ID               string  `json:"id"`
	StripeInvoiceID  string  `json:"stripeInvoiceId"   header:"INVOICE"`
	Amount           int64   `json:"amount"            header:"AMOUNT (cents)"`
	Currency         string  `json:"currency"          header:"CURRENCY"`
	Status           string  `json:"status"            header:"STATUS"`
	PeriodStart      string  `json:"periodStart"       header:"PERIOD START"`
	PeriodEnd        string  `json:"periodEnd"         header:"PERIOD END"`
	PaidAt           *string `json:"paidAt"            header:"PAID AT"`
	HostedInvoiceURL *string `json:"hostedInvoiceUrl"`
	InvoicePDF       *string `json:"invoicePdf"`
}

type billingResponseDTO struct {
	Plan          billingPlanDTO           `json:"plan"`
	Subscription  *billingSubscriptionDTO  `json:"subscription"`
	PaymentMethod *billingPaymentMethodDTO `json:"paymentMethod"`
	Invoices      []billingInvoiceDTO      `json:"invoices"`
}

type portalSessionRequest struct {
	ReturnURL string `json:"returnUrl,omitempty"`
}

type portalSessionResponse struct {
	URL string `json:"url" header:"URL"`
}

// ── me billing root ────────────────────────────────────────────────────────

var meBillingCmd = &cobra.Command{
	Use:   "billing",
	Short: "View your subscription, payment method, and invoice history",
	Long: `Subscription state, last 12 invoices, and a one-click handoff to the
Stripe-hosted Customer Portal where you manage your card and plan.

  bcdock me billing show     → mirror snapshot (plan + subscription + invoices)
  bcdock me billing portal   → URL for the Stripe Customer Portal session
  bcdock me billing checkout → start a Stripe Checkout flow to add a card
  bcdock me billing export   → per-environment usage and cost as CSV
  bcdock me billing cycle    → the company's totals for a period, and trial progress

Auth: requires a JWT or API key bound to your account.

Exit codes:
  0   ok
  1   general error`,
	Example: `  bcdock me billing show
  bcdock me billing portal
  bcdock me billing checkout --tier starter --currency usd`,
}

var meBillingShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current plan, subscription state, and last 12 invoices",
	Long: `Print a snapshot of your billing state: active plan (tier, rates, env cap),
Stripe subscription status, last 12 invoices, and attached payment method.

For ongoing card / plan management use 'bcdock me billing portal' to open the
Stripe Customer Portal in a browser.

Exit codes:
  0   ok
  1   general error
  3   auth failure (missing or invalid token)
  4   rate-limited`,
	Example: `  bcdock me billing show
  bcdock me billing show -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		if r.Token == "" {
			return fmt.Errorf("not authenticated - run 'bcdock auth login', pipe a key to 'bcdock auth set-token', or set BCDOCK_TOKEN")
		}
		var resp billingResponseDTO
		if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/me/billing", nil, &resp); err != nil {
			return err
		}
		return r.Printer.Print(resp)
	},
}

var meBillingPortalCmd = &cobra.Command{
	Use:   "portal",
	Short: "Print the Stripe Customer Portal URL for managing card / plan / invoices",
	Long: `Creates a short-lived Stripe Customer Portal session and returns the URL.
Open it in a browser to update payment method, change plan, or cancel.

Returns 400 if your account has no Stripe Customer yet (still on free trial -
use 'bcdock me billing checkout' to add a card and pick a tier first).

Exit codes:
  0   ok
  1   general error (no Stripe customer yet)
  3   auth failure (missing or invalid token)
  4   rate-limited`,
	Example: `  bcdock me billing portal
  bcdock me billing portal -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		if r.Token == "" {
			return fmt.Errorf("not authenticated - run 'bcdock auth login', pipe a key to 'bcdock auth set-token', or set BCDOCK_TOKEN")
		}
		returnURL, _ := cmd.Flags().GetString("return-url")
		req := portalSessionRequest{ReturnURL: returnURL}
		var resp portalSessionResponse
		if err := r.Client.Do(cmd.Context(), http.MethodPost, "/api/v1/me/billing/portal-session", req, &resp); err != nil {
			return err
		}
		return r.Printer.Print(resp)
	},
}

type checkoutRequest struct {
	Tier       string `json:"tier"`
	Currency   string `json:"currency"`
	SuccessURL string `json:"successUrl,omitempty"`
	CancelURL  string `json:"cancelUrl,omitempty"`
}

type checkoutResponse struct {
	URL string `json:"url" header:"URL"`
}

var meBillingCheckoutCmd = &cobra.Command{
	Use:   "checkout",
	Short: "Start a Stripe Checkout flow for a chosen tier+currency (prints the hosted URL)",
	Long: `Creates a Stripe Checkout Session and prints the URL to open in a browser.
Stripe handles card collection, 3DS, Apple/Google Pay, then redirects back to the
portal /billing page. BCDock mirrors the new subscription server-side within
seconds of the customer completing the flow.

This is the only path for trial users to add a card and convert to a paid tier.
After conversion, use 'bcdock me billing portal' for ongoing card / plan management.

Exit codes:
  0   ok
  1   general error (invalid tier or currency)
  3   auth failure (missing or invalid token)
  4   rate-limited`,
	Example: `  bcdock me billing checkout --tier starter --currency aud
  bcdock me billing checkout --tier pro --currency usd`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		if r.Token == "" {
			return fmt.Errorf("not authenticated - run 'bcdock auth login', pipe a key to 'bcdock auth set-token', or set BCDOCK_TOKEN")
		}
		tier, _ := cmd.Flags().GetString("tier")
		currency, _ := cmd.Flags().GetString("currency")
		if tier == "" {
			return fmt.Errorf("--tier is required (payg|starter|pro|business|enterprise)")
		}
		if currency == "" {
			return fmt.Errorf("--currency is required (aud|usd)")
		}
		// Normalize: API does case-sensitive Currency='AUD' lookup against the
		// SubscriptionPlans table. Tier values are stored lowercase. Examples in
		// help text use lowercase for both, so accept either case and normalize
		// at the CLI boundary rather than forcing users to remember which is which.
		tier = strings.ToLower(strings.TrimSpace(tier))
		currency = strings.ToUpper(strings.TrimSpace(currency))
		successURL, _ := cmd.Flags().GetString("success-url")
		cancelURL, _ := cmd.Flags().GetString("cancel-url")
		req := checkoutRequest{Tier: tier, Currency: currency, SuccessURL: successURL, CancelURL: cancelURL}
		var resp checkoutResponse
		if err := r.Client.Do(cmd.Context(), http.MethodPost, "/api/v1/me/billing/checkout", req, &resp); err != nil {
			return err
		}
		return r.Printer.Print(resp)
	},
}

// dateRangeQuery reads --from/--to (YYYY-MM-DD, both optional) and returns "?from=..&to=.." or "".
// A date in any other shape fails here, before a request is made.
func dateRangeQuery(cmd *cobra.Command) (string, error) {
	params := url.Values{}
	for _, name := range []string{"from", "to"} {
		v, _ := cmd.Flags().GetString(name)
		if v == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", v); err != nil {
			return "", fmt.Errorf("--%s %q is not a date - use YYYY-MM-DD", name, v)
		}
		params.Set(name, v)
	}
	if len(params) == 0 {
		return "", nil
	}
	return "?" + params.Encode(), nil
}

// ── me billing export (#715) ──────────────────────────────────────────────

var meBillingExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Download per-environment usage and cost as CSV",
	Long: `Download the active company's usage per environment as CSV: one row per
environment with its hours and cost for the period. The same file as the portal's
billing export. It streams to stdout, or to --out.

--from and --to are dates (YYYY-MM-DD); they default to the last 30 days.

Auth: an API key with env:read or billing:write; you must be a member of the company.

Exit codes:
  0   ok
  1   general error (a date not in YYYY-MM-DD, or a write failure)
  3   auth failure (missing or invalid token, or a key without env:read/billing:write)
  4   rate-limited
  5   company not found`,
	Example: `  bcdock me billing export > usage.csv
  bcdock me billing export --from 2026-09-01 --to 2026-09-30 --out september.csv`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		outPath, _ := cmd.Flags().GetString("out")
		query, err := dateRangeQuery(cmd)
		if err != nil {
			return err
		}
		id, _, err := activeCompany(cmd, r)
		if err != nil {
			return err
		}
		path := "/api/v1/companies/" + id + "/billing/export.csv" + query

		body, err := r.Client.Stream(cmd.Context(), path)
		if err != nil {
			return err
		}
		defer body.Close()

		// Streamed, never buffered whole: io.Copy moves it in chunks to wherever it is going.
		if outPath == "" {
			_, err = io.Copy(cmd.OutOrStdout(), body)
			return err
		}
		f, err := os.Create(outPath)
		if err != nil {
			return fmt.Errorf("create %s: %w", outPath, err)
		}
		if _, err := io.Copy(f, body); err != nil {
			f.Close()
			_ = os.Remove(outPath) // no half-written file left behind
			return fmt.Errorf("write %s: %w", outPath, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("write %s: %w", outPath, err)
		}
		r.Printer.Info("Wrote %s", outPath)
		return nil
	},
}

// ── me billing cycle (#715) ───────────────────────────────────────────────

type billingCycle struct {
	FromDate               string  `json:"fromDate"`
	ToDate                 string  `json:"toDate"`
	TotalRunningSeconds    int     `json:"totalRunningSeconds"`
	TotalHibernatedSeconds int     `json:"totalHibernatedSeconds"`
	TotalAmount            float64 `json:"totalAmount"`
	Currency               string  `json:"currency"`
	SubscriptionMode       *string `json:"subscriptionMode"`
	Plan                   *struct {
		DisplayName     string `json:"displayName"`
		IncludedSeconds *int   `json:"includedSeconds"`
		ConsumedSeconds int    `json:"consumedSeconds"`
		IsTrialing      bool   `json:"isTrialing"`
	} `json:"plan"`
}

type billingCycleRow struct {
	Period     string `header:"PERIOD"`
	Active     string `header:"ACTIVE"`
	Hibernated string `header:"HIBERNATED"`
	Total      string `header:"TOTAL"`
	Plan       string `header:"PLAN"`
	Included   string `header:"INCLUDED USED"`
}

func hours(seconds int) string { return fmt.Sprintf("%.1fh", float64(seconds)/3600) }

var meBillingCycleCmd = &cobra.Command{
	Use:   "cycle",
	Short: "Show the active company's usage and cost totals for a period, and trial progress",
	Long: `Show the active company's totals for a period: active and hibernated hours, the
cost, the plan, and how much of the plan's included time is used (the trial). The
same figures as the portal's usage page. Per-environment rows: 'bcdock usage
--by-environment'; your plan, subscription and invoices: 'bcdock me billing show'.

--from and --to are dates (YYYY-MM-DD); they default to the last 30 days.

Auth: an API key with env:read or billing:write; you must be a member of the company.

Exit codes:
  0   ok
  1   general error (a date not in YYYY-MM-DD)
  3   auth failure (missing or invalid token, or a key without env:read/billing:write)
  4   rate-limited
  5   company not found`,
	Example: `  bcdock me billing cycle
  bcdock me billing cycle --from 2026-09-01 --to 2026-09-30
  bcdock me billing cycle -o json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		query, err := dateRangeQuery(cmd)
		if err != nil {
			return err
		}
		id, _, err := activeCompany(cmd, r)
		if err != nil {
			return err
		}
		if r.Printer.Format == output.FormatJSON {
			var raw json.RawMessage
			if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/companies/"+id+"/billing"+query, nil, &raw); err != nil {
				return err
			}
			return r.Printer.Print(raw)
		}
		var c billingCycle
		if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/companies/"+id+"/billing"+query, nil, &c); err != nil {
			return err
		}
		row := billingCycleRow{
			Period: c.FromDate + " to " + c.ToDate, Active: hours(c.TotalRunningSeconds), Hibernated: hours(c.TotalHibernatedSeconds),
			Total: fmt.Sprintf("%.2f %s", c.TotalAmount, c.Currency), Plan: "-", Included: "-",
		}
		if c.Plan != nil {
			row.Plan = c.Plan.DisplayName
			if c.Plan.IsTrialing {
				row.Plan += " (trial)"
			}
			if c.Plan.IncludedSeconds != nil {
				row.Included = fmt.Sprintf("%s of %s", hours(c.Plan.ConsumedSeconds), hours(*c.Plan.IncludedSeconds))
			}
		}
		return r.Printer.Print([]billingCycleRow{row})
	},
}

func init() {
	meBillingCycleCmd.Flags().String("from", "", "First day, YYYY-MM-DD (default: 30 days ago)")
	meBillingCycleCmd.Flags().String("to", "", "Last day, YYYY-MM-DD (default: today)")
	meBillingExportCmd.Flags().String("from", "", "First day, YYYY-MM-DD (default: 30 days ago)")
	meBillingExportCmd.Flags().String("to", "", "Last day, YYYY-MM-DD (default: today)")
	meBillingExportCmd.Flags().String("out", "", "Write the CSV to this file instead of stdout")
	meBillingPortalCmd.Flags().String("return-url", "", "URL Stripe redirects back to after the user closes the portal")
	meBillingCheckoutCmd.Flags().String("tier", "", "Tier to subscribe to: payg|starter|pro|business|enterprise (required)")
	meBillingCheckoutCmd.Flags().String("currency", "", "Currency: aud|usd (required)")
	meBillingCheckoutCmd.Flags().String("success-url", "", "URL Stripe redirects to after a successful payment (optional)")
	meBillingCheckoutCmd.Flags().String("cancel-url", "", "URL Stripe redirects to if the user cancels (optional)")
	meBillingCmd.AddCommand(meBillingShowCmd, meBillingPortalCmd, meBillingCheckoutCmd, meBillingExportCmd, meBillingCycleCmd)
	meCmd.AddCommand(meBillingCmd)
}
