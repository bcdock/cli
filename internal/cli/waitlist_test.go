package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// SEC-023: `bcdock waitlist join` is the non-interactive waitlist submit the smoke
// drives. With the dev Turnstile bypass set it submits INLINE (Deferred=false) and the
// client carries the X-Dev-Turnstile-Bypass header; without it, it requests the
// deferred browser-confirm flow.

func TestWaitlistJoin_BypassSet_InlineSubmit_SendsHeader(t *testing.T) {
	t.Setenv("BCDOCK_DEV_TURNSTILE_BYPASS", "bypass-xyz")

	var gotHeader string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Dev-Turnstile-Bypass")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Thanks!"})
	}))
	defer srv.Close()

	_, _, err := RunCmd(t, "--api-url", srv.URL, "waitlist", "join",
		"--name", "CI", "--email", "ci+tok.noemail@example.com")
	if err != nil {
		t.Fatalf("waitlist join: %v", err)
	}
	if gotHeader != "bypass-xyz" {
		t.Errorf("X-Dev-Turnstile-Bypass = %q, want %q", gotHeader, "bypass-xyz")
	}
	if d, _ := gotBody["deferred"].(bool); d {
		t.Error("expected Deferred=false (inline submit) when the bypass is set")
	}
	if gotBody["email"] != "ci+tok.noemail@example.com" {
		t.Errorf("email = %v, want the supplied address", gotBody["email"])
	}
}

func TestWaitlistJoin_NoBypass_DeferredFlow(t *testing.T) {
	t.Setenv("BCDOCK_DEV_TURNSTILE_BYPASS", "") // explicitly absent (real human path)

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Almost there"})
	}))
	defer srv.Close()

	_, _, err := RunCmd(t, "--api-url", srv.URL, "waitlist", "join",
		"--name", "Jane", "--email", "jane@example.com")
	if err != nil {
		t.Fatalf("waitlist join: %v", err)
	}
	if d, _ := gotBody["deferred"].(bool); !d {
		t.Error("expected Deferred=true (deferred flow) when no bypass is set")
	}
}

func TestWaitlistJoin_RequiresNameAndEmail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, _, err := RunCmd(t, "--api-url", srv.URL, "waitlist", "join", "--email", "x@example.com"); err == nil {
		t.Error("expected an error when --name is missing")
	}
	if _, _, err := RunCmd(t, "--api-url", srv.URL, "waitlist", "join", "--name", "X"); err == nil {
		t.Error("expected an error when --email is missing")
	}
}
