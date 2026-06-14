package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAuthJoinWaitlist_PostsFullPayload(t *testing.T) {
	var got waitlistFullRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/public/waitlist" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"Thanks! We'll be in touch."}`))
	}))
	defer srv.Close()

	_, _, err := RunCmd(t,
		"--api-url", srv.URL,
		"auth", "join-waitlist",
		"--name", "Alex Tester",
		"--email", "alex@example.com",
		"--expectations", "smoke testing",
		"--bc-version", "27.5",
		"--country", "AU",
		"--artifact-type", "Sandbox",
		"--region", "australiaeast",
		"--multi-tenant", "false",
	)
	if err != nil {
		t.Fatalf("join-waitlist: %v", err)
	}

	if got.Name != "Alex Tester" {
		t.Errorf("Name = %q, want Alex Tester", got.Name)
	}
	if got.Email != "alex@example.com" {
		t.Errorf("Email = %q, want alex@example.com", got.Email)
	}
	if got.UseCase != "smoke testing" {
		t.Errorf("UseCase = %q, want smoke testing", got.UseCase)
	}
	if got.BcVersion != "27.5" {
		t.Errorf("BcVersion = %q, want 27.5", got.BcVersion)
	}
	if got.Country != "AU" {
		t.Errorf("Country = %q, want AU", got.Country)
	}
	if got.ArtifactType != "Sandbox" {
		t.Errorf("ArtifactType = %q, want Sandbox", got.ArtifactType)
	}
	if got.PreferredRegion != "australiaeast" {
		t.Errorf("PreferredRegion = %q, want australiaeast", got.PreferredRegion)
	}
	if got.MultiTenant == nil || *got.MultiTenant != false {
		t.Errorf("MultiTenant = %v, want false", got.MultiTenant)
	}
}

func TestAuthSignup_NoInviteCode_PrintsHintAndErrors(t *testing.T) {
	// Server should not be hit at all - CLI guards locally.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected server hit: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	_, _, err := RunCmd(t, "--api-url", srv.URL, "auth", "signup")
	if err == nil {
		t.Fatal("expected error when --invite-code missing")
	}
	if !strings.Contains(err.Error(), "invite-code") {
		t.Errorf("error missing 'invite-code' guidance: %v", err)
	}
}

func TestAuthSignup_ValidCode_ActivatesAccount(t *testing.T) {
	var validateHits, signupHits int
	var capturedSignup signupRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/public/validate-invite-code":
			validateHits++
			// The pre-flight must receive BOTH code and email (the pair is
			// validated server-side; the bound email is never returned).
			body, _ := io.ReadAll(r.Body)
			var req map[string]string
			_ = json.Unmarshal(body, &req)
			if req["code"] == "" || req["email"] == "" {
				t.Errorf("validate-invite-code must receive both code and email; got %v", req)
			}
			w.Header().Set("Content-Type", "application/json")
			// Pre-fill from the waitlist entry. No email field - the endpoint
			// no longer reveals the address bound to the code.
			_, _ = w.Write([]byte(`{
				"valid": true,
				"waitlistConfig": {
					"name": "Alex from Waitlist",
					"bcVersion": "27.5",
					"country": "AU",
					"artifactType": "Sandbox",
					"region": "australiaeast"
				}
			}`))
		case "/api/v1/auth/signup":
			signupHits++
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &capturedSignup); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"ok": true,
				"userId": "00000000-0000-0000-0000-000000000001",
				"environmentShortId": "abcd1234"
			}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	stdout, _, err := RunCmd(t,
		"--api-url", srv.URL,
		"auth", "signup",
		"--invite-code", "ABCD1234",
		"--email", "alex@example.com",
		"--accept-eula",
	)
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	if validateHits != 1 {
		t.Errorf("validateHits = %d, want 1", validateHits)
	}
	if signupHits != 1 {
		t.Errorf("signupHits = %d, want 1", signupHits)
	}

	// Pre-fill from waitlist applied
	if capturedSignup.Name != "Alex from Waitlist" {
		t.Errorf("Name pre-fill missing, got %q", capturedSignup.Name)
	}
	if capturedSignup.BcVersion != "27.5" {
		t.Errorf("BcVersion pre-fill missing, got %q", capturedSignup.BcVersion)
	}
	if !capturedSignup.AcceptEula {
		t.Errorf("AcceptEula not set")
	}
	if capturedSignup.Email != "alex@example.com" {
		t.Errorf("Email = %q, want alex@example.com", capturedSignup.Email)
	}
	if capturedSignup.InviteCode != "ABCD1234" {
		t.Errorf("InviteCode = %q, want ABCD1234", capturedSignup.InviteCode)
	}

	// Output should mention the env short id and the next-step login command
	if !strings.Contains(stdout, "abcd1234") {
		t.Errorf("stdout missing env short id: %s", stdout)
	}
}

func TestAuthSignup_InvalidCode_ReturnsOpaqueError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/public/validate-invite-code" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"valid":false}`))
	}))
	defer srv.Close()

	_, _, err := RunCmd(t,
		"--api-url", srv.URL,
		"auth", "signup",
		"--invite-code", "INVALID8",
		"--email", "alex@example.com",
		"--accept-eula",
	)
	if err == nil {
		t.Fatal("expected error for invalid code")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "email or invite code") {
		t.Errorf("error not opaque-format: %v", err)
	}
}

func TestAuthSignup_FlagOverridesPrefill(t *testing.T) {
	var capturedSignup signupRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/public/validate-invite-code":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"valid": true,
				"waitlistConfig": {
					"bcVersion": "27.5",
					"country": "AU",
					"artifactType": "Sandbox",
					"region": "australiaeast"
				}
			}`))
		case "/api/v1/auth/signup":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &capturedSignup)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"userId":"00000000-0000-0000-0000-000000000002"}`))
		}
	}))
	defer srv.Close()

	_, _, err := RunCmd(t,
		"--api-url", srv.URL,
		"auth", "signup",
		"--invite-code", "ABCD1234",
		"--email", "alex@example.com",
		"--bc-version", "28.0",  // override
		"--country", "US",       // override
		"--accept-eula",
	)
	if err != nil {
		t.Fatalf("signup: %v", err)
	}

	// CLI flags should take precedence over waitlist prefill
	if capturedSignup.BcVersion != "28.0" {
		t.Errorf("BcVersion = %q, want 28.0 (flag should override prefill)", capturedSignup.BcVersion)
	}
	if capturedSignup.Country != "US" {
		t.Errorf("Country = %q, want US (flag should override prefill)", capturedSignup.Country)
	}
	// Non-overridden field should still pre-fill
	if capturedSignup.ArtifactType != "Sandbox" {
		t.Errorf("ArtifactType pre-fill not applied, got %q", capturedSignup.ArtifactType)
	}
}

// Ensure stdin pipe doesn't bleed into other tests; each test creates its own.
func init() {
	// no-op - real os.Stdin handling lives in the actual prompt() helper.
	_ = os.Stdin
}
