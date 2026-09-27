package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeEnvServer answers GET /api/environments and GET /api/environments/{id}.
// It cycles through `statuses` on successive GETs to /api/environments/{id} so a single
// run can simulate a queued → running transition without sleeps.
type fakeEnvServer struct {
	t        *testing.T
	statuses []string
	envID    string
	envName  string
	calls    atomic.Int32
	mu       sync.Mutex
}

func (f *fakeEnvServer) currentStatus() string {
	idx := int(f.calls.Add(1)) - 1
	if idx >= len(f.statuses) {
		idx = len(f.statuses) - 1
	}
	return f.statuses[idx]
}

func (f *fakeEnvServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/environments" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": f.envID, "shortId": f.envID[:8], "name": f.envName, "displayName": f.envName, "status": f.statuses[0]},
			})
		case strings.HasPrefix(r.URL.Path, "/api/v1/environments/") && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      f.envID,
				"shortId": f.envID[:8],
				"name":    f.envName,
				"status":  f.currentStatus(),
			})
		default:
			w.WriteHeader(404)
		}
	}
}

func TestEnvWait_RequiresStatusFlag(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	_, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "env", "wait", "any-env")
	if err == nil {
		t.Fatal("expected error when --status is missing")
	}
	if !strings.Contains(err.Error(), "--status") {
		t.Errorf("error should mention --status: %v", err)
	}
}

func TestEnvWait_MatchingStatus_ReturnsNil(t *testing.T) {
	fake := &fakeEnvServer{
		t:        t,
		envID:    "11111111-1111-1111-1111-111111111111",
		envName:  "cli-test-env",
		statuses: []string{"running"},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	_, _, err := RunCmd(t,
		"--api-url", srv.URL, "--token", "bdk_test",
		"env", "wait", fake.envName,
		"--status", "running",
		"--timeout", "5s")
	if err != nil {
		t.Fatalf("expected nil error when env is already running, got: %v", err)
	}
}

func TestEnvWait_FailedWhenLookingForRunning_ExitsTimeout(t *testing.T) {
	fake := &fakeEnvServer{
		t:        t,
		envID:    "22222222-2222-2222-2222-222222222222",
		envName:  "cli-failed-env",
		statuses: []string{"failed"},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	_, _, err := RunCmd(t,
		"--api-url", srv.URL, "--token", "bdk_test",
		"env", "wait", fake.envName,
		"--status", "running",
		"--timeout", "5s")
	if err == nil {
		t.Fatal("expected timeout error when env reached terminal status that wasn't requested")
	}
	if got := exitCodeFor(err, &noopWriter{}); got != 124 {
		t.Errorf("exit code = %d, want 124", got)
	}
}

func TestEnvWait_AnyOfMultipleStatuses_ReturnsNil(t *testing.T) {
	fake := &fakeEnvServer{
		t:        t,
		envID:    "33333333-3333-3333-3333-333333333333",
		envName:  "cli-multi-env",
		statuses: []string{"failed"},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	_, _, err := RunCmd(t,
		"--api-url", srv.URL, "--token", "bdk_test",
		"env", "wait", fake.envName,
		"--status", "running", "--status", "failed",
		"--timeout", "5s")
	if err != nil {
		t.Fatalf("either status should match, got: %v", err)
	}
}

type noopWriter struct{}

func (*noopWriter) Write(p []byte) (int, error) { return len(p), nil }
