package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bcdock/cli/internal/client"
)

// #733. A --wait polls GET /api/v1/environments/{id} for 15-45 minutes. One failed poll used
// to end the wait: live AL-Go run 39568914935 got Traefik's plain-text "404 page not found"
// during an api deploy, `env create --wait` exited 5 after 4 minutes, and the build lost an
// env that went on provisioning and billing. A transient failure is now retried until the
// existing deadline; the API's own JSON 404, 401 and 403 still end the wait as before.

const retryEnvID = "73373373-3733-4733-8733-733733733733"

// fastPolls shrinks the poll interval and backoff cap for one test.
func fastPolls(t *testing.T) {
	t.Helper()
	interval, max := pollInterval, pollRetryMax
	pollInterval, pollRetryMax = 20*time.Millisecond, 80*time.Millisecond
	t.Cleanup(func() { pollInterval, pollRetryMax = interval, max })
}

type pollReply func(w http.ResponseWriter)

func runningEnv(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": retryEnvID, "shortId": "73373373", "name": "retry-env", "status": "running",
		"bcVersion": "28.5.54151.56140", "country": "au", "location": "australiaeast",
		"createdAt": "2026-10-21T00:00:00Z",
	})
}

// traefik404 is what Traefik answers when no router matches: plain text, not the API's JSON.
func traefik404(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("404 page not found\n"))
}

func badGateway(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusBadGateway)
	_, _ = w.Write([]byte("<html><body>502 Bad Gateway</body></html>"))
}

// resetMidBody starts a 200 response that promises a body, sends part of it, and drops the
// connection: the client sees the status and then fails reading the body (Ravi's #742 note).
func resetMidBody(w http.ResponseWriter) {
	conn, buf, err := w.(http.Hijacker).Hijack()
	if err != nil {
		panic(err)
	}
	_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 1000\r\n\r\n{\"id\":")
	_ = buf.Flush()
	_ = conn.Close()
}

// apiNotFound is the API's own answer for an environment that does not exist.
func apiNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"error":"Environment not found","code":"not_found"}`))
}

func apiUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"Invalid API key","code":"unauthorized"}`))
}

// retryStub accepts a create and answers the env's GETs in order from replies, repeating the
// last reply once they run out. gets counts the GETs of the environment.
func retryStub(t *testing.T, gets *int32, replies ...pollReply) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/environments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": retryEnvID, "shortId": "73373373", "status": "queued"})
	})
	mux.HandleFunc("/api/v1/environments/"+retryEnvID, func(w http.ResponseWriter, r *http.Request) {
		n := int(atomic.AddInt32(gets, 1))
		if n > len(replies) {
			n = len(replies)
		}
		replies[n-1](w)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func createWait(t *testing.T, srv *httptest.Server, extra ...string) (int, string, time.Duration) {
	t.Helper()
	args := []string{"--api-url", srv.URL, "--token", "bdk_test", "env", "create", "--name", "retry-env",
		"--version", "28.5.54151.56140", "--country", "au", "--region", "australiaeast", "--wait"}
	return runExit(t, append(args, extra...)...)
}

func TestEnvCreateWait_TraefikPlain404And502MidWait_RetriesAndReachesRunning(t *testing.T) {
	fastPolls(t)
	var gets int32
	srv := retryStub(t, &gets, traefik404, badGateway, runningEnv)
	code, out, _ := createWait(t, srv)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (a deploy's 404 and a 502 are transient); output: %s", code, out)
	}
	if got := atomic.LoadInt32(&gets); got != 3 {
		t.Fatalf("GETs = %d, want 3 (404, 502, then running)", got)
	}
	if !strings.Contains(out, "poll failed, retrying") {
		t.Fatalf("a retried poll should say so; output: %s", out)
	}
}

func TestEnvCreateWait_ConnectionResetMidBody_RetriesAndReachesRunning(t *testing.T) {
	fastPolls(t)
	var gets int32
	srv := retryStub(t, &gets, resetMidBody, runningEnv)
	code, out, _ := createWait(t, srv)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (a reset while reading the body is a network error); output: %s", code, out)
	}
	if got := atomic.LoadInt32(&gets); got != 2 {
		t.Fatalf("GETs = %d, want 2 (the reset, then running)", got)
	}
}

// The control: the API's own JSON 404 means the environment is gone, and still ends the wait
// at once with exit 5, as before #733.
func TestEnvCreateWait_APIJson404_StillExitsFive_WithoutRetry(t *testing.T) {
	fastPolls(t)
	var gets int32
	srv := retryStub(t, &gets, apiNotFound, runningEnv)
	code, out, _ := createWait(t, srv)
	if code != 5 {
		t.Fatalf("exit = %d, want 5; output: %s", code, out)
	}
	if got := atomic.LoadInt32(&gets); got != 1 {
		t.Fatalf("GETs = %d, want 1: the API's JSON 404 must not be retried", got)
	}
}

func TestEnvCreateWait_401_StillExitsThree_WithoutRetry(t *testing.T) {
	fastPolls(t)
	var gets int32
	srv := retryStub(t, &gets, apiUnauthorized, runningEnv)
	code, out, _ := createWait(t, srv)
	if code != 3 {
		t.Fatalf("exit = %d, want 3; output: %s", code, out)
	}
	if got := atomic.LoadInt32(&gets); got != 1 {
		t.Fatalf("GETs = %d, want 1: an auth failure must not be retried", got)
	}
}

// An API that stays unreachable ends the wait AT the deadline, never later, and the message
// names the environment so a script can remove it.
func TestEnvCreateWait_DownForTheWholeWait_EndsAtTheDeadline_NamingTheEnv(t *testing.T) {
	fastPolls(t)
	var gets int32
	srv := retryStub(t, &gets, badGateway)
	code, out, took := createWait(t, srv, "--wait-timeout", "1s")
	if code == 0 {
		t.Fatalf("exit = 0, want a failure; output: %s", out)
	}
	if took > 1800*time.Millisecond {
		t.Fatalf("took %s: retries must not extend the 1s deadline", took)
	}
	if got := atomic.LoadInt32(&gets); got < 3 {
		t.Fatalf("GETs = %d, want several retries before the deadline", got)
	}
	for _, want := range []string{"timed out after 1s", "may still be provisioning", "bcdock env delete " + retryEnvID} {
		if !strings.Contains(out, want) {
			t.Fatalf("message lacks %q; output: %s", want, out)
		}
	}
}

// `env wait` turns a deadline into exit 124; one that ends on an unreachable API is still a
// deadline, so it is 124 too, never the exit 5 of the first failed poll.
func TestEnvWait_DownForTheWholeWait_Exits124(t *testing.T) {
	fastPolls(t)
	var gets int32
	srv := retryStub(t, &gets, traefik404)
	code, out, _ := runExit(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "wait", retryEnvID, "--status", "running", "--timeout", "500ms")
	if code != 124 {
		t.Fatalf("exit = %d, want 124; output: %s", code, out)
	}
}

func TestIsTransientPollError(t *testing.T) {
	live := context.Background()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	apiErr := func(status int, body string) error {
		return &client.APIError{Status: status, RawBody: []byte(body)}
	}
	netErr := &url.Error{Op: "Get", URL: "https://api.bcdock.io/x", Err: errors.New("connection refused")}
	cases := []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"network error", live, netErr, true},
		{"wrapped network error", live, errors.Join(errors.New("request failed"), netErr), true},
		{"502", live, apiErr(502, "<html>bad gateway</html>"), true},
		{"503 with json", live, apiErr(503, `{"error":"unavailable"}`), true},
		{"429", live, apiErr(429, `{"error":"slow down"}`), true},
		{"traefik 404", live, apiErr(404, "404 page not found\n"), true},
		{"empty 404", live, apiErr(404, ""), true},
		{"API json 404", live, apiErr(404, `{"error":"Environment not found","code":"not_found"}`), false},
		{"401", live, apiErr(401, `{"error":"no"}`), false},
		{"403", live, apiErr(403, `{"error":"no"}`), false},
		{"400", live, apiErr(400, `{"error":"bad"}`), false},
		{"cancelled context", cancelled, netErr, false},
		{"body read reset", live, &client.ReadError{Err: errors.New("unexpected EOF")}, true},
		{"other error", live, errors.New("decode response: boom"), false},
	}
	for _, c := range cases {
		if got := isTransientPollError(c.ctx, c.err); got != c.want {
			t.Errorf("%s: transient = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPollRetryDelay_DoublesFromTheIntervalAndIsCapped(t *testing.T) {
	want := []time.Duration{3 * time.Second, 6 * time.Second, 12 * time.Second, 24 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, w := range want {
		if got := pollRetryDelay(i + 1); got != w {
			t.Errorf("failure %d: delay = %s, want %s", i+1, got, w)
		}
	}
}
