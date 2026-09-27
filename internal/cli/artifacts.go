package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/bcdock/cli/internal/output"
	"github.com/spf13/cobra"
)

type artifactVersion struct {
	VersionFull  string `json:"versionFull"`
	VersionMajor int    `json:"versionMajor"`
	VersionMinor int    `json:"versionMinor"`
	Country      string `json:"country"`
	CountryName  string `json:"countryName"`
	ArtifactType string `json:"artifactType"`
	IsPreview    bool   `json:"isPreview"`
	Category     string `json:"category"`
	HasVmImage   bool   `json:"hasVmImage"`
	IsStale      bool   `json:"isStale"`
}

type artifactVersionCatalog struct {
	Region       string            `json:"region"`
	Versions     []artifactVersion `json:"versions"`
	LastSyncedAt *string           `json:"lastSyncedAt"`
}

type artifactVersionRow struct {
	Version string `header:"VERSION"`
	Country string `header:"COUNTRY"`
	Type    string `header:"TYPE"`
	Fast    string `header:"FAST"`
	Stale   string `header:"STALE"`
}

type artifactCountry struct {
	Code string `json:"code" header:"CODE"`
	Name string `json:"name" header:"NAME"`
}

type artifactCountriesResponse struct {
	Countries []artifactCountry `json:"countries"`
}

var artifactsCmd = &cobra.Command{
	Use:     "artifacts",
	GroupID: "discovery",
	Short: "Discover BC artifact versions and countries",
	Long: `Browse the BC artifact catalog for available versions.

Use this before 'env create' to find valid version and country combinations.
FAST=yes means a pre-built image is ready (~7-15 min on a warm pool vs ~78 min
first-time image build).

Exit codes:
  0   ok
  1   general error`,
	Example: `  bcdock artifacts list --region australiaeast --fast-only
  bcdock artifacts list --region australiaeast --country au
  bcdock artifacts countries`,
}

var artifactsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List BC versions available in a region",
	Long: `List BC artifact versions available in the given Azure region.

FAST=yes rows have a pre-built VM image, which means provisioning takes 7-15
minutes on a warm pool instead of ~78 minutes for an image build. Always prefer
fast configs unless you explicitly need a specific version.

Use --country to narrow to one localisation. Use --type to filter to Sandbox
or OnPrem artifacts. Use --fast-only to show only rows with a pre-built image.

Exit codes:
  0   ok
  1   general error (e.g. invalid --type value)
  3   auth failure (missing or invalid token)
  4   rate-limited`,
	Example: `  bcdock artifacts list --region australiaeast
  bcdock artifacts list --region australiaeast --fast-only
  bcdock artifacts list --region australiaeast --country au --type sandbox
  bcdock artifacts list --region westus2 --fast-only -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		region, _ := cmd.Flags().GetString("region")
		country, _ := cmd.Flags().GetString("country")
		artifactType, _ := cmd.Flags().GetString("type")
		includePreview, _ := cmd.Flags().GetBool("include-preview")
		fastOnly, _ := cmd.Flags().GetBool("fast-only")

		if region == "" {
			return fmt.Errorf("--region is required (e.g. --region australiaeast)")
		}

		// Normalise --type input (sandbox / onprem) to the API's casing (Sandbox / OnPrem).
		// Reject anything else loudly so a typo doesn't silently filter to zero rows.
		var typeFilter string
		switch strings.ToLower(strings.TrimSpace(artifactType)) {
		case "":
			// no-op - no filter
		case "sandbox":
			typeFilter = "Sandbox"
		case "onprem", "on-prem":
			typeFilter = "OnPrem"
		default:
			return fmt.Errorf("--type must be 'sandbox' or 'onprem' (got %q)", artifactType)
		}

		params := url.Values{"region": {region}}
		if country != "" {
			params.Set("country", country)
		}
		if includePreview {
			params.Set("includePreview", "true")
		}

		var catalog artifactVersionCatalog
		if err := r.Client.Do(cmd.Context(), http.MethodGet,
			"/api/v1/public/artifact-versions?"+params.Encode(), nil, &catalog); err != nil {
			return err
		}

		// Type and fast-only are filtered client-side - the public catalog endpoint
		// only accepts region/country/includePreview as query params today.
		versions := catalog.Versions
		if typeFilter != "" {
			filtered := versions[:0]
			for _, v := range versions {
				if v.ArtifactType == typeFilter {
					filtered = append(filtered, v)
				}
			}
			versions = filtered
		}
		if fastOnly {
			filtered := versions[:0]
			for _, v := range versions {
				if v.HasVmImage {
					filtered = append(filtered, v)
				}
			}
			versions = filtered
		}

		if r.Printer.Format == output.FormatJSON {
			return r.Printer.Print(versions)
		}

		rows := make([]artifactVersionRow, len(versions))
		for i, v := range versions {
			fast := ""
			if v.HasVmImage {
				fast = "yes"
			}
			stale := ""
			if v.IsStale {
				stale = "yes"
			}
			rows[i] = artifactVersionRow{
				Version: v.VersionFull,
				Country: v.Country,
				Type:    v.ArtifactType,
				Fast:    fast,
				Stale:   stale,
			}
		}
		return r.Printer.Print(rows)
	},
}

var artifactsCountriesCmd = &cobra.Command{
	Use:   "countries",
	Short: "List countries with active BC artifact versions",
	Long: `List all BC localisation country codes that have at least one active artifact
version in the BCDock catalog.

Use the CODE column as the --country value for 'artifacts list' and 'env create'.

Exit codes:
  0   ok
  3   auth failure (missing or invalid token)
  4   rate-limited`,
	Example: `  bcdock artifacts countries
  bcdock artifacts countries -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		var resp artifactCountriesResponse
		if err := r.Client.Do(cmd.Context(), http.MethodGet,
			"/api/v1/public/artifact-countries", nil, &resp); err != nil {
			return err
		}

		if r.Printer.Format == output.FormatJSON {
			return r.Printer.Print(resp.Countries)
		}
		return r.Printer.Print(resp.Countries)
	},
}

func init() {
	artifactsListCmd.Flags().String("region", "", "Azure region (required, e.g. australiaeast)")
	artifactsListCmd.Flags().String("country", "", "Filter by country code (e.g. au)")
	artifactsListCmd.Flags().String("type", "", "Filter by artifact type: sandbox or onprem")
	artifactsListCmd.Flags().Bool("include-preview", false, "Include insider/preview builds")
	artifactsListCmd.Flags().Bool("fast-only", false, "Only versions with pre-built images (~7-15 min on a warm pool)")

	artifactsCmd.AddCommand(artifactsListCmd, artifactsCountriesCmd)
	RootCmd.AddCommand(artifactsCmd)
}
