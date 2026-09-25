package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bcdock/cli/internal/config"
)
// runCmd / resetCmdFlags moved to cmdtest.go as exported RunCmd / ResetCmdFlags
// so the admin test package can share the same harness.

func TestAuthSetToken_SavesCredentials(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BCDOCK_CONFIG_DIR", dir)

	_, _, err := RunCmd(t, "auth", "set-token", "bdk_testkey123")
	if err != nil {
		t.Fatalf("set-token: %v", err)
	}

	creds, err := config.LoadCredentials()
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if creds.Token != "bdk_testkey123" {
		t.Errorf("token = %q, want bdk_testkey123", creds.Token)
	}

	// Credentials file must be mode 0600
	info, err := os.Stat(filepath.Join(dir, "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("credentials.json perm = %o, want 0600", perm)
	}
}

func TestAuthWhoami_PrintsUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/me" || r.Method != http.MethodGet {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing Bearer token")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"email":        "alex@example.com",
			"displayName":  "Alex",
			"platformRole": "Admin",
			"companyName":  "Contoso",
		})
	}))
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "--output", "json", "auth", "whoami")
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if !strings.Contains(out, "alex@example.com") {
		t.Errorf("output missing email: %s", out)
	}
}

func TestAuthWhoami_NoToken_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BCDOCK_CONFIG_DIR", dir)
	t.Setenv("BCDOCK_TOKEN", "")

	_, _, err := RunCmd(t, "--token", "", "auth", "whoami")
	if err == nil {
		t.Fatal("expected error when no token")
	}
	if !strings.Contains(err.Error(), "not authenticated") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAuthLogout_ClearsToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BCDOCK_CONFIG_DIR", dir)

	// Store a token first
	if err := config.SaveCredentials(&config.Credentials{Token: "bdk_old"}); err != nil {
		t.Fatal(err)
	}

	// Logout without hitting a real server (no --api-url, server call is best-effort)
	_, _, err := RunCmd(t, "--token", "bdk_old", "--api-url", "http://127.0.0.1:0", "auth", "logout")
	if err != nil {
		t.Fatalf("logout: %v", err)
	}

	creds, err := config.LoadCredentials()
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if creds.Token != "" {
		t.Errorf("token not cleared, got %q", creds.Token)
	}
}

func TestAuthLogin_OTPFlow(t *testing.T) {
	var gotSendEmail string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/email/send-code":
			var req sendCodeRequest
			json.NewDecoder(r.Body).Decode(&req)
			gotSendEmail = req.Email
			w.WriteHeader(http.StatusOK)
		case "/api/v1/auth/email/exchange":
			var req exchangeRequest
			json.NewDecoder(r.Body).Decode(&req)
			if req.Code != "123456" {
				t.Errorf("code = %q, want 123456", req.Code)
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"key":       "bdk_testapikey123",
				"keyPrefix": "bdk_testapi",
			})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	t.Setenv("BCDOCK_CONFIG_DIR", dir)

	old := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	w.WriteString("123456\n")
	w.Close()
	defer func() { os.Stdin = old }()

	_, _, err := RunCmd(t, "--api-url", srv.URL, "auth", "login", "--email", "alex@example.com")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if gotSendEmail != "alex@example.com" {
		t.Errorf("send-code email = %q, want alex@example.com", gotSendEmail)
	}

	creds, err := config.LoadCredentials()
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if creds.Token != "bdk_testapikey123" {
		t.Errorf("token = %q, want bdk_testapikey123", creds.Token)
	}
}
