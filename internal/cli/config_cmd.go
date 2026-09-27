package cli

import (
	"net/http"

	"github.com/bcdock/cli/internal/output"
	"github.com/spf13/cobra"
)

type publicRegion struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	StateCity   string `json:"stateCity"`
	CountryCode string `json:"countryCode"`
	CountryName string `json:"countryName"`
}

type regionRow struct {
	Region   string `header:"REGION"`
	Name     string `header:"NAME"`
	Location string `header:"LOCATION"`
	Country  string `header:"COUNTRY"`
}

var configCmd = &cobra.Command{
	Use:     "config",
	GroupID: "discovery",
	Short: "Discover available regions and configurations",
	Long: `Discover what Azure regions and BC configurations the BCDock platform supports.

For BC version and country discovery, prefer 'bcdock artifacts list' which
also shows pre-built image availability (FAST column).

Exit codes:
  0   ok
  1   general error`,
	Example: `  bcdock config regions
  bcdock config regions -o json`,
}

var configVersionsCmd = &cobra.Command{
	Use:   "versions",
	Short: "List available BC versions (use 'bcdock artifacts list' instead)",
	Long: `List available BC versions. This command is not yet implemented - use
'bcdock artifacts list' to browse versions with pre-built image information.

Exit codes:
  1   general error (not yet implemented)`,
	Example: `  bcdock artifacts list --region australiaeast --fast-only`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return errNotImplemented()
	},
}

var configRegionsCmd = &cobra.Command{
	Use:   "regions",
	Short: "List available Azure regions",
	Long: `List the Azure regions where BCDock can provision environments.

Use the REGION column value as --region in 'env create' and 'artifacts list'.

Exit codes:
  0   ok
  3   auth failure (missing or invalid token)
  4   rate-limited`,
	Example: `  bcdock config regions
  bcdock config regions -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		var regions []publicRegion
		if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/public/regions", nil, &regions); err != nil {
			return err
		}

		if r.Printer.Format == output.FormatJSON {
			return r.Printer.Print(regions)
		}

		rows := make([]regionRow, len(regions))
		for i, reg := range regions {
			rows[i] = regionRow{
				Region:   reg.ID,
				Name:     reg.Name,
				Location: reg.StateCity,
				Country:  reg.CountryName,
			}
		}
		return r.Printer.Print(rows)
	},
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available configurations (versions, countries, regions)",
	Long: `List all available BC configurations - versions, countries, and regions -
in one call. This command is not yet implemented.

Use these commands today:
  bcdock config regions              - Azure regions
  bcdock artifacts list --region <r> - BC versions + countries in a region
  bcdock artifacts countries         - country codes

Exit codes:
  1   general error (not yet implemented)`,
	Example: `  bcdock config regions
  bcdock artifacts list --region australiaeast --fast-only`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return errNotImplemented()
	},
}

func init() {
	configVersionsCmd.Flags().String("region", "", "Filter by region")
	configVersionsCmd.Flags().String("country", "", "Filter by country")
	configVersionsCmd.Flags().Bool("fast-only", false, "Only versions with pre-built images")

	configCmd.AddCommand(configVersionsCmd, configRegionsCmd, configListCmd)
	RootCmd.AddCommand(configCmd)
}
