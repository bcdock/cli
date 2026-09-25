package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bcdock/cli/internal/client"
)

// fakeCatalogServer mounts /api/public/regions and /api/public/artifact-versions
// with a fixed catalog so the validator can be exercised offline.
func fakeCatalogServer(t *testing.T) *httptest.Server {
	t.Helper()
	regions := []map[string]any{
		{"id": "westus2", "name": "West US 2", "stateCity": "WA-Quincy", "countryCode": "US", "countryName": "United States"},
		{"id": "australiaeast", "name": "Australia East", "stateCity": "NSW-Sydney", "countryCode": "AU", "countryName": "Australia"},
	}
	versions := map[string]any{
		"region": "westus2",
		"versions": []map[string]any{
			{"versionFull": "27.1.41600.0", "country": "US", "artifactType": "Sandbox", "hasVmImage": true},
			{"versionFull": "27.1.41600.0", "country": "AU", "artifactType": "Sandbox", "hasVmImage": false},
			{"versionFull": "26.0.30000.0", "country": "US", "artifactType": "OnPrem", "hasVmImage": false},
		},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/public/regions":
			_ = json.NewEncoder(w).Encode(regions)
		case "/api/v1/public/artifact-versions":
			_ = json.NewEncoder(w).Encode(versions)
		default:
			w.WriteHeader(404)
		}
	}))
}

func newTestClient(baseURL string) *client.Client {
	return client.New(baseURL, "bdk_test", 5*time.Second)
}

func TestValidateEnvCreate_ValidCombo_ReturnsNil(t *testing.T) {
	srv := fakeCatalogServer(t)
	defer srv.Close()
	c := newTestClient(srv.URL)

	if err := validateEnvCreateInput(context.Background(), c, "westus2", "27.1.41600.0", "us", "Sandbox"); err != nil {
		t.Fatalf("expected valid combo to pass, got: %v", err)
	}
}

func TestValidateEnvCreate_BlankInputs_PassThrough(t *testing.T) {
	// When upstream callers haven't set the values yet, the validator returns nil and
	// lets the existing required-flag checks emit the missing-flag error.
	c := newTestClient("http://unused.invalid")
	if err := validateEnvCreateInput(context.Background(), c, "", "", "", "Sandbox"); err != nil {
		t.Errorf("blank inputs should pass through, got: %v", err)
	}
}

func TestValidateEnvCreate_BadRegion_ReturnsErrorWithSuggestion(t *testing.T) {
	srv := fakeCatalogServer(t)
	defer srv.Close()
	c := newTestClient(srv.URL)

	err := validateEnvCreateInput(context.Background(), c, "westus3", "27.1.41600.0", "us", "Sandbox")
	if err == nil {
		t.Fatal("expected typo region to be rejected")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--region") {
		t.Errorf("error should name the offending flag: %s", msg)
	}
	if !strings.Contains(msg, "westus2") {
		t.Errorf("error should suggest closest match 'westus2': %s", msg)
	}
}

func TestValidateEnvCreate_BadVersion_ReturnsError(t *testing.T) {
	srv := fakeCatalogServer(t)
	defer srv.Close()
	c := newTestClient(srv.URL)

	err := validateEnvCreateInput(context.Background(), c, "westus2", "99.9.0.0", "us", "Sandbox")
	if err == nil {
		t.Fatal("expected typo version to be rejected")
	}
	if !strings.Contains(err.Error(), "--version") {
		t.Errorf("error should name --version: %v", err)
	}
}

func TestValidateEnvCreate_BadCountry_ReturnsError(t *testing.T) {
	srv := fakeCatalogServer(t)
	defer srv.Close()
	c := newTestClient(srv.URL)

	err := validateEnvCreateInput(context.Background(), c, "westus2", "27.1.41600.0", "zz", "Sandbox")
	if err == nil {
		t.Fatal("expected unknown country to be rejected")
	}
	if !strings.Contains(err.Error(), "--country") {
		t.Errorf("error should name --country: %v", err)
	}
}

func TestValidateEnvCreate_WrongArtifactTypeForCombo_ReturnsError(t *testing.T) {
	// Catalog has 27.1/US as Sandbox only; OnPrem for that combo doesn't exist.
	srv := fakeCatalogServer(t)
	defer srv.Close()
	c := newTestClient(srv.URL)

	err := validateEnvCreateInput(context.Background(), c, "westus2", "27.1.41600.0", "us", "OnPrem")
	if err == nil {
		t.Fatal("expected (version, country, OnPrem) to be rejected")
	}
	if !strings.Contains(err.Error(), "OnPrem") {
		t.Errorf("error should mention the artifact type: %v", err)
	}
}

func TestValidateEnvCreate_CaseInsensitiveCountry_Accepted(t *testing.T) {
	srv := fakeCatalogServer(t)
	defer srv.Close()
	c := newTestClient(srv.URL)

	if err := validateEnvCreateInput(context.Background(), c, "westus2", "27.1.41600.0", "US", "Sandbox"); err != nil {
		t.Errorf("uppercase country should be accepted: %v", err)
	}
}

func TestValidateEnvCreate_TransientCatalogError_PassesThrough(t *testing.T) {
	// If the catalog endpoint is unreachable we don't block - server-side validation will catch it.
	c := newTestClient("http://127.0.0.1:1") // unrouteable
	c.HTTPClient.Timeout = 200 * time.Millisecond

	if err := validateEnvCreateInput(context.Background(), c, "westus2", "27.1", "us", "Sandbox"); err != nil {
		t.Errorf("transient catalog failure should not block: %v", err)
	}
}
