package cli

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/bcdock/cli/internal/client"
	"github.com/bcdock/cli/internal/exitcode"
)

func TestExitCodeFor_NilError_ReturnsZero(t *testing.T) {
	var buf bytes.Buffer
	if got := exitCodeFor(nil, &buf); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", buf.String())
	}
}

func TestExitCodeFor_GenericError_WritesStderrAndReturnsGeneralError(t *testing.T) {
	var buf bytes.Buffer
	got := exitCodeFor(errors.New("dial tcp: connection refused"), &buf)
	if got != exitcode.GeneralError {
		t.Fatalf("expected %d, got %d", exitcode.GeneralError, got)
	}
	if !strings.Contains(buf.String(), "connection refused") {
		t.Fatalf("expected stderr to mention the underlying error, got %q", buf.String())
	}
	if !strings.HasPrefix(buf.String(), "error: ") {
		t.Fatalf("expected stderr to be prefixed with 'error: ', got %q", buf.String())
	}
}

func TestExitCodeFor_EmptyMessage_StillWritesStderr(t *testing.T) {
	var buf bytes.Buffer
	got := exitCodeFor(emptyError{}, &buf)
	if got != exitcode.GeneralError {
		t.Fatalf("expected %d, got %d", exitcode.GeneralError, got)
	}
	if buf.Len() == 0 {
		t.Fatal("expected fallback stderr line, got empty buffer")
	}
}

func TestExitCodeFor_APIError_MapsAuthFailure(t *testing.T) {
	var buf bytes.Buffer
	got := exitCodeFor(&client.APIError{Status: http.StatusUnauthorized, Message: "token expired"}, &buf)
	if got != exitcode.AuthFailure {
		t.Fatalf("expected %d, got %d", exitcode.AuthFailure, got)
	}
	if !strings.Contains(buf.String(), "token expired") {
		t.Fatalf("expected stderr to include API message, got %q", buf.String())
	}
}

func TestExitCodeFor_WrappedAPIError_StillMapped(t *testing.T) {
	var buf bytes.Buffer
	wrapped := &wrapErr{
		msg:   "context",
		inner: &client.APIError{Status: http.StatusNotFound, Message: "missing"},
	}
	if got := exitCodeFor(wrapped, &buf); got != exitcode.NotFound {
		t.Fatalf("expected %d, got %d", exitcode.NotFound, got)
	}
}

// CLI-018: error from a command implementing ExitCode() (e.g. env wait timeoutError)
// must route through to its own exit code instead of the generic 1.
func TestExitCodeFor_CustomExitCoder_RoutesToCode(t *testing.T) {
	var buf bytes.Buffer
	if got := exitCodeFor(&fakeExitCoder{msg: "deadline", code: 124}, &buf); got != 124 {
		t.Fatalf("expected 124, got %d", got)
	}
	if !strings.Contains(buf.String(), "deadline") {
		t.Fatalf("expected stderr to include error message, got %q", buf.String())
	}
}

type fakeExitCoder struct {
	msg  string
	code int
}

func (e *fakeExitCoder) Error() string { return e.msg }
func (e *fakeExitCoder) ExitCode() int { return e.code }

type emptyError struct{}

func (emptyError) Error() string { return "" }

type wrapErr struct {
	msg   string
	inner error
}

func (e *wrapErr) Error() string { return e.msg + ": " + e.inner.Error() }
func (e *wrapErr) Unwrap() error { return e.inner }
