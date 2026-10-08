package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #591. The REAL /api/v1/me/billing body for a Free Trial company (AdminCompaniesEndpointTests).
// In table mode ENV CAP printed as 0x..., and a subscription as &{0x.. 0x.. ...}.
const realMeBillingBody = `{"plan":{"tier":"free_trial","displayName":"Free Trial","currency":"AUD","baseMonthlyAmount":0.0,"hourlyRate":0.0,"storedHourlyRate":0.0,"maxEnvironments":4},"subscription":null,"paymentMethod":null,"invoices":[]}`

func billingTableStub(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMeBillingShow_Table_FreeTrial_EnvCapIsANumber(t *testing.T) {
	srv := billingTableStub(t, realMeBillingBody)
	stdout, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "me", "billing", "show")
	if err != nil {
		t.Fatalf("me billing show: %v", err)
	}
	if !strings.Contains(stdout, "{free_trial Free Trial AUD 0 0 0 4}") {
		t.Errorf("the plan cell must carry the env cap 4:\n%s", stdout)
	}
	if strings.Contains(stdout, "0x") || strings.Contains(stdout, "&{") || strings.Contains(stdout, "<nil>") {
		t.Errorf("an address leaked:\n%s", stdout)
	}
}

// The same body with a live subscription, which is where the nested pointer struct printed as &{0x..}.
func TestMeBillingShow_Table_WithSubscription_FieldsAreReadable(t *testing.T) {
	body := strings.Replace(realMeBillingBody, `"subscription":null`,
		`"subscription":{"stripeSubscriptionId":"sub_591","stripeStatus":"active","currentPeriodStart":"2026-10-01T00:00:00Z","currentPeriodEnd":"2026-11-01T00:00:00Z","cancelAtPeriodEnd":false,"canceledAt":null}`, 1)
	srv := billingTableStub(t, body)
	stdout, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "me", "billing", "show")
	if err != nil {
		t.Fatalf("me billing show: %v", err)
	}
	if !strings.Contains(stdout, "{sub_591 active 2026-10-01T00:00:00Z 2026-11-01T00:00:00Z false -}") {
		t.Errorf("subscription cell not readable:\n%s", stdout)
	}
	if strings.Contains(stdout, "0x") || strings.Contains(stdout, "&{") {
		t.Errorf("an address leaked:\n%s", stdout)
	}
}
