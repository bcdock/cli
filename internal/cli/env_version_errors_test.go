package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #622. Two API refusals a first-time user meets, worded for the API rather than for the CLI.
// The bodies below are the controller's own shapes (EnvironmentsController: the FastOnly gate's
// RequestVersionRequiredResponse, and resume's upgrade_required Conflict), not guesses.

const fastOnly422Body = `{"code":"request_version_required","versionId":"27.1.41600.0","requestEndpoint":"/api/v1/version-requests"}`

const upgradeRequired409Body = `{"error":"Resume requires a platform-only upgrade from BC 27.1.41600.0 to BC 27.5.46862.54899. ` +
	`Your installed apps will stay at the version baked into your backup — they will not be re-published, re-synced, or data-upgraded. ` +
	`Resubmit with targetVersion: \"27.5.46862.54899\" to confirm.","code":"upgrade_required","fromVersion":"27.1.41600.0","toVersion":"27.5.46862.54899"}`

func statusServer(t *testing.T, path string, status int, body string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func failWith(t *testing.T, format string, args ...string) (int, string) {
	t.Helper()
	_, stderr, err := RunCmd(t, args...)
	if err == nil {
		t.Fatalf("expected a failure, got none (stderr %q)", stderr)
	}
	var b bytes.Buffer
	return exitCodeForFormat(err, &b, format), b.String()
}

func TestEnvCreate_RequestVersionRequired_SaysWhatToDo(t *testing.T) {
	srv := statusServer(t, "/api/v1/environments", http.StatusUnprocessableEntity, fastOnly422Body)
	code, out := failWith(t, "table", "--api-url", srv.URL, "--token", "bdk_test",
		"env", "create", "--name", "x", "--version", "27.1.41600.0", "--country", "au")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	for _, want := range []string{"BC 27.1.41600.0 (au) has no ready image in your default region",
		"bcdock artifacts list --region <region> --fast-only", "run 'bcdock env create' with no version"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in: %s", want, out)
		}
	}
	// The pre-fix output was the bare code with nothing after it.
	if strings.TrimSpace(out) == "error: request_version_required:" {
		t.Errorf("still the bare code: %s", out)
	}
}

func TestEnvCreate_RequestVersionRequired_NamesTheRegion_AndKeepsTheJSONCode(t *testing.T) {
	srv := statusServer(t, "/api/v1/environments", http.StatusUnprocessableEntity, fastOnly422Body)
	code, out := failWith(t, "json", "--api-url", srv.URL, "--token", "bdk_test", "-o", "json",
		"env", "create", "--name", "x", "--version", "27.1.41600.0", "--country", "au", "--region", "westus2")
	if code != 1 || !strings.Contains(out, `"error":"request_version_required"`) {
		t.Errorf("-o json must keep the API's code and exit 1; code %d: %s", code, out)
	}
	if !strings.Contains(out, "no ready image in westus2") || !strings.Contains(out, "artifacts list --region westus2 --fast-only") {
		t.Errorf("the message must name the region it was refused in: %s", out)
	}
}

func TestEnvResume_UpgradeRequired_SaysTheCLICommandNotTheAPIField(t *testing.T) {
	const id = "77777777-8888-9999-aaaa-bbbbbbbbbbbb"
	srv := statusServer(t, "/api/v1/environments/"+id+"/resume", http.StatusConflict, upgradeRequired409Body)
	code, out := failWith(t, "table", "--api-url", srv.URL, "--token", "bdk_test", "env", "resume", id)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "bcdock env resume "+id+" --version 27.5.46862.54899 --wait") {
		t.Errorf("must give the CLI command to confirm: %s", out)
	}
	if strings.Contains(out, "targetVersion") {
		t.Errorf("the API's field name leaked into CLI output: %s", out)
	}
	if !strings.Contains(out, "from BC 27.1.41600.0 to BC 27.5.46862.54899") {
		t.Errorf("the API's explanation must survive: %s", out)
	}
}

// Every other API error passes through untouched.
func TestEnvCreate_OtherErrors_Unchanged(t *testing.T) {
	srv := statusServer(t, "/api/v1/environments", http.StatusBadRequest,
		`{"error":"invalid_input","field":"version","message":"No BC artifact for 27 (au, Sandbox). Run 'bcdock artifacts list --region centralus' to see valid combinations."}`)
	_, out := failWith(t, "table", "--api-url", srv.URL, "--token", "bdk_test",
		"env", "create", "--name", "x", "--version", "27", "--country", "au")
	if !strings.Contains(out, "No BC artifact for 27") || strings.Contains(out, "no ready image") {
		t.Errorf("a 400 must pass through unchanged: %s", out)
	}
}
