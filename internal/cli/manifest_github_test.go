package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ghServer stands in for api.github.com. It serves one release with the given
// assets, and serves each asset's bytes from its own URL - the two-request shape
// the real API uses.
func ghServer(t *testing.T, tag string, assets map[string]string) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/repos/acme/tools/releases/tags/"+tag, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Clone(r.Context()))
		out := githubRelease{TagName: tag}
		for name := range assets {
			out.Assets = append(out.Assets, githubAsset{
				Name: name,
				URL:  srv.URL + "/assets/" + name,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/assets/", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Clone(r.Context()))
		name := strings.TrimPrefix(r.URL.Path, "/assets/")
		body, ok := assets[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// The real API returns metadata unless octet-stream is requested; assert
		// the client asks for bytes.
		if r.Header.Get("Accept") != "application/octet-stream" {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		_, _ = w.Write([]byte(body))
	})

	return srv, &seen
}

func TestFetchGitHubAsset_Happy(t *testing.T) {
	srv, _ := ghServer(t, "v1.4.0", map[string]string{"BcTools.app": "APPBYTES"})

	got, err := fetchGitHubAsset(context.Background(), srv.URL, "acme", "tools", "v1.4.0", "BcTools.app", 5*time.Second)
	if err != nil {
		t.Fatalf("fetchGitHubAsset: %v", err)
	}
	if string(got) != "APPBYTES" {
		t.Fatalf("got %q, want %q", got, "APPBYTES")
	}
}

// The plan's named case: asset not found must produce a clear error NAMING the
// asset. It also lists what the release does have, because the cause is almost
// always a typo or a filename that changed between releases, and both are obvious
// from the list without opening a browser.
func TestFetchGitHubAsset_AssetNotFound_NamesAssetAndLists(t *testing.T) {
	srv, _ := ghServer(t, "v1.4.0", map[string]string{
		"BcTools.app":      "x",
		"BcTools.Test.app": "y",
	})

	_, err := fetchGitHubAsset(context.Background(), srv.URL, "acme", "tools", "v1.4.0", "Missing.app", 5*time.Second)
	if err == nil {
		t.Fatal("want an error for a missing asset, got nil")
	}
	if !strings.Contains(err.Error(), "Missing.app") {
		t.Fatalf("error must name the requested asset, got: %v", err)
	}
	if !strings.Contains(err.Error(), "BcTools.app") {
		t.Fatalf("error must list the available assets, got: %v", err)
	}
}

func TestFetchGitHubAsset_ReleaseNotFound(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })

	_, err := fetchGitHubAsset(context.Background(), srv.URL, "acme", "tools", "v9.9.9", "X.app", 5*time.Second)
	if err == nil {
		t.Fatal("want an error for a missing release")
	}
	if !strings.Contains(err.Error(), "v9.9.9") || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error must name the tag and say not found, got: %v", err)
	}
}

// A 403 from GitHub is nearly always the unauthenticated rate limit, and the
// GOTCHA in the ticket calls it out (60/hour). A bare "HTTP 403" sends the reader
// looking for a permissions problem they do not have.
func TestFetchGitHubAsset_RateLimited_NamesTheFix(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })

	_, err := fetchGitHubAsset(context.Background(), srv.URL, "acme", "tools", "v1", "X.app", 5*time.Second)
	if err == nil {
		t.Fatal("want an error on 403")
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Fatalf("a 403 must point at GITHUB_TOKEN, got: %v", err)
	}
}

func TestFetchGitHubAsset_SendsTokenWhenSet(t *testing.T) {
	srv, seen := ghServer(t, "v1", map[string]string{"X.app": "bytes"})

	t.Setenv("GITHUB_TOKEN", "ghp_secret")
	if _, err := fetchGitHubAsset(context.Background(), srv.URL, "acme", "tools", "v1", "X.app", 5*time.Second); err != nil {
		t.Fatalf("fetchGitHubAsset: %v", err)
	}
	if len(*seen) == 0 {
		t.Fatal("server saw no requests")
	}
	for i, r := range *seen {
		if got := r.Header.Get("Authorization"); got != "Bearer ghp_secret" {
			t.Fatalf("request %d: Authorization = %q, want the bearer token on EVERY request", i, got)
		}
	}
}

// The control for the test above: with no token set, no Authorization header is
// sent. Without this, the token test would pass against an implementation that
// hardcoded the header.
func TestFetchGitHubAsset_NoTokenNoHeader(t *testing.T) {
	srv, seen := ghServer(t, "v1", map[string]string{"X.app": "bytes"})

	t.Setenv("GITHUB_TOKEN", "")
	if _, err := fetchGitHubAsset(context.Background(), srv.URL, "acme", "tools", "v1", "X.app", 5*time.Second); err != nil {
		t.Fatalf("fetchGitHubAsset: %v", err)
	}
	for i, r := range *seen {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("request %d: Authorization = %q, want none when GITHUB_TOKEN is unset", i, got)
		}
	}
}

func TestFetchGitHubAsset_EmptyAssetIsAnError(t *testing.T) {
	srv, _ := ghServer(t, "v1", map[string]string{"X.app": ""})

	_, err := fetchGitHubAsset(context.Background(), srv.URL, "acme", "tools", "v1", "X.app", 5*time.Second)
	if err == nil {
		t.Fatal("want an error for a zero-byte asset - publishing it would fail later and further from the cause")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error should say the asset is empty, got: %v", err)
	}
}

func TestFetchGitHubAsset_ReleaseWithNoAssets(t *testing.T) {
	srv, _ := ghServer(t, "v1", map[string]string{})

	_, err := fetchGitHubAsset(context.Background(), srv.URL, "acme", "tools", "v1", "X.app", 5*time.Second)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "no assets") {
		t.Fatalf("want a distinct message when the release has no assets at all, got: %v", err)
	}
}

// Ravi, #280: the asset URL comes from the response BODY and the next request
// carries a bearer token. Safe today only because apiBase is a const - so bind
// the two while the invariant holds, not after GHES makes apiBase configurable.
func TestFetchGitHubAsset_AssetURLOnAnotherHost_RefusesBeforeSendingToken(t *testing.T) {
	var evilHits int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&evilHits, 1)
		_, _ = w.Write([]byte("stolen"))
	}))
	t.Cleanup(evil.Close)

	mux := http.NewServeMux()
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)
	mux.HandleFunc("/repos/acme/tools/releases/tags/v1", func(w http.ResponseWriter, r *http.Request) {
		// A release whose asset URL points somewhere else entirely.
		_ = json.NewEncoder(w).Encode(githubRelease{
			TagName: "v1",
			Assets:  []githubAsset{{Name: "X.app", URL: evil.URL + "/assets/X.app"}},
		})
	})

	t.Setenv("GITHUB_TOKEN", "ghp_secret")
	_, err := fetchGitHubAsset(context.Background(), api.URL, "acme", "tools", "v1", "X.app", 5*time.Second)
	if err == nil {
		t.Fatal("want a refusal when the asset URL is on a different host")
	}
	if !strings.Contains(err.Error(), "does not match the API host") {
		t.Fatalf("error must name the host mismatch, got: %v", err)
	}
	// The assertion that matters: the token never left.
	if n := atomic.LoadInt32(&evilHits); n != 0 {
		t.Fatalf("the other host was contacted %d time(s) - the refusal must happen BEFORE the request", n)
	}
}

// Ravi, #280 second pass: binding the HOST alone lets http://api.github.com match
// https://api.github.com, and the bearer token goes out in cleartext on the first
// hop. The scheme is half of "the same place".
func TestFetchGitHubAsset_AssetURLDowngradesScheme_Refuses(t *testing.T) {
	mux := http.NewServeMux()
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)
	// Same host, downgraded scheme. httptest is http, so the API base is forced to
	// https here to make the downgrade the ONLY difference.
	httpsBase := strings.Replace(api.URL, "http://", "https://", 1)
	mux.HandleFunc("/repos/acme/tools/releases/tags/v1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(githubRelease{
			TagName: "v1",
			Assets:  []githubAsset{{Name: "X.app", URL: api.URL + "/assets/X.app"}},
		})
	})

	err := sameHost(httpsBase, api.URL+"/assets/X.app")
	if err == nil {
		t.Fatal("want a refusal when the asset URL downgrades https to http")
	}
	if !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("error must name the scheme mismatch, got: %v", err)
	}
}

// Control: identical scheme and host must still pass, or the check rejects every
// legitimate asset.
func TestSameHost_IdenticalSchemeAndHost_Passes(t *testing.T) {
	if err := sameHost("https://api.github.com", "https://api.github.com/repos/x/y/releases/assets/1"); err != nil {
		t.Fatalf("a legitimate same-scheme same-host asset URL must pass, got: %v", err)
	}
}
