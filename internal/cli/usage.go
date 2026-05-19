package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/bcdock/cli/internal/output"
	"github.com/spf13/cobra"
)

type companyUsageSummary struct {
	Company      string `header:"COMPANY"`
	From         string `header:"FROM"`
	To           string `header:"TO"`
	TotalHours   string `header:"TOTAL_HOURS"`
	Environments int    `header:"ENVIRONMENTS"`
}

type companyEnvUsageRow struct {
	Name        string `header:"ENVIRONMENT"`
	TotalHours  string `header:"TOTAL_HOURS"`
	TotalAmount string `header:"AMOUNT"`
}

var usageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Show company-level usage and billing",
	Long: `Show usage summary for the active company.

Defaults to the last 30 days. Use --from/--to for a custom date range.
Use --by-environment for a per-environment breakdown.

For usage scoped to a single environment, use 'bcdock env usage <name>'.

Exit codes:
  0   ok
  1   general error (no active company)
  3   auth failure (missing or invalid token)
  4   rate-limited`,
	Example: `  bcdock usage
  bcdock usage --from 2026-03-01 --to 2026-03-31
  bcdock usage --by-environment
  bcdock usage -o csv > usage.csv`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		from, _ := cmd.Flags().GetString("from")
		to, _ := cmd.Flags().GetString("to")
		byEnv, _ := cmd.Flags().GetBool("by-environment")

		var me struct {
			CompanyId   string `json:"companyId"`
			CompanyName string `json:"companyName"`
		}
		if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/auth/me", nil, &me); err != nil {
			return err
		}
		if me.CompanyId == "" {
			return fmt.Errorf("no active company - run 'bcdock companies switch <name>' first")
		}

		params := buildUsageDateParams(from, to)
		basePath := "/api/v1/companies/" + me.CompanyId

		if byEnv {
			return printUsageByEnvironment(cmd, r, basePath, params)
		}
		return printUsageSummary(cmd, r, basePath, params, me.CompanyName, from, to)
	},
}

func printUsageSummary(cmd *cobra.Command, r *Resolved, basePath, params, companyName, from, to string) error {
	var result struct {
		Summary struct {
			TotalSeconds     int     `json:"totalSeconds"`
			TotalHours       float64 `json:"totalHours"`
			EnvironmentCount int     `json:"environmentCount"`
		} `json:"summary"`
		Records []struct {
			Date         string `json:"date"`
			TotalSeconds int    `json:"totalSeconds"`
		} `json:"records"`
	}
	if err := r.Client.Do(cmd.Context(), http.MethodGet, basePath+"/usage"+params, nil, &result); err != nil {
		return err
	}

	if r.Printer.Format == output.FormatJSON {
		return r.Printer.Print(result)
	}

	displayFrom := from
	if displayFrom == "" {
		displayFrom = time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	}
	displayTo := to
	if displayTo == "" {
		displayTo = time.Now().Format("2006-01-02")
	}

	return r.Printer.Print(companyUsageSummary{
		Company:      companyName,
		From:         displayFrom,
		To:           displayTo,
		TotalHours:   fmt.Sprintf("%.1fh", result.Summary.TotalHours),
		Environments: result.Summary.EnvironmentCount,
	})
}

func printUsageByEnvironment(cmd *cobra.Command, r *Resolved, basePath, params string) error {
	var rows []struct {
		DisplayName         string  `json:"displayName"`
		TotalRunningSeconds int     `json:"totalRunningSeconds"`
		TotalAmount         float64 `json:"totalAmount"`
	}
	if err := r.Client.Do(cmd.Context(), http.MethodGet, basePath+"/usage/by-environment"+params, nil, &rows); err != nil {
		return err
	}

	if r.Printer.Format == output.FormatJSON {
		return r.Printer.Print(rows)
	}

	displayRows := make([]companyEnvUsageRow, len(rows))
	for i, row := range rows {
		hours := float64(row.TotalRunningSeconds) / 3600.0
		displayRows[i] = companyEnvUsageRow{
			Name:        row.DisplayName,
			TotalHours:  fmt.Sprintf("%.1fh", hours),
			TotalAmount: fmt.Sprintf("%.2f", row.TotalAmount),
		}
	}
	return r.Printer.Print(displayRows)
}

func buildUsageDateParams(from, to string) string {
	params := url.Values{}
	if from != "" {
		params.Set("from", from)
	}
	if to != "" {
		params.Set("to", to)
	}
	if len(params) == 0 {
		return ""
	}
	return "?" + params.Encode()
}

func init() {
	usageCmd.Flags().String("from", "", "Start date (YYYY-MM-DD, default: 30 days ago)")
	usageCmd.Flags().String("to", "", "End date (YYYY-MM-DD, default: today)")
	usageCmd.Flags().Bool("by-environment", false, "Show per-environment breakdown")
	RootCmd.AddCommand(usageCmd)
}
