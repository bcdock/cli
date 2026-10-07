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
	if !strings.Contains(stdout, "Welcome to BC[)OCK!") {
		t.Errorf("--help output does not contain welcome line; got:\n%s", stdout)
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
