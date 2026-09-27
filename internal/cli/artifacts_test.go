package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArtifactsList_RequiresRegion(t *testing.T) {
	_, _, err := RunCmd(t, "--token", "bdk_test", "artifacts", "list")
	if err == nil {
		t.Fatal("expected error when --region is missing")
	}
	if !strings.Contains(err.Error(), "--region") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestArtifactsList_PrintsTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/public/artifact-versions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("region") != "australiaeast" {
			t.Errorf("region query param missing or wrong: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"region": "australiaeast",
			"versions": []map[string]any{
				{"versionFull": "25.5.12345.0", "country": "AU", "artifactType": "Sandbox", "hasVmImage": true, "isStale": false},
				{"versionFull": "25.4.11000.0", "country": "AU", "artifactType": "Sandbox", "hasVmImage": false, "isStale": false},
			},
		})
	}))
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "artifacts", "list", "--region", "australiaeast")
	if err != nil {
		t.Fatalf("artifacts list: %v", err)
	}
	if !strings.Contains(out, "25.5.12345.0") {
		t.Errorf("output missing version: %s", out)
	}
	if !strings.Contains(out, "yes") {
		t.Errorf("output missing FAST=yes: %s", out)
	}
}

func TestArtifactsList_TypeFilter_NormalisesAndDropsOther(t *testing.T) {
	// API doesn't accept artifactType - CLI normalises 'sandbox' / 'onprem' (case-
	// insensitive) and filters client-side. Anything else is rejected with a
	// helpful error so a typo doesn't silently filter to zero rows.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"region": "australiaeast",
			"versions": []map[string]any{
				{"versionFull": "25.5.12345.0", "country": "AU", "artifactType": "Sandbox", "hasVmImage": true},
				{"versionFull": "25.5.12345.0", "country": "AU", "artifactType": "OnPrem", "hasVmImage": true},
			},
		})
	}))
	defer srv.Close()

	// sandbox keeps Sandbox, drops OnPrem.
	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"artifacts", "list", "--region", "australiaeast", "--type", "sandbox")
	if err != nil {
		t.Fatalf("artifacts list --type sandbox: %v", err)
	}
	if !strings.Contains(out, "Sandbox") || strings.Contains(out, "OnPrem") {
		t.Errorf("--type sandbox didn't isolate Sandbox rows: %s", out)
	}

	// onprem keeps OnPrem, drops Sandbox. Case-insensitive accepts uppercase too.
	out, _, err = RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"artifacts", "list", "--region", "australiaeast", "--type", "OnPrem")
	if err != nil {
		t.Fatalf("artifacts list --type OnPrem: %v", err)
	}
	if !strings.Contains(out, "OnPrem") || strings.Contains(out, "Sandbox") {
		t.Errorf("--type onprem didn't isolate OnPrem rows: %s", out)
	}

	// Unknown value rejected loudly, no API call made.
	_, _, err = RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"artifacts", "list", "--region", "australiaeast", "--type", "weird")
	if err == nil {
		t.Fatal("expected error for unknown --type value")
	}
	if !strings.Contains(err.Error(), "sandbox") || !strings.Contains(err.Error(), "onprem") {
		t.Errorf("error should suggest the valid values: %v", err)
	}
}

func TestArtifactsList_FastOnly_FiltersResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"region": "australiaeast",
			"versions": []map[string]any{
				{"versionFull": "25.5.12345.0", "country": "AU", "artifactType": "Sandbox", "hasVmImage": true, "isStale": false},
				{"versionFull": "25.4.11000.0", "country": "AU", "artifactType": "Sandbox", "hasVmImage": false, "isStale": false},
			},
		})
	}))
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"artifacts", "list", "--region", "australiaeast", "--fast-only")
	if err != nil {
		t.Fatalf("artifacts list --fast-only: %v", err)
	}
	if !strings.Contains(out, "25.5.12345.0") {
		t.Errorf("output missing fast version: %s", out)
	}
	if strings.Contains(out, "25.4.11000.0") {
		t.Errorf("output contains slow version that should be filtered: %s", out)
	}
}

func TestArtifactsList_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"region": "australiaeast",
			"versions": []map[string]any{
				{"versionFull": "25.5.12345.0", "country": "AU", "artifactType": "Sandbox", "hasVmImage": true},
			},
		})
	}))
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"-o", "json", "artifacts", "list", "--region", "australiaeast")
	if err != nil {
		t.Fatalf("artifacts list -o json: %v", err)
	}

	var result []map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("parse JSON: %v - output: %s", err, out)
	}
	if len(result) != 1 || result[0]["versionFull"] != "25.5.12345.0" {
		t.Errorf("unexpected JSON: %v", result)
	}
}

func TestArtifactsCountries_PrintsTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/public/artifact-countries" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"countries": []map[string]string{
				{"code": "AU", "name": "Australia"},
				{"code": "US", "name": "United States"},
			},
		})
	}))
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "artifacts", "countries")
	if err != nil {
		t.Fatalf("artifacts countries: %v", err)
	}
	if !strings.Contains(out, "AU") || !strings.Contains(out, "Australia") {
		t.Errorf("output missing AU/Australia: %s", out)
	}
	if !strings.Contains(out, "US") {
		t.Errorf("output missing US: %s", out)
	}
}

func TestArtifactsCountries_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"countries": []map[string]string{
				{"code": "AU", "name": "Australia"},
			},
		})
	}))
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"-o", "json", "artifacts", "countries")
	if err != nil {
		t.Fatalf("artifacts countries -o json: %v", err)
	}

	var result []map[string]string
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("parse JSON: %v - output: %s", err, out)
	}
	if len(result) != 1 || result[0]["code"] != "AU" {
		t.Errorf("unexpected JSON: %v", result)
	}
}

func TestConfigRegions_PrintsTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/public/regions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]string{
			{"id": "australiaeast", "name": "Australia East", "stateCity": "New South Wales", "countryCode": "AU", "countryName": "Australia"},
			{"id": "eastus", "name": "East US", "stateCity": "Virginia", "countryCode": "US", "countryName": "United States"},
		})
	}))
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "config", "regions")
	if err != nil {
		t.Fatalf("config regions: %v", err)
	}
	if !strings.Contains(out, "australiaeast") {
		t.Errorf("output missing australiaeast: %s", out)
	}
	if !strings.Contains(out, "Australia") {
		t.Errorf("output missing Australia: %s", out)
	}
}
