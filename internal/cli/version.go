package cli

import (
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the bcdock CLI version",
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		type versionInfo struct {
			Version string `json:"version" header:"VERSION"`
			Commit  string `json:"commit"  header:"COMMIT"`
			API     string `json:"api"     header:"API"`
		}
		info := versionInfo{Version: version, Commit: commit, API: apiMajor}
		return r.Printer.Print(info)
	},
}

func init() {
	RootCmd.AddCommand(versionCmd)
	// Override PersistentPreRunE so version works without auth
	versionCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error { return nil }
}
