package cli

import (
	"fmt"
	"net/http"
	"strings"

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
			return fmt.Errorf("not authenticated - run 'bcdock auth set-token <token>' or set BCDOCK_TOKEN")
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
			return fmt.Errorf("not authenticated - run 'bcdock auth set-token <token>' or set BCDOCK_TOKEN")
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
			return fmt.Errorf("not authenticated - run 'bcdock auth set-token <token>' or set BCDOCK_TOKEN")
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

func init() {
	meBillingPortalCmd.Flags().String("return-url", "", "URL Stripe redirects back to after the user closes the portal")
	meBillingCheckoutCmd.Flags().String("tier", "", "Tier to subscribe to: payg|starter|pro|business|enterprise (required)")
	meBillingCheckoutCmd.Flags().String("currency", "", "Currency: aud|usd (required)")
	meBillingCheckoutCmd.Flags().String("success-url", "", "URL Stripe redirects to after a successful payment (optional)")
	meBillingCheckoutCmd.Flags().String("cancel-url", "", "URL Stripe redirects to if the user cancels (optional)")
	meBillingCmd.AddCommand(meBillingShowCmd, meBillingPortalCmd, meBillingCheckoutCmd)
	meCmd.AddCommand(meBillingCmd)
}
