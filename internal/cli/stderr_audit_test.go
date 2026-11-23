package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// CLI-018: agent test scenarios F1/F6/F10 surfaced empty-stderr exits in several places.
// These tests pin the contract: every non-zero exit emits at least one actionable line
// naming the resource or constraint, so an agent can read it and decide what to try next.

func TestEnvGet_MissingEnv_ReturnsErrorWithName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Empty list → resolveEnvID can't match by name → 404 surfaced.
		if r.URL.Path == "/api/v1/environments" {
			_ = json.NewEncoder(w).Encode([]any{})
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	_, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "env", "get", "ghost-env")
	if err == nil {
		t.Fatal("expected error for missing env")
	}
	if !strings.Contains(err.Error(), "ghost-env") {
		t.Errorf("error must name the missing env: %v", err)
	}
}

func TestEnvCreate_MissingVersion_NamesTheFlag(t *testing.T) {
	// No interactive picker possible (stdin in tests is not a TTY) → falls through to required-flag check.
	_, _, err := RunCmd(t, "--token", "bdk_test", "env", "create", "--country", "us", "--region", "westus2")
	if err == nil {
		t.Fatal("expected missing-flag error")
	}
	if !strings.Contains(err.Error(), "--version") {
		t.Errorf("error should name --version flag: %v", err)
	}
}

func TestEnvCreate_MissingCountry_NamesTheFlag(t *testing.T) {
	_, _, err := RunCmd(t, "--token", "bdk_test", "env", "create", "--version", "27.1", "--region", "westus2")
	if err == nil {
		t.Fatal("expected missing-flag error")
	}
	if !strings.Contains(err.Error(), "--country") {
		t.Errorf("error should name --country flag: %v", err)
	}
}

func TestAuthWhoami_NoToken_ReturnsActionableHint(t *testing.T) {
	t.Setenv("BCDOCK_TOKEN", "")
	t.Setenv("BCDOCK_CONFIG_DIR", t.TempDir())

	_, _, err := RunCmd(t, "auth", "whoami")
	if err == nil {
		t.Fatal("expected error when no credentials are present")
	}
	if !strings.Contains(err.Error(), "auth") {
		t.Errorf("error should suggest the auth fix: %v", err)
	}
}

func TestAuthWhoami_ApiReturnsUnauthorized_SurfacesScopeMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    "forbidden",
			"message": "API key requires 'env:read' scope.",
		})
	}))
	defer srv.Close()

	_, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "auth", "whoami")
	if err == nil {
		t.Fatal("expected forbidden error")
	}
	if !strings.Contains(err.Error(), "env:read") {
		t.Errorf("error should preserve the scope message: %v", err)
	}
	if got := exitCodeFor(err, &noopWriter{}); got != 3 {
		t.Errorf("expected exit 3 (auth failure), got %d", got)
	}
}
