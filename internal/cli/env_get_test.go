package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const envGetTestID = "11111111-2222-3333-4444-555555555555"

func envGetPayload(extras ...func(map[string]any)) map[string]any {
	p := map[string]any{
		"id":             envGetTestID,
		"shortId":        "11111111",
		"name":           "myenv-11111111",
		"displayName":    "myenv",
		"bcVersion":      "27.1.41698.0",
		"country":        "us",
		"location":       "westus2",
		"artifactType":   "Sandbox",
		"status":         "running",
		"webClientUrl":   "https://myenv-11111111.dev.bcdock.io/BC/",
		"soapUrl":        "https://myenv-11111111.dev.bcdock.io/BC/WS/",
		"oDataUrl":       "https://myenv-11111111.dev.bcdock.io/BC/ODataV4/",
		"devEndpointUrl": "https://myenv-11111111.dev.bcdock.io/BC/dev/",
		"downloadsUrl":   "https://myenv-11111111.dev.bcdock.io/bcdownloads/",
		"username":       "admin",
		"password":       "P@ssw0rd!",
		"multiTenant":    false,
		"createdAt":      "2026-05-01T10:00:00Z",
	}
	for _, f := range extras {
		f(p)
	}
	return p
}

func envGetServer(t *testing.T, payload map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/environments/"+envGetTestID || r.Method != http.MethodGet {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
}

// SEC-039. This test asserted the opposite until 2026-09-05 - that `env get` prints the BC
// password - and the server fixture below STILL sends one. That is deliberate and is what
// makes the assertion strong: a stale or malicious API can put `password` in the detail
// response and this CLI will not render it, because the field no longer exists on the
// struct it decodes into. A fixture that simply stopped sending a password would pass with
// the old rendering code still in place.
func TestEnvGet_Table_RendersEndpointsAndUsernameButNeverTheSecrets(t *testing.T) {
	srv := envGetServer(t, envGetPayload())
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "get", envGetTestID)
	if err != nil {
		t.Fatalf("env get: %v", err)
	}

	for _, want := range []string{
		"NAME:", "myenv",
		"WEB CLIENT:", "https://myenv-11111111.dev.bcdock.io/BC/",
		"DEV ENDPOINT:", "https://myenv-11111111.dev.bcdock.io/BC/dev/",
		"SOAP:", "https://myenv-11111111.dev.bcdock.io/BC/WS/",
		"ODATA V4:", "https://myenv-11111111.dev.bcdock.io/BC/ODataV4/",
		"DOWNLOADS:", "https://myenv-11111111.dev.bcdock.io/bcdownloads/",
		"USERNAME:", "admin",
		"BC VERSION:", "27.1.41698.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}

	for _, mustNotAppear := range []string{"PASSWORD:", "WS ACCESS KEY:", "P@ssw0rd!"} {
		if strings.Contains(out, mustNotAppear) {
			t.Errorf("SEC-039: %q must not reach `env get` output, got:\n%s", mustNotAppear, out)
		}
	}
}

func TestEnvGet_Table_SkipsEmptyAndUnsetFields(t *testing.T) {
	// Pending env: no URLs, no credentials yet, never hibernated/suspended/deleted.
	pending := map[string]any{
		"id":           envGetTestID,
		"shortId":      "11111111",
		"name":         "pending",
		"displayName":  "pending",
		"bcVersion":    "27.1",
		"country":      "us",
		"location":     "westus2",
		"artifactType": "Sandbox",
		"status":       "queued",
		"createdAt":    "2026-05-01T10:00:00Z",
	}
	srv := envGetServer(t, pending)
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "get", envGetTestID)
	if err != nil {
		t.Fatalf("env get: %v", err)
	}

	for _, mustNotAppear := range []string{
		"WEB CLIENT:", "DEV ENDPOINT:", "SOAP:", "ODATA V4:", "DOWNLOADS:",
		"USERNAME:",
		// "PASSWORD:" is deliberately NOT in this list any more. Since SEC-039 it can never
		// appear for ANY env, so asserting its absence here would pass without saying
		// anything about the empty-field skipping this test exists to check. Its real
		// assertion lives in the test above, where the server does send one.
		"HIBERNATED:", "SUSPENDED:", "DELETED:", "ERROR:",
	} {
		if strings.Contains(out, mustNotAppear) {
			t.Errorf("expected %q to be omitted for pending env, got:\n%s", mustNotAppear, out)
		}
	}
}

func TestEnvGet_JSON_EmitsRawRecord(t *testing.T) {
	srv := envGetServer(t, envGetPayload())
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"-o", "json", "env", "get", envGetTestID)
	if err != nil {
		t.Fatalf("env get -o json: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if got["devEndpointUrl"] != "https://myenv-11111111.dev.bcdock.io/BC/dev/" {
		t.Errorf("missing devEndpointUrl in JSON: %v", got["devEndpointUrl"])
	}
	if got["soapUrl"] == nil {
		t.Errorf("missing soapUrl in JSON: %v", got)
	}
	// SEC-039: -o json emits the decoded record, so the secrets are gone from the scripting
	// surface too - even though the fixture server sends a password.
	if _, present := got["password"]; present {
		t.Errorf("SEC-039: `env get -o json` must not carry a password key: %v", got)
	}
	if _, present := got["webServiceAccessKey"]; present {
		t.Errorf("SEC-039: `env get -o json` must not carry a webServiceAccessKey key: %v", got)
	}
}

func TestEnvGet_CSV_KeepsListRowShape(t *testing.T) {
	srv := envGetServer(t, envGetPayload())
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"-o", "csv", "env", "get", envGetTestID)
	if err != nil {
		t.Fatalf("env get -o csv: %v", err)
	}

	// CSV stays on envRow shape - header should be the list columns, not key/value.
	if !strings.Contains(out, "NAME,SHORT_ID,VERSION,COUNTRY,STATUS") {
		t.Errorf("CSV header doesn't match envRow shape:\n%s", out)
	}
}
