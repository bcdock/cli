package cli

import (
	"strings"
	"testing"

	"github.com/bcdock/cli/internal/config"
)

// #487: `auth set-token` with no argument reads the key from stdin, so it never lands in shell
// history or the process list. The positional argument still works, with a warning.

func runSetToken(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Setenv("BCDOCK_CONFIG_DIR", t.TempDir())
	RootCmd.SetIn(strings.NewReader(stdin))
	t.Cleanup(func() { RootCmd.SetIn(nil) })
	return RunCmd(t, append([]string{"auth", "set-token"}, args...)...)
}

func storedToken(t *testing.T) string {
	t.Helper()
	creds, err := config.LoadCredentials()
	if err != nil || creds == nil {
		return ""
	}
	return creds.Token
}

func TestAuthSetToken_FromStdin_SavesTheKey_NoWarning_NeverEchoed(t *testing.T) {
	const key = "bdk_stdinkey487"
	stdout, stderr, err := runSetToken(t, key+"\n")
	if err != nil {
		t.Fatalf("set-token from stdin: %v", err)
	}
	if got := storedToken(t); got != key {
		t.Errorf("stored token = %q, want %q", got, key)
	}
	if strings.Contains(stderr, "warning") {
		t.Errorf("stdin is the recommended form - no warning expected: %q", stderr)
	}
	if strings.Contains(stdout+stderr, key) {
		t.Errorf("the key must not be echoed: stdout %q stderr %q", stdout, stderr)
	}
}

func TestAuthSetToken_FromStdin_CRLFAndSpaces_Trimmed(t *testing.T) {
	if _, _, err := runSetToken(t, "  bdk_crlfkey487\r\n"); err != nil {
		t.Fatalf("set-token: %v", err)
	}
	if got := storedToken(t); got != "bdk_crlfkey487" {
		t.Errorf("stored token = %q, want bdk_crlfkey487", got)
	}
}

func TestAuthSetToken_EmptyStdin_ErrorsAndStoresNothing(t *testing.T) {
	_, _, err := runSetToken(t, " \n")
	if err == nil || !strings.Contains(err.Error(), "no API key on stdin") {
		t.Fatalf("want 'no API key on stdin', got %v", err)
	}
	if got := storedToken(t); got != "" {
		t.Errorf("nothing should be stored, got %q", got)
	}
}

func TestAuthSetToken_TwoKeysOnStdin_Refused(t *testing.T) {
	_, _, err := runSetToken(t, "bdk_one\nbdk_two\n")
	if err == nil || !strings.Contains(err.Error(), "expected one API key") {
		t.Fatalf("want 'expected one API key', got %v", err)
	}
	if got := storedToken(t); got != "" {
		t.Errorf("nothing should be stored, got %q", got)
	}
}

func TestAuthSetToken_PositionalArgument_StillWorks_WithAWarning(t *testing.T) {
	const key = "bdk_argkey487"
	_, stderr, err := runSetToken(t, "", key)
	if err != nil {
		t.Fatalf("set-token <key>: %v", err)
	}
	if got := storedToken(t); got != key {
		t.Errorf("stored token = %q, want %q", got, key)
	}
	if !strings.Contains(stderr, "warning: a key passed as an argument is saved in your shell history") {
		t.Errorf("want the shell-history warning, stderr: %q", stderr)
	}
}

func TestAuthSetToken_TwoArguments_Refused(t *testing.T) {
	if _, _, err := runSetToken(t, "", "bdk_a", "bdk_b"); err == nil {
		t.Fatal("two positional keys must be refused")
	}
}
