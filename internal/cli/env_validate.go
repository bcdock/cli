package cli

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/bcdock/cli/internal/client"
)

// validateEnvCreateInput rejects a (version, country, region, artifactType) combo client-side
// before it reaches the API. Without this, a typo in --version or --country leaves a dangling
// EnvironmentRecord that goes to status=error after a slow round-trip; the server-side guard
// (API-001) catches it too, but raising the error locally gives the user a closer-match hint.
//
// Inputs:
//   - region: Azure region id (e.g. "westus2")
//   - version: BC version full (e.g. "27.1.41600.0")
//   - country: BC localisation code (e.g. "us") - case-insensitive
//   - artifactType: "Sandbox" or "OnPrem" (already normalised by the caller)
func validateEnvCreateInput(ctx context.Context, c *client.Client, region, version, country, artifactType string) error {
	if region == "" || version == "" || country == "" {
		// Earlier flag checks already rejected blanks; nothing to validate.
		return nil
	}

	// 1. Region must be in /api/public/regions.
	var regions []publicRegion
	if err := c.Do(ctx, http.MethodGet, "/api/v1/public/regions", nil, &regions); err != nil {
		// Don't block on a transient catalog fetch failure - the API will still validate.
		return nil
	}
	regionIDs := make([]string, len(regions))
	for i, r := range regions {
		regionIDs[i] = r.ID
	}
	if !containsFold(regionIDs, region) {
		return fmt.Errorf("invalid --region %q.%s Run 'bcdock config regions' to see all available regions",
			region, suggestion("region", region, regionIDs))
	}

	// 2. (version, country, artifactType) must be in the per-region artifact catalog.
	var catalog artifactVersionCatalog
	if err := c.Do(ctx, http.MethodGet, "/api/v1/public/artifact-versions?region="+region, nil, &catalog); err != nil {
		return nil
	}

	var versions, countries []string
	versionSet, countrySet := map[string]bool{}, map[string]bool{}
	matchedVersion := false
	for _, v := range catalog.Versions {
		if !versionSet[v.VersionFull] {
			versions = append(versions, v.VersionFull)
			versionSet[v.VersionFull] = true
		}
		c2 := strings.ToLower(v.Country)
		if !countrySet[c2] {
			countries = append(countries, c2)
			countrySet[c2] = true
		}
		if v.VersionFull == version {
			matchedVersion = true
		}
	}

	if !matchedVersion {
		return fmt.Errorf("invalid --version %q for region %q.%s Run 'bcdock artifacts list --region %s' to see valid versions",
			version, region, suggestion("version", version, versions), region)
	}

	if !containsFold(countries, country) {
		return fmt.Errorf("invalid --country %q for BC %s in %q.%s Run 'bcdock artifacts list --region %s' to see valid countries",
			country, version, region, suggestion("country", country, countries), region)
	}

	// Tuple match: (version, country, artifactType) all together.
	for _, v := range catalog.Versions {
		if v.VersionFull == version &&
			strings.EqualFold(v.Country, country) &&
			strings.EqualFold(v.ArtifactType, artifactType) {
			return nil
		}
	}
	return fmt.Errorf("BC %s is not published as %s for country %s in %s. Run 'bcdock artifacts list --region %s' to see valid combinations",
		version, artifactType, strings.ToLower(country), region, region)
}

// containsFold reports whether s is in candidates under case-insensitive comparison.
func containsFold(candidates []string, s string) bool {
	for _, c := range candidates {
		if strings.EqualFold(c, s) {
			return true
		}
	}
	return false
}

// suggestion returns " Did you mean \"<closest>\"?" or "" when no candidate is close enough.
// "Close enough" = edit distance ≤ 3, scaled with input length so short typos still match.
func suggestion(field, input string, candidates []string) string {
	best := ""
	bestDist := 1 << 30
	for _, c := range candidates {
		d := levenshtein(strings.ToLower(input), strings.ToLower(c))
		if d < bestDist {
			bestDist = d
			best = c
		}
	}
	threshold := 3
	if len(input) <= 4 {
		threshold = 2
	}
	if best == "" || bestDist > threshold {
		return ""
	}
	return fmt.Sprintf(" Did you mean %q?", best)
}

// levenshtein computes the edit distance between two strings.
// Small alphabet, candidate lists in the tens - naive O(mn) is fine.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
