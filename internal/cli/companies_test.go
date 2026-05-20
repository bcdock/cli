package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bcdock/cli/internal/config"
)

func TestCompaniesList_PrintsTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/companies" || r.Method != http.MethodGet {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": "abc123", "name": "Contoso", "slug": "contoso", "role": "owner", "createdAt": "2026-01-15T00:00:00Z"},
			{"id": "def456", "name": "Fabrikam", "slug": "fabrikam", "role": "member", "createdAt": "2026-02-20T00:00:00Z"},
		})
	}))
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "companies", "list")
	if err != nil {
		t.Fatalf("companies list: %v", err)
	}
	if !strings.Contains(out, "Contoso") {
		t.Errorf("output missing Contoso: %s", out)
	}
	if !strings.Contains(out, "owner") {
		t.Errorf("output missing role: %s", out)
	}
	if !strings.Contains(out, "2026-01-15") {
		t.Errorf("output missing date: %s", out)
	}
}

func TestCompaniesList_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": "abc123", "name": "Contoso", "slug": "contoso", "role": "owner", "createdAt": "2026-01-15T00:00:00Z"},
		})
	}))
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "-o", "json", "companies", "list")
	if err != nil {
		t.Fatalf("companies list -o json: %v", err)
	}

	var result []map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("parse JSON: %v - output: %s", err, out)
	}
	if len(result) != 1 || result[0]["name"] != "Contoso" {
		t.Errorf("unexpected result: %v", result)
	}
}

func TestCompaniesSwitch_ByGUID_SavesCredentials(t *testing.T) {
	const companyID = "3072f5a0-0000-0000-0000-000000000000"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/companies/"+companyID+"/switch" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new_access",
			"refresh_token": "new_refresh",
			"company":       map[string]string{"id": companyID, "name": "Contoso", "slug": "contoso", "role": "owner"},
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	t.Setenv("BCDOCK_CONFIG_DIR", dir)

	_, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "companies", "switch", companyID)
	if err != nil {
		t.Fatalf("companies switch: %v", err)
	}

	creds, err := config.LoadCredentials()
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if creds.Token != "new_access" {
		t.Errorf("token = %q, want new_access", creds.Token)
	}
}

func TestCompaniesSwitch_ByName_ResolvesAndSwitches(t *testing.T) {
	const companyID = "3072f5a0-1111-0000-0000-000000000000"
	listCalled := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/companies":
			listCalled = true
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": companyID, "name": "Fabrikam", "slug": "fabrikam", "role": "member"},
			})
		case "/api/v1/companies/" + companyID + "/switch":
			json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "tok_fab",
				"refresh_token": "ref_fab",
				"company":       map[string]string{"id": companyID, "name": "Fabrikam", "slug": "fabrikam", "role": "member"},
			})
		default:
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	t.Setenv("BCDOCK_CONFIG_DIR", dir)

	_, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "companies", "switch", "fabrikam")
	if err != nil {
		t.Fatalf("companies switch by name: %v", err)
	}
	if !listCalled {
		t.Error("expected /api/companies to be called for name resolution")
	}

	creds, _ := config.LoadCredentials()
	if creds.Token != "tok_fab" {
		t.Errorf("token = %q, want tok_fab", creds.Token)
	}
}

func TestCompaniesSwitch_UnknownName_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": "abc", "name": "Contoso", "slug": "contoso", "role": "owner"},
		})
	}))
	defer srv.Close()

	_, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "companies", "switch", "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown company")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}
