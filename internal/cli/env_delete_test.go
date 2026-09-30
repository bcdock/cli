package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// #595. `env delete --wait` polls the environment after the DELETE is accepted, and the API
// answers a soft-deleted environment's by-id read with 404 (tombstones are excluded). That 404
// is the success the wait exists for: exit 0. A wrong id or name must still fail with 5, and
// that failure happens BEFORE the wait - at the DELETE, or at name resolution.

// deleteStub serves DELETE with the real Accepted, then answers each GET in turn from gets;
// the last entry repeats. It records how many DELETEs arrived.
type deleteStub struct {
	deleteStatus int
	deleteBody   string
	gets         []stubResp
	getCalls     atomic.Int32
	deleteCalls  atomic.Int32
}

type stubResp struct {
	status int
	body   string
}

func (s *deleteStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/environments/"+envGetTestID:
			s.deleteCalls.Add(1)
			w.WriteHeader(s.deleteStatus)
			_, _ = w.Write([]byte(s.deleteBody))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/environments":
			_, _ = w.Write([]byte(`[]`)) // B-style list: no environment by that name
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/environments/"+envGetTestID:
			n := int(s.getCalls.Add(1)) - 1
			if n >= len(s.gets) {
				n = len(s.gets) - 1
			}
			w.WriteHeader(s.gets[n].status)
			_, _ = w.Write([]byte(s.gets[n].body))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func deleteExit(t *testing.T, srv *httptest.Server, target string) (int, string, error) {
	t.Helper()
	_, stderr, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "delete", target, "--force", "--wait", "--wait-timeout", "30s")
	if err == nil {
		return 0, stderr, nil
	}
	var b bytes.Buffer
	return exitCodeForFormat(err, &b, flagOutput), stderr, err
}

const deletingBody = `{"id":"` + envGetTestID + `","name":"t","status":"deleting","provisioningProgressPercent":100}`

// The path that produced the bug: the first poll after an accepted delete already 404s.
func TestEnvDeleteWait_404AfterAcceptedDelete_ExitsZero(t *testing.T) {
	s := &deleteStub{deleteStatus: http.StatusAccepted, gets: []stubResp{{http.StatusNotFound, realEnv404TypedBody}}}
	code, stderr, err := deleteExit(t, s.server(t), envGetTestID)
	if code != 0 {
		t.Fatalf("exit = %d (%v), want 0: the environment is gone, which is what --wait waited for", code, err)
	}
	if !strings.Contains(stderr, "Deleted.") {
		t.Errorf("want the usual Deleted. line, stderr = %q", stderr)
	}
	if s.deleteCalls.Load() != 1 {
		t.Errorf("DELETE calls = %d, want 1", s.deleteCalls.Load())
	}
}

// The prod shape: the wait sees `deleting` first, then the tombstone's 404 on a later poll.
func TestEnvDeleteWait_DeletingThen404_ExitsZero(t *testing.T) {
	if testing.Short() {
		t.Skip("waits one 3s poll interval")
	}
	s := &deleteStub{deleteStatus: http.StatusAccepted, gets: []stubResp{
		{http.StatusOK, deletingBody},
		{http.StatusNotFound, realEnv404Body}, // an API without #570's typed body 404s the same way
	}}
	code, _, err := deleteExit(t, s.server(t), envGetTestID)
	if code != 0 {
		t.Fatalf("exit = %d (%v), want 0", code, err)
	}
	if s.getCalls.Load() < 2 {
		t.Errorf("GET calls = %d, want the deleting poll and the 404 poll", s.getCalls.Load())
	}
}

// Control: an unknown id fails at the DELETE itself, before any wait, and stays exit 5.
func TestEnvDeleteWait_UnknownID_Exits5(t *testing.T) {
	s := &deleteStub{deleteStatus: http.StatusNotFound, deleteBody: realEnv404TypedBody,
		gets: []stubResp{{http.StatusNotFound, realEnv404TypedBody}}}
	code, _, _ := deleteExit(t, s.server(t), envGetTestID)
	if code != 5 {
		t.Fatalf("exit = %d, want 5 for an environment that never existed", code)
	}
	if s.getCalls.Load() != 0 {
		t.Errorf("GET calls = %d, want 0: a refused DELETE must not start the wait", s.getCalls.Load())
	}
}

// Control: an unknown NAME fails at resolution (the list has no match), still exit 5.
func TestEnvDeleteWait_UnknownName_Exits5(t *testing.T) {
	s := &deleteStub{deleteStatus: http.StatusAccepted, gets: []stubResp{{http.StatusOK, deletingBody}}}
	code, _, _ := deleteExit(t, s.server(t), "no-such-env")
	if code != 5 {
		t.Fatalf("exit = %d, want 5 for a name that resolves to nothing", code)
	}
	if s.deleteCalls.Load() != 0 {
		t.Errorf("DELETE calls = %d, want 0", s.deleteCalls.Load())
	}
}

// Only a 404 means gone: any other error during the wait still fails the command.
func TestEnvDeleteWait_ServerErrorDuringWait_StillFails(t *testing.T) {
	s := &deleteStub{deleteStatus: http.StatusAccepted, gets: []stubResp{{http.StatusInternalServerError, `{"error":"boom"}`}}}
	code, _, err := deleteExit(t, s.server(t), envGetTestID)
	if code == 0 || err == nil {
		t.Fatalf("exit = %d, want a failure: a 500 during the wait is not a completed delete", code)
	}
}
