package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #715 slice 3. Bodies are the REAL API's shapes, from the platform source: CompaniesController
// UpdateLocation's anonymous response and its BadRequestError, AuthController /auth/me, the
// SubscriptionPlanResponse projection, and ExportBillingCsv's header row.
const (
	companyID15     = "3072f5a0-6c1e-4b3a-9d2f-8e7a6b5c4d3e"
	realMe200       = `{"id":"9b1c2d3e-4f50-6172-8394-a5b6c7d8e9f0","email":"dev@contoso.example","companyId":"` + companyID15 + `","companyName":"Contoso Ltd"}`
	realLocation200 = `{"id":"` + companyID15 + `","name":"Contoso Ltd","country":{"code":"US","name":"United States"},"region":{"id":"7d6c5b4a-3928-4170-8e6f-5a4b3c2d1e0f","name":"United States - Iowa","azureRegion":"centralus"}}`
	realBadCountry  = `{"error":"Unknown country code","code":"invalid_input"}`
	realForbidden   = `{"error":"Forbidden"}`
	realPlans200    = `[{"id":"a1","name":"payg","displayName":"Pay-As-You-Go","description":"No commitment","hourlyRate":0.56,"currency":"USD","maxEnvironments":3},` +
		`{"id":"a2","name":"pro","displayName":"Pro","description":"Teams","hourlyRate":0.4,"currency":"USD","maxEnvironments":null}]`
	realExportCSV = "date_range_from,date_range_to,environment_id,environment_name,is_deleted,hours,cost_aud\n" +
		"2026-09-01,2026-09-30,5b1a3c6e-2f4d-4a8b-9c0d-7e6f5a4b3c2d,\"demo \"\"q\"\"\",false,12.5,7.13\n"
)

// ── companies set-location ──

func TestCompaniesSetLocation_PutsCountryAndRegion_ToTheActiveCompany(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{
		"GET /api/v1/auth/me":                                {200, realMe200},
		"PUT /api/v1/companies/" + companyID15 + "/location": {200, realLocation200},
	})
	code, _, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "companies", "set-location", " us ", "--region", "centralus")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	put := (*seen)[1]
	var body map[string]any
	_ = json.Unmarshal([]byte(put.body), &body)
	if put.method != "PUT" || body["countryCode"] != "US" || body["azureRegionName"] != "centralus" {
		t.Errorf("request = %s %s %s", put.method, put.path, put.body)
	}
	if !strings.Contains(stderr, "Contoso Ltd: new environments now default to United States (US), region centralus") {
		t.Errorf("stderr: %s", stderr)
	}
}

// Without --region the field is omitted, so the API applies the country's default region.
func TestCompaniesSetLocation_WithoutRegion_OmitsTheField(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{
		"GET /api/v1/auth/me":                                {200, realMe200},
		"PUT /api/v1/companies/" + companyID15 + "/location": {200, realLocation200},
	})
	if code, _, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "companies", "set-location", "AU"); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if strings.Contains((*seen)[1].body, "azureRegionName") {
		t.Errorf("body carries azureRegionName without --region: %s", (*seen)[1].body)
	}
}

func TestCompaniesSetLocation_Json_IsTheApiResponse(t *testing.T) {
	srv, _ := routeStub(t, map[string]stubRoute{
		"GET /api/v1/auth/me":                                {200, realMe200},
		"PUT /api/v1/companies/" + companyID15 + "/location": {200, realLocation200},
	})
	code, stdout, _ := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "-o", "json", "companies", "set-location", "US")
	if code != 0 || !strings.Contains(stdout, `"azureRegion": "centralus"`) {
		t.Errorf("exit %d, stdout %s", code, stdout)
	}
}

func TestCompaniesSetLocation_UnknownCountry_Exit1_NotOwner_Exit3(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		code   int
		want   string
	}{{400, realBadCountry, 1, "Unknown country code"}, {403, realForbidden, 3, ""}} {
		srv, _ := routeStub(t, map[string]stubRoute{
			"GET /api/v1/auth/me":                                {200, realMe200},
			"PUT /api/v1/companies/" + companyID15 + "/location": {tc.status, tc.body},
		})
		code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "companies", "set-location", "ZZ")
		if code != tc.code || !strings.Contains(out, tc.want) {
			t.Errorf("status %d: exit %d (want %d), out %s", tc.status, code, tc.code, out)
		}
	}
}

// ── me set-timezone ──

func TestMeSetTimezone_PutsTheIanaId(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{"PUT /api/v1/auth/me/timezone": {204, ""}})
	code, _, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "me", "set-timezone", "Australia/Melbourne")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if (*seen)[0].method != "PUT" || (*seen)[0].body != `{"timeZone":"Australia/Melbourne"}` {
		t.Errorf("request = %s %s", (*seen)[0].method, (*seen)[0].body)
	}
	if !strings.Contains(stderr, "Time zone set to Australia/Melbourne") {
		t.Errorf("stderr: %s", stderr)
	}
}

// The API stores any string, so a typo must fail HERE, before anything is sent.
func TestMeSetTimezone_NotAnIanaId_FailsWithoutCallingTheApi(t *testing.T) {
	for _, tz := range []string{"Australia/Melbrne", "Local", "AEST+10"} {
		srv, seen := routeStub(t, map[string]stubRoute{"PUT /api/v1/auth/me/timezone": {204, ""}})
		code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "me", "set-timezone", tz)
		if code != 1 || !strings.Contains(out, "is not an IANA time zone id") || len(*seen) != 0 {
			t.Errorf("%q: exit %d, %d request(s), out %s", tz, code, len(*seen), out)
		}
	}
}

// ── me billing export ──

// The shared routeStub drops the query and answers JSON; the export needs both, so its own stub.
func csvStub(t *testing.T, status int, body string) (*httptest.Server, *[]string) {
	t.Helper()
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/auth/me":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(realMe200))
		case "GET /api/v1/companies/" + companyID15 + "/billing/export.csv":
			queries = append(queries, r.URL.RawQuery)
			if status != 200 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body))
				return
			}
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			_, _ = w.Write([]byte(body))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &queries
}

func TestMeBillingExport_WritesTheCsvByteForByte_ToStdout(t *testing.T) {
	srv, queries := csvStub(t, 200, realExportCSV)
	code, stdout, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "me", "billing", "export", "--from", "2026-09-01", "--to", "2026-09-30")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if stdout != realExportCSV {
		t.Errorf("stdout differs from the CSV:\n%q\nwant\n%q", stdout, realExportCSV)
	}
	if len(*queries) != 1 || (*queries)[0] != "from=2026-09-01&to=2026-09-30" {
		t.Errorf("query = %v", *queries)
	}
}

func TestMeBillingExport_Out_WritesTheFileByteForByte(t *testing.T) {
	srv, queries := csvStub(t, 200, realExportCSV)
	path := filepath.Join(t.TempDir(), "usage.csv")
	code, stdout, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "me", "billing", "export", "--out", path)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != realExportCSV {
		t.Errorf("file = %q (err %v)", got, err)
	}
	if stdout != "" || !strings.Contains(stderr, "Wrote "+path) {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
	if (*queries)[0] != "" { // no dates given: the API's own 30-day default applies
		t.Errorf("query = %q, want none", (*queries)[0])
	}
}

func TestMeBillingExport_BadDate_FailsWithoutCallingTheApi(t *testing.T) {
	srv, queries := csvStub(t, 200, realExportCSV)
	code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "me", "billing", "export", "--from", "01/09/2026")
	if code != 1 || !strings.Contains(out, "--from \"01/09/2026\" is not a date - use YYYY-MM-DD") || len(*queries) != 0 {
		t.Errorf("exit %d, %d request(s), out %s", code, len(*queries), out)
	}
}

func TestMeBillingExport_CompanyNotFound_Exit5_AndNoFileLeft(t *testing.T) {
	srv, _ := csvStub(t, 404, `{"error":"Company not found or you are not a member","code":"not_found"}`)
	path := filepath.Join(t.TempDir(), "usage.csv")
	code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "me", "billing", "export", "--out", path)
	if code != 5 || !strings.Contains(out, "Company not found") {
		t.Errorf("exit %d, out %s", code, out)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a file was left at %s", path)
	}
}

// ── plans list ──

// The live catalogue has one row per plan per currency, and Enterprise's rate is 0 (custom pricing).
func TestPlansList_GroupsByCurrency_AndAZeroRateReadsCustom(t *testing.T) {
	live := `[{"id":"e1","name":"Enterprise","displayName":"Enterprise","description":"","hourlyRate":0,"currency":"USD","maxEnvironments":null},` +
		`{"id":"s1","name":"Starter","displayName":"Starter","description":"","hourlyRate":0.57,"currency":"AUD","maxEnvironments":5},` +
		`{"id":"s2","name":"Starter","displayName":"Starter","description":"","hourlyRate":0.4,"currency":"USD","maxEnvironments":5}]`
	srv, _ := routeStub(t, map[string]stubRoute{"GET /api/v1/subscription/plans": {200, live}})
	code, stdout, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "plans", "list")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	aud, usdStarter, ent := strings.Index(stdout, "AUD"), strings.Index(stdout, "0.40/hr"), strings.Index(stdout, "custom")
	if aud < 0 || usdStarter < 0 || ent < 0 || !(aud < usdStarter && usdStarter < ent) {
		t.Errorf("want AUD rows first, then USD cheapest-first with Enterprise (custom) last:\n%s", stdout)
	}
	if strings.Contains(stdout, "0.00") {
		t.Errorf("a 0 rate printed as a price:\n%s", stdout)
	}
}

func TestPlansList_Table_AndJson(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{"GET /api/v1/subscription/plans": {200, realPlans200}})
	code, stdout, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "plans", "list")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"Pay-As-You-Go", "0.56/hr", "Pro", "0.40/hr", "no cap", "USD"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table missing %q:\n%s", want, stdout)
		}
	}
	if (*seen)[0].path != "/api/v1/subscription/plans" {
		t.Errorf("path = %s", (*seen)[0].path)
	}
	code, stdout, _ = runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "-o", "json", "plans", "list")
	if code != 0 || !strings.Contains(stdout, `"displayName": "Pay-As-You-Go"`) || !strings.Contains(stdout, `"maxEnvironments": null`) {
		t.Errorf("json: exit %d, %s", code, stdout)
	}
}

// ── me billing cycle (added from the #715 parity sweep) ──

const realCycle200 = `{"companyId":"` + companyID15 + `","fromDate":"2026-09-09","toDate":"2026-10-09","totalRunningSeconds":9000,` +
	`"totalHibernatedSeconds":36000,"totalAmount":1.25,"totalRunningAmount":1.05,"totalHibernatedAmount":0.2,"currency":"AUD",` +
	`"plan":{"name":"free_trial","displayName":"Free Trial","hourlyRate":0,"storedHourlyRate":0,"includedSeconds":25200,` +
	`"includedStoredSeconds":360000,"consumedSeconds":9000,"isTrialing":true},"segments":[],"subscriptionMode":"Deferred"}`

func TestMeBillingCycle_Table_ShowsTotalsAndTrialProgress(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{
		"GET /api/v1/auth/me":                               {200, realMe200},
		"GET /api/v1/companies/" + companyID15 + "/billing": {200, realCycle200},
	})
	code, stdout, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "me", "billing", "cycle")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"2026-09-09 to 2026-10-09", "2.5h", "10.0h", "1.25 AUD", "Free Trial (trial)", "2.5h of 7.0h"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table missing %q:\n%s", want, stdout)
		}
	}
	if (*seen)[1].path != "/api/v1/companies/"+companyID15+"/billing" {
		t.Errorf("path = %s", (*seen)[1].path)
	}
}

func TestMeBillingCycle_Json_IsTheWholeApiResponse(t *testing.T) {
	srv, _ := routeStub(t, map[string]stubRoute{
		"GET /api/v1/auth/me":                               {200, realMe200},
		"GET /api/v1/companies/" + companyID15 + "/billing": {200, realCycle200},
	})
	code, stdout, _ := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "-o", "json", "me", "billing", "cycle")
	if code != 0 || !strings.Contains(stdout, `"subscriptionMode": "Deferred"`) || !strings.Contains(stdout, `"totalRunningAmount": 1.05`) {
		t.Errorf("exit %d: %s", code, stdout)
	}
}

func TestMeBillingCycle_BadDate_FailsWithoutCallingTheApi(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{"GET /api/v1/auth/me": {200, realMe200}})
	code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "me", "billing", "cycle", "--to", "yesterday")
	if code != 1 || !strings.Contains(out, `--to "yesterday" is not a date`) || len(*seen) != 0 {
		t.Errorf("exit %d, %d request(s), out %s", code, len(*seen), out)
	}
}

// ── me export latest (added from the #715 parity sweep) ──

const realExportReady = `{"id":"8c7b6a59-4837-4261-9150-1f2e3d4c5b6a","status":"ready","requestedAt":"2026-10-08T10:00:00Z",` +
	`"completedAt":"2026-10-08T10:02:11Z","expiresAt":"2026-10-15T10:02:11Z","downloadUrl":"DOWNLOAD","errorMessage":null}`

func TestMeExportLatest_Ready_ShowsIt_AndOutDownloadsIt_WithoutStartingANewOne(t *testing.T) {
	zip := []byte("PK\x03\x04 fake zip bytes")
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/me/export":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(strings.Replace(realExportReady, "DOWNLOAD", "http://"+r.Host+"/blob/export.zip", 1)))
		case "GET /blob/export.zip":
			_, _ = w.Write(zip)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	code, stdout, stderr := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "me", "export", "latest")
	if code != 0 || !strings.Contains(stdout, "ready") {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	path := filepath.Join(t.TempDir(), "export.zip")
	code, _, stderr = runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "me", "export", "latest", "--out", path)
	got, _ := os.ReadFile(path)
	if code != 0 || string(got) != string(zip) {
		t.Fatalf("exit %d, file %q: %s", code, got, stderr)
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "POST ") {
			t.Errorf("'latest' started a new export: %v", calls)
		}
	}
}

func TestMeExportLatest_NoExportYet_SaysSo_JsonIsNull(t *testing.T) {
	srv, _ := routeStub(t, map[string]stubRoute{"GET /api/v1/me/export": {204, ""}})
	code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "me", "export", "latest")
	if code != 0 || !strings.Contains(out, "No data export yet") {
		t.Errorf("exit %d: %s", code, out)
	}
	code, stdout, _ := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "-o", "json", "me", "export", "latest")
	if code != 0 || strings.TrimSpace(stdout) != "null" {
		t.Errorf("json: exit %d, %q", code, stdout)
	}
}

func TestMeExportLatest_OutOnANotReadyExport_Exit1(t *testing.T) {
	pending := strings.Replace(strings.Replace(realExportReady, `"ready"`, `"pending"`, 1), `"DOWNLOAD"`, `null`, 1)
	srv, _ := routeStub(t, map[string]stubRoute{"GET /api/v1/me/export": {200, pending}})
	code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "me", "export", "latest", "--out", filepath.Join(t.TempDir(), "x.zip"))
	if code != 1 || !strings.Contains(out, "cannot download - the latest export is pending") {
		t.Errorf("exit %d: %s", code, out)
	}
}

// Control: adding the 'latest' subcommand must not change what plain 'me export' does.
func TestMeExport_Plain_StillStartsAnExport(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{"POST /api/v1/me/export": {202, strings.Replace(realExportReady, `"ready"`, `"pending"`, 1)}})
	code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "me", "export")
	if code != 0 || len(*seen) != 1 || (*seen)[0].method != "POST" {
		t.Errorf("exit %d, requests %v: %s", code, *seen, out)
	}
}
