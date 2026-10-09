package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUsage_Summary_PrintsTable(t *testing.T) {
	const companyID = "aaaabbbb-0000-0000-0000-000000000000"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/me":
			json.NewEncoder(w).Encode(map[string]any{
				"email":       "alex@example.com",
				"companyId":   companyID,
				"companyName": "Contoso",
			})
		case "/api/v1/companies/" + companyID + "/usage":
			json.NewEncoder(w).Encode(map[string]any{
				"records": []any{},
				"summary": map[string]any{
					"totalSeconds":     7200,
					"totalHours":       2.0,
					"environmentCount": 3,
				},
			})
		default:
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "usage")
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if !strings.Contains(out, "Contoso") {
		t.Errorf("output missing company name: %s", out)
	}
	if !strings.Contains(out, "2.0h") {
		t.Errorf("output missing hours: %s", out)
	}
}

func TestUsage_Summary_WithDateRange(t *testing.T) {
	const companyID = "aaaabbbb-1111-0000-0000-000000000000"

	var gotFrom, gotTo string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/me":
			json.NewEncoder(w).Encode(map[string]any{"companyId": companyID, "companyName": "Contoso"})
		case "/api/v1/companies/" + companyID + "/usage":
			gotFrom = r.URL.Query().Get("from")
			gotTo = r.URL.Query().Get("to")
			json.NewEncoder(w).Encode(map[string]any{
				"records": []any{},
				"summary": map[string]any{"totalSeconds": 0, "totalHours": 0.0, "environmentCount": 0},
			})
		}
	}))
	defer srv.Close()

	_, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"usage", "--from", "2026-03-01", "--to", "2026-03-31")
	if err != nil {
		t.Fatalf("usage with date range: %v", err)
	}
	if gotFrom != "2026-03-01" {
		t.Errorf("from = %q, want 2026-03-01", gotFrom)
	}
	if gotTo != "2026-03-31" {
		t.Errorf("to = %q, want 2026-03-31", gotTo)
	}
}

func TestUsage_ByEnvironment_PrintsTable(t *testing.T) {
	const companyID = "aaaabbbb-2222-0000-0000-000000000000"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/me":
			json.NewEncoder(w).Encode(map[string]any{"companyId": companyID, "companyName": "Contoso"})
		case "/api/v1/companies/" + companyID + "/usage/by-environment":
			json.NewEncoder(w).Encode([]map[string]any{
				{"displayName": "dev-env", "totalRunningSeconds": 3600, "totalAmount": 0.50},
				{"displayName": "test-env", "totalRunningSeconds": 7200, "totalAmount": 1.00},
			})
		default:
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "usage", "--by-environment")
	if err != nil {
		t.Fatalf("usage --by-environment: %v", err)
	}
	if !strings.Contains(out, "dev-env") {
		t.Errorf("output missing dev-env: %s", out)
	}
	if !strings.Contains(out, "1.0h") {
		t.Errorf("output missing 1.0h for test-env: %s", out)
	}
}

func TestUsage_NoCompany_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/auth/me" {
			json.NewEncoder(w).Encode(map[string]any{
				"email":     "alex@example.com",
				"companyId": "",
			})
		}
	}))
	defer srv.Close()

	_, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "usage")
	if err == nil {
		t.Fatal("expected error when no company")
	}
	if !strings.Contains(err.Error(), "no active company") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUsage_JSONOutput(t *testing.T) {
	const companyID = "aaaabbbb-3333-0000-0000-000000000000"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/me":
			json.NewEncoder(w).Encode(map[string]any{"companyId": companyID, "companyName": "Contoso"})
		case "/api/v1/companies/" + companyID + "/usage":
			json.NewEncoder(w).Encode(map[string]any{
				"records": []any{},
				"summary": map[string]any{"totalSeconds": 3600, "totalHours": 1.0, "environmentCount": 1},
			})
		}
	}))
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "-o", "json", "usage")
	if err != nil {
		t.Fatalf("usage -o json: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("parse JSON: %v - output: %s", err, out)
	}
	if result["summary"] == nil {
		t.Errorf("JSON missing summary field: %v", result)
	}
}
