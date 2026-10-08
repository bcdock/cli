package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bcdock/cli/internal/client"
)

func TestProbeVersionSkew_WarnsWhenCLIIsOlder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"api":"1.5.0","minimumCliVersion":"0.9.0"}`))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	c := client.New(srv.URL, "", time.Second)
	probeVersionSkew(context.Background(), c, "0.5.0", &buf)

	if !strings.Contains(buf.String(), "0.9.0") {
		t.Fatalf("expected stderr warning naming 0.9.0, got %q", buf.String())
	}
}

func TestProbeVersionSkew_SilentWhenCLIIsAtLeastMinimum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"api":"1.5.0","minimumCliVersion":"0.9.0"}`))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	c := client.New(srv.URL, "", time.Second)
	probeVersionSkew(context.Background(), c, "0.9.0", &buf)
	if buf.Len() != 0 {
		t.Fatalf("expected no warning when CLI matches minimum, got %q", buf.String())
	}

	probeVersionSkew(context.Background(), c, "1.2.0", &buf)
	if buf.Len() != 0 {
		t.Fatalf("expected no warning when CLI exceeds minimum, got %q", buf.String())
	}
}

func TestProbeVersionSkew_SilentForDevBuild(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("dev build should not contact /api/version")
	}))
	defer srv.Close()

	var buf bytes.Buffer
	c := client.New(srv.URL, "", time.Second)
	probeVersionSkew(context.Background(), c, "dev", &buf)
	if buf.Len() != 0 {
		t.Fatalf("expected no warning for dev build, got %q", buf.String())
	}
}

func TestProbeVersionSkew_SilentWhenServerHasNoEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	c := client.New(srv.URL, "", time.Second)
	probeVersionSkew(context.Background(), c, "0.5.0", &buf)
	if buf.Len() != 0 {
		t.Fatalf("expected no warning when server lacks /api/version, got %q", buf.String())
	}
}

func TestProbeVersionSkew_SilentWhenVersionUnparseable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"api":"1.5.0","minimumCliVersion":"not-semver"}`))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	c := client.New(srv.URL, "", time.Second)
	probeVersionSkew(context.Background(), c, "0.5.0", &buf)
	if buf.Len() != 0 {
		t.Fatalf("expected no warning when minimum isn't semver, got %q", buf.String())
	}
}

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"0.5.0", "0.9.0", -1, true},
		{"0.9.0", "0.9.0", 0, true},
		{"1.0.0", "0.9.0", 1, true},
		{"v0.5.0", "0.9.0", -1, true},
		{"0.5.0-rc1", "0.5.0", 0, true}, // pre-release stripped
		{"abc", "0.9.0", 0, false},
		{"0.9", "0.9.0", 0, false},
	}
	for _, tc := range cases {
		got, ok := compareSemver(tc.a, tc.b)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("compareSemver(%q, %q) = (%d, %v); want (%d, %v)",
				tc.a, tc.b, got, ok, tc.want, tc.ok)
		}
	}
}
