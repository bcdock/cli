package cli

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bcdock/cli/internal/exitcode"
)

const testRunEnvID = "44444444-5555-6666-7777-888888888888"
const testRunPassword = "Sup3r-Secret-Pw!"

// fakeTestApp builds a .app (NAVX header + zip) whose SymbolReference.json has
// the given top-level codeunits and one nested namespace, in the shape alc writes:
// {"Codeunits":[{"Id":..,"Name":..,"Properties":[{"Name":"Subtype","Value":"Test"}]}],"Namespaces":[...]}.
func fakeTestApp(t *testing.T, symbolReference string) []byte {
	t.Helper()
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, err := zw.Create("SymbolReference.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("\xef\xbb\xbf" + symbolReference))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	hdr := make([]byte, 40)
	copy(hdr, "NAVX")
	binary.LittleEndian.PutUint32(hdr[4:8], 40)
	return append(hdr, zbuf.Bytes()...)
}

const twoTestCodeunitsSymbols = `{
  "RuntimeVersion": "16.0",
  "Codeunits": [
    {"Id": 50100, "Name": "Passing Tests", "Properties": [{"Name": "Subtype", "Value": "Test"}]},
    {"Id": 50000, "Name": "Helper", "Properties": []}
  ],
  "Namespaces": [
    {"Name": "Contoso", "Codeunits": [
      {"Id": 50101, "Name": "Failing Tests", "Properties": [{"Name": "Subtype", "Value": "Test"}]},
      {"Id": 50102, "Name": "Install", "Properties": [{"Name": "Subtype", "Value": "Install"}]}
    ], "Namespaces": []}
  ],
  "AppId": "x", "Name": "MyTests", "Publisher": "Foo", "Version": "1.0.0.0"
}`

func TestAppTestCodeunits_FindsTestSubtypeAcrossNamespaces(t *testing.T) {
	got, err := appTestCodeunits(fakeTestApp(t, twoTestCodeunitsSymbols))
	if err != nil {
		t.Fatal(err)
	}
	want := []testCodeunit{{50100, "Passing Tests"}, {50101, "Failing Tests"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v want %+v", got, want)
	}

	if _, err := appTestCodeunits(fakeTestApp(t, `{"Codeunits":[{"Id":1,"Name":"X","Properties":[]}]}`)); err == nil || !strings.Contains(err.Error(), "no test codeunits") {
		t.Fatalf("an app without test codeunits must say so, got %v", err)
	}
	if _, err := appTestCodeunits([]byte("not an app")); err == nil {
		t.Fatal("a non-.app file must be refused")
	}
}

func TestSelectCodeunits_UnknownIdNamesWhatTheAppHas(t *testing.T) {
	all := []testCodeunit{{50100, "Passing Tests"}, {50101, "Failing Tests"}}
	got, err := selectCodeunits(all, []int{50101})
	if err != nil || len(got) != 1 || got[0].ID != 50101 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := selectCodeunits(all, []int{50999}); err == nil || !strings.Contains(err.Error(), "50100 Passing Tests") {
		t.Fatalf("expected the available codeunits listed, got %v", err)
	}
}

func TestParseDotnetRuntimes_NeedsBothFrameworks(t *testing.T) {
	listing := `Microsoft.AspNetCore.App 9.0.20 [/usr/lib/dotnet/shared/Microsoft.AspNetCore.App]
Microsoft.AspNetCore.App 10.0.15 [/usr/lib/dotnet/shared/Microsoft.AspNetCore.App]
Microsoft.NETCore.App 8.0.11 [/usr/lib/dotnet/shared/Microsoft.NETCore.App]
Microsoft.NETCore.App 9.0.20 [/usr/lib/dotnet/shared/Microsoft.NETCore.App]
Microsoft.NETCore.App 10.0.15 [/usr/lib/dotnet/shared/Microsoft.NETCore.App]
`
	got := parseDotnetRuntimes(listing).majors
	if len(got) != 2 || got[0] != 9 || got[1] != 10 {
		t.Fatalf("8 has no ASP.NET Core here, so only 9 and 10 count; got %v", got)
	}
}

func TestPickAlToolBuild_ByInstalledRuntime(t *testing.T) {
	dir := t.TempDir()
	for _, tfm := range []string{"net8.0", "net10.0"} {
		p := filepath.Join(dir, "tools", tfm, "any")
		_ = os.MkdirAll(p, 0o755)
		_ = os.WriteFile(filepath.Join(p, "altool.dll"), []byte("x"), 0o644)
	}
	cases := []struct {
		majors []int
		tfm    string
		roll   bool
		fail   bool
	}{
		{[]int{10}, "net10.0", false, false},
		{[]int{8}, "net8.0", false, false},
		{[]int{8, 10}, "net10.0", false, false},
		{[]int{9}, "net8.0", true, false},
		{[]int{11}, "net10.0", true, false},
		{[]int{6}, "", false, true},
	}
	for _, c := range cases {
		got, err := pickAlToolBuild(dir, "18.1.0.0", dotnetRuntimes{majors: c.majors})
		if c.fail {
			if err == nil {
				t.Errorf("%v: expected a refusal", c.majors)
			}
			continue
		}
		if err != nil || !strings.Contains(got.DLL, c.tfm) || got.RollForward != c.roll {
			t.Errorf("%v: got %+v, %v; want %s roll=%v", c.majors, got, err, c.tfm, c.roll)
		}
	}
}

func TestNewestVersion_ReleaseBeatsPrerelease(t *testing.T) {
	if got := newestVersion([]string{"18.0.43.1464", "18.1.43.24897", "18.1.9.1"}); got != "18.1.43.24897" {
		t.Errorf("got %s", got)
	}
	if got := newestVersion([]string{"30.0.44.47290-beta", "30.0.43.45776-beta"}); got != "30.0.44.47290-beta" {
		t.Errorf("got %s", got)
	}
	if compareNuGetVersion("30.0.44.1", "30.0.44.1-beta") <= 0 {
		t.Error("a release must sort above its prerelease")
	}
	// Ravi on #1010: a newer beta must not win over the newest release of the major.
	if got := newestPreferRelease([]string{"18.0.39.10160-beta", "18.1.43.24897", "18.2.1.1-beta"}); got != "18.1.43.24897" {
		t.Errorf("release must beat a newer beta, got %s", got)
	}
	if got := newestPreferRelease([]string{"30.0.43.45776-beta", "30.0.44.47290-beta"}); got != "30.0.44.47290-beta" {
		t.Errorf("beta-only major falls back to the newest beta, got %s", got)
	}
	if altoolMajorFor(28) != 18 || altoolMajorFor(29) != 18 || altoolMajorFor(30) != 30 {
		t.Error("altool major mapping")
	}
}

func TestAltoolConnectionArgs_FromDevEndpoint(t *testing.T) {
	got, err := altoolConnectionArgs("https://myenv-a1b2c3d4.bcdock.io/BC-dev/", "Sandbox", "CRONUS AU")
	if err != nil {
		t.Fatal(err)
	}
	want := "--server https://myenv-a1b2c3d4.bcdock.io --serverinstance BC-dev --port 443 --authentication UserPassword --tenant default --environmenttype Sandbox --company CRONUS AU"
	if strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
}

// --- end to end through the command, with a fake dotnet and a fake NuGet feed ---

type testRunHarness struct {
	apiURL    string
	credHits  *int32
	feedHits  *int32
	dotnetLog string
	dir       string
}

// newTestRunHarness fakes the Platform API (env + credentials), a NuGet flat
// container serving an altool package with net8.0 and net10.0 builds, and a
// `dotnet` on PATH. The fake dotnet answers --list-runtimes from $FAKE_RUNTIMES
// and, for `<dll> runtests ...`, logs its argv and whether the BC credentials
// arrived in its environment, then prints $FAKE_JSON and exits $FAKE_EXIT -
// altool exits 1 whenever its response is not a success (failed tests included).
func newTestRunHarness(t *testing.T, bcVersion string) *testRunHarness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake dotnet is a shell script")
	}
	h := &testRunHarness{dir: t.TempDir()}
	var cred, feed int32
	h.credHits, h.feedHits = &cred, &feed

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/environments/"+testRunEnvID, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": testRunEnvID, "shortId": "44444444", "name": "test-env", "status": "running",
			"bcVersion": bcVersion, "artifactType": "Sandbox", "createdAt": "2026-11-23T00:00:00Z",
			"devEndpointUrl": "https://test-env-44444444.bcdock.io/BC-dev/",
		})
	})
	mux.HandleFunc("/api/v1/environments/"+testRunEnvID+"/credentials", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&cred, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"environmentId": testRunEnvID, "username": "admin", "password": testRunPassword})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	h.apiURL = srv.URL

	nupkg := func() []byte {
		var b bytes.Buffer
		zw := zip.NewWriter(&b)
		for _, tfm := range []string{"net8.0", "net10.0"} {
			w, _ := zw.Create("tools/" + tfm + "/any/altool.dll")
			_, _ = w.Write([]byte("fake altool " + tfm))
		}
		_ = zw.Close()
		return b.Bytes()
	}()
	feedMux := http.NewServeMux()
	feedMux.HandleFunc("/index.json", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&feed, 1)
		_, _ = w.Write([]byte(`{"versions":["17.0.1.1","18.0.39.10160-beta","18.0.43.1464","18.1.43.24897","18.2.1.1-beta","30.0.44.47290-beta"]}`))
	})
	feedMux.HandleFunc("/18.1.43.24897/"+altoolPackage+".18.1.43.24897.nupkg", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&feed, 1)
		_, _ = w.Write(nupkg)
	})
	feedSrv := httptest.NewServer(feedMux)
	t.Cleanup(feedSrv.Close)
	t.Setenv("BCDOCK_ALTOOL_FEED", feedSrv.URL)
	t.Setenv("BCDOCK_AL_CACHE", filepath.Join(h.dir, "cache"))

	bin := filepath.Join(h.dir, "bin")
	_ = os.MkdirAll(bin, 0o755)
	h.dotnetLog = filepath.Join(h.dir, "dotnet.log")
	script := `#!/bin/sh
if [ "$1" = "--list-runtimes" ]; then
  [ -n "$FAKE_NO_DOTNET" ] && exit 127
  printf '%s\n' "$FAKE_RUNTIMES"; exit 0
fi
echo "ARGV: $*" >> "` + h.dotnetLog + `"
echo "USER=$BC_SERVER_USERNAME PASSSET=$([ -n "$BC_SERVER_PASSWORD" ] && echo yes || echo no)" >> "` + h.dotnetLog + `"
echo "progress line on stderr" >&2
printf '%s\n' "$FAKE_JSON"
exit ${FAKE_EXIT:-0}
`
	if err := os.WriteFile(filepath.Join(bin, "dotnet"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RUNTIMES", "Microsoft.AspNetCore.App 10.0.15 [/x]\nMicrosoft.NETCore.App 10.0.15 [/x]")
	return h
}

func (h *testRunHarness) app(t *testing.T) string {
	t.Helper()
	p := filepath.Join(h.dir, "MyTests_1.0.0.0.app")
	if err := os.WriteFile(p, fakeTestApp(t, twoTestCodeunitsSymbols), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (h *testRunHarness) log(t *testing.T) string {
	b, _ := os.ReadFile(h.dotnetLog)
	return string(b)
}

// altool's structured output: camelCase, enums as camelCase strings, plus the
// extra empty-methodName row per codeunit that repeats a failure (#686).
const passFailJSON = `{
  "succeeded": false,
  "message": "Test run completed: 1 passed, 1 failed, 0 skipped.",
  "data": {
    "success": false, "passed": 1, "failed": 2, "skipped": 0, "total": 3,
    "results": [
      {"codeunitId": 50100, "methodName": "AddsUp", "status": "passed", "output": "", "durationMs": 120},
      {"codeunitId": 50101, "methodName": "Breaks", "status": "failed", "output": "Assert.AreEqual failed. Expected:<1> Actual:<2>.\nAL Callstack:\n\"Failing Tests\"(CodeUnit 50101).Breaks line 5", "durationMs": 80},
      {"codeunitId": 50101, "methodName": "", "status": "failed", "output": "Assert.AreEqual failed. Expected:<1> Actual:<2>.", "durationMs": 0}
    ]
  },
  "nextSteps": [], "warnings": []
}`

const passOnlyJSON = `{"succeeded": true, "message": "Test run completed: 1 passed, 0 failed, 0 skipped.",
  "data": {"success": true, "passed": 1, "failed": 0, "skipped": 0, "total": 1,
  "results": [{"codeunitId": 50100, "methodName": "AddsUp", "status": "passed", "output": "", "durationMs": 120}]},
  "nextSteps": [], "warnings": []}`

func runAlTest(t *testing.T, h *testRunHarness, extra ...string) (string, string, int) {
	t.Helper()
	args := append([]string{"--api-url", h.apiURL, "--token", "bdk_test", "al", "test", h.app(t), "--env", testRunEnvID}, extra...)
	out, errOut, err := RunCmd(t, args...)
	code := 0
	if err != nil {
		code = exitCodeOf(err)
		errOut += "\nerror: " + err.Error()
	}
	return out, errOut, code
}

func TestAlTest_PassAndFail_ExitsTestsFailedAndWritesJUnit(t *testing.T) {
	h := newTestRunHarness(t, "28.5")
	t.Setenv("FAKE_JSON", passFailJSON)
	t.Setenv("FAKE_EXIT", "1")
	junit := filepath.Join(h.dir, "results.xml")

	out, errOut, code := runAlTest(t, h, "--junit", junit)
	if code != exitcode.TestsFailed {
		t.Fatalf("exit %d, want %d (tests failed)\n%s\n%s", code, exitcode.TestsFailed, out, errOut)
	}
	for _, want := range []string{"Codeunit 50100 Passing Tests: Success (1 passed, 0 failed)", "Codeunit 50101 Failing Tests: Failure (0 passed, 1 failed)",
		"passed  AddsUp", "failed  Breaks", "Assert.AreEqual failed", "2 test(s): 1 passed, 1 failed, 0 skipped"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}

	x, err := os.ReadFile(junit)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(x), "<testcase "); n != 2 {
		t.Fatalf("JUnit has %d testcases, want 2 (the empty-methodName row is not a test):\n%s", n, x)
	}
	for _, want := range []string{`<testsuite name="50100 Passing Tests" tests="1" failures="0"`, `<testsuite name="50101 Failing Tests" tests="1" failures="1"`,
		`<failure message="Assert.AreEqual failed. Expected:&lt;1&gt; Actual:&lt;2&gt;.">`} {
		if !strings.Contains(string(x), want) {
			t.Errorf("JUnit lacks %q:\n%s", want, x)
		}
	}

	log := h.log(t)
	if strings.Contains(log, testRunPassword) || strings.Contains(out+errOut, testRunPassword) {
		t.Fatal("the BC password reached argv or output")
	}
	if !strings.Contains(log, "USER=admin PASSSET=yes") {
		t.Fatalf("credentials must arrive in altool's environment:\n%s", log)
	}
	for _, want := range []string{"/tools/net10.0/any/altool.dll runtests --testgroups ", "--server https://test-env-44444444.bcdock.io --serverinstance BC-dev --port 443 --authentication UserPassword --tenant default --environmenttype Sandbox"} {
		if !strings.Contains(log, want) {
			t.Errorf("altool argv lacks %q:\n%s", want, log)
		}
	}
}

func TestAlTest_OnlyPassingCodeunit_ExitsZero_AndReusesTheCache(t *testing.T) {
	h := newTestRunHarness(t, "28.5")
	t.Setenv("FAKE_JSON", passOnlyJSON)

	out, errOut, code := runAlTest(t, h, "--codeunit", "50100")
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "1 test(s): 1 passed, 0 failed, 0 skipped") {
		t.Errorf("summary:\n%s", out)
	}
	first := atomic.LoadInt32(h.feedHits)
	if _, _, code := runAlTest(t, h, "--codeunit", "50100"); code != 0 {
		t.Fatalf("second run exit %d", code)
	}
	if atomic.LoadInt32(h.feedHits) != first {
		t.Fatalf("a cached altool must not touch NuGet again (hits %d -> %d)", first, atomic.LoadInt32(h.feedHits))
	}
}

func TestAlTest_JSONOutput(t *testing.T) {
	h := newTestRunHarness(t, "28.5")
	t.Setenv("FAKE_JSON", passFailJSON)
	t.Setenv("FAKE_EXIT", "1")

	out, _, code := runAlTest(t, h, "-o", "json")
	if code != exitcode.TestsFailed {
		t.Fatalf("exit %d", code)
	}
	var res testRunResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("stdout is not the JSON result: %v\n%s", err, out)
	}
	if res.Total != 2 || res.Passed != 1 || res.Failed != 1 || len(res.Codeunits) != 2 {
		t.Fatalf("counts: %+v", res)
	}
}

func TestAlTest_NoJSONFromAltool_IsNotRun(t *testing.T) {
	h := newTestRunHarness(t, "28.5")
	t.Setenv("FAKE_JSON", "Unrecognized command or argument 'runtests'.")
	t.Setenv("FAKE_EXIT", "1")
	junit := filepath.Join(h.dir, "results.xml")

	_, errOut, code := runAlTest(t, h, "--junit", junit)
	if code != exitcode.TestsNotRun {
		t.Fatalf("exit %d, want %d (not run)\n%s", code, exitcode.TestsNotRun, errOut)
	}
	if !strings.Contains(errOut, "did not return JSON") || !strings.Contains(errOut, "Unrecognized command") {
		t.Errorf("the reason and altool's output must be shown:\n%s", errOut)
	}
	x, _ := os.ReadFile(junit)
	if !strings.Contains(string(x), `errors="1"`) {
		t.Errorf("a run that did not happen is an error case in JUnit, never an empty pass:\n%s", x)
	}
}

func TestAlTest_ConnectionFailure_IsNotRun(t *testing.T) {
	h := newTestRunHarness(t, "28.5")
	t.Setenv("FAKE_JSON", `{"succeeded": false, "message": "Could not connect to the server: 401 Unauthorized", "nextSteps": [], "warnings": []}`)
	t.Setenv("FAKE_EXIT", "1")

	_, errOut, code := runAlTest(t, h)
	if code != exitcode.TestsNotRun || !strings.Contains(errOut, "401 Unauthorized") {
		t.Fatalf("exit %d, want %d with altool's message:\n%s", code, exitcode.TestsNotRun, errOut)
	}
}

// Acceptance 2: a BC 27 env is refused up front and nothing runs - no
// credential reveal, no altool download, no altool process.
func TestAlTest_BC27_RefusedBeforeAnythingRuns(t *testing.T) {
	h := newTestRunHarness(t, "27.1")
	t.Setenv("FAKE_JSON", passOnlyJSON)

	_, errOut, code := runAlTest(t, h)
	if code != exitcode.TestsNotRun {
		t.Fatalf("exit %d, want %d", code, exitcode.TestsNotRun)
	}
	if !strings.Contains(errOut, "needs BC 28 or later") || !strings.Contains(errOut, "27.1") {
		t.Errorf("refusal must say BC 28 and the env's version:\n%s", errOut)
	}
	if atomic.LoadInt32(h.credHits) != 0 || atomic.LoadInt32(h.feedHits) != 0 || h.log(t) != "" {
		t.Fatalf("nothing may run on a BC 27 env: creds=%d feed=%d altool=%q", *h.credHits, *h.feedHits, h.log(t))
	}
}

// Acceptance 2 (Stella's ruling): no .NET 8+ is refused with the install hint, before credentials.
func TestAlTest_NoDotnet8_RefusedWithInstallHint(t *testing.T) {
	for name, set := range map[string]func(){
		"no dotnet": func() { t.Setenv("FAKE_NO_DOTNET", "1") },
		"only .NET 6": func() {
			t.Setenv("FAKE_RUNTIMES", "Microsoft.AspNetCore.App 6.0.36 [/x]\nMicrosoft.NETCore.App 6.0.36 [/x]")
		},
		"no ASP.NET 8": func() { t.Setenv("FAKE_RUNTIMES", "Microsoft.NETCore.App 8.0.11 [/x]") },
	} {
		t.Run(name, func(t *testing.T) {
			h := newTestRunHarness(t, "28.5")
			set()
			_, errOut, code := runAlTest(t, h)
			if code != exitcode.TestsNotRun {
				t.Fatalf("exit %d, want %d\n%s", code, exitcode.TestsNotRun, errOut)
			}
			if !strings.Contains(errOut, ".NET 8 or later") || !strings.Contains(errOut, "https://dot.net") {
				t.Errorf("refusal must name .NET 8 and where to get it:\n%s", errOut)
			}
			if atomic.LoadInt32(h.credHits) != 0 || h.log(t) != "" {
				t.Fatalf("nothing may run without .NET: creds=%d altool=%q", *h.credHits, h.log(t))
			}
		})
	}
}

// Ravi on #1010: codeunit 50101 produced only its codeunit-level row (here: not
// published). It must not vanish while 50100 passes, or the run would exit 0.
func TestAlTest_CodeunitWithNoMethodRows_IsAnErrorNotAPass(t *testing.T) {
	h := newTestRunHarness(t, "28.5")
	t.Setenv("FAKE_JSON", `{"succeeded": false, "message": "Test run completed: 1 passed, 1 failed, 0 skipped.",
  "data": {"success": false, "passed": 1, "failed": 1, "skipped": 0, "total": 2, "results": [
    {"codeunitId": 50100, "methodName": "AddsUp", "status": "passed", "output": "", "durationMs": 120},
    {"codeunitId": 50101, "methodName": "", "status": "failed", "output": "The Codeunit with ID 50101 does not exist.", "durationMs": 0}
  ]}, "nextSteps": [], "warnings": []}`)
	t.Setenv("FAKE_EXIT", "1")
	junit := filepath.Join(h.dir, "results.xml")

	out, errOut, code := runAlTest(t, h, "--junit", junit)
	if code != exitcode.TestsFailed {
		t.Fatalf("exit %d, want %d: a codeunit that ran nothing is not a pass\n%s\n%s", code, exitcode.TestsFailed, out, errOut)
	}
	if !strings.Contains(out, "Codeunit 50101 Failing Tests: Failure") || !strings.Contains(out, "does not exist") {
		t.Errorf("50101 must be reported with altool's message:\n%s", out)
	}
	x, _ := os.ReadFile(junit)
	if !strings.Contains(string(x), `<testsuite name="50101 Failing Tests" tests="1" failures="0" errors="1"`) || !strings.Contains(string(x), "does not exist") {
		t.Errorf("JUnit needs an error case for 50101:\n%s", x)
	}

	// With no codeunit-level row at all, the message still says what to check.
	t.Setenv("FAKE_JSON", passOnlyJSON)
	out, _, code = runAlTest(t, h)
	if code != exitcode.TestsFailed || !strings.Contains(out, "no test results for this codeunit") {
		t.Fatalf("exit %d; a selected codeunit absent from the results must be an error:\n%s", code, out)
	}
}
