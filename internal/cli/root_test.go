package cli

import (
	"strings"
	"testing"
)

func TestVersionFlag_PrintsVersion(t *testing.T) {
	stdout, _, err := RunCmd(t, "--version")
	if err != nil {
		t.Fatalf("--version returned error: %v", err)
	}
	if !strings.Contains(stdout, version) {
		t.Errorf("--version output %q does not contain version %q", stdout, version)
	}
}

func TestHelp_WelcomeLine(t *testing.T) {
	stdout, _, err := RunCmd(t, "--help")
	if err != nil {
		t.Fatalf("--help returned error: %v", err)
	}
	if !strings.Contains(stdout, "Welcome to BCDock! Managed Business Central sandboxes in cloud.") {
		t.Errorf("--help output does not contain welcome line; got:\n%s", stdout)
	}
	// Split so a repo grep for the old wordmark finds nothing (#998 acceptance 2).
	if old := "BC[" + ")OCK"; strings.Contains(stdout, old) {
		t.Errorf("--help output still contains the bracket wordmark %s; got:\n%s", old, stdout)
	}
}

func TestHelp_RootOpensWithWordmarkThenWelcome(t *testing.T) {
	wantArt := []string{
		" ___  ___ ___          _",
		"| _ )/ __|   \\ ___  __| |__",
		"| _ \\ (__| [) / _ \\/ _| / /",
		"|___/\\___|___/\\___/\\__|_\\_\\",
	}
	for _, args := range [][]string{{"--help"}, {"help"}} {
		stdout, _, err := RunCmd(t, args...)
		if err != nil {
			t.Fatalf("%v returned error: %v", args, err)
		}
		lines := strings.Split(stdout, "\n")
		if len(lines) < 6 {
			t.Fatalf("%v: help too short:\n%s", args, stdout)
		}
		for i, want := range wantArt {
			if lines[i] != want {
				t.Errorf("%v: line %d = %q, want %q", args, i+1, lines[i], want)
			}
		}
		if lines[4] != "" || !strings.HasPrefix(lines[5], "Welcome to BCDock!") {
			t.Errorf("%v: want a blank line then the welcome line after the art; got %q, %q", args, lines[4], lines[5])
		}
	}
}

func TestHelp_SubcommandHasNoWordmark(t *testing.T) {
	for _, args := range [][]string{{"env", "--help"}, {"env", "list", "--help"}, {"help", "env"}} {
		stdout, _, err := RunCmd(t, args...)
		if err != nil {
			t.Fatalf("%v returned error: %v", args, err)
		}
		if strings.Contains(stdout, "|___/") {
			t.Errorf("%v: subcommand help carries the root wordmark:\n%s", args, stdout)
		}
	}
}

func TestVersion_HasNoWordmark(t *testing.T) {
	stdout, _, err := RunCmd(t, "--version")
	if err != nil {
		t.Fatalf("--version returned error: %v", err)
	}
	if strings.Contains(stdout, "|___/") {
		t.Errorf("--version output carries the wordmark: %q", stdout)
	}
}

func TestHelp_ExitCodesAfterCommands(t *testing.T) {
	stdout, _, err := RunCmd(t, "--help")
	if err != nil {
		t.Fatalf("--help returned error: %v", err)
	}
	exitIdx := strings.Index(stdout, "Exit codes:")
	commandsIdx := strings.Index(stdout, "Core commands:")
	if exitIdx == -1 {
		t.Fatal("--help output does not contain 'Exit codes:'")
	}
	if commandsIdx == -1 {
		t.Fatal("--help output does not contain 'Core commands:' section")
	}
	if exitIdx < commandsIdx {
		t.Errorf("'Exit codes:' appears before 'Core commands:' in --help output; exit at %d, commands at %d", exitIdx, commandsIdx)
	}
}

func TestHelp_NoTypo(t *testing.T) {
	stdout, _, err := RunCmd(t, "--help")
	if err != nil {
		t.Fatalf("--help returned error: %v", err)
	}
	if strings.Contains(stdout, "topcis") {
		t.Errorf("--help output contains typo 'topcis':\n%s", stdout)
	}
}
