package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMeShow_RendersFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/me" || r.Method != http.MethodGet {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":                  "u-1",
			"email":               "consultant@example.com",
			"displayName":         "consultant",
			"platformRole":        "",
			"companyId":           "c-1",
			"companyName":         "consultant's Company",
			"azureRegion":         "westus2",
			"timeZone":            "",
			"status":              "active",
			"deletionScheduledAt": "",
		})
	}))
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "me", "show")
	if err != nil {
		t.Fatalf("me show: %v", err)
	}
	for _, want := range []string{"consultant@example.com", "active", "consultant's Company"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestMeShow_NotAuthenticated(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BCDOCK_CONFIG_DIR", dir)

	_, _, err := RunCmd(t, "--api-url", "http://example.invalid", "me", "show")
	if err == nil || !strings.Contains(err.Error(), "not authenticated") {
		t.Errorf("expected not-authenticated error, got %v", err)
	}
}

func TestMeExport_NoWait_ReturnsPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/me/export" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "exp-1",
			"status":      "pending",
			"requestedAt": "2026-05-05T00:00:00Z",
		})
	}))
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"me", "export", "-o", "json")
	if err != nil {
		t.Fatalf("me export: %v", err)
	}
	if !strings.Contains(out, `"status": "pending"`) || !strings.Contains(out, `"id": "exp-1"`) {
		t.Errorf("expected pending status in JSON output, got:\n%s", out)
	}
}

func TestMeExport_Wait_PollsUntilReady(t *testing.T) {
	var polls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/me/export" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "exp-2", "status": "pending"})
		case r.URL.Path == "/api/v1/me/export/exp-2" && r.Method == http.MethodGet:
			n := atomic.AddInt32(&polls, 1)
			status := "processing"
			if n >= 2 {
				status = "ready"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "exp-2",
				"status":      status,
				"downloadUrl": "https://example.invalid/blob.zip",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"me", "export", "--wait", "--wait-timeout", "5s", "-o", "json")
	if err != nil {
		t.Fatalf("me export --wait: %v", err)
	}
	if !strings.Contains(out, `"status": "ready"`) {
		t.Errorf("expected ready status, got:\n%s", out)
	}
	if atomic.LoadInt32(&polls) < 2 {
		t.Errorf("expected at least 2 polls, got %d", polls)
	}
}

func TestMeExport_Wait_TimesOutWith124(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/me/export" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "exp-3", "status": "pending"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "exp-3", "status": "processing"})
		}
	}))
	defer srv.Close()

	_, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"me", "export", "--wait", "--wait-timeout", "1s", "-o", "json")
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	type exitCoder interface{ ExitCode() int }
	ec, ok := err.(exitCoder)
	if !ok || ec.ExitCode() != 124 {
		t.Errorf("expected ExitCode 124, got err=%v", err)
	}
}

func TestMeExport_Out_RequiresWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "exp-4", "status": "pending"})
	}))
	defer srv.Close()

	_, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"me", "export", "--out", "/tmp/should-not-create.zip")
	if err == nil || !strings.Contains(err.Error(), "--out requires --wait") {
		t.Errorf("expected --out requires --wait error, got %v", err)
	}
}

func TestMeExport_Wait_OutDownloadsFile(t *testing.T) {
	// Stand up a separate "blob" server that serves the bytes for the SAS URL.
	const blobBody = "ZIP_BYTES_PLACEHOLDER"
	blob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = io.WriteString(w, blobBody)
	}))
	defer blob.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/me/export" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "exp-5", "status": "pending"})
		case r.URL.Path == "/api/v1/me/export/exp-5" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "exp-5", "status": "ready", "downloadUrl": blob.URL,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	dest := filepath.Join(t.TempDir(), "export.zip")
	_, _, err := RunCmd(t, "--api-url", api.URL, "--token", "bdk_test",
		"me", "export", "--wait", "--wait-timeout", "5s", "--out", dest, "-o", "json")
	if err != nil {
		t.Fatalf("me export --wait --out: %v", err)
	}
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(body) != blobBody {
		t.Errorf("downloaded body = %q, want %q", body, blobBody)
	}
}

func TestMeDelete_RequiresConfirm(t *testing.T) {
	_, _, err := RunCmd(t, "--api-url", "http://example.invalid", "--token", "bdk_test",
		"me", "delete")
	if err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Errorf("expected required --confirm error, got %v", err)
	}
}

func TestMeDelete_PostsConfirmAndPrintsSchedule(t *testing.T) {
	var gotConfirm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/me/delete" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Confirm string `json:"confirm"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotConfirm = body.Confirm
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"userId":               "u-1",
			"scheduledAnonymiseAt": "2026-06-04T00:00:00Z",
			"companiesToBeDeleted": 1,
		})
	}))
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"me", "delete", "--confirm", "user@example.com", "-o", "json")
	if err != nil {
		t.Fatalf("me delete: %v", err)
	}
	if gotConfirm != "user@example.com" {
		t.Errorf("server saw confirm=%q, want user@example.com", gotConfirm)
	}
	if !strings.Contains(out, "2026-06-04") {
		t.Errorf("expected schedule date in output:\n%s", out)
	}
}

func TestMeCancelDeletion_PostsAndPrintsResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/me/cancel-deletion" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"cancelled": true})
	}))
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"me", "cancel-deletion", "-o", "json")
	if err != nil {
		t.Fatalf("me cancel-deletion: %v", err)
	}
	if !strings.Contains(out, "true") {
		t.Errorf("expected cancelled=true in output, got:\n%s", out)
	}
}
