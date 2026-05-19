package cli

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const publishEnvID = "22222222-3333-4444-5555-666666666666"

// envPublishHarness builds a single httptest server that pretends to be both
// the Platform API (for env lookup) and the BC dev endpoint (for the multipart
// POST). Routing on path keeps the test self-contained.
func envPublishHarness(t *testing.T, devHandler http.HandlerFunc) (apiURL string, devCalls *int32) {
	t.Helper()

	calls := int32(0)
	devCalls = &calls

	mux := http.NewServeMux()

	// Platform API: GET /api/environments/{id}
	mux.HandleFunc("/api/v1/environments/"+publishEnvID, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// devEndpointUrl points back at our own server's /dev/ path so the publish
		// POST lands on devHandler below.
		serverURL := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             publishEnvID,
			"shortId":        "22222222",
			"name":           "pub-env",
			"displayName":    "pub-env",
			"bcVersion":      "27.1",
			"country":        "us",
			"location":       "westus2",
			"artifactType":   "Sandbox",
			"status":         "running",
			"webClientUrl":   serverURL + "/BC/",
			"devEndpointUrl": serverURL + "/BC-dev/",
			"username":       "admin",
			"password":       "P@ssw0rd!",
			"createdAt":      "2026-05-01T10:00:00Z",
		})
	})

	// BC dev endpoint: POST {serverInstance}/dev/apps; in subdomain mode the
	// public serverInstance is "BC-dev" and bcdock appends "dev/apps".
	mux.HandleFunc("/BC-dev/dev/apps", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		devHandler(w, r)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, devCalls
}

func writeApp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "MyApp_1.0.0.0.app")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEnvPublish_HappyPath_PostsMultipartWithBasicAuth(t *testing.T) {
	var (
		gotAuth   string
		gotMode   string
		gotTenant string
		gotPart   string
		gotBytes  []byte
	)

	apiURL, calls := envPublishHarness(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMode = r.URL.Query().Get("SchemaUpdateMode")
		gotTenant = r.URL.Query().Get("tenant")

		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("content-type: %v", err)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		part, err := mr.NextPart()
		if err != nil {
			t.Fatalf("multipart: %v", err)
		}
		gotPart = part.FileName()
		gotBytes, _ = io.ReadAll(part)

		w.WriteHeader(http.StatusNoContent)
	})

	appPath := writeApp(t, "AL_PACKAGE_BYTES")

	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "publish", publishEnvID, appPath)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	if *calls != 1 {
		t.Errorf("expected 1 dev-endpoint call, got %d", *calls)
	}
	// Basic admin:P@ssw0rd! → YWRtaW46UEBzc3cwcmQh
	if gotAuth != "Basic YWRtaW46UEBzc3cwcmQh" {
		t.Errorf("auth header wrong: %q", gotAuth)
	}
	if gotMode != "synchronize" {
		t.Errorf("default schema mode should be synchronize, got %q", gotMode)
	}
	if gotTenant != "default" {
		t.Errorf("default tenant should be default, got %q", gotTenant)
	}
	if gotPart != "MyApp_1.0.0.0.app" {
		t.Errorf("filename in part: %q", gotPart)
	}
	if string(gotBytes) != "AL_PACKAGE_BYTES" {
		t.Errorf("app bytes mismatch: %q", string(gotBytes))
	}
}

func TestEnvPublish_FlagsOverrideQueryParams(t *testing.T) {
	var (
		gotMode   string
		gotTenant string
		gotDep    string
	)
	apiURL, _ := envPublishHarness(t, func(w http.ResponseWriter, r *http.Request) {
		gotMode = r.URL.Query().Get("SchemaUpdateMode")
		gotTenant = r.URL.Query().Get("tenant")
		gotDep = r.URL.Query().Get("DependencyPublishingOption")
		w.WriteHeader(http.StatusNoContent)
	})

	appPath := writeApp(t, "x")

	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "publish", publishEnvID, appPath,
		"--schema-update-mode", "ForceSync",
		"--tenant", "tenantA",
		"--dependency-publishing", "Strict")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if gotMode != "forcesync" {
		t.Errorf("schema mode should be lowercased to forcesync, got %q", gotMode)
	}
	if gotTenant != "tenantA" {
		t.Errorf("tenant: %q", gotTenant)
	}
	if gotDep != "Strict" {
		t.Errorf("dependency: %q", gotDep)
	}
}

func TestEnvPublish_DevEndpointReturns400_SurfacesBody(t *testing.T) {
	apiURL, _ := envPublishHarness(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "App version 1.0.0.0 already published; bump version or use --force-upgrade", http.StatusBadRequest)
	})

	appPath := writeApp(t, "x")
	_, stderr, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "publish", publishEnvID, appPath)

	if err == nil {
		t.Fatal("expected error for 400")
	}
	combined := err.Error() + "\n" + stderr
	if !strings.Contains(combined, "400") {
		t.Errorf("error should include status: %v / %s", err, stderr)
	}
	if !strings.Contains(combined, "already published") {
		t.Errorf("error should include BC message body: %v / %s", err, stderr)
	}
}

func TestEnvPublish_AppFileMissing_FailsBeforeNetwork(t *testing.T) {
	apiURL, calls := envPublishHarness(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("dev endpoint should not be called when app file is missing")
	})

	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "publish", publishEnvID, "/nonexistent/path/missing.app")

	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "read app file") {
		t.Errorf("error should mention read app file: %v", err)
	}
	if *calls != 0 {
		t.Errorf("dev endpoint should not have been called, got %d calls", *calls)
	}
}
