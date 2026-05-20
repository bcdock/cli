// Command bcdock is the public BCDock CLI. It wires up only the public command
// surface (env, me, al, auth, billing, ...) and ships in the mirrored public
// repo. Admin verbs live in a separate package not imported here.
package main

import (
	"os"

	"github.com/bcdock/cli/internal/cli"
)

func main() {
	// Importing public registers all public verbs against cli.RootCmd via
	// init(). Execute drives that command tree and returns a process exit code.
	os.Exit(cli.Execute())
}
