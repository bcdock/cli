package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const launchEnvID = "55555555-6666-7777-8888-999999999999"

func envLaunchJsonHarness(t *testing.T, devEndpointUrl string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/environments/"+launchEnvID {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             launchEnvID,
			"shortId":        "55555555",
			"name":           "lj-env",
			"displayName":    "lj-env",
			"bcVersion":      "28.0",
			"country":        "us",
			"location":       "westus2",
			"artifactType":   "Sandbox",
			"status":         "running",
			"webClientUrl":   "https://lj-env.dev.bcdock.io/BC/?tenant=default",
			"devEndpointUrl": devEndpointUrl,
			"username":       "admin",
			"password":       "x",
			"createdAt":      "2026-05-01T10:00:00Z",
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestEnvLaunchJson_Subdomain_DerivesBcDevAsServerInstance(t *testing.T) {
	apiURL := envLaunchJsonHarness(t, "https://lj-env.dev.bcdock.io/BC-dev/")

	out, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "launch-json", launchEnvID)
	if err != nil {
		t.Fatalf("env launch-json: %v", err)
	}

	var doc struct {
		Version        string                   `json:"version"`
		Configurations []map[string]interface{} `json:"configurations"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if doc.Version != "0.2.0" {
		t.Errorf("version: got %q want 0.2.0", doc.Version)
	}
	if len(doc.Configurations) != 1 {
		t.Fatalf("expected 1 config, got %d", len(doc.Configurations))
	}
	cfg := doc.Configurations[0]

	checks := map[string]any{
		"type":            "al",
		"request":         "launch",
		"environmentType": "OnPrem",
		"server":          "https://lj-env.dev.bcdock.io",
		"serverInstance":  "BC-dev",
		"authentication":  "UserPassword",
		"tenant":          "default",
		"launchBrowser":   false, // default - see notes in command help
	}
	for k, want := range checks {
		if cfg[k] != want {
			t.Errorf("config[%q]: got %v want %v", k, cfg[k], want)
		}
	}

	// Credentials must NOT be in launch.json - VS Code prompts on first publish.
	for _, mustNotAppear := range []string{"username", "password", "credentials"} {
		if _, present := cfg[mustNotAppear]; present {
			t.Errorf("config must not include %q (security)", mustNotAppear)
		}
	}
}

func TestEnvLaunchJson_PathMode_DerivesNameDevAsServerInstance(t *testing.T) {
	// Path mode: env name in URL, dev path is /{name}-dev/
	apiURL := envLaunchJsonHarness(t, "https://pool.dev.bcdock.io/lj-env-abc12345-dev/")

	out, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "launch-json", launchEnvID)
	if err != nil {
		t.Fatalf("env launch-json: %v", err)
	}

	var doc struct {
		Configurations []map[string]interface{} `json:"configurations"`
	}
	_ = json.Unmarshal([]byte(out), &doc)
	cfg := doc.Configurations[0]

	if cfg["server"] != "https://pool.dev.bcdock.io" {
		t.Errorf("server: %v", cfg["server"])
	}
	if cfg["serverInstance"] != "lj-env-abc12345-dev" {
		t.Errorf("serverInstance: got %v, want lj-env-abc12345-dev (path mode)", cfg["serverInstance"])
	}
}

func TestEnvLaunchJson_OutFile_WritesToDisk(t *testing.T) {
	apiURL := envLaunchJsonHarness(t, "https://lj-env.dev.bcdock.io/BC-dev/")
	out := filepath.Join(t.TempDir(), ".vscode", "launch.json")

	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "launch-json", launchEnvID, "--out", out)
	if err != nil {
		t.Fatalf("env launch-json --out: %v", err)
	}

	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if !strings.Contains(string(body), `"serverInstance": "BC-dev"`) {
		t.Errorf("written file missing serverInstance: %s", string(body))
	}
}

func TestEnvLaunchJson_ConfigName_OverridesDefault(t *testing.T) {
	apiURL := envLaunchJsonHarness(t, "https://lj-env.dev.bcdock.io/BC-dev/")

	out, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "launch-json", launchEnvID, "--config-name", "My Custom Config")
	if err != nil {
		t.Fatalf("env launch-json: %v", err)
	}
	if !strings.Contains(out, `"name": "My Custom Config"`) {
		t.Errorf("custom config name not used: %s", out)
	}
	if strings.Contains(out, "BCDock:") {
		t.Errorf("default name leaked when --config-name provided: %s", out)
	}
}

func TestEnvLaunchJson_NoDevEndpointUrl_FailsCleanly(t *testing.T) {
	// Env without devEndpointUrl (e.g. status=creating, webClientUrl null).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "id":"` + launchEnvID + `","shortId":"55555555","name":"x","displayName":"x",
		  "bcVersion":"28.0","country":"us","location":"westus2",
		  "artifactType":"Sandbox","status":"creating",
		  "createdAt":"2026-05-01T10:00:00Z"
		}`))
	}))
	defer srv.Close()

	_, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "launch-json", launchEnvID)
	if err == nil {
		t.Fatal("expected error when devEndpointUrl is missing")
	}
	if !strings.Contains(err.Error(), "devEndpointUrl") {
		t.Errorf("error should name devEndpointUrl: %v", err)
	}
}
