package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// #576: outlook.com can junk our mail even with SPF/DKIM/DMARC passing (#402), so every place the
// CLI makes a human wait for an email says where to look. The --otp path has no human waiting at
// a prompt (agents, smokes), so it stays quiet.

const junkCodeHint = "No code? Check your Junk or Spam folder."
const junkEmailHint = "No email? Check your Junk or Spam folder."

func loginServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/email/send-code":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/auth/email/exchange":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"key": "bdk_testapikey123", "keyPrefix": "bdk_testapi"})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestAuthLogin_Interactive_PrintsJunkHint(t *testing.T) {
	srv := loginServer(t)
	defer srv.Close()
	t.Setenv("BCDOCK_CONFIG_DIR", t.TempDir())

	old := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	_, _ = w.WriteString("123456\n")
	_ = w.Close()
	defer func() { os.Stdin = old }()

	_, stderr, err := RunCmd(t, "--api-url", srv.URL, "auth", "login", "--email", "alex@example.com")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(stderr, junkCodeHint) {
		t.Errorf("stderr lacks %q:\n%s", junkCodeHint, stderr)
	}
}

func TestAuthLogin_OTPFlag_NoJunkHint(t *testing.T) {
	srv := loginServer(t)
	defer srv.Close()
	t.Setenv("BCDOCK_CONFIG_DIR", t.TempDir())

	_, stderr, err := RunCmd(t, "--api-url", srv.URL, "auth", "login", "--email", "alex@example.com", "--otp", "123456")
	if err != nil {
		t.Fatalf("login --otp: %v", err)
	}
	if strings.Contains(stderr, "Junk or Spam") {
		t.Errorf("--otp path printed the junk hint (no human is waiting):\n%s", stderr)
	}
}

func TestAuthJoinWaitlist_PrintsJunkHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"Thanks! We'll be in touch."}`))
	}))
	defer srv.Close()

	_, stderr, err := RunCmd(t, "--api-url", srv.URL, "auth", "join-waitlist",
		"--name", "Alex Tester", "--email", "alex@example.com", "--expectations", "smoke testing",
		"--bc-version", "27.5", "--country", "AU", "--artifact-type", "Sandbox",
		"--region", "australiaeast", "--multi-tenant", "false")
	if err != nil {
		t.Fatalf("join-waitlist: %v", err)
	}
	if !strings.Contains(stderr, junkEmailHint) {
		t.Errorf("stderr lacks %q:\n%s", junkEmailHint, stderr)
	}
}
