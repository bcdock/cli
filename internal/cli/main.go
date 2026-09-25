package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"

	"github.com/bcdock/cli/internal/client"
	"github.com/bcdock/cli/internal/exitcode"
	"github.com/bcdock/cli/internal/output"
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
	return executeTo(os.Stderr)
}

// executeTo runs the root command and reports its failure in the format -o selected
// (CLI-019). flagOutput is read AFTER Execute, so it holds the parsed -o value.
func executeTo(stderr io.Writer) int {
	err := RootCmd.Execute()
	return exitCodeForFormat(err, stderr, flagOutput)
}

// exitCodeFor maps a command result to a process exit code, always writing at
// least one stderr line for non-nil errors. Cobra's SilenceErrors=true means it
// won't print on its own - without this, transient failures (connection
// refused, dial timeout, file I/O) exit silently and an agent has no way to
// diagnose what happened.
func exitCodeFor(err error, stderr io.Writer) int {
	return exitCodeForFormat(err, stderr, output.FormatTable)
}

// exitCodeForFormat is exitCodeFor for a given -o format (CLI-019). Under -o json a failure
// writes ONE JSON object to stderr instead of the `error: ...` line, so a script or an agent
// can act on a failure without regexing it:
//
//	{"error":"<code>","message":"<text>","exitCode":N}            plus "status":<http> for an API error
//
// The exit code, the message text and stdout (empty) are the same in every format; table and
// CSV keep the text line exactly. `error` is the API's own code when it sent one, else a
// small fixed set - see errorCode.
func exitCodeForFormat(err error, stderr io.Writer, format string) int {
	if err == nil {
		return 0
	}
	msg := err.Error()
	if msg == "" {
		msg = "command failed (no error message)"
	}
	code := exitCodeOf(err)

	if format == output.FormatJSON {
		obj := jsonError{Error: errorCode(err), Message: msg, ExitCode: code}
		var apiErr *client.APIError
		if errors.As(err, &apiErr) {
			obj.Status = apiErr.Status
		}
		enc := json.NewEncoder(stderr)
		enc.SetEscapeHTML(false)
		if encErr := enc.Encode(obj); encErr == nil {
			return code
		}
		// Unreachable for these field types; never leave a failure unreported.
	}
	fmt.Fprintln(stderr, "error: "+msg)
	return code
}

func exitCodeOf(err error) int {
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

// jsonError is the -o json failure object (CLI-019). Field names are a public surface.
type jsonError struct {
	Error    string `json:"error"`
	Message  string `json:"message"`
	ExitCode int    `json:"exitCode"`
	Status   int    `json:"status,omitempty"`
}

// apiCodeShape is what an API error code looks like (not_found, invalid_state).
var apiCodeShape = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// errorCode is the `error` field of the -o json failure object. The documented set:
//
//	<the API's code>     the API sent one, e.g. not_found, invalid_state, invalid_input,
//	                     quota_exceeded (the API's canonical `code`, or the older shape
//	                     where `error` held the code and `message` the text)
//	unauthorized, forbidden, not_found, rate_limited, internal_error, service_unavailable
//	                     the API failed with that HTTP status and sent no code
//	api_error            any other API failure with no code
//	timeout              `env wait` gave up waiting (exit 124)
//	network              the request did not complete: refused, DNS, TLS, request timeout
//	cli_error            anything else the CLI itself rejected (bad input, a local file)
func errorCode(err error) string {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Code != "" {
			return apiErr.Code
		}
		if apiErr.Message != "" && apiCodeShape.MatchString(apiErr.ErrorText) {
			return apiErr.ErrorText
		}
		switch apiErr.Status {
		case http.StatusUnauthorized:
			return "unauthorized"
		case http.StatusForbidden:
			return "forbidden"
		case http.StatusNotFound:
			return "not_found"
		case http.StatusTooManyRequests:
			return "rate_limited"
		case http.StatusInternalServerError:
			return "internal_error"
		case http.StatusServiceUnavailable:
			return "service_unavailable"
		}
		return "api_error"
	}
	var te *timeoutError
	if errors.As(err, &te) {
		return "timeout"
	}
	var ue *url.Error
	var ne net.Error
	if errors.As(err, &ue) || errors.As(err, &ne) {
		return "network"
	}
	return "cli_error"
}
