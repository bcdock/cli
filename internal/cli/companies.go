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

// activeCompany returns the company the caller's credentials are scoped to (GET /auth/me).
func activeCompany(cmd *cobra.Command, r *Resolved) (id, name string, err error) {
	var me struct {
		CompanyID   string `json:"companyId"`
		CompanyName string `json:"companyName"`
	}
	if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/auth/me", nil, &me); err != nil {
		return "", "", err
	}
	if me.CompanyID == "" {
		return "", "", fmt.Errorf("no active company - run 'bcdock companies switch <name>' first")
	}
	return me.CompanyID, me.CompanyName, nil
}

// ── companies set-location (#715) ──────────────────────────────────────────

type companyLocationRequest struct {
	CountryCode     string `json:"countryCode"`
	AzureRegionName string `json:"azureRegionName,omitempty"`
}

type companyLocationResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Country struct {
		Code string `json:"code"`
		Name string `json:"name"`
	} `json:"country"`
	Region *struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		AzureRegion string `json:"azureRegion"`
	} `json:"region"`
}

var companiesSetLocationCmd = &cobra.Command{
	Use:   "set-location <country-code>",
	Short: "Set the active company's default country and region for new environments",
	Long: `Set the active company's default location: a country, and optionally the Azure
region within it. New environments default to it; existing environments do not move.

Without --region the country's default region is used. --region must be one of the
country's regions (see 'bcdock config regions'). Owners and admins only.

Auth: any API key scope; your role in the company must be owner or admin.

Exit codes:
  0   ok
  1   general error (unknown country code, or a region outside that country)
  3   auth failure (missing or invalid token, or not an owner/admin)
  4   rate-limited
  5   company not found`,
	Example: `  bcdock companies set-location AU
  bcdock companies set-location US --region centralus
  bcdock companies set-location AU -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		id, _, err := activeCompany(cmd, r)
		if err != nil {
			return err
		}
		region, _ := cmd.Flags().GetString("region")
		req := companyLocationRequest{CountryCode: strings.ToUpper(strings.TrimSpace(args[0])), AzureRegionName: strings.TrimSpace(region)}

		var resp companyLocationResponse
		if err := r.Client.Do(cmd.Context(), http.MethodPut, "/api/v1/companies/"+id+"/location", req, &resp); err != nil {
			return err
		}
		if r.Printer.Format == output.FormatJSON {
			return r.Printer.Print(resp)
		}
		where := resp.Country.Name + " (" + resp.Country.Code + ")"
		if resp.Region != nil {
			where += ", region " + resp.Region.AzureRegion
		}
		r.Printer.Info("%s: new environments now default to %s", resp.Name, where)
		return nil
	},
}

func init() {
	companiesSetLocationCmd.Flags().String("region", "", "Azure region within the country (default: the country's default region)")
	companiesCmd.AddCommand(companiesListCmd, companiesSwitchCmd, companiesSetLocationCmd)
	RootCmd.AddCommand(companiesCmd)
}
