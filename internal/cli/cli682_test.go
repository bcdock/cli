package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #682 slice 1. The bodies are the REAL API's, captured from the platform's tests
// (VersionRequestsControllerTests, EnvironmentsEndpointTests) and a GET of rollout-config, not
// written from what this code reads.
const (
	realVersionRequest200 = `{"id":"19c7dd5b-0edd-4fc9-86e0-f8be69366537","status":"submitted"}`
	realVersionRequest400 = `{"error":"No BC artifact for 99.9 (US, Sandbox).","code":"missing_required_field"}`
	realRolloutConfig     = `{"versionGating":{"mode":"FastOnly","responseSlaHours":48}}`
	realRetry202          = `{"id":"8d4efbec-5cfd-4a4d-85f6-94e8182d1173","shortId":"rtryok01","status":"queued","jobId":"1f23917f-51fc-4122-bd11-0c218b913bb4"}`
	realRetry400          = `{"error":"Only errored environments can be retried.","code":"invalid_state"}`
	realRename200         = `{"id":"23165bad-615a-4a99-91a9-7289cf7af248","displayName":"My Production BC"}`
	realRename400         = `{"type":"https://tools.ietf.org/html/rfc9110#section-15.5.1","title":"One or more validation errors occurred.","status":400,"errors":{"DisplayName":["The field DisplayName must be a string with a minimum length of 1 and a maximum length of 60."]},"traceId":"00-e8fc7412a6a9ad8d883659e03e51e1b0-032581a98e00c203-01"}`
)

type stubRoute struct {
	status int
	body   string
}

type seenRequest struct{ method, path, body string }

// routeStub answers "METHOD /path" keys; anything else is a 404 and is recorded too.
func routeStub(t *testing.T, routes map[string]stubRoute) (*httptest.Server, *[]seenRequest) {
	t.Helper()
	var seen []seenRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = append(seen, seenRequest{r.Method, r.URL.Path, string(b)})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if rt, ok := routes[r.Method+" "+r.URL.Path]; ok {
			w.WriteHeader(rt.status)
			_, _ = w.Write([]byte(rt.body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func runOut(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	stdout, stderr, err := RunCmd(t, args...)
	var b bytes.Buffer
	code := 0
	if err != nil {
		code = exitCodeForFormat(err, &b, flagOutput)
	}
	return code, stdout, stderr + b.String()
}

// ── bcdock artifacts request ──

func TestArtifactsRequest_PostsTheVersion_SaysWhatHappensNext(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{
		"POST /api/v1/version-requests":     {200, realVersionRequest200},
		"GET /api/v1/public/rollout-config": {200, realRolloutConfig},
	})
	code, stdout, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "artifacts", "request", "27.1.41600.0", "--country", "au")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte((*seen)[0].body), &body)
	if (*seen)[0].path != "/api/v1/version-requests" || body["versionId"] != "27.1.41600.0" || body["country"] != "au" ||
		body["artifactType"] != "Sandbox" || body["multiTenant"] != true {
		t.Errorf("request = %s %s", (*seen)[0].path, (*seen)[0].body)
	}
	if !strings.Contains(stderr, "Requested BC 27.1.41600.0 (AU, Sandbox). BCDock usually replies by email within 48 hours.") {
		t.Errorf("stderr = %q", stderr)
	}
	if !strings.Contains(stdout, "19c7dd5b-0edd-4fc9-86e0-f8be69366537") || !strings.Contains(stdout, "submitted") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestArtifactsRequest_OnPrem_SingleTenant_Message(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{"POST /api/v1/version-requests": {200, realVersionRequest200}})
	code, _, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "artifacts", "request", "27.1.41600.0",
		"--country", "us", "--type", "onprem", "--multi-tenant=false", "--message", "client demo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte((*seen)[0].body), &body)
	if body["artifactType"] != "OnPrem" || body["multiTenant"] != false || body["message"] != "client demo" {
		t.Errorf("body = %s", (*seen)[0].body)
	}
	// rollout-config 404 here: still a success, just without the reply-time sentence.
	if strings.Contains(stderr, "within") {
		t.Errorf("no SLA was served, none should be claimed: %q", stderr)
	}
}

func TestArtifactsRequest_UnknownVersion_IsTheAPIsError(t *testing.T) {
	srv, _ := routeStub(t, map[string]stubRoute{"POST /api/v1/version-requests": {400, realVersionRequest400}})
	code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "artifacts", "request", "99.9", "--country", "us")
	if code != 1 || !strings.Contains(out, "No BC artifact for 99.9 (US, Sandbox).") {
		t.Errorf("exit %d, output: %s", code, out)
	}
}

func TestArtifactsRequest_NotFastOnly_404_SaysCreateDirectly(t *testing.T) {
	srv, _ := routeStub(t, map[string]stubRoute{"POST /api/v1/version-requests": {404, ``}})
	code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "artifacts", "request", "27.1.41600.0", "--country", "au")
	if code == 0 || !strings.Contains(out, "Version requests are not open on this platform") {
		t.Errorf("exit %d, output: %s", code, out)
	}
}

func TestArtifactsRequest_BadInput_NoRequest(t *testing.T) {
	for _, args := range [][]string{
		{"artifacts", "request", "27.1.41600.0"},                                   // no --country
		{"artifacts", "request", "27.1.41600.0", "--country", "au", "--type", "x"}, // bad --type
	} {
		srv, seen := routeStub(t, map[string]stubRoute{})
		code, _, _ := runOut(t, append([]string{"--api-url", srv.URL, "--token", "bdk_test"}, args...)...)
		if code == 0 || len(*seen) != 0 {
			t.Errorf("%v: exit %d, requests %v", args, code, *seen)
		}
	}
}

// ── env create's 422 names the request verb, and that verb works as printed ──

func TestEnvCreate_422_NamesTheRequestVerb_WithTheVersionFilledIn(t *testing.T) {
	srv := statusServer(t, "/api/v1/environments", http.StatusUnprocessableEntity, fastOnly422Body)
	_, out := failWith(t, "table", "--api-url", srv.URL, "--token", "bdk_test",
		"env", "create", "--name", "x", "--version", "27.1.41600.0", "--country", "au")
	if !strings.Contains(out, "request it: bcdock artifacts request 27.1.41600.0 --country au.") {
		t.Errorf("the request verb is not named: %s", out)
	}
	srv2 := statusServer(t, "/api/v1/environments", http.StatusUnprocessableEntity, fastOnly422Body)
	_, out2 := failWith(t, "table", "--api-url", srv2.URL, "--token", "bdk_test",
		"env", "create", "--name", "x", "--version", "27.1.41600.0", "--country", "au", "--type", "onprem", "--multi-tenant=false")
	if !strings.Contains(out2, "bcdock artifacts request 27.1.41600.0 --country au --type onprem --multi-tenant=false.") {
		t.Errorf("the request verb must carry the create's type and tenancy: %s", out2)
	}
}

// An agent copies the printed command: it must parse and send the same version, type and tenancy.
func TestEnvCreate_422_PrintedRequestCommand_RunsAsIs(t *testing.T) {
	srv := statusServer(t, "/api/v1/environments", http.StatusUnprocessableEntity, fastOnly422Body)
	_, out := failWith(t, "table", "--api-url", srv.URL, "--token", "bdk_test",
		"env", "create", "--name", "x", "--version", "27.1.41600.0", "--country", "au", "--type", "onprem", "--multi-tenant=false")
	i := strings.Index(out, "bcdock artifacts request ")
	if i < 0 {
		t.Fatalf("no request command in: %s", out)
	}
	printed := out[i : i+strings.Index(out[i:], ". ")] // up to the sentence's full stop
	words := strings.Fields(printed)[1:]               // drop "bcdock"
	srv2, seen := routeStub(t, map[string]stubRoute{"POST /api/v1/version-requests": {200, realVersionRequest200}})
	code, _, stderr := runOut(t, append([]string{"--api-url", srv2.URL, "--token", "bdk_test"}, words...)...)
	if code != 0 {
		t.Fatalf("the printed command %q failed: %s", printed, stderr)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte((*seen)[0].body), &body)
	if body["versionId"] != "27.1.41600.0" || body["artifactType"] != "OnPrem" || body["multiTenant"] != false {
		t.Errorf("the printed command sent %s", (*seen)[0].body)
	}
}

// ── bcdock env retry ──

func TestEnvRetry_PostsRetry_PrintsQueued(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{"POST /api/v1/environments/" + failEnvID + "/retry": {202, realRetry202}})
	code, stdout, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "env", "retry", failEnvID)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if (*seen)[0].method != http.MethodPost || (*seen)[0].path != "/api/v1/environments/"+failEnvID+"/retry" {
		t.Errorf("request = %+v", (*seen)[0])
	}
	if !strings.Contains(stderr, "Retry started (status: queued).") || !strings.Contains(stdout, "rtryok01") {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
}

func TestEnvRetry_NotFailed_IsTheAPIsError(t *testing.T) {
	srv, _ := routeStub(t, map[string]stubRoute{"POST /api/v1/environments/" + failEnvID + "/retry": {400, realRetry400}})
	code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "env", "retry", failEnvID)
	if code != 1 || !strings.Contains(out, "Only errored environments can be retried.") {
		t.Errorf("exit %d, output: %s", code, out)
	}
}

func TestEnvRetry_Wait_FailsAgain_PrintsTheReason_Exit1(t *testing.T) {
	envBody, _ := json.Marshal(map[string]any{
		"id": failEnvID, "shortId": "66666666", "name": "fail-env", "status": "error",
		"errorMessage": "Artifact download failed again", "createdAt": "2026-10-01T00:00:00Z",
	})
	srv, _ := routeStub(t, map[string]stubRoute{
		"POST /api/v1/environments/" + failEnvID + "/retry": {202, realRetry202},
		"GET /api/v1/environments/" + failEnvID:             {200, string(envBody)},
	})
	code, out, _ := runExit(t, "--api-url", srv.URL, "--token", "bdk_test", "env", "retry", failEnvID, "--wait")
	if code != 1 || !strings.Contains(out, "Artifact download failed again") {
		t.Errorf("exit %d, output: %s", code, out)
	}
}

// ── bcdock env rename ──

func TestEnvRename_PatchesTheDisplayNameOnly(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{"PATCH /api/v1/environments/" + failEnvID: {200, realRename200}})
	code, stdout, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "env", "rename", failEnvID, "My Production BC")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if (*seen)[0].method != http.MethodPatch || (*seen)[0].body != `{"displayName":"My Production BC"}` {
		t.Errorf("request = %+v (only displayName may be sent)", (*seen)[0])
	}
	if !strings.Contains(stdout, "My Production BC") || !strings.Contains(stderr, `Renamed `+failEnvID+` to "My Production BC".`) {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
}

func TestEnvRename_TooLong_IsTheAPIsError(t *testing.T) {
	srv, _ := routeStub(t, map[string]stubRoute{"PATCH /api/v1/environments/" + failEnvID: {400, realRename400}})
	code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "env", "rename", failEnvID, strings.Repeat("A", 61))
	if code != 1 || !strings.Contains(out, "maximum length of 60") {
		t.Errorf("exit %d, output: %s", code, out)
	}
}
