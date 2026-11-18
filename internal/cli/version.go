package cli

import (
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:     "version",
	GroupID: "utility",
	Short:   "Print the bcdock CLI version",
	Long: `Print the bcdock CLI version, the git commit it was built from and that
commit's date, and the API major version. A build made without the release
stamps (a plain 'go build') prints "unknown" for the commit and its date.

This command never checks auth or calls the API. Use it to confirm which
CLI build is installed. The CLI probes the platform for a minimum-version
requirement on each command run and warns to stderr when upgrade is needed.

Exit codes:
  0   ok`,
	Example: `  bcdock version
  bcdock version -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		type versionInfo struct {
			Version    string `json:"version" header:"VERSION"`
			Commit     string `json:"commit"     header:"COMMIT"`
			CommitDate string `json:"commitDate" header:"COMMIT DATE"`
			API        string `json:"api"        header:"API"`
		}
		info := versionInfo{Version: version, Commit: commit, CommitDate: commitDate, API: apiMajor}
		return r.Printer.Print(info)
	},
}

func init() {
	RootCmd.AddCommand(versionCmd)
	// Override PersistentPreRunE so version works without auth
	versionCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error { return nil }
}
