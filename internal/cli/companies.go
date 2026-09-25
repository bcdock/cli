package cli

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/bcdock/cli/internal/client"
	"github.com/bcdock/cli/internal/config"
	"github.com/bcdock/cli/internal/output"
	"github.com/spf13/cobra"
)

type company struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt"`
}

type companyRow struct {
	Name    string `header:"NAME"`
	Slug    string `header:"SLUG"`
	Role    string `header:"ROLE"`
	Created string `header:"CREATED"`
}

type switchCompanyResponse struct {
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	Company      *companyInfoDto `json:"company"`
}

type companyInfoDto struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	Role string `json:"role"`
}

var companiesCmd = &cobra.Command{
	Use:     "companies",
	GroupID: "account",
	Short: "Manage companies (billing entities)",
	Long: `List and switch between companies.

A company is the billing entity in BCDock. Use 'companies switch' to change
which company's environments and billing context are active.

Exit codes:
  0   ok
  1   general error`,
	Example: `  bcdock companies list
  bcdock companies switch contoso
  bcdock companies switch 3072f5a0-0000-0000-0000-000000000000`,
}

var companiesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List companies you are a member of",
	Long: `List all companies your account belongs to and your role in each.

A company is the billing entity in BCDock - environments, invoices, and usage
roll up to a company. Use 'bcdock companies switch' to change the active company.

Exit codes:
  0   ok
  3   auth failure (missing or invalid token)
  4   rate-limited`,
	Example: `  bcdock companies list
  bcdock companies list -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		var companies []company
		if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/companies", nil, &companies); err != nil {
			return err
		}

		if r.Printer.Format == output.FormatJSON {
			return r.Printer.Print(companies)
		}

		rows := make([]companyRow, len(companies))
		for i, c := range companies {
			created := c.CreatedAt
			if len(created) >= 10 {
				created = created[:10]
			}
			rows[i] = companyRow{
				Name:    c.Name,
				Slug:    c.Slug,
				Role:    c.Role,
				Created: created,
			}
		}
		return r.Printer.Print(rows)
	},
}

var companiesSwitchCmd = &cobra.Command{
	Use:   "switch <id|name|slug>",
	Short: "Switch active company context",
	Long: `Switch to a different company. All subsequent commands run against the new company.

New tokens are stored in ~/.bcdock/credentials.json. Pass the company name,
slug, or GUID - the CLI resolves all three.

Exit codes:
  0   ok
  3   auth failure (missing or invalid token)
  4   rate-limited
  5   company not found`,
	Example: `  bcdock companies switch contoso
  bcdock companies switch 3072f5a0-0000-0000-0000-000000000000`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		id, err := resolveCompanyID(cmd, r, args[0])
		if err != nil {
			return err
		}

		var resp switchCompanyResponse
		if err := r.Client.Do(cmd.Context(), http.MethodPost, "/api/v1/companies/"+id+"/switch", nil, &resp); err != nil {
			return err
		}

		if err := config.SaveCredentials(&config.Credentials{
			Token: resp.AccessToken,
		}); err != nil {
			return fmt.Errorf("save credentials: %w", err)
		}

		name := args[0]
		if resp.Company != nil {
			name = resp.Company.Name
		}
		r.Printer.Info("Switched to company: %s", name)
		return nil
	},
}

func resolveCompanyID(cmd *cobra.Command, r *Resolved, nameOrID string) (string, error) {
	if IsGUID(nameOrID) {
		return nameOrID, nil
	}
	var companies []company
	if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/companies", nil, &companies); err != nil {
		return "", err
	}
	for _, c := range companies {
		if strings.EqualFold(c.Name, nameOrID) || strings.EqualFold(c.Slug, nameOrID) {
			return c.ID, nil
		}
	}
	return "", &client.APIError{
		Message: fmt.Sprintf("company %q not found", nameOrID),
		Status:  http.StatusNotFound,
	}
}

func init() {
	companiesCmd.AddCommand(companiesListCmd, companiesSwitchCmd)
	RootCmd.AddCommand(companiesCmd)
}
