package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// RunCmd executes RootCmd with the given args and returns stdout/stderr/error.
// Resets persistent flag state and local flag-changed bits between invocations
// so tests don't pollute each other. Exported so the admin test package can
// drive the same RootCmd tree (after admin.Register has wired admin verbs).
//
// Lives in a non-test file so it's importable from sibling packages' tests;
// dead-code elimination keeps it out of production binaries.
func RunCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	flagOutput = "table"
	flagToken = ""
	flagAPIURL = ""
	flagQuiet = false
	flagNoColor = false
	flagTimeout = 30 * time.Second

	ResetCmdFlags(RootCmd)

	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}

	RootCmd.SetOut(outBuf)
	RootCmd.SetErr(errBuf)
	RootCmd.SetArgs(args)

	err = RootCmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// ResetCmdFlags resets all local flags on a command and its subcommands to
// their default values, clearing the Changed bit so subsequent runs see them
// as untouched.
func ResetCmdFlags(c *cobra.Command) {
	c.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Changed {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		}
	})
	for _, sub := range c.Commands() {
		ResetCmdFlags(sub)
	}
}
