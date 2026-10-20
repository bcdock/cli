package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const envCredsTestID = "11111111-2222-3333-4444-555555555555"

const (
	credsPassword = "P@ssw0rd!"
	credsWsKey    = "wsk-abc123"
)

// credsServer serves BOTH the detail read and the reveal, and counts each. The counts are
// the point: SEC-039's whole claim is about which call reads Key Vault and how often, so a
// test that only checked the output could not tell one reveal from three.
type credsServer struct {
	*httptest.Server
	detailCalls int
	revealCalls int
}

func newCredsServer(t *testing.T) *credsServer {
	t.Helper()
	cs := &credsServer{}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/environments/" + envCredsTestID:
			cs.detailCalls++
			// The detail response a SEC-039 server sends: username, no secrets.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             envCredsTestID,
				"shortId":        "11111111",
				"name":           "myenv-11111111",
				"displayName":    "myenv",
				"bcVersion":      "27.1.41698.0",
				"country":        "us",
				"location":       "westus2",
				"artifactType":   "Sandbox",
				"status":         "running",
				"devEndpointUrl": "https://myenv-11111111.dev.bcdock.io/BC/dev/",
				"username":       "admin",
				"multiTenant":    false,
				"createdAt":      "2026-05-01T10:00:00Z",
			})
		case "/api/v1/environments/" + envCredsTestID + "/credentials":
			cs.revealCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"environmentId":       envCredsTestID,
				"username":            "admin",
				"password":            credsPassword,
				"webServiceAccessKey": credsWsKey,
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	return cs
}

func TestEnvCredentials_Table_PrintsBothSecrets(t *testing.T) {
	srv := newCredsServer(t)
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "credentials", envCredsTestID)
	if err != nil {
		t.Fatalf("env credentials: %v", err)
	}

	for _, want := range []string{"USERNAME:", "admin", "PASSWORD:", credsPassword, "WS ACCESS KEY:", credsWsKey} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestEnvCredentials_JSON_CarriesTheRevealKeys(t *testing.T) {
	srv := newCredsServer(t)
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"-o", "json", "env", "credentials", envCredsTestID)
	if err != nil {
		t.Fatalf("env credentials -o json: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if got["password"] != credsPassword {
		t.Errorf("password: got %v, want %q", got["password"], credsPassword)
	}
	if got["webServiceAccessKey"] != credsWsKey {
		t.Errorf("webServiceAccessKey: got %v, want %q", got["webServiceAccessKey"], credsWsKey)
	}
}

// TestEnvCredentials_RevealsExactlyOncePerInvocation guards the property the audit trail
// depends on. Every reveal writes an audit row, so a verb that fetches per app - or twice
// for one command - turns a meaningful record into noise nobody reads.
func TestEnvCredentials_RevealsExactlyOncePerInvocation(t *testing.T) {
	srv := newCredsServer(t)
	defer srv.Close()

	if _, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "credentials", envCredsTestID); err != nil {
		t.Fatalf("env credentials: %v", err)
	}

	if srv.revealCalls != 1 {
		t.Errorf("reveal calls: got %d, want exactly 1", srv.revealCalls)
	}
}

// TestEnvGet_NeverReachesTheRevealEndpoint is the other half. `env get` must not fetch
// credentials at all - if it did, SEC-039 would have moved the ambient exposure rather than
// removed it, and every audit row would be one page load.
func TestEnvGet_NeverReachesTheRevealEndpoint(t *testing.T) {
	srv := newCredsServer(t)
	defer srv.Close()

	out, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "get", envCredsTestID)
	if err != nil {
		t.Fatalf("env get: %v", err)
	}

	if srv.revealCalls != 0 {
		t.Errorf("`env get` reached the reveal endpoint %d time(s); it must not reach it at all", srv.revealCalls)
	}
	if srv.detailCalls == 0 {
		t.Fatal("`env get` never called the detail endpoint - the counters are not wired to the path under test")
	}
	if !strings.Contains(out, "USERNAME:") {
		t.Errorf("expected the username to survive on `env get`:\n%s", out)
	}
}

// TestNoVerbAcceptsAPasswordFlag is the invariant SEC-039 asks for and that nothing tested
// before: a BC credential is never a flag or a positional argument anywhere in this CLI.
// A password passed on a command line lands in shell history and in the process list of
// every user on the box, which is a worse exposure than the one this ticket removes.
//
// It walks the whole command tree rather than checking the verbs that happen to be on our
// minds - the point is the ones nobody is thinking about.
func TestNoVerbAcceptsAPasswordFlag(t *testing.T) {
	forbidden := []string{"password", "passwd", "web-service-access-key", "wskey", "bc-password"}

	var checked int
	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		checked++
		c.Flags().VisitAll(func(f *pflag.Flag) {
			for _, bad := range forbidden {
				if f.Name == bad {
					t.Errorf("%s: flag --%s accepts a credential on the command line", path, f.Name)
				}
			}
		})
		// A positional named <password> is the same hazard wearing different clothes.
		if strings.Contains(strings.ToLower(c.Use), "<password>") {
			t.Errorf("%s: takes a password as a positional argument (%q)", path, c.Use)
		}
		for _, sub := range c.Commands() {
			walk(sub, path+" "+sub.Name())
		}
	}
	walk(RootCmd, RootCmd.Name())

	// Without this the test passes on an empty tree, which is the shape it would take if
	// RootCmd were ever built lazily and this ran before registration.
	if checked < 20 {
		t.Fatalf("walked only %d commands - the tree is not registered, so this proved nothing", checked)
	}
}
