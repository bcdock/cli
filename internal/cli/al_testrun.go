package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bcdock/cli/internal/exitcode"
	"github.com/bcdock/cli/internal/output"
	"github.com/spf13/cobra"
)

// minTestRunBcMajor: remote test running arrived with the BC 28 dev endpoint
// (dev API 7.0). A BC 27 server cannot run tests from outside the container.
const minTestRunBcMajor = 28

var alTestCmd = &cobra.Command{
	Use:   "test <test.app>",
	Short: "Run the test codeunits of a published test app on a BC env",
	Long: `Run the test codeunits of a test app against a BCDock env and print a
pass/fail summary per codeunit. The app must already be published to the env
('bcdock env publish'); this command only runs its tests.

How it works:
  1. Reads the test codeunits (Subtype = Test) from the .app file itself.
  2. Gets Microsoft's AL tool (altool) for the env's BC version from NuGet
     (18.x for BC 28-29, 30.x for BC 30+), cached per version under
     ${BCDOCK_AL_CACHE:-$XDG_CACHE_HOME/bcdock}/altool. Never a global install.
  3. Reveals the env's credentials (audited) and passes them to altool in its
     environment, never on the command line.
  4. Runs 'altool runtests' against the env's dev endpoint.

Requirements:
  - The env runs BC 28 or later. Older envs cannot run tests remotely; the
    command refuses before doing anything.
  - A .NET 8 or later runtime (Microsoft.NETCore.App and
    Microsoft.AspNetCore.App), because altool runs on .NET. Check with
    'dotnet --list-runtimes'; install from https://dot.net.

--codeunit narrows the run to the named test codeunit ids (repeatable).
--company runs in that company instead of the env's default.
--junit writes JUnit XML: one testsuite per codeunit, one testcase per test.

Exit codes:
  0   every test ran and passed
  1   general error (bad flags, unreadable app, download failed)
  3   auth failure (missing or invalid token)
  5   environment not found
  6   tests ran and at least one failed
  7   tests could not run (BC version below 28, no .NET 8+, altool or the env failed)`,
	Example: `  bcdock env publish my-env build/MyTests_1.0.0.0.app
  bcdock al test build/MyTests_1.0.0.0.app --env my-env
  bcdock al test MyTests.app --env my-env --codeunit 50100 --junit results.xml
  bcdock al test MyTests.app --env my-env -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		appPath := args[0]
		envArg, _ := cmd.Flags().GetString("env")
		junitPath, _ := cmd.Flags().GetString("junit")
		company, _ := cmd.Flags().GetString("company")
		only, _ := cmd.Flags().GetIntSlice("codeunit")
		refresh, _ := cmd.Flags().GetBool("refresh")
		timeout, _ := cmd.Flags().GetDuration("timeout")

		if envArg == "" {
			return fmt.Errorf("--env is required (the env the test app is published to)")
		}

		// Everything local first, so a bad app or flag never touches the env.
		appBytes, err := os.ReadFile(appPath)
		if err != nil {
			return fmt.Errorf("read test app: %w", err)
		}
		codeunits, err := appTestCodeunits(appBytes)
		if err != nil {
			return fmt.Errorf("%s: %w", appPath, err)
		}
		codeunits, err = selectCodeunits(codeunits, only)
		if err != nil {
			return err
		}

		id, err := resolveEnvID(cmd.Context(), r.Client, envArg)
		if err != nil {
			return err
		}
		var env environment
		if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/environments/"+id, nil, &env); err != nil {
			return err
		}
		if err := checkTestRunBcVersion(envArg, env.BcVersion); err != nil {
			return err
		}
		if env.DevEndpointUrl == nil || *env.DevEndpointUrl == "" {
			return testsNotRun("environment %q has no dev endpoint URL (status: %s)", envArg, env.Status)
		}
		conn, err := altoolConnectionArgs(*env.DevEndpointUrl, env.ArtifactType, company)
		if err != nil {
			return testsNotRun("%v", err)
		}

		bcMaj, _ := bcMajor(env.BcVersion)
		rt, err := detectDotnet(cmd.Context())
		if err != nil {
			return err
		}
		tool, err := ensureAlTool(cmd.Context(), r.Printer, bcMaj, rt, refresh)
		if err != nil {
			return err
		}

		creds, err := revealEnvCredentials(cmd.Context(), r.Client, id)
		if err != nil {
			return err
		}
		bcUser, bcPass, err := requireBcAdmin(creds, envArg, env.Status)
		if err != nil {
			return err
		}

		ids := make([]string, len(codeunits))
		for i, c := range codeunits {
			ids[i] = strconv.Itoa(c.ID)
		}
		r.Printer.Info("Running %d test codeunit(s) of %s on %s: %s", len(codeunits), filepath.Base(appPath), envArg, strings.Join(ids, ", "))

		runCtx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()
		raw, runErr := runAlToolTests(runCtx, tool, codeunits, conn, bcUser, bcPass)

		result := buildTestRunResult(envArg, filepath.Base(appPath), codeunits, raw)
		if runErr != nil && result.NotRunReason == "" {
			result.NotRunReason = runErr.Error()
		}
		if junitPath != "" {
			if err := writeJUnit(junitPath, result); err != nil {
				return fmt.Errorf("write JUnit: %w", err)
			}
		}
		if err := printTestRunResult(r.Printer, result); err != nil {
			return err
		}
		switch {
		case result.NotRunReason != "":
			return testsNotRun("tests did not run: %s", result.NotRunReason)
		case result.Failed > 0:
			return &testRunError{code: exitcode.TestsFailed, slug: "tests_failed",
				msg: fmt.Sprintf("%d of %d test(s) failed", result.Failed, result.Total)}
		}
		return nil
	},
}

// testRunError carries the exit code and the -o json error slug for al test.
type testRunError struct {
	code int
	slug string
	msg  string
}

func (e *testRunError) Error() string     { return e.msg }
func (e *testRunError) ExitCode() int     { return e.code }
func (e *testRunError) ErrorCode() string { return e.slug }

func testsNotRun(format string, a ...any) error {
	return &testRunError{code: exitcode.TestsNotRun, slug: "tests_not_run", msg: fmt.Sprintf(format, a...)}
}

func checkTestRunBcVersion(envArg, bcVersion string) error {
	major, ok := bcMajor(bcVersion)
	if !ok {
		return testsNotRun("cannot read the BC version of %q (%q); bcdock al test needs BC %d or later", envArg, bcVersion, minTestRunBcMajor)
	}
	if major < minTestRunBcMajor {
		return testsNotRun("bcdock al test needs BC %d or later: remote test running arrived with the BC %d dev endpoint, and %q runs BC %s. Nothing was run.",
			minTestRunBcMajor, minTestRunBcMajor, envArg, bcVersion)
	}
	return nil
}

// --- test codeunits -------------------------------------------------------

type testCodeunit struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// appTestCodeunits reads the codeunits with Subtype = Test from the .app's
// SymbolReference.json, walking nested namespaces. Offline, any BC version.
func appTestCodeunits(data []byte) ([]testCodeunit, error) {
	raw, ok := readAppEntry(data, "SymbolReference.json")
	if !ok {
		return nil, fmt.Errorf("not a compiled .app with a SymbolReference.json")
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	type cu struct {
		ID         int    `json:"Id"`
		Name       string `json:"Name"`
		Properties []struct {
			Name  string `json:"Name"`
			Value string `json:"Value"`
		} `json:"Properties"`
	}
	type ns struct {
		Codeunits  []cu              `json:"Codeunits"`
		Namespaces []json.RawMessage `json:"Namespaces"`
	}
	seen := map[int]bool{}
	var out []testCodeunit
	var walk func(msg []byte) error
	walk = func(msg []byte) error {
		var n ns
		if err := json.Unmarshal(msg, &n); err != nil {
			return err
		}
		for _, c := range n.Codeunits {
			for _, p := range c.Properties {
				if strings.EqualFold(p.Name, "Subtype") && strings.EqualFold(p.Value, "Test") && !seen[c.ID] {
					seen[c.ID] = true
					out = append(out, testCodeunit{ID: c.ID, Name: c.Name})
				}
			}
		}
		for _, child := range n.Namespaces {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(raw); err != nil {
		return nil, fmt.Errorf("parse SymbolReference.json: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no test codeunits (Subtype = Test) in this app")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// selectCodeunits narrows to --codeunit ids, which must all be test codeunits of the app.
func selectCodeunits(all []testCodeunit, only []int) ([]testCodeunit, error) {
	if len(only) == 0 {
		return all, nil
	}
	byID := map[int]testCodeunit{}
	for _, c := range all {
		byID[c.ID] = c
	}
	var out []testCodeunit
	for _, id := range only {
		c, ok := byID[id]
		if !ok {
			var have []string
			for _, c := range all {
				have = append(have, fmt.Sprintf("%d %s", c.ID, c.Name))
			}
			return nil, fmt.Errorf("--codeunit %d is not a test codeunit of this app (it has: %s)", id, strings.Join(have, ", "))
		}
		out = append(out, c)
	}
	return out, nil
}

// --- connection -----------------------------------------------------------

// altoolConnectionArgs turns the env's dev endpoint ({server}/{instance}/) into
// altool's connection flags. Our envs are multi-tenant: without --tenant default
// the test runner hub answers 401 (#686).
func altoolConnectionArgs(devEndpoint, artifactType, company string) ([]string, error) {
	u, err := url.Parse(devEndpoint)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("unreadable dev endpoint %q", devEndpoint)
	}
	instance := strings.Split(strings.Trim(u.Path, "/"), "/")[0]
	if instance == "" {
		return nil, fmt.Errorf("dev endpoint %q has no server instance", devEndpoint)
	}
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	envType := "Sandbox"
	if strings.EqualFold(artifactType, "OnPrem") {
		envType = "OnPrem"
	}
	a := []string{
		"--server", u.Scheme + "://" + u.Hostname(),
		"--serverinstance", instance,
		"--port", port,
		"--authentication", "UserPassword",
		"--tenant", "default",
		"--environmenttype", envType,
	}
	if company != "" {
		a = append(a, "--company", company)
	}
	return a, nil
}

// --- .NET runtime ---------------------------------------------------------

// dotnetRuntimes is what 'dotnet --list-runtimes' reports: the majors that have
// BOTH Microsoft.NETCore.App and Microsoft.AspNetCore.App (altool needs both).
type dotnetRuntimes struct {
	majors []int // ascending
}

var dotnetInstallHint = "install the .NET 8 or later runtime (ASP.NET Core Runtime includes both parts) from https://dot.net, then check 'dotnet --list-runtimes'"

func detectDotnet(ctx context.Context) (dotnetRuntimes, error) {
	out, err := exec.CommandContext(ctx, "dotnet", "--list-runtimes").Output()
	if err != nil {
		return dotnetRuntimes{}, testsNotRun("bcdock al test needs a .NET 8 or later runtime to run altool, and 'dotnet' was not found: %s", dotnetInstallHint)
	}
	rt := parseDotnetRuntimes(string(out))
	if len(rt.majors) == 0 || rt.majors[len(rt.majors)-1] < 8 {
		return rt, testsNotRun("bcdock al test needs a .NET 8 or later runtime (Microsoft.NETCore.App and Microsoft.AspNetCore.App) to run altool, and none was found: %s", dotnetInstallHint)
	}
	return rt, nil
}

var runtimeLine = regexp.MustCompile(`^(Microsoft\.NETCore\.App|Microsoft\.AspNetCore\.App) (\d+)\.`)

func parseDotnetRuntimes(listing string) dotnetRuntimes {
	core, asp := map[int]bool{}, map[int]bool{}
	for _, line := range strings.Split(listing, "\n") {
		m := runtimeLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		if m[1] == "Microsoft.NETCore.App" {
			core[n] = true
		} else {
			asp[n] = true
		}
	}
	var rt dotnetRuntimes
	for n := range core {
		if asp[n] {
			rt.majors = append(rt.majors, n)
		}
	}
	sort.Ints(rt.majors)
	return rt
}

// --- altool from NuGet ----------------------------------------------------

const altoolPackage = "microsoft.dynamics.businesscentral.development.tools"

// altoolFeed is NuGet's flat container for the altool package. BCDOCK_ALTOOL_FEED
// overrides it (tests, or a mirror).
func altoolFeed() string {
	if f := os.Getenv("BCDOCK_ALTOOL_FEED"); f != "" {
		return strings.TrimRight(f, "/")
	}
	return "https://api.nuget.org/v3-flatcontainer/" + altoolPackage
}

// altoolMajorFor maps an env's BC major to the altool major that drives it:
// 18.x speaks BC 28-29's dev API 7.0, 30.x adds BC 30's 8.0. altool rejects a
// server NEWER than itself, so the match is by BC version (#686).
//
// Revisit when BC 31 ships: if altool 30.x rejects a BC 31 server the way 18.x
// rejects BC 30, BC 31 needs its own altool major here.
func altoolMajorFor(bcMajor int) int {
	if bcMajor >= 30 {
		return 30
	}
	return 18
}

func altoolCacheRoot() string {
	if env := os.Getenv("BCDOCK_AL_CACHE"); env != "" {
		return filepath.Join(env, "altool")
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = filepath.Join(os.TempDir(), "bcdock-cache")
	}
	return filepath.Join(base, "bcdock", "altool")
}

type alTool struct {
	Version     string
	DLL         string // .../tools/netN.0/any/altool.dll
	RollForward bool   // run on a newer .NET major than the build targets
}

// ensureAlTool returns a cached altool for the BC major, downloading the newest
// matching NuGet version when none is cached (or on --refresh).
func ensureAlTool(ctx context.Context, p *output.Printer, bcMajor int, rt dotnetRuntimes, refresh bool) (alTool, error) {
	major := altoolMajorFor(bcMajor)
	root := altoolCacheRoot()
	version := ""
	if !refresh {
		version = newestCachedAlTool(root, major)
	}
	if version == "" {
		v, err := newestNuGetAlTool(ctx, major)
		if err != nil {
			return alTool{}, testsNotRun("find altool %d.x on NuGet: %v", major, err)
		}
		version = v
	}
	dir := filepath.Join(root, version)
	if refresh || !fileExists(filepath.Join(dir, ".complete")) {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return alTool{}, fmt.Errorf("create altool cache: %w", err)
		}
		// Download and extract into a private temp dir, then rename into place, so two
		// first runs at once never share a half-written package or directory.
		tmp, err := os.MkdirTemp(root, version+".tmp-")
		if err != nil {
			return alTool{}, fmt.Errorf("create altool temp dir: %w", err)
		}
		defer os.RemoveAll(tmp)
		pkg := filepath.Join(tmp, "altool.nupkg")
		u := fmt.Sprintf("%s/%s/%s.%s.nupkg", altoolFeed(), strings.ToLower(version), altoolPackage, strings.ToLower(version))
		p.Info("Fetching altool %s (BC %d) from NuGet", version, bcMajor)
		if err := downloadFile(ctx, u, pkg, false, 10*time.Minute); err != nil {
			return alTool{}, testsNotRun("download altool %s: %v", version, err)
		}
		staged := filepath.Join(tmp, "x")
		if err := extractZip(pkg, staged); err != nil {
			return alTool{}, fmt.Errorf("extract altool %s: %w", version, err)
		}
		if err := os.WriteFile(filepath.Join(staged, ".complete"), []byte(version+"\n"), 0o644); err != nil {
			return alTool{}, err
		}
		_ = os.RemoveAll(dir)
		if err := os.Rename(staged, dir); err != nil && !fileExists(filepath.Join(dir, ".complete")) {
			return alTool{}, fmt.Errorf("install altool %s: %w", version, err)
		}
	}
	return pickAlToolBuild(dir, version, rt)
}

// pickAlToolBuild chooses the tools/netN.0 build the installed runtime can run:
// the highest N with N <= the newest installed major. If the runtime is newer
// than every build (e.g. only .NET 9 for a net8.0/net10.0 package), the build
// runs with roll-forward to that major.
func pickAlToolBuild(dir, version string, rt dotnetRuntimes) (alTool, error) {
	matches, _ := filepath.Glob(filepath.Join(dir, "tools", "net*", "any", "altool.dll"))
	type build struct {
		n   int
		dll string
	}
	var builds []build
	for _, m := range matches {
		tfm := filepath.Base(filepath.Dir(filepath.Dir(m))) // netN.0
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(tfm, "net"), ".0"))
		if err == nil {
			builds = append(builds, build{n, m})
		}
	}
	if len(builds) == 0 {
		return alTool{}, testsNotRun("altool %s has no tools/netN.0/any/altool.dll (package layout changed?)", version)
	}
	sort.Slice(builds, func(i, j int) bool { return builds[i].n > builds[j].n })
	have := map[int]bool{}
	for _, m := range rt.majors {
		have[m] = true
	}
	for _, b := range builds {
		if have[b.n] {
			return alTool{Version: version, DLL: b.dll}, nil
		}
	}
	newest := 0
	if len(rt.majors) > 0 {
		newest = rt.majors[len(rt.majors)-1]
	}
	for _, b := range builds {
		if b.n < newest {
			return alTool{Version: version, DLL: b.dll, RollForward: true}, nil
		}
	}
	return alTool{}, testsNotRun("altool %s needs .NET %d or later, and the newest runtime found is %d: %s", version, builds[len(builds)-1].n, newest, dotnetInstallHint)
}

func newestCachedAlTool(root string, major int) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var vs []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), strconv.Itoa(major)+".") && fileExists(filepath.Join(root, e.Name(), ".complete")) {
			vs = append(vs, e.Name())
		}
	}
	return newestPreferRelease(vs)
}

// newestPreferRelease is the newest release, or the newest prerelease when there
// is no release. A release beats any prerelease, even a newer one: a beta posted
// beside 18.1 must not become every first run's altool (Ravi, #1010). 30.x is
// beta-only today, so BC 30+ falls back to the newest prerelease.
func newestPreferRelease(vs []string) string {
	var releases, pre []string
	for _, v := range vs {
		if strings.Contains(v, "-") {
			pre = append(pre, v)
		} else {
			releases = append(releases, v)
		}
	}
	if best := newestVersion(releases); best != "" {
		return best
	}
	return newestVersion(pre)
}

func newestNuGetAlTool(ctx context.Context, major int) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, altoolFeed()+"/index.json", nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: time.Minute}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", req.URL, resp.Status)
	}
	var idx struct {
		Versions []string `json:"versions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&idx); err != nil {
		return "", err
	}
	var vs []string
	for _, v := range idx.Versions {
		if strings.HasPrefix(v, strconv.Itoa(major)+".") {
			vs = append(vs, v)
		}
	}
	best := newestPreferRelease(vs)
	if best == "" {
		return "", fmt.Errorf("no %d.x version listed", major)
	}
	return best, nil
}

// newestVersion picks the highest of NuGet versions like 18.1.43.24897 or
// 30.0.44.47290-beta, numeric part by part; a release beats its prerelease.
func newestVersion(vs []string) string {
	best := ""
	for _, v := range vs {
		if best == "" || compareNuGetVersion(v, best) > 0 {
			best = v
		}
	}
	return best
}

func compareNuGetVersion(a, b string) int {
	an, ap, _ := strings.Cut(a, "-")
	bn, bp, _ := strings.Cut(b, "-")
	as, bs := strings.Split(an, "."), strings.Split(bn, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case ap == bp:
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	}
	return strings.Compare(ap, bp)
}

// --- running altool -------------------------------------------------------

type altoolRun struct {
	stdout []byte
	stderr []byte
}

// runAlToolTests runs 'altool runtests' with the codeunits as test groups. The
// BC credentials travel in BC_SERVER_USERNAME/BC_SERVER_PASSWORD, never argv.
func runAlToolTests(ctx context.Context, tool alTool, codeunits []testCodeunit, conn []string, user, pass string) (altoolRun, error) {
	groups := make([]map[string]int, len(codeunits))
	for i, c := range codeunits {
		groups[i] = map[string]int{"codeunitId": c.ID}
	}
	f, err := os.CreateTemp("", "bcdock-testgroups-*.json")
	if err != nil {
		return altoolRun{}, err
	}
	defer os.Remove(f.Name())
	if err := json.NewEncoder(f).Encode(groups); err != nil {
		_ = f.Close()
		return altoolRun{}, err
	}
	_ = f.Close()

	args := append([]string{tool.DLL, "runtests", "--testgroups", f.Name()}, conn...)
	c := exec.CommandContext(ctx, "dotnet", args...)
	c.Env = append(os.Environ(), "BC_SERVER_USERNAME="+user, "BC_SERVER_PASSWORD="+pass)
	if tool.RollForward {
		c.Env = append(c.Env, "DOTNET_ROLL_FORWARD=Major")
	}
	var so, se bytes.Buffer
	c.Stdout, c.Stderr = &so, &se
	runErr := c.Run()
	run := altoolRun{stdout: so.Bytes(), stderr: scrubSecret(se.Bytes(), pass)}
	run.stdout = scrubSecret(run.stdout, pass)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return run, fmt.Errorf("altool runtests timed out (raise --timeout)")
	}
	// A non-zero exit with JSON on stdout is altool reporting failed tests; the
	// JSON decides. Only a run with no JSON is a run that did not happen.
	_ = runErr
	return run, nil
}

func scrubSecret(b []byte, secret string) []byte {
	if secret == "" {
		return b
	}
	return bytes.ReplaceAll(b, []byte(secret), []byte("***"))
}

// --- results --------------------------------------------------------------

type testCaseResult struct {
	Name       string  `json:"name"`
	Status     string  `json:"status"` // passed | failed | skipped | error
	DurationMs float64 `json:"durationMs"`
	Message    string  `json:"message,omitempty"`
}

type codeunitResult struct {
	ID     int              `json:"id"`
	Name   string           `json:"name"`
	Passed int              `json:"passed"`
	Failed int              `json:"failed"`
	Tests  []testCaseResult `json:"tests"`
}

type testRunResult struct {
	Env          string           `json:"env"`
	App          string           `json:"app"`
	Total        int              `json:"total"`
	Passed       int              `json:"passed"`
	Failed       int              `json:"failed"`
	Skipped      int              `json:"skipped"`
	NotRunReason string           `json:"notRunReason,omitempty"`
	Codeunits    []codeunitResult `json:"codeunits"`
}

// flexInt decodes a JSON number or a numeric string.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	n, err := strconv.Atoi(s)
	if err != nil {
		return err
	}
	*f = flexInt(n)
	return nil
}

// buildTestRunResult reads altool's runtests JSON. altool adds an extra row per
// codeunit with an empty methodName that repeats a failure; those are not tests
// and are skipped, so altool's own summary counts are not used (#686).
func buildTestRunResult(env, app string, codeunits []testCodeunit, run altoolRun) testRunResult {
	res := testRunResult{Env: env, App: app}
	var doc struct {
		Message string `json:"message"`
		Data    *struct {
			Results []struct {
				CodeunitID flexInt `json:"codeunitId"`
				MethodName string  `json:"methodName"`
				Status     string  `json:"status"`
				DurationMs float64 `json:"durationMs"`
				Output     string  `json:"output"`
			} `json:"results"`
		} `json:"data"`
	}
	body := run.stdout
	if i := bytes.IndexByte(body, '{'); i > 0 {
		body = body[i:]
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		res.NotRunReason = "altool runtests did not return JSON" + altoolExcerpt(run)
		return res
	}
	names := map[int]string{}
	order := []int{}
	for _, c := range codeunits {
		names[c.ID] = c.Name
		order = append(order, c.ID)
	}
	byID := map[int]*codeunitResult{}
	codeunitLevel := map[int]string{} // the empty-methodName row's output, per codeunit
	if doc.Data != nil {
		for _, row := range doc.Data.Results {
			if row.MethodName == "" {
				if out := strings.TrimSpace(row.Output); out != "" {
					codeunitLevel[int(row.CodeunitID)] = out
				}
				continue
			}
			id := int(row.CodeunitID)
			cr := byID[id]
			if cr == nil {
				cr = &codeunitResult{ID: id, Name: names[id]}
				byID[id] = cr
				if _, known := names[id]; !known {
					order = append(order, id)
				}
			}
			tc := testCaseResult{Name: row.MethodName, Status: strings.ToLower(row.Status), DurationMs: row.DurationMs}
			switch tc.Status {
			case "passed":
				cr.Passed++
				res.Passed++
			case "skipped":
				res.Skipped++
			case "failed":
				cr.Failed++
				res.Failed++
				tc.Message = strings.TrimSpace(row.Output)
			default:
				tc.Message = fmt.Sprintf("unknown status %q: %s", row.Status, strings.TrimSpace(row.Output))
				tc.Status = "error"
				cr.Failed++
				res.Failed++
			}
			res.Total++
			cr.Tests = append(cr.Tests, tc)
		}
	}
	// Ravi on #1010: a selected codeunit with no method rows (never published, or
	// failed at codeunit level) must not vanish, or a run where another codeunit
	// passed would exit 0. It becomes one error entry, counted as failed (exit 6).
	// When NOTHING ran, the run as a whole did not happen (exit 7) instead.
	if res.Total > 0 {
		for _, c := range codeunits {
			if byID[c.ID] != nil {
				continue
			}
			msg := codeunitLevel[c.ID]
			if msg == "" {
				msg = "no test results for this codeunit (is the app published to the env?)"
			}
			byID[c.ID] = &codeunitResult{ID: c.ID, Name: c.Name, Failed: 1,
				Tests: []testCaseResult{{Name: "(codeunit)", Status: "error", Message: msg}}}
			res.Failed++
			res.Total++
		}
	}
	for _, id := range order {
		if cr := byID[id]; cr != nil {
			res.Codeunits = append(res.Codeunits, *cr)
		}
	}
	if res.Total == 0 {
		msg := strings.TrimSpace(doc.Message)
		if msg == "" {
			msg = "altool ran no tests" + altoolExcerpt(run)
		}
		res.NotRunReason = msg
	}
	return res
}

func altoolExcerpt(run altoolRun) string {
	var parts []string
	for _, s := range []struct {
		name string
		b    []byte
	}{{"stdout", run.stdout}, {"stderr", run.stderr}} {
		lines := strings.Split(strings.TrimSpace(string(s.b)), "\n")
		if len(lines) == 1 && lines[0] == "" {
			continue
		}
		if len(lines) > 15 {
			lines = lines[:15]
		}
		parts = append(parts, s.name+":\n  "+strings.Join(lines, "\n  "))
	}
	if len(parts) == 0 {
		return ""
	}
	return "\n" + strings.Join(parts, "\n")
}

func printTestRunResult(p *output.Printer, res testRunResult) error {
	if p.Format == output.FormatJSON {
		return p.Print(res)
	}
	w := p.W
	for _, cu := range res.Codeunits {
		state := "Success"
		if cu.Failed > 0 {
			state = "Failure"
		}
		fmt.Fprintf(w, "Codeunit %d %s: %s (%d passed, %d failed)\n", cu.ID, cu.Name, state, cu.Passed, cu.Failed)
		for _, t := range cu.Tests {
			fmt.Fprintf(w, "  %-7s %s (%.3fs)\n", t.Status, t.Name, t.DurationMs/1000)
			if t.Message != "" {
				for _, line := range strings.Split(t.Message, "\n") {
					fmt.Fprintf(w, "          %s\n", strings.TrimRight(line, "\r "))
				}
			}
		}
	}
	if res.NotRunReason != "" {
		return nil
	}
	fmt.Fprintf(w, "%d test(s): %d passed, %d failed, %d skipped\n", res.Total, res.Passed, res.Failed, res.Skipped)
	return nil
}

// --- JUnit ----------------------------------------------------------------

type junitSuites struct {
	XMLName xml.Name     `xml:"testsuites"`
	Suites  []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Errors   int         `xml:"errors,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Time     string      `xml:"time,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	ClassName string        `xml:"classname,attr"`
	Name      string        `xml:"name,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitProblem `xml:"failure,omitempty"`
	Error     *junitProblem `xml:"error,omitempty"`
	Skipped   *struct{}     `xml:"skipped,omitempty"`
}

type junitProblem struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

// writeJUnit writes one testsuite per codeunit, named "<id> <name>" as AL-Go and
// BcContainerHelper name them. A run that did not happen is one error case, so a
// CI reader never mistakes it for an empty pass.
func writeJUnit(path string, res testRunResult) error {
	var doc junitSuites
	secs := func(ms float64) string { return strconv.FormatFloat(ms/1000, 'f', 3, 64) }
	if res.NotRunReason != "" {
		doc.Suites = append(doc.Suites, junitSuite{Name: res.App, Tests: 1, Errors: 1, Time: "0.000",
			Cases: []junitCase{{ClassName: res.App, Name: "altool runtests", Time: "0.000",
				Error: &junitProblem{Message: firstLine(res.NotRunReason), Body: res.NotRunReason}}}})
	}
	for _, cu := range res.Codeunits {
		name := strings.TrimSpace(fmt.Sprintf("%d %s", cu.ID, cu.Name))
		s := junitSuite{Name: name}
		var total float64
		for _, t := range cu.Tests {
			total += t.DurationMs
			c := junitCase{ClassName: name, Name: t.Name, Time: secs(t.DurationMs)}
			switch t.Status {
			case "failed":
				c.Failure = &junitProblem{Message: firstLine(t.Message), Body: t.Message}
				s.Failures++
			case "error":
				c.Error = &junitProblem{Message: firstLine(t.Message), Body: t.Message}
				s.Errors++
			case "skipped":
				c.Skipped = &struct{}{}
				s.Skipped++
			}
			s.Tests++
			s.Cases = append(s.Cases, c)
		}
		s.Time = secs(total)
		doc.Suites = append(doc.Suites, s)
	}
	b, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(xml.Header), append(b, '\n')...), 0o644)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

func init() {
	alTestCmd.Flags().String("env", "", "Env the test app is published to (required)")
	alTestCmd.Flags().IntSlice("codeunit", nil, "Run only this test codeunit id (repeatable)")
	alTestCmd.Flags().String("company", "", "Company to run the tests in (default: the env's default company)")
	alTestCmd.Flags().String("junit", "", "Write JUnit XML results to this file")
	alTestCmd.Flags().Bool("refresh", false, "Re-download altool even if a cached copy exists")
	alTestCmd.Flags().Duration("timeout", 30*time.Minute, "Maximum time for the test run")
	alCmd.AddCommand(alTestCmd)
}
