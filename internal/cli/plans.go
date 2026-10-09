package cli

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/bcdock/cli/internal/output"
	"github.com/spf13/cobra"
)

// #715: the plan catalogue. GET /subscription/plans is reference data, the same for every company
// (the platform's own comment: nothing a caller could not read from the public pricing page).

type subscriptionPlan struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	DisplayName     string  `json:"displayName"`
	Description     string  `json:"description"`
	HourlyRate      float64 `json:"hourlyRate"`
	Currency        string  `json:"currency"`
	MaxEnvironments *int    `json:"maxEnvironments"`
}

type planRow struct {
	Plan       string `header:"PLAN"`
	Currency   string `header:"CURRENCY"`
	ActiveRate string `header:"ACTIVE RATE"`
	MaxEnvs    string `header:"MAX ENVS"`
}

var plansCmd = &cobra.Command{
	Use:     "plans",
	GroupID: "account",
	Short:   "Browse the subscription plans",
	Long: `Browse the plans a company can subscribe to. Switching plan is not a CLI command
yet: it opens when subscriptions do; until then it is done with BCDock.

Exit codes:
  0   ok
  1   general error`,
	Example: `  bcdock plans list`,
}

var plansListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the subscription plans and their active rates",
	Long: `List the plans a company can subscribe to, cheapest active rate first, with each
plan's environment cap. The same catalogue as the pricing page.

Auth: any API key scope (reference data, the same for every company).

Exit codes:
  0   ok
  3   auth failure (missing or invalid token)
  4   rate-limited`,
	Example: `  bcdock plans list
  bcdock plans list -o json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		var plans []subscriptionPlan
		if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/subscription/plans", nil, &plans); err != nil {
			return err
		}
		if r.Printer.Format == output.FormatJSON {
			return r.Printer.Print(plans)
		}
		// The catalogue has one row per plan per currency, so group by currency, cheapest first,
		// with custom-priced plans (rate 0, Enterprise) after the priced ones.
		sort.SliceStable(plans, func(i, j int) bool {
			if plans[i].Currency != plans[j].Currency {
				return plans[i].Currency < plans[j].Currency
			}
			ci, cj := plans[i].HourlyRate <= 0, plans[j].HourlyRate <= 0
			if ci != cj {
				return cj
			}
			return plans[i].HourlyRate < plans[j].HourlyRate
		})
		rows := make([]planRow, len(plans))
		for i, p := range plans {
			envs := "no cap"
			if p.MaxEnvironments != nil {
				envs = fmt.Sprintf("%d", *p.MaxEnvironments)
			}
			// A 0 rate is Enterprise's custom pricing (the pricing page says "Custom"), not free.
			rate := "custom"
			if p.HourlyRate > 0 {
				rate = fmt.Sprintf("%.2f/hr", p.HourlyRate)
			}
			rows[i] = planRow{Plan: p.DisplayName, Currency: p.Currency, ActiveRate: rate, MaxEnvs: envs}
		}
		return r.Printer.Print(rows)
	},
}

func init() {
	plansCmd.AddCommand(plansListCmd)
	RootCmd.AddCommand(plansCmd)
}
