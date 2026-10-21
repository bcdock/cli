package cli

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bcdock/cli/internal/client"
	"github.com/spf13/cobra"
)

// #682: request a BC version that has no ready image, over POST /api/v1/version-requests (the
// portal's "Request this version"). Under version gating (FastOnly) `env create` refuses such a
// version with 422 request_version_required, and before this verb an agent had no way past it.

type versionRequestBody struct {
	VersionID    string `json:"versionId"`
	Country      string `json:"country"`
	ArtifactType string `json:"artifactType"`
	MultiTenant  bool   `json:"multiTenant"`
	Message      string `json:"message,omitempty"`
}

type versionRequestResponse struct {
	ID     string `json:"id"     header:"ID"`
	Status string `json:"status" header:"STATUS"`
}

type rolloutConfig struct {
	VersionGating struct {
		Mode             string `json:"mode"`
		ResponseSlaHours int    `json:"responseSlaHours"`
	} `json:"versionGating"`
}

// artifactTypeForAPI maps --type (sandbox / onprem) to the catalog's casing, as `artifacts list` does.
func artifactTypeForAPI(t string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "", "sandbox":
		return "Sandbox", nil
	case "onprem", "on-prem":
		return "OnPrem", nil
	}
	return "", fmt.Errorf("--type must be sandbox or onprem, got %q", t)
}

var artifactsRequestCmd = &cobra.Command{
	Use:   "request <version>",
	Short: "Request a BC version that has no ready image yet",
	Long: `Ask BCDock to build a BC version that has no pre-built image, so that
environments can be created on it. This is the same request as the portal's
"Request this version" button.

Use the full version (versionFull from 'bcdock artifacts list'). When
'bcdock env create' refuses a version because it has no ready image, its error
prints this command with the version filled in.

What happens next: BCDock replies by email, usually within the time this
command prints. Once the image is built, 'bcdock artifacts list --fast-only'
shows the version and 'bcdock env create' accepts it.

Needs the env:write scope (keys from 'bcdock auth login' have it).

Exit codes:
  0   ok, request submitted
  1   general error (for example, the version is not in the catalog for that
      country and type, or version requests are not open on this platform)
  3   auth failure (missing or invalid token)
  4   rate-limited`,
	Example: `  bcdock artifacts list --region australiaeast --country au   # find the versionFull
  bcdock artifacts request <version> --country au
  bcdock artifacts request <version> --country us --type onprem --message "Needed for a client demo"
  bcdock artifacts request <version> --country au -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		country, _ := cmd.Flags().GetString("country")
		typeFlag, _ := cmd.Flags().GetString("type")
		multiTenant, _ := cmd.Flags().GetBool("multi-tenant")
		message, _ := cmd.Flags().GetString("message")

		if strings.TrimSpace(country) == "" {
			return fmt.Errorf("--country is required (e.g. --country au)")
		}
		artifactType, err := artifactTypeForAPI(typeFlag)
		if err != nil {
			return err
		}
		version := strings.TrimSpace(args[0])

		var resp versionRequestResponse
		err = r.Client.Do(cmd.Context(), http.MethodPost, "/api/v1/version-requests", versionRequestBody{
			VersionID: version, Country: country, ArtifactType: artifactType,
			MultiTenant: multiTenant, Message: message,
		}, &resp)
		if err != nil {
			// The route answers 404 when version gating is not in FastOnly mode: there is nothing to
			// request, because any catalog version can then be created directly.
			var apiErr *client.APIError
			if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
				friendly := *apiErr
				friendly.ErrorText = ""
				friendly.Message = "Version requests are not open on this platform: create the environment directly with 'bcdock env create'"
				return &friendly
			}
			return err
		}

		sla := ""
		var rc rolloutConfig
		if r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/public/rollout-config", nil, &rc) == nil && rc.VersionGating.ResponseSlaHours > 0 {
			sla = fmt.Sprintf(" BCDock usually replies by email within %d hours.", rc.VersionGating.ResponseSlaHours)
		}
		r.Printer.Info("Requested BC %s (%s, %s).%s When it is built, 'bcdock artifacts list --fast-only' shows it.",
			version, strings.ToUpper(country), artifactType, sla)
		return r.Printer.Print(resp)
	},
}

func init() {
	artifactsRequestCmd.Flags().String("country", "", "Country localisation (required, e.g. au, us, gb)")
	artifactsRequestCmd.Flags().String("type", "sandbox", "Artifact type: sandbox or onprem")
	artifactsRequestCmd.Flags().Bool("multi-tenant", true, "Multi-tenant (the default for 'env create')")
	artifactsRequestCmd.Flags().String("message", "", "Optional note for the BCDock team (up to 2000 characters)")
	artifactsCmd.AddCommand(artifactsRequestCmd)
}
