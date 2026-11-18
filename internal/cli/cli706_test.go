package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #706 slice 2. Bodies are the REAL API's shapes, from the platform source:
// EnvironmentCreateValidator's INSIDER_EULA_REQUIRED refusal, ApiKeyListItemResponse, and
// NotFoundError's { error, code }.
const (
	realCreate202        = `{"id":"5b1a3c6e-2f4d-4a8b-9c0d-7e6f5a4b3c2d","shortId":"prvw0001","status":"queued"}`
	realEulaRequired400  = `{"error":"Insider EULA must be accepted for preview versions.","code":"INSIDER_EULA_REQUIRED"}`
	realApiKeyNotFound   = `{"error":"API key not found","code":"not_found"}`
	keyID                = "0f8e7d6c-5b4a-4392-8170-6f5e4d3c2b1a"
	realApiKeysList200   = `[{"id":"` + keyID + `","name":"CI pipeline","keyPrefix":"bdk_3f9a","scopes":["env:read","env:write"],"createdAt":"2026-09-30T04:12:33.104271Z","expiresAt":null,"lastUsedAt":"2026-10-08T21:05:17.88Z"}]`
	previewVersion       = "29.0.53903.0"
	previewCreateCountry = "au"
)

// ── env create --accept-insider-eula ──

func TestEnvCreate_AcceptInsiderEula_SetsTheBodyField(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{"POST /api/v1/environments": {202, realCreate202}})
	code, _, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "create", "--name", "p", "--version", previewVersion, "--country", previewCreateCountry, "--accept-insider-eula")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte((*seen)[0].body), &body)
	if body["acceptInsiderEula"] != true {
		t.Errorf("acceptInsiderEula = %v in %s", body["acceptInsiderEula"], (*seen)[0].body)
	}
}

// The control: without the flag the field is not sent at all, so the EULA is never accepted implicitly.
func TestEnvCreate_WithoutTheFlag_NeverSendsTheField(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{"POST /api/v1/environments": {202, realCreate202}})
	code, _, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "create", "--name", "p", "--version", "27.5.46862.55261", "--country", "au")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if strings.Contains((*seen)[0].body, "acceptInsiderEula") {
		t.Errorf("body carries acceptInsiderEula without the flag: %s", (*seen)[0].body)
	}
}

func TestEnvCreate_InsiderEulaRequired_NamesTheFlag(t *testing.T) {
	srv, _ := routeStub(t, map[string]stubRoute{"POST /api/v1/environments": {400, realEulaRequired400}})
	code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "create", "--name", "p", "--version", previewVersion, "--country", previewCreateCountry)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	for _, want := range []string{"BC " + previewVersion + " is a preview (insider) version", "--accept-insider-eula"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in: %s", want, out)
		}
	}

	// -o json keeps the API's code, so a script can branch on it.
	code, _, out = runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "-o", "json",
		"env", "create", "--name", "p", "--version", previewVersion, "--country", previewCreateCountry)
	if code != 1 || !strings.Contains(out, `"INSIDER_EULA_REQUIRED"`) || !strings.Contains(out, "--accept-insider-eula") {
		t.Errorf("json: exit %d, out %s", code, out)
	}
}

// With --region, the CLI checks the version against the region's catalog first. That read must
// include preview versions, or a preview version is refused here as an invalid --version and the
// API never gets to name the flag.
func TestEnvCreate_RegionCheck_IncludesPreviewVersions(t *testing.T) {
	var catalogQuery string
	var posted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch r.URL.Path {
		case "/api/v1/public/regions":
			_, _ = w.Write([]byte(`[{"id":"australiaeast","name":"Australia East","stateCity":"NSW-Sydney","countryCode":"AU","countryName":"Australia"}]`))
		case "/api/v1/public/artifact-versions":
			catalogQuery = r.URL.RawQuery
			versions := `[]`
			if r.URL.Query().Get("includePreview") == "true" { // the real endpoint hides preview otherwise
				versions = `[{"versionFull":"` + previewVersion + `","country":"AU","artifactType":"Sandbox","hasVmImage":true,"isPreview":true}]`
			}
			_, _ = w.Write([]byte(`{"region":"australiaeast","versions":` + versions + `}`))
		case "/api/v1/environments":
			posted = true
			w.WriteHeader(202)
			_, _ = w.Write([]byte(realCreate202))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)

	code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "env", "create", "--name", "p",
		"--version", previewVersion, "--country", "au", "--region", "australiaeast", "--accept-insider-eula")
	if code != 0 || !posted {
		t.Fatalf("exit %d, posted %v: %s", code, posted, out)
	}
	if !strings.Contains(catalogQuery, "includePreview=true") {
		t.Errorf("catalog query = %q, want includePreview=true", catalogQuery)
	}
}

// ── bcdock auth keys list / revoke ──

func TestAuthKeysList_Table_ShowsPrefixAndDates_NeverASecret(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{"GET /api/v1/api-keys": {200, realApiKeysList200}})
	code, stdout, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "auth", "keys", "list")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if (*seen)[0].method != http.MethodGet || (*seen)[0].path != "/api/v1/api-keys" {
		t.Errorf("request = %s %s", (*seen)[0].method, (*seen)[0].path)
	}
	for _, want := range []string{keyID, "CI pipeline", "bdk_3f9a", "env:read,env:write", "2026-09-30", "2026-10-08", "LAST USED"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("missing %q in:\n%s", want, stdout)
		}
	}
}

func TestAuthKeysList_Json_IsTheApiRecord(t *testing.T) {
	srv, _ := routeStub(t, map[string]stubRoute{"GET /api/v1/api-keys": {200, realApiKeysList200}})
	code, stdout, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "-o", "json", "auth", "keys", "list")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var keys []map[string]any
	if err := json.Unmarshal([]byte(stdout), &keys); err != nil || len(keys) != 1 {
		t.Fatalf("json = %s (%v)", stdout, err)
	}
	if keys[0]["id"] != keyID || keys[0]["keyPrefix"] != "bdk_3f9a" || keys[0]["expiresAt"] != nil {
		t.Errorf("record = %v", keys[0])
	}
}

func TestAuthKeysRevoke_Yes_DeletesTheKey(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{"DELETE /api/v1/api-keys/" + keyID: {204, ""}})
	code, stdout, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "-o", "json",
		"auth", "keys", "revoke", keyID, "--yes")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if len(*seen) != 1 || (*seen)[0].method != http.MethodDelete {
		t.Fatalf("requests = %v", *seen)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(stdout), &res); err != nil || res["id"] != keyID || res["revoked"] != true {
		t.Errorf("json = %s (%v)", stdout, err)
	}
}

// A 404 is also what another company's key gets: the route only sees the active company's keys
// (ApiKeysEndpointTests.DeleteKey_OtherCompanyKey_Returns404 proves that server side).
func TestAuthKeysRevoke_NotFound_Exit5_SaysActiveCompany(t *testing.T) {
	srv, _ := routeStub(t, map[string]stubRoute{"DELETE /api/v1/api-keys/" + keyID: {404, realApiKeyNotFound}})
	code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "auth", "keys", "revoke", keyID, "--yes")
	if code != 5 {
		t.Errorf("exit = %d, want 5", code)
	}
	if !strings.Contains(out, "No API key "+keyID+" in your active company") {
		t.Errorf("out = %s", out)
	}
}

// Tests run without a terminal on stdin, the same as an agent: without --yes nothing is sent.
func TestAuthKeysRevoke_NoYesWithoutATerminal_RefusesAndSendsNothing(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{"DELETE /api/v1/api-keys/" + keyID: {204, ""}})
	code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "auth", "keys", "revoke", keyID)
	if code != 1 || !strings.Contains(out, "pass --yes") {
		t.Errorf("exit %d, out %s", code, out)
	}
	if len(*seen) != 0 {
		t.Errorf("sent %v without --yes", *seen)
	}
}

func TestAuthKeysRevoke_NotAnID_SendsNothing(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{})
	code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "auth", "keys", "revoke", "bdk_3f9a", "--yes")
	if code != 1 || !strings.Contains(out, "is not a key id") {
		t.Errorf("exit %d, out %s", code, out)
	}
	if len(*seen) != 0 {
		t.Errorf("sent %v for a non-id", *seen)
	}
}
