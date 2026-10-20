//go:build linux

package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/bcdock/cli/internal/config"
)

// #725: an agent logs in with two calls and one relayed code, with no terminal. A newly sent code
// cancels every earlier one, so `--otp` must never send; without a terminal, the first call sends
// and exits 0 saying how to finish. At a terminal (a real pty) nothing changes: send, prompt, log in.

type loginRecorder struct {
	mu        sync.Mutex
	paths     []string
	exchanged string
}

func (l *loginRecorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		l.paths = append(l.paths, r.URL.Path)
		l.mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/auth/email/send-code":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/auth/email/exchange":
			var req exchangeRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			l.mu.Lock()
			l.exchanged = req.Code
			l.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"key": "bdk_testapikey123", "keyPrefix": "bdk_testapi"})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (l *loginRecorder) count(path string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, p := range l.paths {
		if p == path {
			n++
		}
	}
	return n
}

const sendPath, exchangePath = "/api/v1/auth/email/send-code", "/api/v1/auth/email/exchange"

// Acceptance (1), first half: --otp makes NO send-code request.
func TestAuthLogin_OTP_NeverSendsACode(t *testing.T) {
	rec := &loginRecorder{}
	srv := rec.server(t)
	t.Setenv("BCDOCK_CONFIG_DIR", t.TempDir())
	withStdin(t, devNull(t))

	_, _, err := RunCmd(t, "--api-url", srv.URL, "auth", "login", "--email", "alex@example.com", "--otp", "123456")
	if err != nil {
		t.Fatalf("login --otp: %v", err)
	}
	if n := rec.count(sendPath); n != 0 {
		t.Errorf("--otp made %d send-code request(s), want 0: a new code would cancel the one passed in", n)
	}
	if rec.count(exchangePath) != 1 || rec.exchanged != "123456" {
		t.Errorf("exchange: %d call(s), code %q", rec.count(exchangePath), rec.exchanged)
	}
	if creds, _ := config.LoadCredentials(); creds == nil || creds.Token != "bdk_testapikey123" {
		t.Errorf("credentials not stored: %+v", creds)
	}
}

// Acceptance (1), second half: no terminal and no --otp sends exactly once, exits 0, says how to
// finish, and stores nothing. Both non-terminal stdins agents run with.
func TestAuthLogin_NoTerminal_SendsOnce_ExitsZero_SaysReRunWithOTP(t *testing.T) {
	for name, stdin := range map[string]func(*testing.T) *os.File{"devnull": devNull, "pipe": pipeStdin} {
		t.Run(name, func(t *testing.T) {
			rec := &loginRecorder{}
			srv := rec.server(t)
			t.Setenv("BCDOCK_CONFIG_DIR", t.TempDir())
			withStdin(t, stdin(t))

			_, stderr, err := RunCmd(t, "--api-url", srv.URL, "auth", "login", "--email", "alex@example.com")
			if err != nil {
				t.Fatalf("login without a terminal: %v", err)
			}
			if rec.count(sendPath) != 1 || rec.count(exchangePath) != 0 {
				t.Errorf("requests %v, want exactly one send-code and no exchange", rec.paths)
			}
			for _, want := range []string{"Code sent to alex@example.com", "bcdock auth login --email alex@example.com --otp <code>", junkCodeHint} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr lacks %q:\n%s", want, stderr)
				}
			}
			if creds, _ := config.LoadCredentials(); creds != nil && creds.Token != "" {
				t.Errorf("stored a token without a code: %q", creds.Token)
			}
		})
	}
}

func TestAuthLogin_NoTerminal_JSON(t *testing.T) {
	rec := &loginRecorder{}
	srv := rec.server(t)
	t.Setenv("BCDOCK_CONFIG_DIR", t.TempDir())
	withStdin(t, devNull(t))

	stdout, _, err := RunCmd(t, "--api-url", srv.URL, "-o", "json", "auth", "login", "--email", "alex@example.com")
	if err != nil {
		t.Fatalf("login -o json: %v", err)
	}
	var got codeSentResponse
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout)
	}
	if got.Email != "alex@example.com" || !got.CodeSent || got.Next != "bcdock auth login --email alex@example.com --otp <code>" {
		t.Errorf("json = %+v", got)
	}
}

// The two-call agent flow end to end, against one server: the relayed code from call 1 is the one
// call 2 exchanges, and nothing was sent in between.
func TestAuthLogin_AgentTwoCallFlow(t *testing.T) {
	rec := &loginRecorder{}
	srv := rec.server(t)
	t.Setenv("BCDOCK_CONFIG_DIR", t.TempDir())
	withStdin(t, devNull(t))

	if _, _, err := RunCmd(t, "--api-url", srv.URL, "auth", "login", "--email", "alex@example.com"); err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if _, _, err := RunCmd(t, "--api-url", srv.URL, "auth", "login", "--email", "alex@example.com", "--otp", "654321"); err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if got := strings.Join(rec.paths, ","); got != sendPath+","+exchangePath {
		t.Errorf("requests = %s, want exactly send-code then exchange", got)
	}
	if rec.exchanged != "654321" {
		t.Errorf("exchanged %q, want the relayed 654321", rec.exchanged)
	}
}

func TestAuthLogin_NoTerminal_NoEmail_IsAnError(t *testing.T) {
	rec := &loginRecorder{}
	srv := rec.server(t)
	t.Setenv("BCDOCK_CONFIG_DIR", t.TempDir())
	withStdin(t, devNull(t))

	_, _, err := RunCmd(t, "--api-url", srv.URL, "auth", "login")
	if err == nil || !strings.Contains(err.Error(), "--email is required without a terminal") {
		t.Errorf("err = %v", err)
	}
	if len(rec.paths) != 0 {
		t.Errorf("requests %v without an email", rec.paths)
	}
}

// The control: at a real terminal the flow is unchanged (send, prompt, log in), and the junk-folder
// hint shows. Moved here from a pipe-fed test: a pipe is not a terminal any more (#708).
func TestAuthLogin_Pty_SendsPromptsAndLogsIn(t *testing.T) {
	rec := &loginRecorder{}
	srv := rec.server(t)
	t.Setenv("BCDOCK_CONFIG_DIR", t.TempDir())
	master, slave := openPty(t)
	withStdin(t, slave)
	if _, err := master.Write([]byte("123456\n")); err != nil {
		t.Fatal(err)
	}

	var stderr string
	var err error
	prompted := captureOSStderr(t, func() {
		_, stderr, err = RunCmd(t, "--api-url", srv.URL, "auth", "login", "--email", "alex@example.com")
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if got := strings.Join(rec.paths, ","); got != sendPath+","+exchangePath || rec.exchanged != "123456" {
		t.Errorf("requests = %s, exchanged %q", got, rec.exchanged)
	}
	if !strings.Contains(prompted, "Code: ") {
		t.Errorf("no Code prompt at a terminal: %q", prompted)
	}
	if !strings.Contains(stderr, junkCodeHint) {
		t.Errorf("stderr lacks %q:\n%s", junkCodeHint, stderr)
	}
	if strings.Contains(stderr, "--otp <code>") {
		t.Errorf("a terminal got the no-terminal instruction:\n%s", stderr)
	}
	if creds, _ := config.LoadCredentials(); creds == nil || creds.Token != "bdk_testapikey123" {
		t.Errorf("credentials not stored: %+v", creds)
	}
}
