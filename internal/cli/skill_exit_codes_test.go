package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// #685: skills/bcdock/SKILL.md tells agents which exit code a timed-out wait gives. These tests pin
// the skill's sentences AND run each case, so the text cannot drift from what the CLI does: 124
// only from `env wait` and `me export --wait` (timeoutError); every other --wait timeout exits 1.

func skillText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../skills/bcdock/SKILL.md")
	if err != nil {
		t.Fatalf("read the skill: %v", err)
	}
	return strings.Join(strings.Fields(string(b)), " ") // sentences wrap across lines
}

func TestSkill_StatesTheExitCodesTheCLIGives(t *testing.T) {
	s := skillText(t)
	for _, want := range []string{
		"a `--wait` that times out on create, resume, hibernate or delete",
		"`124` comes only when `bcdock env wait` or `bcdock me export --wait` gives up waiting",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the skill no longer says %q", want)
		}
	}
	// #680 item 10: not every portal action has a command, so the skill must not claim parity.
	if strings.Contains(s, "anything the portal can do") {
		t.Error("the skill claims portal parity, which #680 removed as untrue")
	}
}

func TestSkill_WaitTimeouts_OnCreateResumeHibernateDelete_Exit1(t *testing.T) {
	cases := map[string][]string{
		"hibernating":  {"env", "hibernate", failEnvID, "--wait", "--wait-timeout", "1ms"},
		"resuming":     {"env", "resume", failEnvID, "--wait", "--wait-timeout", "1ms"},
		"deleting":     {"env", "delete", failEnvID, "--wait", "--wait-timeout", "1ms", "--force"},
		"provisioning": {"env", "create", "--version", "27.1.41600.0", "--country", "au", "--region", "westus2", "--wait", "--wait-timeout", "1ms"},
	}
	for status, args := range cases {
		srv := failStub(t, status, "")
		code, out, _ := runExit(t, append([]string{"--api-url", srv.URL, "--token", "bdk_test"}, args...)...)
		if code != 1 || !strings.Contains(out, "timed out") {
			t.Errorf("%s (%s): exit %d, want 1 with a timeout message; output: %s", args[1], status, code, out)
		}
	}
}

func TestSkill_EnvWaitTimeout_Exit124(t *testing.T) {
	srv := failStub(t, "starting", "")
	code, out, _ := runExit(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "wait", failEnvID, "--status", "running", "--timeout", "1ms")
	if code != 124 {
		t.Errorf("env wait timeout: exit %d, want 124; output: %s", code, out)
	}
}

func TestSkill_MeExportWaitTimeout_Exit124(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "exp-685", "status": "pending"})
	}))
	t.Cleanup(srv.Close)
	code, out, _ := runExit(t, "--api-url", srv.URL, "--token", "bdk_test", "me", "export", "--wait", "--wait-timeout", "1ms")
	if code != 124 {
		t.Errorf("me export --wait timeout: exit %d, want 124; output: %s", code, out)
	}
}
