package cli

import (
	"strings"
	"testing"
)

// #650. A --wait that settles in a terminal status it did not ask for, and that is not a failure
// status, says why when the environment carries an errorMessage. The case it exists for: a
// failed hibernate is rolled back (#454/#649), so the env settles back in running with
// "Hibernation failed: <why>; your environment is still running" - the API's own wording.
const rolledBackHibernate = "Hibernation failed: snapshot upload timed out; your environment is still running"

func TestEnvHibernateWait_RolledBackToRunning_PrintsTheReason_ExitsOne(t *testing.T) {
	srv := failStub(t, "running", rolledBackHibernate)
	code, out, took := runExit(t, "--api-url", srv.URL, "--token", "bdk_test", "env", "hibernate", failEnvID, "--wait")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; output: %s", code, out)
	}
	if !strings.Contains(out, rolledBackHibernate+" (status: running)") {
		t.Errorf("the reason must be printed with the status; output: %s", out)
	}
	if took > withinOneInterval {
		t.Errorf("took %s: it should stop at the first poll", took)
	}
}

// With no errorMessage the text is exactly today's.
func TestEnvHibernateWait_RunningWithoutMessage_KeepsTodaysText(t *testing.T) {
	srv := failStub(t, "running", "")
	code, out, _ := runExit(t, "--api-url", srv.URL, "--token", "bdk_test", "env", "hibernate", failEnvID, "--wait")
	if code != 1 || !strings.Contains(out, "hibernate failed (status: running)") {
		t.Errorf("want exit 1 with today's text; code %d, output: %s", code, out)
	}
}

// env wait for hibernated: the same rollback is still a 124 (it never reached what was asked),
// now with the reason on the end.
func TestEnvWait_SettledElsewhere_AppendsTheReason_Still124(t *testing.T) {
	srv := failStub(t, "running", rolledBackHibernate)
	code, out, _ := runExit(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "wait", failEnvID, "--status", "hibernated", "--timeout", "30s")
	if code != 124 {
		t.Fatalf("exit = %d, want 124; output: %s", code, out)
	}
	want := `environment reached terminal status "running" without matching any of [hibernated]: ` + rolledBackHibernate
	if !strings.Contains(out, want) {
		t.Errorf("want %q in output: %s", want, out)
	}
}

func TestEnvWait_SettledElsewhere_NoMessage_KeepsTodaysText(t *testing.T) {
	srv := failStub(t, "running", "")
	code, out, _ := runExit(t, "--api-url", srv.URL, "--token", "bdk_test",
		"env", "wait", failEnvID, "--status", "hibernated", "--timeout", "30s")
	want := `environment reached terminal status "running" without matching any of [hibernated]`
	if code != 124 || !strings.Contains(out, want) || strings.Contains(out, want+":") {
		t.Errorf("want exit 124 with today's text and nothing appended; code %d, output: %s", code, out)
	}
}
