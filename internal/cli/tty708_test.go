//go:build linux

package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// #708: "is there a terminal?" must come from the terminal driver. The old check
// (os.ModeCharDevice) was true for /dev/null, which is how agents and CI jobs often run, so the
// env create picker and the revoke prompt opened there and failed on EOF. Each case runs with
// stdin as /dev/null, a pipe, and a real pty; the pty is the control that still gets the prompt.

// openPty returns a pseudo-terminal pair: master (the "keyboard") and slave (the process's stdin).
func openPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx: %v", err)
	}
	if err := unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlockpt: %v", err)
	}
	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("ptsname: %v", err)
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open pts: %v", err)
	}
	t.Cleanup(func() { s.Close(); m.Close() })
	return m, s
}

func devNull(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// pipeStdin is a pipe whose writer is already closed, as when an agent pipes nothing in.
func pipeStdin(t *testing.T) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	t.Cleanup(func() { r.Close() })
	return r
}

// withStdin swaps os.Stdin for one test; the commands read os.Stdin directly.
func withStdin(t *testing.T, f *os.File) {
	t.Helper()
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = old })
}

// captureOSStderr returns what is written to os.Stderr while fn runs. Prompt writes there
// directly, not to the command's stderr, so this is how a test sees whether it prompted.
func captureOSStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	fn()
	os.Stderr = old
	w.Close()
	b, _ := io.ReadAll(r)
	r.Close()
	return string(b)
}

func TestIsTTY_DevNullAndPipeAreNotTerminals_PtyIs(t *testing.T) {
	if isTTY(devNull(t)) {
		t.Error("/dev/null reported as a terminal (it is a character device, not a tty)")
	}
	if isTTY(pipeStdin(t)) {
		t.Error("a pipe reported as a terminal")
	}
	_, slave := openPty(t)
	if !isTTY(slave) {
		t.Error("a pty slave not reported as a terminal (the control)")
	}
}

// ── env create with no flags ──

func TestEnvCreate_NoFlags_NoTerminal_NamesTheFlags_NoPicker(t *testing.T) {
	for name, stdin := range map[string]func(*testing.T) *os.File{"devnull": devNull, "pipe": pipeStdin} {
		t.Run(name, func(t *testing.T) {
			srv, seen := routeStub(t, map[string]stubRoute{"GET /api/v1/public/regions": {200, `[]`}})
			withStdin(t, stdin(t))
			code, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "env", "create")
			if code != 1 {
				t.Errorf("exit = %d, want 1", code)
			}
			for _, want := range []string{"no terminal for the interactive picker", "--version", "--country", "--region"} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q in: %s", want, out)
				}
			}
			if len(*seen) != 0 {
				t.Errorf("the picker ran without a terminal: %v", *seen)
			}
		})
	}
}

// The control: at a real terminal the picker still runs (it asks for regions first).
func TestEnvCreate_NoFlags_Pty_RunsThePicker(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{"GET /api/v1/public/regions": {200, `[]`}})
	_, slave := openPty(t)
	withStdin(t, slave)
	_, _, out := runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "env", "create")
	if len(*seen) == 0 || (*seen)[0].path != "/api/v1/public/regions" {
		t.Errorf("picker did not run at a terminal: requests %v, out %s", *seen, out)
	}
	if strings.Contains(out, "no terminal for the interactive picker") {
		t.Errorf("a pty was treated as no terminal: %s", out)
	}
}

// ── auth keys revoke without --yes ──

func TestAuthKeysRevoke_NoTerminal_NeedsYes_NoPrompt(t *testing.T) {
	for name, stdin := range map[string]func(*testing.T) *os.File{"devnull": devNull, "pipe": pipeStdin} {
		t.Run(name, func(t *testing.T) {
			srv, seen := routeStub(t, map[string]stubRoute{"DELETE /api/v1/api-keys/" + keyID: {204, ""}})
			withStdin(t, stdin(t))
			var code int
			var out string
			prompted := captureOSStderr(t, func() {
				code, _, out = runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "auth", "keys", "revoke", keyID)
			})
			if code != 1 || !strings.Contains(out, "pass --yes") {
				t.Errorf("exit %d, out %s", code, out)
			}
			if strings.Contains(prompted, "[y/N]") {
				t.Errorf("prompted without a terminal: %q", prompted)
			}
			if len(*seen) != 0 {
				t.Errorf("sent %v without --yes", *seen)
			}
		})
	}
}

// The control: at a real terminal it prompts; answering "n" cancels and sends nothing.
func TestAuthKeysRevoke_Pty_Prompts_NoCancels(t *testing.T) {
	srv, seen := routeStub(t, map[string]stubRoute{"DELETE /api/v1/api-keys/" + keyID: {204, ""}})
	master, slave := openPty(t)
	withStdin(t, slave)
	if _, err := master.Write([]byte("n\n")); err != nil {
		t.Fatal(err)
	}
	var code int
	var out string
	prompted := captureOSStderr(t, func() {
		code, _, out = runOut(t, "--api-url", srv.URL, "--token", "bdk_test", "auth", "keys", "revoke", keyID)
	})
	if code != 0 || strings.Contains(out, "pass --yes") {
		t.Errorf("exit %d, out %s", code, out)
	}
	if !strings.Contains(prompted, "[y/N]") {
		t.Errorf("no prompt at a terminal: %q", prompted)
	}
	if len(*seen) != 0 {
		t.Errorf("sent %v after answering n", *seen)
	}
}
