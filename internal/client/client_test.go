package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bcdock/cli/internal/client"
	"github.com/bcdock/cli/internal/exitcode"
)

func TestAPIError_ExitCode(t *testing.T) {
	cases := []struct {
		status   int
		wantCode int
	}{
		{http.StatusUnauthorized, exitcode.AuthFailure},
		{http.StatusForbidden, exitcode.AuthFailure},
		{http.StatusNotFound, exitcode.NotFound},
		{http.StatusTooManyRequests, exitcode.RateLimited},
		{http.StatusInternalServerError, exitcode.GeneralError},
	}
	for _, tc := range cases {
		err := &client.APIError{Status: tc.status}
		if got := err.ExitCode(); got != tc.wantCode {
			t.Errorf("status %d: ExitCode() = %d, want %d", tc.status, got, tc.wantCode)
		}
	}
}

func TestClient_Do_Success(t *testing.T) {
	type response struct {
		ID string `json:"id"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer testtoken" {
			t.Errorf("missing or wrong Authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response{ID: "abc123"})
	}))
	defer srv.Close()

	c := client.New(srv.URL, "testtoken", 5*time.Second)
	var out response
	if err := c.Do(context.Background(), http.MethodGet, "/test", nil, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if out.ID != "abc123" {
		t.Errorf("got ID %q, want abc123", out.ID)
	}
}

func TestClient_Do_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": "not_found", "message": "environment not found"},
		})
	}))
	defer srv.Close()

	c := client.New(srv.URL, "", 5*time.Second)
	err := c.Do(context.Background(), http.MethodGet, "/missing", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", apiErr.Status)
	}
	if apiErr.ExitCode() != exitcode.NotFound {
		t.Errorf("ExitCode() = %d, want %d", apiErr.ExitCode(), exitcode.NotFound)
	}
}

func TestClient_Do_NonJsonErrorBodyIncluded(t *testing.T) {
	// Regression for CLI-ERROR-BODY: when the API returns a 5xx with a non-
	// canonical body (Stripe.net stack trace, DeveloperExceptionPage HTML,
	// upstream 5xx page), the CLI must surface the body excerpt — not just
	// "HTTP 500" — so the operator has something actionable to debug.
	const stripeStackBody = "StripeException: You must specify a tax code in all line items to calculate taxes. Alternatively you can set a default tax code on your tax settings."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(stripeStackBody))
	}))
	defer srv.Close()

	c := client.New(srv.URL, "", 5*time.Second)
	err := c.Do(context.Background(), http.MethodPost, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "tax code") {
		t.Errorf("expected error body excerpt in message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("expected HTTP 500 marker in message, got: %v", err)
	}
}

func TestClient_Do_EmptyBodyFallsBackToStatusOnly(t *testing.T) {
	// Negative: when the body is genuinely empty, don't dump "HTTP 500: "
	// with a trailing space — just "HTTP 500".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "", 5*time.Second)
	err := c.Do(context.Background(), http.MethodGet, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); got != "HTTP 503" {
		t.Errorf("error = %q, want %q", got, "HTTP 503")
	}
}

func TestClient_Do_LongBodyTruncated(t *testing.T) {
	// Pathological 5xx body (multi-KB stack trace) must not flood the terminal.
	huge := strings.Repeat("x", 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(huge))
	}))
	defer srv.Close()

	c := client.New(srv.URL, "", 5*time.Second)
	err := c.Do(context.Background(), http.MethodGet, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "truncated") {
		t.Errorf("expected 'truncated' marker, got: %s", msg)
	}
	if len(msg) > 700 {
		t.Errorf("error message too long (%d chars): expected ~500 + status prefix + suffix", len(msg))
	}
}

// SEC-023: DoWithHeaders attaches the given per-request headers (the waitlist-join verb uses it
// for the X-Dev-Turnstile-Bypass header, scoped to that one POST); plain Do attaches none.

func TestClient_DoWithHeaders_SendsExtraHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Dev-Turnstile-Bypass")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "ok"})
	}))
	defer srv.Close()

	c := client.New(srv.URL, "", 5*time.Second)
	headers := map[string]string{"X-Dev-Turnstile-Bypass": "bypass-xyz"}
	if err := c.DoWithHeaders(context.Background(), http.MethodPost, "/api/v1/public/waitlist", map[string]string{"email": "x"}, nil, headers); err != nil {
		t.Fatalf("DoWithHeaders: %v", err)
	}
	if got != "bypass-xyz" {
		t.Errorf("X-Dev-Turnstile-Bypass = %q, want %q", got, "bypass-xyz")
	}
}

func TestClient_Do_NoExtraHeaders(t *testing.T) {
	var present bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, present = r.Header["X-Dev-Turnstile-Bypass"]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "ok"})
	}))
	defer srv.Close()

	c := client.New(srv.URL, "", 5*time.Second)
	if err := c.Do(context.Background(), http.MethodPost, "/x", map[string]string{}, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if present {
		t.Error("plain Do attached the X-Dev-Turnstile-Bypass header")
	}
}
