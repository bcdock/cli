package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bcdock/cli/internal/client"
)

// CLI-019. Under -o json a failure is ONE JSON object on stderr - {"error","message",
// "exitCode"[,"status"]} - so a script or an agent can act on it without regexing text.
// Each case runs the real command against a stub API, then hands its error to the handler
// with the parsed -o value exactly as Execute does, and checks BOTH formats: the object
// under json, and the unchanged `error: ...` line under table. Same exit code in both, and
// stdout empty.

type jsonErrorCase struct {
	stdout, table, json string
	exitTable, exitJSON int
	msg                 string
}

// runFailure runs args once under -o json and once under table.
func runFailure(t *testing.T, args ...string) jsonErrorCase {
	t.Helper()
	var c jsonErrorCase

	out, _, err := RunCmd(t, append([]string{"-o", "json"}, args...)...)
	if err == nil {
		t.Fatalf("expected a failure from %v", args)
	}
	if flagOutput != "json" {
		t.Fatalf("-o json did not reach flagOutput (got %q) - the case would test the table path", flagOutput)
	}
	c.stdout, c.msg = out, err.Error()
	var jb bytes.Buffer
	c.exitJSON = exitCodeForFormat(err, &jb, flagOutput)
	c.json = jb.String()

	_, _, err = RunCmd(t, args...)
	var tb bytes.Buffer
	c.exitTable = exitCodeForFormat(err, &tb, flagOutput)
	c.table = tb.String()
	return c
}

// check asserts the object, the unchanged table line, the shared exit code and empty stdout.
func (c jsonErrorCase) check(t *testing.T, wantError string, wantExit, wantStatus int) {
	t.Helper()
	if c.stdout != "" {
		t.Errorf("stdout must stay empty on failure under -o json, got %q", c.stdout)
	}
	if strings.Count(c.json, "\n") != 1 {
		t.Errorf("want exactly one line on stderr, got %q", c.json)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(c.json), &obj); err != nil {
		t.Fatalf("stderr is not one JSON object: %v: %q", err, c.json)
	}
	if obj["error"] != wantError {
		t.Errorf("error = %v, want %q (%s)", obj["error"], wantError, c.json)
	}
	if obj["message"] != c.msg {
		t.Errorf("message = %v, want the same text as the line: %q", obj["message"], c.msg)
	}
	if obj["exitCode"] != float64(wantExit) || c.exitJSON != wantExit || c.exitTable != wantExit {
		t.Errorf("exitCode field %v, json exit %d, table exit %d; want %d everywhere", obj["exitCode"], c.exitJSON, c.exitTable, wantExit)
	}
	status, has := obj["status"]
	if wantStatus == 0 && has {
		t.Errorf("status present for a non-API error: %v", status)
	}
	if wantStatus != 0 && status != float64(wantStatus) {
		t.Errorf("status = %v, want %d", status, wantStatus)
	}
	if c.table != "error: "+c.msg+"\n" {
		t.Errorf("table output changed: %q, want %q", c.table, "error: "+c.msg+"\n")
	}
}

func apiErrorServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return apiErrorServerType(t, status, "application/json; charset=utf-8", body)
}

func apiErrorServerType(t *testing.T, status int, contentType, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The bodies below are the REAL API's, captured from the platform's own test fixture
// (Ravi, #567: the first version stubbed a coded 404 the environments API never sends).
//
// An unknown environment id, since #570: the by-id actions return the typed body, so
// `error` is the API's own code and the line is a sentence.
const realEnv404TypedBody = `{"error":"Environment not found","code":"not_found"}`

// A 404 with NO code: ASP.NET's ProblemDetails for a bare NotFound(). Every environment
// by-id action sent this before #570, and any route that still returns a bare NotFound (or
// an API not yet deployed with #570) still does. `error` then comes from the status
// fallback, and `message` is the client's "HTTP 404: <body>" line.
const realEnv404Body = `{"type":"https://tools.ietf.org/html/rfc9110#section-15.5.5","title":"Not Found","status":404,"traceId":"00-92d915df578d533d8c5f2252ae011ccd-e2b8912896a0ae10-01"}`

// hibernate on an environment that is not running: BadRequestError(InvalidState, ...).
const realHibernateInvalidStateBody = `{"error":"Only running environments can be hibernated (current status: hibernated).","code":"invalid_state"}`

func TestJSONError_API404_EnvironmentNotFound_Typed(t *testing.T) {
	srv := apiErrorServer(t, http.StatusNotFound, realEnv404TypedBody)
	c := runFailure(t, "--api-url", srv.URL, "--token", "bdk_test", "env", "get", envGetTestID)
	c.check(t, "not_found", 5, 404)
	if c.msg != "not_found: Environment not found" {
		t.Errorf("message = %q, want the API's sentence (#570)", c.msg)
	}
}

func TestJSONError_API404_NoCode_StatusFallback(t *testing.T) {
	srv := apiErrorServerType(t, http.StatusNotFound, "application/problem+json; charset=utf-8", realEnv404Body)
	c := runFailure(t, "--api-url", srv.URL, "--token", "bdk_test", "env", "get", envGetTestID)
	c.check(t, "not_found", 5, 404)
	if !strings.HasPrefix(c.msg, "HTTP 404: ") {
		t.Errorf("message = %q, want the client's HTTP 404 line for a body with no code", c.msg)
	}
}

func TestJSONError_APIInvalidState(t *testing.T) {
	srv := apiErrorServer(t, http.StatusBadRequest, realHibernateInvalidStateBody)
	c := runFailure(t, "--api-url", srv.URL, "--token", "bdk_test", "env", "hibernate", "a1b2c3d4")
	c.check(t, "invalid_state", 1, 400)
	if c.msg != "invalid_state: Only running environments can be hibernated (current status: hibernated)." {
		t.Errorf("message = %q", c.msg)
	}
}

func TestJSONError_NetworkFailure(t *testing.T) {
	// A port that was listening a moment ago and is now closed: connection refused.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	c := runFailure(t, "--api-url", "http://"+addr, "--token", "bdk_test", "env", "get", envGetTestID)
	c.check(t, "network", 1, 0)
}

func TestJSONError_EnvWaitTimeout_Exit124(t *testing.T) {
	fake := &fakeEnvServer{t: t, envID: "22222222-2222-2222-2222-222222222222", envName: "cli-json-timeout", statuses: []string{"failed"}}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	c := runFailure(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "wait", fake.envName, "--status", "running", "--timeout", "5s")
	c.check(t, "timeout", 124, 0)
}

// The documented fallbacks, straight through the handler.
func TestJSONError_CodeFallbacks(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
		exit int
	}{
		{"older shape: error holds the code", &client.APIError{Status: 400, ErrorText: "invalid_input", Message: "Unknown region id 7"}, "invalid_input", 1},
		{"no code, 503", &client.APIError{Status: 503, ErrorText: "Service Unavailable"}, "service_unavailable", 1},
		{"no code, 401", &client.APIError{Status: 401, Message: "token expired"}, "unauthorized", 3},
		{"no code, 409", &client.APIError{Status: 409, ErrorText: "Conflict"}, "api_error", 1},
		{"human text that happens to be one lowercase word is not taken as a code", &client.APIError{Status: 400, ErrorText: "invalid"}, "api_error", 1},
		{"wrapped API error keeps its code", errors.Join(errors.New("while creating"), &client.APIError{Status: 402, Code: "quota_exceeded", ErrorText: "Quota exceeded"}), "quota_exceeded", 1},
		{"anything else the CLI rejected", errors.New("manifest: unknown key 'foo'"), "cli_error", 1},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		got := exitCodeForFormat(tc.err, &buf, "json")
		var obj map[string]any
		if err := json.Unmarshal(buf.Bytes(), &obj); err != nil {
			t.Fatalf("%s: not JSON: %q", tc.name, buf.String())
		}
		if obj["error"] != tc.want || got != tc.exit || obj["exitCode"] != float64(tc.exit) {
			t.Errorf("%s: error=%v exit=%d exitCode=%v, want %q/%d", tc.name, obj["error"], got, obj["exitCode"], tc.want, tc.exit)
		}
		var table bytes.Buffer
		if exitCodeForFormat(tc.err, &table, "table") != tc.exit || table.String() != "error: "+tc.err.Error()+"\n" {
			t.Errorf("%s: table path changed: %q", tc.name, table.String())
		}
	}
}

// CSV is not json: it keeps the text line (control on the format switch).
func TestJSONError_CSVKeepsTheTextLine(t *testing.T) {
	var buf bytes.Buffer
	exitCodeForFormat(errors.New("boom"), &buf, "csv")
	if buf.String() != "error: boom\n" {
		t.Fatalf("csv output changed: %q", buf.String())
	}
}

// Through the real entry path: Execute reads the PARSED -o value, not the default. Without
// this, a handler that is right but never handed "json" would pass every test above.
func TestJSONError_ExecutePassesTheParsedOutputFlag(t *testing.T) {
	srv := apiErrorServerType(t, http.StatusNotFound, "application/problem+json; charset=utf-8", realEnv404Body)
	flagOutput, flagToken, flagAPIURL = "table", "", ""
	ResetCmdFlags(RootCmd)
	var out, stderr bytes.Buffer
	RootCmd.SetOut(&out)
	RootCmd.SetErr(&bytes.Buffer{})
	RootCmd.SetArgs([]string{"-o", "json", "--api-url", srv.URL, "--token", "bdk_test", "env", "get", envGetTestID})
	got := executeTo(&stderr)
	var obj map[string]any
	if err := json.Unmarshal(stderr.Bytes(), &obj); err != nil {
		t.Fatalf("Execute did not write the JSON object for -o json: %q", stderr.String())
	}
	if got != 5 || obj["error"] != "not_found" || out.Len() != 0 {
		t.Fatalf("exit %d, object %v, stdout %q", got, obj, out.String())
	}
}
