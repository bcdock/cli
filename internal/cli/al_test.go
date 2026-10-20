package cli

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

const alEnvID = "44444444-5555-6666-7777-888888888888"

// alSubpathInZip mirrors what the real ALLanguage.vsix ships:
// extension/bin/{linux|darwin|win32}/alc[.exe]
func alSubpathInZip() string {
	switch runtime.GOOS {
	case "windows":
		return "extension/bin/win32/alc.exe"
	case "darwin":
		return "extension/bin/darwin/alc"
	default:
		return "extension/bin/linux/alc"
	}
}

// makeFakeVsix builds a real .zip containing a tiny shell-script alc that
// echoes its argv to stdout, so tests can assert flag passthrough.
func makeFakeVsix(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	defer zw.Close()

	// extension/package.json - a few extensions inspect this; harmless to include.
	w, err := zw.Create("extension/package.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(`{"name":"al","publisher":"ms-dynamics-smb","version":"17.0.fake"}`))

	// The "alc" binary - a portable shell script on POSIX, batch on Windows.
	var script []byte
	switch runtime.GOOS {
	case "windows":
		script = []byte("@echo off\r\necho FAKE_ALC %*\r\n")
	default:
		script = []byte("#!/bin/sh\necho \"FAKE_ALC $@\"\n")
	}
	w, err = zw.Create(alSubpathInZip())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(script)

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// alHarness fakes the Platform API (env lookup) + the BC downloads endpoint.
// downloadsCalls counts vsix fetches so caching tests can assert miss-vs-hit.
func alHarness(t *testing.T, vsix []byte) (apiURL string, downloadsCalls *int32) {
	t.Helper()
	c := int32(0)
	downloadsCalls = &c

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/environments/"+alEnvID, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":              alEnvID,
			"shortId":         "44444444",
			"name":            "al-env",
			"displayName":     "al-env",
			"bcVersion":       "27.1.41698.0",
			"platformVersion": "27.1.41698.0",
			"country":         "us",
			"location":        "westus2",
			"artifactType":    "Sandbox",
			"status":          "running",
			"webClientUrl":    "http://" + r.Host + "/BC/",
			"downloadsUrl":    "http://" + r.Host + "/bcdownloads/",
			"username":        "admin",
			"password":        "x",
			"createdAt":       "2026-05-01T10:00:00Z",
		})
	})
	mux.HandleFunc("/bcdownloads/ALLanguage.vsix", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(downloadsCalls, 1)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(vsix)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, downloadsCalls
}

// withAlCacheDir points BCDOCK_AL_CACHE at a fresh temp dir so tests don't
// share state with each other or with the developer's real cache.
func withAlCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("BCDOCK_AL_CACHE", dir)
	return dir
}

func TestALCompile_DownloadsAndExtractsThenInvokes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test stubs alc as a POSIX shell script; Windows path uses a separate batch fixture")
	}
	cacheDir := withAlCacheDir(t)
	apiURL, calls := alHarness(t, makeFakeVsix(t))

	stdout, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"al", "compile", "--env", alEnvID,
		"--project", "/tmp/myproj",
		"--package-cache", "/tmp/myproj/.alpackages",
		"--out", "/tmp/myproj/out.app")
	if err != nil {
		t.Fatalf("al compile: %v", err)
	}

	if *calls != 1 {
		t.Errorf("expected 1 vsix download, got %d", *calls)
	}
	expected := "FAKE_ALC /project:/tmp/myproj /packagecachepath:/tmp/myproj/.alpackages /out:/tmp/myproj/out.app"
	if !strings.Contains(stdout, expected) {
		t.Errorf("alc was not invoked with expected args.\nwant substring: %s\ngot stdout: %s", expected, stdout)
	}

	// Cache layout sanity: the alc binary was extracted under the platform-version dir.
	alcPath := filepath.Join(cacheDir, "27.1.41698.0", "extension", "bin", runtime.GOOS, "alc")
	if _, err := os.Stat(alcPath); err != nil {
		t.Errorf("expected cached alc at %s, got: %v", alcPath, err)
	}
}

func TestALCompile_ReusesCachedVsix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only stub")
	}
	withAlCacheDir(t)
	apiURL, calls := alHarness(t, makeFakeVsix(t))

	for i := 0; i < 3; i++ {
		_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
			"al", "compile", "--env", alEnvID, "--out", "/tmp/o.app")
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if *calls != 1 {
		t.Errorf("expected 1 download across 3 runs (cache hit), got %d", *calls)
	}
}

func TestALCompile_RefreshForcesRedownload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only stub")
	}
	withAlCacheDir(t)
	apiURL, calls := alHarness(t, makeFakeVsix(t))

	// Warm the cache.
	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"al", "compile", "--env", alEnvID, "--out", "/tmp/o.app")
	if err != nil {
		t.Fatal(err)
	}
	// Force re-download.
	_, _, err = RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"al", "compile", "--env", alEnvID, "--out", "/tmp/o.app", "--refresh")
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Errorf("expected 2 downloads with --refresh, got %d", *calls)
	}
}

func TestALCompile_PassesThroughUnknownALCFlags(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only stub")
	}
	withAlCacheDir(t)
	apiURL, _ := alHarness(t, makeFakeVsix(t))

	stdout, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"al", "compile", "--env", alEnvID, "--out", "/tmp/o.app",
		"--", "/generatecode+", "/errorlog:diag.log")
	if err != nil {
		t.Fatalf("al compile: %v", err)
	}
	for _, flag := range []string{"/generatecode+", "/errorlog:diag.log"} {
		if !strings.Contains(stdout, flag) {
			t.Errorf("expected alc to receive %q, got stdout: %s", flag, stdout)
		}
	}
}

func TestALCompile_RequiresEnvFlag(t *testing.T) {
	withAlCacheDir(t)
	_, _, err := RunCmd(t, "--api-url", "http://unused", "--token", "bdk_test",
		"al", "compile")
	if err == nil {
		t.Fatal("expected error when --env is missing")
	}
	if !strings.Contains(err.Error(), "--env is required") {
		t.Errorf("expected --env required error, got: %v", err)
	}
}

func TestALCompile_FallsBackToBcVersionWhenPlatformVersionNull(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only stub")
	}
	cacheDir := withAlCacheDir(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/environments/"+alEnvID, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Note: platformVersion explicitly null (legacy env)
		_, _ = w.Write([]byte(`{
		  "id":"` + alEnvID + `","shortId":"44444444","name":"x","displayName":"x",
		  "bcVersion":"26.5.99999.0","platformVersion":null,
		  "country":"us","location":"westus2","artifactType":"Sandbox","status":"running",
		  "webClientUrl":"http://` + r.Host + `/BC/",
		  "downloadsUrl":"http://` + r.Host + `/bcdownloads/",
		  "username":"admin","password":"x","createdAt":"2026-05-01T10:00:00Z"
		}`))
	})
	mux.HandleFunc("/bcdownloads/ALLanguage.vsix", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(makeFakeVsix(t))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"al", "compile", "--env", alEnvID, "--out", "/tmp/o.app")
	if err != nil {
		t.Fatalf("al compile: %v", err)
	}

	// Cache key should fall back to bcVersion.
	if _, err := os.Stat(filepath.Join(cacheDir, "26.5.99999.0")); err != nil {
		t.Errorf("expected cache dir keyed by bcVersion fallback, got: %v", err)
	}
}

func TestALCompile_VsixServerError_SurfacesActionableError(t *testing.T) {
	withAlCacheDir(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/environments/"+alEnvID, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
		  "id":"` + alEnvID + `","shortId":"44444444","name":"x","displayName":"x",
		  "bcVersion":"27.1.0.0","platformVersion":"27.1.0.0",
		  "country":"us","location":"westus2","artifactType":"Sandbox","status":"running",
		  "webClientUrl":"http://` + r.Host + `/BC/",
		  "downloadsUrl":"http://` + r.Host + `/bcdownloads/",
		  "username":"admin","password":"x","createdAt":"2026-05-01T10:00:00Z"
		}`))
	})
	mux.HandleFunc("/bcdownloads/ALLanguage.vsix", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "vsix not yet provisioned", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"al", "compile", "--env", alEnvID, "--out", "/tmp/o.app")
	if err == nil {
		t.Fatal("expected error when vsix download fails")
	}
	combined := err.Error()
	if !strings.Contains(combined, "404") || !strings.Contains(combined, "vsix") {
		t.Errorf("error should name status + endpoint: %v", err)
	}
}
