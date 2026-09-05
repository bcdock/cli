package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DEV-024: resolving a manifest's `github:` app to bytes.
//
// A release asset is two requests: the release metadata by TAG (never "latest" -
// see parseGitHubRef), then the asset itself by its numeric id with an
// octet-stream Accept, because the same URL returns JSON metadata otherwise.

// githubAPIBase is the default API root. Tests inject a httptest server here.
const githubAPIBase = "https://api.github.com"

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// fetchGitHubAsset downloads one named asset from one tagged release.
//
// GITHUB_TOKEN is sent when set: the unauthenticated API allows 60 requests an
// hour per IP, which a manifest with several github apps can exhaust on a shared
// runner. A token is never required for a public repo.
func fetchGitHubAsset(ctx context.Context, apiBase, owner, repo, tag, assetName string, timeout time.Duration) ([]byte, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	client := &http.Client{Transport: &http.Transport{}, Timeout: timeout}

	relURL := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", strings.TrimRight(apiBase, "/"), owner, repo, tag)
	rel, err := githubGetRelease(ctx, client, relURL, owner, repo, tag)
	if err != nil {
		return nil, err
	}

	var match *githubAsset
	for i := range rel.Assets {
		if rel.Assets[i].Name == assetName {
			match = &rel.Assets[i]
			break
		}
	}
	if match == nil {
		// Name the asset AND list what the release actually has. "asset not found"
		// alone sends the reader to the browser; the available names usually make
		// the typo or the version drift obvious without leaving the terminal.
		return nil, fmt.Errorf("release %s/%s@%s has no asset %q%s",
			owner, repo, tag, assetName, availableAssets(rel.Assets))
	}

	// The asset URL comes out of the RESPONSE BODY, and the next request carries
	// a bearer token. Today that is safe only because apiBase is a const, so the
	// body is GitHub's - and it stops being safe the moment apiBase is
	// configurable for GHES (Ravi, #280). Bind the two now, while the invariant
	// still holds, rather than when it does not.
	//
	// Redirects are already handled: Go strips Authorization on a cross-host
	// redirect, which is what the S3 asset hand-off relies on. This covers the
	// FIRST hop, which the stdlib cannot know is untrusted.
	if err := sameHost(apiBase, match.URL); err != nil {
		return nil, fmt.Errorf("release %s/%s@%s asset %q: %w", owner, repo, tag, assetName, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, match.URL, nil)
	if err != nil {
		return nil, err
	}
	// Without this the asset URL returns the asset's JSON metadata, not its bytes.
	req.Header.Set("Accept", "application/octet-stream")
	githubAuth(req)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading %s from %s/%s@%s: %w", assetName, owner, repo, tag, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s from %s/%s@%s: HTTP %d", assetName, owner, repo, tag, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", assetName, err)
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("asset %s from %s/%s@%s is empty", assetName, owner, repo, tag)
	}
	return b, nil
}

func githubGetRelease(ctx context.Context, client *http.Client, url, owner, repo, tag string) (*githubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	githubAuth(req)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching release %s/%s@%s: %w", owner, repo, tag, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("release %s/%s@%s not found (check the tag, and that the repo is public or GITHUB_TOKEN is set)", owner, repo, tag)
	case http.StatusForbidden, http.StatusTooManyRequests:
		// The unauthenticated limit is the likeliest cause and the one with a fix
		// the reader can apply, so name it rather than printing a bare 403.
		return nil, fmt.Errorf("fetching release %s/%s@%s: HTTP %d - the GitHub API rate limit is 60/hour unauthenticated; set GITHUB_TOKEN to raise it",
			owner, repo, tag, resp.StatusCode)
	default:
		return nil, fmt.Errorf("fetching release %s/%s@%s: HTTP %d", owner, repo, tag, resp.StatusCode)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("parsing release %s/%s@%s: %w", owner, repo, tag, err)
	}
	return &rel, nil
}

// sameHost refuses an asset URL whose host differs from the API's. An attacker
// who can shape the release JSON would otherwise choose where our token goes.
func sameHost(apiBase, assetURL string) error {
	a, err := url.Parse(apiBase)
	if err != nil {
		return fmt.Errorf("cannot parse API base %q: %w", apiBase, err)
	}
	u, err := url.Parse(assetURL)
	if err != nil {
		return fmt.Errorf("cannot parse asset URL %q: %w", assetURL, err)
	}
	if !strings.EqualFold(u.Host, a.Host) {
		return fmt.Errorf("asset URL host %q does not match the API host %q - refusing to send credentials to it",
			u.Host, a.Host)
	}
	// Binding the HOST alone is not enough: http://api.github.com matches the host
	// of https://api.github.com, and the token would then go out in cleartext on
	// the first hop (Ravi, #280). The scheme is half of "the same place".
	if !strings.EqualFold(u.Scheme, a.Scheme) {
		return fmt.Errorf("asset URL scheme %q does not match the API scheme %q - refusing to send credentials over it",
			u.Scheme, a.Scheme)
	}
	return nil
}

func githubAuth(req *http.Request) {
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
}

func availableAssets(assets []githubAsset) string {
	if len(assets) == 0 {
		return " (the release has no assets)"
	}
	names := make([]string, len(assets))
	for i, a := range assets {
		names[i] = a.Name
	}
	return " (available: " + strings.Join(names, ", ") + ")"
}
