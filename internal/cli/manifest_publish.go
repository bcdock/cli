package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/bcdock/cli/internal/client"
	"github.com/bcdock/cli/internal/output"
)

// DEV-024: publishing a manifest's apps into a freshly created environment.
//
// This is orchestration over verbs that already exist. It reuses publishApp - the
// same multipart POST `bcdock env publish` uses - rather than reimplementing the
// /dev/apps client, so there is one wire implementation to keep correct.

// manifestPublishDefaults mirror `bcdock env publish`'s flag defaults. They are
// named here rather than inlined so a change to the publish verb's defaults is a
// visible mismatch rather than a silent divergence.
const (
	manifestSchemaUpdateMode = "synchronize"
	manifestPublishTimeout   = 10 * time.Minute
	// BCDock ships single-tenant=default for multi-tenant containers (env.go:634),
	// so an MT environment publishes into the "default" tenant. A single-tenant
	// container has no tenant query parameter at all.
	manifestMTTenant = "default"
)

// publishManifestApps publishes every app in MANIFEST ORDER into a running
// environment, printing one line each.
//
// v1 does not resolve AL dependencies - the manifest's order IS the dependency
// order, which is why this loop is strictly sequential and stops at the first
// failure. Continuing past a failed dependency would publish apps that cannot
// load and report a partially-broken environment as a partial success.
//
// On failure the environment is deliberately LEFT RUNNING: the apps are the cheap
// half and the environment is the twenty-five-minute half, so the useful recovery
// is to fix the app and re-publish, not to provision again.
func publishManifestApps(
	ctx context.Context,
	p *output.Printer,
	c *client.Client,
	envID string,
	m *Manifest,
	multiTenant bool,
	insecure bool,
) error {
	if len(m.Apps) == 0 {
		return nil
	}

	var env environment
	if err := c.Do(ctx, http.MethodGet, "/api/v1/environments/"+envID, nil, &env); err != nil {
		return fmt.Errorf("fetching environment for publish: %w", err)
	}
	if env.DevEndpointUrl == nil || *env.DevEndpointUrl == "" {
		return fmt.Errorf("environment has no dev endpoint URL (status: %s) - cannot publish manifest apps", env.Status)
	}
	// SEC-039: ONE reveal for the whole manifest, not one per app - each reveal writes an
	// audit row, and a per-app fetch would turn an auditable event into noise. This path was
	// not in SEC-039's original consumer list; it was added by DEV-024 (#280), the PR that
	// produced that list, and the compiler is what found it.
	creds, err := revealEnvCredentials(ctx, c, envID)
	if err != nil {
		return err
	}
	if creds.Username == nil || creds.Password == nil {
		return fmt.Errorf("environment has no admin credentials (status: %s) - cannot publish manifest apps", env.Status)
	}

	publishURL := manifestPublishURL(*env.DevEndpointUrl, multiTenant)

	for i, app := range m.Apps {
		n := i + 1
		// Publish is synchronous and runs 37-52s per app on a warm pool, so a
		// three-app manifest is ~2.5 minutes of apparent silence. Announce the app
		// BEFORE the call, not after.
		p.Info("Publishing app %d/%d: %s", n, len(m.Apps), app.Source())

		bytes, name, err := manifestAppBytes(ctx, m, app)
		if err != nil {
			return fmt.Errorf("app %d/%d (%s): %w", n, len(m.Apps), app.Source(), err)
		}

		if err := publishApp(ctx, p, publishURL, *creds.Username, *creds.Password, name, bytes, manifestPublishTimeout, insecure); err != nil {
			return fmt.Errorf("app %d/%d (%s): %w", n, len(m.Apps), app.Source(), err)
		}
		p.Info("Published %d/%d: %s", n, len(m.Apps), name)
	}
	return nil
}

// manifestPublishURL builds the same URL `bcdock env publish` builds, with the
// same defaults. The tenant is carried in the QUERY, which is why publishApp needs
// no tenant parameter.
func manifestPublishURL(devEndpointURL string, multiTenant bool) string {
	u := devEndpointURL + "dev/apps"
	q := url.Values{}
	q.Set("SchemaUpdateMode", strings.ToLower(manifestSchemaUpdateMode))
	if multiTenant {
		q.Set("tenant", manifestMTTenant)
	}
	return u + "?" + q.Encode()
}

// manifestAppBytes resolves one app entry to its bytes and the filename to publish
// it under. file: apps were already proven to exist by LoadManifest; github: apps
// are fetched now, because a release can disappear between validation and here.
func manifestAppBytes(ctx context.Context, m *Manifest, app ManifestApp) ([]byte, string, error) {
	if app.File != "" {
		resolved := app.ResolvedFile(m.Dir)
		b, err := os.ReadFile(resolved)
		if err != nil {
			return nil, "", fmt.Errorf("reading %s: %w", resolved, err)
		}
		return b, baseName(resolved), nil
	}

	owner, repo, tag, err := parseGitHubRef(app.GitHub)
	if err != nil {
		return nil, "", err
	}
	b, err := fetchGitHubAsset(ctx, githubAPIBase, owner, repo, tag, app.Asset, manifestPublishTimeout)
	if err != nil {
		return nil, "", err
	}
	return b, app.Asset, nil
}

// baseName is filepath.Base, spelled locally so this file does not import
// filepath solely for one call in a path that is already OS-agnostic.
func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
