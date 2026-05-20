package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const symbolsEnvID = "33333333-4444-5555-6666-777777777777"

// envSymbolsHarness fakes both the Platform API (env lookup) and the BC dev
// endpoint (/dev/packages). devHandler decides what each /packages call returns.
func envSymbolsHarness(t *testing.T, devHandler http.HandlerFunc) (apiURL string, calls *int32) {
	t.Helper()
	c := int32(0)
	calls = &c

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/environments/"+symbolsEnvID, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             symbolsEnvID,
			"shortId":        "33333333",
			"name":           "sym-env",
			"displayName":    "sym-env",
			"bcVersion":      "27.1",
			"country":        "us",
			"location":       "westus2",
			"artifactType":   "Sandbox",
			"status":         "running",
			"webClientUrl":   "http://" + r.Host + "/BC/",
			"devEndpointUrl": "http://" + r.Host + "/BC-dev/",
			"username":       "admin",
			"password":       "P@ssw0rd!",
			"createdAt":      "2026-05-01T10:00:00Z",
		})
	})
	mux.HandleFunc("/BC-dev/dev/packages", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		devHandler(w, r)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, calls
}

func writeAppJSON(t *testing.T, dir string, content string) string {
	t.Helper()
	p := filepath.Join(dir, "app.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnvSymbols_DownloadsApplicationPlatformAndDeps(t *testing.T) {
	type call struct{ publisher, appName, version, tenant string }
	var got []call

	apiURL, _ := envSymbolsHarness(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		got = append(got, call{
			publisher: q.Get("publisher"),
			appName:   q.Get("appName"),
			version:   q.Get("versionText"),
			tenant:    q.Get("tenant"),
		})
		// Echo a synthetic .app body that uniquely identifies the request.
		body := q.Get("publisher") + "_" + q.Get("appName") + "_" + q.Get("versionText")
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(body))
	})

	dir := t.TempDir()
	writeAppJSON(t, dir, `{
	  "name":"MyExt","publisher":"Foo","version":"1.0.0.0",
	  "application":"27.1.0.0",
	  "platform":"27.0.0.0",
	  "dependencies":[
	    {"name":"OtherExt","publisher":"Acme","version":"2.0.0.0"}
	  ]
	}`)
	outDir := filepath.Join(dir, ".alpackages")

	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "download-symbols", symbolsEnvID,
		"--app-json", filepath.Join(dir, "app.json"),
		"--out-dir", outDir)
	if err != nil {
		t.Fatalf("env download-symbols: %v", err)
	}

	// app.json's "application" field synthesizes 4 fetches (the modern split set
	// + the legacy single bundle, all marked Optional so 404s on names not
	// present on the env are silent skips). Plus 1 for "platform" and 1 per
	// declared dependency.
	want := []call{
		{"Microsoft", "System Application", "27.1.0.0", "default"},
		{"Microsoft", "Base Application", "27.1.0.0", "default"},
		{"Microsoft", "Business Foundation", "27.1.0.0", "default"},
		{"Microsoft", "Application", "27.1.0.0", "default"},
		{"Microsoft", "System", "27.0.0.0", "default"},
		{"Acme", "OtherExt", "2.0.0.0", "default"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d calls, got %d: %+v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("call %d: got %+v want %+v", i, got[i], w)
		}
	}

	for _, fn := range []string{
		"Microsoft_System Application_27.1.0.0.app",
		"Microsoft_Base Application_27.1.0.0.app",
		"Microsoft_Business Foundation_27.1.0.0.app",
		"Microsoft_Application_27.1.0.0.app",
		"Microsoft_System_27.0.0.0.app",
		"Acme_OtherExt_2.0.0.0.app",
	} {
		path := filepath.Join(outDir, fn)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing %s: %v", fn, err)
		}
	}
}

func TestEnvDownloadSymbols_OptionalTarget404IsSkippedNotFatal(t *testing.T) {
	// Only "System Application" exists; the other three application-bucket names
	// 404. The single "platform" target succeeds. Caller should NOT see an error.
	apiURL, _ := envSymbolsHarness(t, func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("appName")
		if name == "System Application" || name == "System" {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("ok"))
			return
		}
		http.NotFound(w, r)
	})

	dir := t.TempDir()
	writeAppJSON(t, dir, `{
	  "name":"X","publisher":"Y","version":"1",
	  "application":"28.0.0.0","platform":"28.0.0.0"
	}`)
	outDir := filepath.Join(dir, "out")

	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "download-symbols", symbolsEnvID,
		"--app-json", filepath.Join(dir, "app.json"),
		"--out-dir", outDir)
	if err != nil {
		t.Fatalf("env download-symbols should succeed when optional names 404: %v", err)
	}

	// Files for the 200 names are present; the 404'd ones are absent.
	for _, present := range []string{
		"Microsoft_System Application_28.0.0.0.app",
		"Microsoft_System_28.0.0.0.app",
	} {
		if _, err := os.Stat(filepath.Join(outDir, present)); err != nil {
			t.Errorf("expected %s present: %v", present, err)
		}
	}
	for _, absent := range []string{
		"Microsoft_Base Application_28.0.0.0.app",
		"Microsoft_Business Foundation_28.0.0.0.app",
		"Microsoft_Application_28.0.0.0.app",
	} {
		if _, err := os.Stat(filepath.Join(outDir, absent)); err == nil {
			t.Errorf("expected %s absent (404 skip), but it was written", absent)
		}
	}
}


func TestEnvSymbols_BasicAuthHeaderUsed(t *testing.T) {
	var gotAuth string
	apiURL, _ := envSymbolsHarness(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("ok"))
	})

	dir := t.TempDir()
	writeAppJSON(t, dir, `{"name":"X","publisher":"Y","version":"1.0.0.0","platform":"27.0.0.0"}`)

	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "download-symbols", symbolsEnvID,
		"--app-json", filepath.Join(dir, "app.json"),
		"--out-dir", filepath.Join(dir, "out"))
	if err != nil {
		t.Fatalf("env download-symbols: %v", err)
	}
	// Basic admin:P@ssw0rd! → YWRtaW46UEBzc3cwcmQh
	if gotAuth != "Basic YWRtaW46UEBzc3cwcmQh" {
		t.Errorf("auth header: %q", gotAuth)
	}
}

func TestEnvSymbols_SkipsAlreadyDownloadedUnlessForce(t *testing.T) {
	apiURL, calls := envSymbolsHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("data"))
	})

	dir := t.TempDir()
	writeAppJSON(t, dir, `{"name":"X","publisher":"Y","version":"1","platform":"27.0.0.0"}`)
	outDir := filepath.Join(dir, "out")
	_ = os.MkdirAll(outDir, 0o755)
	// Pre-seed the file as if already downloaded.
	pre := filepath.Join(outDir, "Microsoft_System_27.0.0.0.app")
	if err := os.WriteFile(pre, []byte("OLD"), 0o600); err != nil {
		t.Fatal(err)
	}

	// First run - should skip (no network call).
	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "download-symbols", symbolsEnvID,
		"--app-json", filepath.Join(dir, "app.json"),
		"--out-dir", outDir)
	if err != nil {
		t.Fatalf("env download-symbols (skip): %v", err)
	}
	if *calls != 0 {
		t.Errorf("expected 0 calls when file present, got %d", *calls)
	}
	// File still has old content.
	got, _ := os.ReadFile(pre)
	if string(got) != "OLD" {
		t.Errorf("file overwritten without --force: %q", string(got))
	}

	// Second run with --force - should re-download.
	_, _, err = RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "download-symbols", symbolsEnvID,
		"--app-json", filepath.Join(dir, "app.json"),
		"--out-dir", outDir, "--force")
	if err != nil {
		t.Fatalf("env download-symbols (force): %v", err)
	}
	if *calls != 1 {
		t.Errorf("expected 1 call with --force, got %d", *calls)
	}
	got, _ = os.ReadFile(pre)
	if string(got) != "data" {
		t.Errorf("file not re-downloaded with --force: %q", string(got))
	}
}

func TestEnvSymbols_404SurfacesActionableError(t *testing.T) {
	apiURL, _ := envSymbolsHarness(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	dir := t.TempDir()
	writeAppJSON(t, dir, `{"name":"X","publisher":"Y","version":"1",
	  "dependencies":[{"name":"NotInstalled","publisher":"Acme","version":"9.9.9.9"}]}`)

	_, stderr, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "download-symbols", symbolsEnvID,
		"--app-json", filepath.Join(dir, "app.json"),
		"--out-dir", filepath.Join(dir, "out"))

	if err == nil {
		t.Fatal("expected error on 404")
	}
	combined := err.Error() + "\n" + stderr
	if !strings.Contains(combined, "Acme_NotInstalled") {
		t.Errorf("error should name the missing package: %v / %s", err, stderr)
	}
	if !strings.Contains(combined, "9.9.9.9") {
		t.Errorf("error should include version: %v / %s", err, stderr)
	}
}

func TestEnvSymbols_NoTargetsInAppJson_NoOp(t *testing.T) {
	apiURL, calls := envSymbolsHarness(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("dev endpoint should not be called when app.json has no deps")
	})

	dir := t.TempDir()
	// app.json with no application/platform/dependencies - nothing to fetch.
	writeAppJSON(t, dir, `{"name":"X","publisher":"Y","version":"1.0.0.0"}`)

	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "download-symbols", symbolsEnvID,
		"--app-json", filepath.Join(dir, "app.json"),
		"--out-dir", filepath.Join(dir, "out"))
	if err != nil {
		t.Fatalf("env download-symbols: %v", err)
	}
	if *calls != 0 {
		t.Errorf("dev endpoint called unexpectedly: %d", *calls)
	}
}

func TestEnvSymbols_TenantFlagPropagatesToQuery(t *testing.T) {
	var gotTenant string
	apiURL, _ := envSymbolsHarness(t, func(w http.ResponseWriter, r *http.Request) {
		gotTenant = r.URL.Query().Get("tenant")
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("ok"))
	})

	dir := t.TempDir()
	writeAppJSON(t, dir, `{"name":"X","publisher":"Y","version":"1","platform":"27.0.0.0"}`)

	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "download-symbols", symbolsEnvID,
		"--app-json", filepath.Join(dir, "app.json"),
		"--out-dir", filepath.Join(dir, "out"),
		"--tenant", "tenantB")
	if err != nil {
		t.Fatalf("env download-symbols: %v", err)
	}
	if gotTenant != "tenantB" {
		t.Errorf("tenant query: %q", gotTenant)
	}
}

// Sanity: the URL we build is well-formed even with quotation/encoding edge
// cases in publisher/name (which are real - e.g. "Microsoft Inc." or
// "_Exclude_DocumentTextSearchAPI"). url.Values.Encode handles this; the
// test guards against a future regression where someone hand-builds the URL.
func TestEnvSymbols_QueryEscapesSpecialChars(t *testing.T) {
	var gotURL string
	apiURL, _ := envSymbolsHarness(t, func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("ok"))
	})

	dir := t.TempDir()
	writeAppJSON(t, dir, `{"name":"X","publisher":"Y","version":"1",
	  "dependencies":[{"name":"_Exclude DocumentTextSearch","publisher":"Microsoft Inc.","version":"1.0.0.0"}]}`)

	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "download-symbols", symbolsEnvID,
		"--app-json", filepath.Join(dir, "app.json"),
		"--out-dir", filepath.Join(dir, "out"))
	if err != nil {
		t.Fatalf("env download-symbols: %v", err)
	}
	parsed, perr := url.Parse(gotURL)
	if perr != nil {
		t.Fatalf("server saw malformed URL %q: %v", gotURL, perr)
	}
	if parsed.Query().Get("appName") != "_Exclude DocumentTextSearch" {
		t.Errorf("appName decoded wrong: %q", parsed.Query().Get("appName"))
	}
	if parsed.Query().Get("publisher") != "Microsoft Inc." {
		t.Errorf("publisher decoded wrong: %q", parsed.Query().Get("publisher"))
	}
}
