package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

const testLibsMoveHint = `"Tests-TestLibraries" is OnPrem-only from BC 28 and not installed on sandbox envs; ` +
	`its cloud test libraries moved to "Application Test Library" - depend on that in app.json`

// runSymbolsWithDeps runs download-symbols for an app.json whose only dependency
// is name, against a fake env serving pkgs. Returns the error and the fetches.
func runSymbolsWithDeps(t *testing.T, bcVersion string, pkgs map[string][]byte, depName string) (error, []string) {
	t.Helper()
	apiURL, fetched := transitiveHarnessAt(t, bcVersion, pkgs)
	dir := t.TempDir()
	writeAppJSON(t, dir, `{
	  "name":"MyTests","publisher":"Foo","version":"1.0.0.0",
	  "dependencies":[{"name":"`+depName+`","publisher":"Microsoft","version":"28.5.0.0"}]
	}`)
	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "download-symbols", symbolsEnvID,
		"--app-json", filepath.Join(dir, "app.json"), "--out-dir", filepath.Join(dir, ".alpackages"))
	return err, *fetched
}

// The env serves only Application Test Library, as a BC 28 sandbox does.
func TestEnvSymbols_MovedAppGetsTheHintAndNothingIsSubstituted(t *testing.T) {
	pkgs := map[string][]byte{"Microsoft/Application Test Library": fakeAppPackage(t, "Application Test Library")}

	err, fetched := runSymbolsWithDeps(t, "28.5", pkgs, "Tests-TestLibraries")
	if err == nil {
		t.Fatal("expected a not-found error for Tests-TestLibraries")
	}
	want := "symbol not found on env: Microsoft_Tests-TestLibraries 28.5.0.0 - confirm the dependency is installed in the env, or pin to a version present there\n" + testLibsMoveHint
	if err.Error() != want {
		t.Fatalf("error:\n got %q\nwant %q", err.Error(), want)
	}
	if !strings.Contains(err.Error(), "BC 28") || !strings.Contains(err.Error(), `"Application Test Library"`) {
		t.Fatalf("hint must name the new app and BC 28: %v", err)
	}
	// Hint only: the replacement is never fetched under a name the user did not declare.
	if strings.Join(fetched, "|") != "Microsoft/Tests-TestLibraries" {
		t.Fatalf("expected only the declared name requested, got %v", fetched)
	}
}

func TestEnvSymbols_UnknownMissingAppKeepsTodaysError(t *testing.T) {
	pkgs := map[string][]byte{"Microsoft/Application Test Library": fakeAppPackage(t, "Application Test Library")}

	err, _ := runSymbolsWithDeps(t, "28.5", pkgs, "Some Other Library")
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	want := "symbol not found on env: Microsoft_Some Other Library 28.5.0.0 - confirm the dependency is installed in the env, or pin to a version present there"
	if err.Error() != want {
		t.Fatalf("error:\n got %q\nwant %q", err.Error(), want)
	}
}

// A transitive dependency on the moved app keeps its chain and gains the hint.
func TestEnvSymbols_TransitiveMovedAppKeepsTheChainAndGetsTheHint(t *testing.T) {
	pkgs := map[string][]byte{
		"Acme/A": fakeAppPackage(t, "A", "Microsoft/Tests-TestLibraries/28.5.0.0"),
	}
	apiURL, _ := transitiveHarnessAt(t, "28.5", pkgs)
	dir := t.TempDir()
	writeAppJSON(t, dir, `{
	  "name":"MyExt","publisher":"Foo","version":"1.0.0.0",
	  "dependencies":[{"name":"A","publisher":"Acme","version":"1.0.0.0"}]
	}`)
	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "download-symbols", symbolsEnvID,
		"--app-json", filepath.Join(dir, "app.json"), "--out-dir", filepath.Join(dir, ".alpackages"))
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	if !strings.Contains(err.Error(), "MyExt needs A needs Tests-TestLibraries") || !strings.HasSuffix(err.Error(), "\n"+testLibsMoveHint) {
		t.Fatalf("expected the chain and the hint: %v", err)
	}
}

// Ravi on #1009: before BC 28 Tests-TestLibraries is still the app, and Application
// Test Library does not exist, so a BC 27 env must get today's error, not the hint.
func TestEnvSymbols_MovedAppOnBC27KeepsTodaysError(t *testing.T) {
	err, _ := runSymbolsWithDeps(t, "27.1", map[string][]byte{}, "Tests-TestLibraries")
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	want := "symbol not found on env: Microsoft_Tests-TestLibraries 28.5.0.0 - confirm the dependency is installed in the env, or pin to a version present there"
	if err.Error() != want {
		t.Fatalf("error:\n got %q\nwant %q", err.Error(), want)
	}
}

func TestAppMoveHint_GatesOnPublisherNameAndBcMajor(t *testing.T) {
	cases := []struct {
		publisher, name, bc string
		hint                bool
	}{
		{"microsoft", "tests-testlibraries", "28.5", true},
		{"Microsoft", "Tests-TestLibraries", "28.5.54151.56140", true},
		{"Microsoft", "Tests-TestLibraries", "29.0", true},
		{"Microsoft", "Tests-TestLibraries", "28", true},
		{"Microsoft", "Tests-TestLibraries", "27.3", false},
		{"Microsoft", "Tests-TestLibraries", "", false},
		{"Microsoft", "Tests-TestLibraries", "latest", false},
		{"Acme", "Tests-TestLibraries", "28.5", false},
	}
	for _, c := range cases {
		if got := appMoveHint(c.publisher, c.name, c.bc) != ""; got != c.hint {
			t.Errorf("appMoveHint(%q, %q, %q): hint=%v want %v", c.publisher, c.name, c.bc, got, c.hint)
		}
	}
}
