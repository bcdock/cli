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
	Use:   "config",
	Short: "Discover available regions and configurations",
}

var configVersionsCmd = &cobra.Command{
	Use:   "versions",
	Short: "List available BC versions (use 'bcdock artifacts list' instead)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return errNotImplemented()
	},
}

var configRegionsCmd = &cobra.Command{
	Use:   "regions",
	Short: "List available Azure regions",
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
