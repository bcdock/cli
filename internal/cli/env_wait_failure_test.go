package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// #621. The API's failure status is "error" (EnvironmentStatuses.Error), plus "failed-debug".
// pollEnv treated only "failed" as terminal - a status the API never sends - so a failed
// create or resume looked like a 30-minute hang and never showed why. Every command that
// waits now stops on a failure status at the first poll, prints the environment's
// errorMessage, and exits 1 (the exit-codes page: a provisioning failure exits 1).

const failEnvID = "66666666-7777-8888-9999-000000000000"

// failStub serves create, resume, hibernate and delete as accepted, and answers every GET of
// the environment with the given status and message.
func failStub(t *testing.T, status, message string) *httptest.Server {
	t.Helper()
	env := map[string]any{
		"id": failEnvID, "shortId": "66666666", "name": "fail-env", "status": status,
		"bcVersion": "27.1.41600.0", "country": "au", "location": "westus2",
		"createdAt": "2026-09-30T00:00:00Z",
	}
	if message != "" {
		env["errorMessage"] = message
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/environments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": failEnvID, "shortId": "66666666", "name": "fail-env", "status": "queued"})
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/api/v1/environments/"+failEnvID, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(env)
	})
	for _, action := range []string{"resume", "hibernate"} {
		mux.HandleFunc("/api/v1/environments/"+failEnvID+"/"+action, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// runExit runs a command and returns its exit code, the stderr the user sees, and the elapsed time.
func runExit(t *testing.T, args ...string) (int, string, time.Duration) {
	t.Helper()
	start := time.Now()
	_, stderr, err := RunCmd(t, args...)
	elapsed := time.Since(start)
	var b bytes.Buffer
	code := exitCodeForFormat(err, &b, flagOutput)
	return code, stderr + b.String(), elapsed
}

// The poll ticks every 3s. Stopping on the FIRST poll is the claim, so anything near a tick
// means the loop went round again (and a hang is 30 minutes).
const withinOneInterval = 2500 * time.Millisecond

func TestEnvCreateWait_StatusError_ExitsOneWithTheMessage_AtTheFirstPoll(t *testing.T) {
	srv := failStub(t, "error", "Artifact download failed: BC 27.1 us is no longer published")
	code, out, took := runExit(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "create", "--version", "27.1.41600.0", "--country", "au", "--region", "westus2", "--wait")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; output: %s", code, out)
	}
	if !strings.Contains(out, "Artifact download failed: BC 27.1 us is no longer published") {
		t.Errorf("the environment's errorMessage must be printed; output: %s", out)
	}
	if !strings.Contains(out, "status: error") {
		t.Errorf("the failure status must be named; output: %s", out)
	}
	if took > withinOneInterval {
		t.Errorf("took %s: it polled again instead of stopping on the first 'error'", took)
	}
}

func TestEnvCreateWait_StatusError_JSON_CarriesMessageAndExitCode(t *testing.T) {
	srv := failStub(t, "error", "pool out of disk")
	code, out, _ := runExit(t, "--api-url", srv.URL, "--token", "bdk_test", "-o", "json",
		"env", "create", "--version", "27.1.41600.0", "--country", "au", "--region", "westus2", "--wait")
	if code != 1 || !strings.Contains(out, `"exitCode":1`) || !strings.Contains(out, "pool out of disk") {
		t.Errorf("want a JSON error with exitCode 1 and the message; code %d, output: %s", code, out)
	}
}

func TestEnvCreateWait_StatusError_NoMessage_UsesTheFallback(t *testing.T) {
	srv := failStub(t, "error", "")
	code, out, _ := runExit(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "create", "--version", "27.1.41600.0", "--country", "au", "--region", "westus2", "--wait")
	if code != 1 || !strings.Contains(out, "provisioning failed (status: error)") {
		t.Errorf("want exit 1 with the fallback message; code %d, output: %s", code, out)
	}
}

func TestEnvResumeWait_FailedDebug_ExitsOneWithTheMessage(t *testing.T) {
	srv := failStub(t, "failed-debug", "restore from blob failed")
	code, out, took := runExit(t, "--api-url", srv.URL, "--token", "bdk_test", "env", "resume", failEnvID, "--wait")
	if code != 1 || !strings.Contains(out, "restore from blob failed") || !strings.Contains(out, "status: failed-debug") {
		t.Errorf("want exit 1 with the message and status; code %d, output: %s", code, out)
	}
	if took > withinOneInterval {
		t.Errorf("took %s: failed-debug must stop the wait at the first poll", took)
	}
}

// Making "error" terminal changes every caller. A delete whose wait now stops on "error"
// must not fall through to exit 0 - that would report a failed delete as a success.
func TestEnvDeleteWait_StatusError_IsAFailureNotASuccess(t *testing.T) {
	srv := failStub(t, "error", "could not remove the container")
	code, out, _ := runExit(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "delete", failEnvID, "--force", "--wait", "--wait-timeout", "30s")
	if code != 1 || !strings.Contains(out, "could not remove the container") {
		t.Errorf("a delete that ended in error must exit 1 with the reason; code %d, output: %s", code, out)
	}
	if strings.Contains(out, "Deleted.") {
		t.Errorf("printed Deleted. for an environment in error: %s", out)
	}
}

func TestEnvHibernateWait_StatusError_PrintsTheMessage(t *testing.T) {
	srv := failStub(t, "error", "snapshot upload failed")
	code, out, _ := runExit(t, "--api-url", srv.URL, "--token", "bdk_test", "env", "hibernate", failEnvID, "--wait")
	if code != 1 || !strings.Contains(out, "snapshot upload failed") {
		t.Errorf("want exit 1 with the message; code %d, output: %s", code, out)
	}
}

// env wait: a failure you did not ask for is a failure (exit 1 + reason), not a 124 timeout.
func TestEnvWait_ErrorNotRequested_ExitsOneNot124(t *testing.T) {
	srv := failStub(t, "error", "image not built for 27.1 us")
	code, out, took := runExit(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "wait", failEnvID, "--status", "running", "--timeout", "30s")
	if code != 1 || !strings.Contains(out, "image not built for 27.1 us") {
		t.Errorf("want exit 1 with the message (not 124); code %d, output: %s", code, out)
	}
	if took > withinOneInterval {
		t.Errorf("took %s: it should stop at the first poll", took)
	}
}

// ...and when the caller asked for "error", reaching it is the success they waited for.
func TestEnvWait_ErrorRequested_ExitsZero(t *testing.T) {
	srv := failStub(t, "error", "anything")
	code, out, _ := runExit(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "wait", failEnvID, "--status", "running", "--status", "error", "--timeout", "30s")
	if code != 0 {
		t.Errorf("--status error was requested and reached: want 0, got %d: %s", code, out)
	}
}

// An older server that still says "failed" keeps working.
func TestEnvCreateWait_LegacyFailed_StillStopsAndPrints(t *testing.T) {
	srv := failStub(t, "failed", "legacy failure")
	code, out, _ := runExit(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "create", "--version", "27.1.41600.0", "--country", "au", "--region", "westus2", "--wait")
	if code != 1 || !strings.Contains(out, "legacy failure") {
		t.Errorf("want exit 1 with the message; code %d, output: %s", code, out)
	}
}
