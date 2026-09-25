package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const manifestEnvID = "88888888-9999-aaaa-bbbb-cccccccccccc"

// createHarness fakes the Platform API create + get, and the BC dev endpoint that
// manifest apps publish to.
//
// apiCalls counts EVERY request the CLI makes to the platform API. It exists for
// the test that matters most here: a manifest that names a missing .app must fail
// with NOTHING created, and "an error was returned" does not prove that - only a
// call count of zero does.
func createHarness(t *testing.T, publishHandler http.HandlerFunc) (apiURL string, apiCalls *int32, lastCreate *map[string]any) {
	t.Helper()
	var calls int32
	body := map[string]any{}
	manifestRevealCalls = 0

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/environments", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": manifestEnvID, "shortId": "88888888", "name": "mf-env", "status": "creating",
			})
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("/api/v1/environments/"+manifestEnvID, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": manifestEnvID, "shortId": "88888888", "name": "mf-env", "displayName": "mf-env",
			"bcVersion": "27.1.41600.0", "country": "au", "location": "westus2",
			"artifactType": "Sandbox", "status": "running",
			"webClientUrl":   "http://" + r.Host + "/BC/",
			"devEndpointUrl": "http://" + r.Host + "/BC-dev/",
			"username":  "admin",
			"createdAt": "2026-09-04T10:00:00Z",
		})
	})

	// SEC-039: the manifest publish path reveals credentials once per invocation, not once
	// per app. The detail response above carries no secrets any more - which is what made
	// this path visible as a fourth consumer at all.
	mux.HandleFunc("/api/v1/environments/"+manifestEnvID+"/credentials", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&manifestRevealCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"environmentId":       manifestEnvID,
			"username":            "admin",
			"password":            "P@ssw0rd!",
			"webServiceAccessKey": "wsk-mf",
		})
	})
	if publishHandler != nil {
		mux.HandleFunc("/BC-dev/dev/apps", publishHandler)
	}
	// Catalog endpoints validateEnvCreateInput consults. Returning a transport-level
	// failure would be wrong; returning nothing useful makes it skip validation,
	// which is its documented behaviour on a failed catalog fetch.
	mux.HandleFunc("/api/v1/public/regions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, &calls, &body
}

// manifestRevealCalls counts credential reveals across a run of createHarness. Package-level
// because the harness returns a fixed tuple that several tests already destructure.
var manifestRevealCalls int32

// THE TEST THE TICKET NAMED. A manifest can be structurally valid and still point
// at a .app that is not on disk. That must fail before anything is created - and
// the assertion is the CALL COUNT, not the error text, because an error string
// would also be produced by a create that succeeded and a publish that failed.
func TestEnvCreate_ManifestMissingApp_MakesZeroAPICalls(t *testing.T) {
	apiURL, calls, _ := createHarness(t, nil)

	path := writeManifest(t, `
schema: bcdock-env/v1
bcVersion: 27.1.41600.0
country: au
region: westus2
apps:
  - file: ./nope.app
`)

	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "create", "--manifest", path)
	if err == nil {
		t.Fatal("want an error for a manifest naming a missing .app")
	}
	if got := atomic.LoadInt32(calls); got != 0 {
		t.Fatalf("made %d API call(s); a bad manifest must create NOTHING", got)
	}
}

// Precedence, upper half: an explicitly passed flag beats the manifest.
func TestEnvCreate_ExplicitFlagBeatsManifest(t *testing.T) {
	apiURL, _, body := createHarness(t, nil)

	path := writeManifest(t, `
schema: bcdock-env/v1
bcVersion: 27.1.41600.0
country: au
region: westus2
`)

	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "create", "--manifest", path, "--version", "28.0.99999.0", "--wait-timeout", "1ms")
	// The create succeeds; the wait then times out against a harness that never
	// leaves "running" - irrelevant to what this asserts.
	_ = err
	if got := (*body)["version"]; got != "28.0.99999.0" {
		t.Fatalf("version = %v, want the explicit flag to beat the manifest", got)
	}
}

// Precedence, lower half, and the case Stella named: a flag the user never typed
// must NOT beat the manifest. --type defaults to "sandbox", so reading its VALUE
// would silently create a Sandbox from a manifest that says OnPrem.
func TestEnvCreate_ManifestBeatsUntypedFlagDefault(t *testing.T) {
	apiURL, _, body := createHarness(t, nil)

	path := writeManifest(t, `
schema: bcdock-env/v1
bcVersion: 27.1.41600.0
country: au
region: westus2
artifactType: OnPrem
multiTenant: false
`)

	_, _, _ = RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "create", "--manifest", path, "--wait-timeout", "1ms")

	if got := (*body)["imageType"]; got != "OnPrem" {
		t.Fatalf("imageType = %v, want OnPrem from the manifest - a --type nobody passed must not win", got)
	}
	if got := (*body)["multiTenant"]; got != false {
		t.Fatalf("multiTenant = %v, want false from the manifest - --multi-tenant defaults to true", got)
	}
}

// --manifest implies --wait: apps can only be published into a Running
// environment, so returning at "creating" would report success on an environment
// whose apps were never published.
func TestEnvCreate_ManifestImpliesWait(t *testing.T) {
	apiURL, _, _ := createHarness(t, nil)

	path := writeManifest(t, `
schema: bcdock-env/v1
bcVersion: 27.1.41600.0
country: au
region: westus2
`)

	_, stderr, _ := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "create", "--manifest", path, "--wait-timeout", "1ms")

	// The wait banner is only printed on the --wait path.
	if !strings.Contains(stderr, "Waiting for environment") {
		t.Fatalf("--manifest must imply --wait; stderr had no wait banner:\n%s", stderr)
	}
}

// Failure part-way through the app list: the earlier apps are reported published,
// the environment's shortId reaches the caller, and the exit is non-zero.
func TestEnvCreate_PublishFailsOnSecondApp_ReportsFirstAndKeepsEnv(t *testing.T) {
	var n int32
	apiURL, _, _ := createHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("app 2 is broken"))
	})

	path := writeManifest(t, `
schema: bcdock-env/v1
bcVersion: 27.1.41600.0
country: au
region: westus2
apps:
  - file: ./one.app
  - file: ./two.app
  - file: ./three.app
`, "one.app", "two.app", "three.app")

	stdout, stderr, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "create", "--manifest", path)

	if err == nil {
		t.Fatal("want a non-zero exit when an app fails to publish")
	}
	if !strings.Contains(stderr, "Published 1/3") {
		t.Fatalf("app 1 must be reported as published; stderr:\n%s", stderr)
	}
	if !strings.Contains(err.Error(), "2/3") {
		t.Fatalf("the error must name which app failed, got: %v", err)
	}
	// The environment is real, running and billable. Losing its handle in the
	// error path would be worse than the publish failure.
	if !strings.Contains(stdout, "88888888") {
		t.Fatalf("the env shortId must reach stdout even on publish failure; stdout:\n%s", stdout)
	}
	if got := atomic.LoadInt32(&n); got != 2 {
		t.Fatalf("publish attempts = %d, want 2 - the loop must STOP at the first failure, "+
			"because manifest order is dependency order", got)
	}
}

// Ravi, #280: --wait=false with --manifest is a contradiction. Silently winning
// the argument would publish nothing and exit 0 - the one field that was not
// asking Changed().
// SEC-039: ONE reveal for the whole manifest, however many apps it lists. Every reveal
// writes an audit row, so a per-app fetch would turn an auditable event into a count of
// apps - the record would still exist and would no longer mean anything. Three apps is the
// smallest case that can tell "once" from "once per app".
func TestEnvCreate_Manifest_RevealsCredentialsOncePerRunNotPerApp(t *testing.T) {
	apiURL, _, _ := createHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	path := writeManifest(t, `
schema: bcdock-env/v1
bcVersion: 27.1.41600.0
country: au
region: westus2
apps:
  - file: ./one.app
  - file: ./two.app
  - file: ./three.app
`, "one.app", "two.app", "three.app")

	_, stderr, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "create", "--manifest", path)
	if err != nil {
		t.Fatalf("env create --manifest: %v\n%s", err, stderr)
	}

	// The control: without this, a run that published nothing would also report one reveal
	// and the assertion below would be measuring an absence.
	if !strings.Contains(stderr, "Published 3/3") {
		t.Fatalf("all three apps must publish, or the reveal count means nothing:\n%s", stderr)
	}

	if got := atomic.LoadInt32(&manifestRevealCalls); got != 1 {
		t.Errorf("credential reveals: got %d for a 3-app manifest, want exactly 1", got)
	}
}

func TestEnvCreate_ExplicitWaitFalseWithManifest_IsAnError(t *testing.T) {
	apiURL, calls, _ := createHarness(t, nil)

	path := writeManifest(t, `
schema: bcdock-env/v1
bcVersion: 27.1.41600.0
country: au
region: westus2
`)

	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "create", "--manifest", path, "--wait=false")
	if err == nil {
		t.Fatal("want an error for --wait=false with --manifest")
	}
	if !strings.Contains(err.Error(), "--wait=false cannot be combined with --manifest") {
		t.Fatalf("error must name the contradiction, got: %v", err)
	}
	if got := atomic.LoadInt32(calls); got != 0 {
		t.Fatalf("made %d API call(s); the contradiction must be caught before anything is created", got)
	}
}

// The control: --wait=true with --manifest is redundant, not contradictory, and
// must still work. Without this the fix could reject the flag entirely.
func TestEnvCreate_ExplicitWaitTrueWithManifest_IsAccepted(t *testing.T) {
	apiURL, _, _ := createHarness(t, nil)

	path := writeManifest(t, `
schema: bcdock-env/v1
bcVersion: 27.1.41600.0
country: au
region: westus2
`)

	_, stderr, _ := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "create", "--manifest", path, "--wait=true", "--wait-timeout", "1ms")
	if !strings.Contains(stderr, "Waiting for environment") {
		t.Fatalf("--wait=true with --manifest must proceed; stderr:\n%s", stderr)
	}
}
