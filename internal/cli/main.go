package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/bcdock/cli/internal/client"
	"github.com/bcdock/cli/internal/exitcode"
)

// Injected at build time via -ldflags.
var (
	version = "dev"
	commit  = "unknown"
)

// apiMajor is the Platform API major version this CLI is built against.
// Bumped when the public CLI moves from /api/v1/ to /api/v2/.
// Surfaced via `bcdock version` so users can confirm their CLI matches the deployed API.
const apiMajor = "v1"

// Execute runs the root command and returns a process exit code.
// Binary entry points (cmd/bcdock, cmd/bcdockadm) call this from main().
func Execute() int {
	return exitCodeFor(RootCmd.Execute(), os.Stderr)
}

// exitCodeFor maps a command result to a process exit code, always writing at
// least one stderr line for non-nil errors. Cobra's SilenceErrors=true means it
// won't print on its own - without this, transient failures (connection
// refused, dial timeout, file I/O) exit silently and an agent has no way to
// diagnose what happened.
func exitCodeFor(err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}
	msg := err.Error()
	if msg == "" {
		msg = "command failed (no error message)"
	}
	fmt.Fprintln(stderr, "error: "+msg)

	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ExitCode()
	}
	// Commands like `env wait` need to signal a specific exit code (e.g. 124 for timeout)
	// without leaking type-specific knowledge into this central handler.
	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return exitcode.GeneralError
}
