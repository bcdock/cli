package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestCobraHelpCoverage walks RootCmd and asserts every non-hidden command has
// non-empty Long and non-empty Example. This is the drift guard: future commands
// added without docs will fail this test.
func TestCobraHelpCoverage(t *testing.T) {
	t.Parallel()
	missing := walkCommands(RootCmd, func(cmd *cobra.Command) (name, problem string, fail bool) {
		if strings.TrimSpace(cmd.Long) == "" {
			return cmd.CommandPath(), "missing Long", true
		}
		if strings.TrimSpace(cmd.Example) == "" {
			return cmd.CommandPath(), "missing Example", true
		}
		return "", "", false
	})
	for _, m := range missing {
		t.Errorf("%s: %s", m[0], m[1])
	}
}

// TestExitCodeKeyPresent asserts every non-hidden command's Long contains the
// literal "Exit codes:" substring. Commands must document their exit codes.
// Root is exempted: its exit codes live in the help template (verified by TestHelp_ExitCodesAfterCommands).
func TestExitCodeKeyPresent(t *testing.T) {
	t.Parallel()
	missing := walkCommands(RootCmd, func(cmd *cobra.Command) (name, problem string, fail bool) {
		if !cmd.HasParent() {
			return "", "", false
		}
		if !strings.Contains(cmd.Long, "Exit codes:") {
			return cmd.CommandPath(), "Long does not contain 'Exit codes:'", true
		}
		return "", "", false
	})
	for _, m := range missing {
		t.Errorf("%s: %s", m[0], m[1])
	}
}

// walkCommands recursively visits all non-hidden, non-builtin commands under
// root (including root itself) and collects results from check().
//
// "Builtin" means cobra's auto-generated commands (completion, help) that we
// don't author and therefore cannot annotate with Exit codes / Example.
func walkCommands(root *cobra.Command, check func(*cobra.Command) (name, problem string, fail bool)) [][2]string {
	var results [][2]string

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Hidden {
			return
		}
		if isCobraBuiltin(cmd) {
			return
		}
		name, problem, fail := check(cmd)
		if fail {
			results = append(results, [2]string{name, problem})
		}
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return results
}

// isCobraBuiltin returns true for commands injected by cobra itself that we
// don't write (completion and its subcommands, help).
func isCobraBuiltin(cmd *cobra.Command) bool {
	name := cmd.Name()
	if name == "help" || name == "completion" {
		return true
	}
	// Sub-commands of completion: bash, fish, powershell, zsh
	if cmd.HasParent() && cmd.Parent().Name() == "completion" {
		return true
	}
	return false
}
