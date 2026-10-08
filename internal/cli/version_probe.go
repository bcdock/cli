package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bcdock/cli/internal/client"
)

// probeVersionSkew fetches /api/version and prints a one-line stderr warning
// when the local CLI build is older than the server's advertised
// minimumCliVersion.
//
// Network failures, parse errors, "dev" CLI builds, and pre-skew-aware servers
// (no /api/version endpoint) are silent - the probe is informational, never a
// gate. Bounded to 1.5s so adding it doesn't make every command feel sluggish
// when the server is slow.
func probeVersionSkew(ctx context.Context, c *client.Client, cliVersion string, stderr io.Writer) {
	if cliVersion == "" || cliVersion == "dev" {
		return
	}

	probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, c.BaseURL+"/api/version", nil)
	if err != nil {
		return
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return
	}

	var body struct {
		API               string `json:"api"`
		MinimumCliVersion string `json:"minimumCliVersion"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return
	}
	if body.MinimumCliVersion == "" {
		return
	}

	cmp, ok := compareSemver(cliVersion, body.MinimumCliVersion)
	if !ok || cmp >= 0 {
		return
	}

	fmt.Fprintf(stderr,
		"warning: bcdock CLI %s is older than the server's minimum (%s) - upgrade to avoid surprise errors on new flags/endpoints.\n",
		cliVersion, body.MinimumCliVersion)
}

// compareSemver returns -1/0/+1 for a vs b. Tolerates a leading "v" and
// ignores pre-release/build metadata. Returns ok=false if either side isn't
// parseable as N.N.N - caller treats that as "no warning".
func compareSemver(a, b string) (int, bool) {
	pa, ok := parseSemver(a)
	if !ok {
		return 0, false
	}
	pb, ok := parseSemver(b)
	if !ok {
		return 0, false
	}
	for i := range pa {
		if pa[i] < pb[i] {
			return -1, true
		}
		if pa[i] > pb[i] {
			return 1, true
		}
	}
	return 0, true
}

func parseSemver(v string) ([3]int, bool) {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// versionProbeWriter is overridable in tests.
var versionProbeWriter io.Writer = os.Stderr
