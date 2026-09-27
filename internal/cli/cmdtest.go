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
			// A slice flag's Set APPENDS once the flag has been set, so Set(DefValue) turned
			// `--status running` into [running [] running] and kept every earlier test's
			// values: `env wait` then matched a status from a previous test (found by CLI-019,
			// whose timeout case passed or failed by test order). Replace clears it.
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				_ = sv.Replace(nil)
			} else {
				_ = f.Value.Set(f.DefValue)
			}
			f.Changed = false
		}
	})
	for _, sub := range c.Commands() {
		ResetCmdFlags(sub)
	}
}
