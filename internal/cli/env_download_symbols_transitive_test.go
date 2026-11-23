package cli

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeAppPackage builds bytes shaped like a real AL .app: a 40-byte NAVX header
// (magic, uint32 header length, then padding), a zip holding NavxManifest.xml in
// the namespaced form BC writes, and trailing signature bytes as signed packages
// carry. deps are "Publisher/Name/Version".
func fakeAppPackage(t *testing.T, name string, deps ...string) []byte {
	t.Helper()
	var items strings.Builder
	for _, d := range deps {
		p := strings.SplitN(d, "/", 3)
		fmt.Fprintf(&items, `    <Dependency Id="00000000-0000-0000-0000-000000000000" Name="%s" Publisher="%s" MinVersion="%s" CompatibilityId="0.0.0.0" />`+"\n", p[1], p[0], p[2])
	}
	manifest := "\xef\xbb\xbf" + `<?xml version="1.0" encoding="utf-8"?>
<Package xmlns="http://schemas.microsoft.com/navx/2015/manifest">
  <App Id="11111111-1111-1111-1111-111111111111" Name="` + name + `" Publisher="Test" Version="1.0.0.0" />
  <Dependencies>
` + items.String() + `  </Dependencies>
</Package>`

	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, err := zw.Create("NavxManifest.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(manifest)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	hdr := make([]byte, 40)
	copy(hdr, "NAVX")
	binary.LittleEndian.PutUint32(hdr[4:8], 40)
	copy(hdr[36:], "NAVX")
	out := append(hdr, zbuf.Bytes()...)
	return append(out, []byte("\x00\x01\x02\x03NXSB")...)
}

func TestAppPackageDeps_ReadsNamespacedManifest(t *testing.T) {
	deps, ok := appPackageDeps(fakeAppPackage(t, "Application Test Library",
		"Microsoft/Any/28.5.0.0", "Microsoft/Library Assert/28.5.0.0"))
	if !ok {
		t.Fatal("expected a readable package")
	}
	want := []appDep{
		{Name: "Any", Publisher: "Microsoft", Version: "28.5.0.0"},
		{Name: "Library Assert", Publisher: "Microsoft", Version: "28.5.0.0"},
	}
	if len(deps) != len(want) {
		t.Fatalf("got %+v want %+v", deps, want)
	}
	for i := range want {
		if deps[i] != want[i] {
			t.Errorf("dep %d: got %+v want %+v", i, deps[i], want[i])
		}
	}

	if _, ok := appPackageDeps([]byte("Acme_OtherExt_2.0.0.0")); ok {
		t.Error("a body that is not a .app package must read as not ok")
	}
}

// transitiveHarness serves packages from a map keyed "Publisher/Name"; a missing
// key is a 404. It records every fetch in order.
func transitiveHarness(t *testing.T, pkgs map[string][]byte) (apiURL string, fetched *[]string) {
	t.Helper()
	return transitiveHarnessAt(t, "27.1", pkgs)
}

func transitiveHarnessAt(t *testing.T, bcVersion string, pkgs map[string][]byte) (apiURL string, fetched *[]string) {
	t.Helper()
	var got []string
	apiURL, _ = envSymbolsHarnessAt(t, bcVersion, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		key := q.Get("publisher") + "/" + q.Get("appName")
		got = append(got, key)
		body, ok := pkgs[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	})
	return apiURL, &got
}

// MyExt -> A -> B -> C -> A (cycle). B also needs System Application, which
// app.json's "application" already queued: it must not be fetched a second time.
func TestEnvSymbols_FetchesTransitiveDepsOnceAndTerminatesOnCycle(t *testing.T) {
	pkgs := map[string][]byte{
		"Microsoft/System Application":  fakeAppPackage(t, "System Application"),
		"Microsoft/Base Application":    fakeAppPackage(t, "Base Application"),
		"Microsoft/Business Foundation": fakeAppPackage(t, "Business Foundation"),
		"Microsoft/Application":         fakeAppPackage(t, "Application"),
		"Microsoft/System":              fakeAppPackage(t, "System"),
		"Acme/A":                        fakeAppPackage(t, "A", "Acme/B/1.0.0.0"),
		"Acme/B":                        fakeAppPackage(t, "B", "Acme/C/1.0.0.0", "Microsoft/System Application/28.0.0.0"),
		"Acme/C":                        fakeAppPackage(t, "C", "Acme/A/1.0.0.0"),
	}
	apiURL, fetched := transitiveHarness(t, pkgs)

	dir := t.TempDir()
	writeAppJSON(t, dir, `{
	  "name":"MyExt","publisher":"Foo","version":"1.0.0.0",
	  "application":"28.0.0.0","platform":"28.0.0.0",
	  "dependencies":[{"name":"A","publisher":"Acme","version":"1.0.0.0"}]
	}`)
	outDir := filepath.Join(dir, ".alpackages")

	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "download-symbols", symbolsEnvID,
		"--app-json", filepath.Join(dir, "app.json"), "--out-dir", outDir)
	if err != nil {
		t.Fatalf("env download-symbols: %v", err)
	}

	want := []string{
		"Microsoft/System Application", "Microsoft/Base Application", "Microsoft/Business Foundation",
		"Microsoft/Application", "Microsoft/System", "Acme/A", "Acme/B", "Acme/C",
	}
	if strings.Join(*fetched, "|") != strings.Join(want, "|") {
		t.Fatalf("fetch order:\n got %v\nwant %v", *fetched, want)
	}
	for _, fn := range []string{"Acme_B_1.0.0.0.app", "Acme_C_1.0.0.0.app"} {
		if _, err := os.Stat(filepath.Join(outDir, fn)); err != nil {
			t.Errorf("expected %s written: %v", fn, err)
		}
	}
}

func TestEnvSymbols_MissingTransitiveDepNamesTheChain(t *testing.T) {
	pkgs := map[string][]byte{
		"Acme/A": fakeAppPackage(t, "A", "Acme/B/1.0.0.0"),
		"Acme/B": fakeAppPackage(t, "B", "Acme/C/1.0.0.0"),
	}
	apiURL, _ := transitiveHarness(t, pkgs)

	dir := t.TempDir()
	writeAppJSON(t, dir, `{
	  "name":"MyExt","publisher":"Foo","version":"1.0.0.0",
	  "dependencies":[{"name":"A","publisher":"Acme","version":"1.0.0.0"}]
	}`)

	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "download-symbols", symbolsEnvID,
		"--app-json", filepath.Join(dir, "app.json"), "--out-dir", filepath.Join(dir, ".alpackages"))
	if err == nil {
		t.Fatal("expected an error for the missing transitive dependency")
	}
	if !strings.Contains(err.Error(), "MyExt needs A needs B needs C") {
		t.Fatalf("error does not name the chain: %v", err)
	}
	if !strings.Contains(err.Error(), "Acme_C 1.0.0.0") {
		t.Fatalf("error does not name the missing app: %v", err)
	}
}

// A package already in --out-dir is skipped, but its dependencies still count:
// a re-run after adding one direct dependency must not miss what it needs.
func TestEnvSymbols_AlreadyPresentPackageStillExpandsItsDeps(t *testing.T) {
	pkgs := map[string][]byte{
		"Acme/B": fakeAppPackage(t, "B"),
	}
	apiURL, fetched := transitiveHarness(t, pkgs)

	dir := t.TempDir()
	writeAppJSON(t, dir, `{
	  "name":"MyExt","publisher":"Foo","version":"1.0.0.0",
	  "dependencies":[{"name":"A","publisher":"Acme","version":"1.0.0.0"}]
	}`)
	outDir := filepath.Join(dir, ".alpackages")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "Acme_A_1.0.0.0.app"), fakeAppPackage(t, "A", "Acme/B/1.0.0.0"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "download-symbols", symbolsEnvID,
		"--app-json", filepath.Join(dir, "app.json"), "--out-dir", outDir)
	if err != nil {
		t.Fatalf("env download-symbols: %v", err)
	}
	if strings.Join(*fetched, "|") != "Acme/B" {
		t.Fatalf("expected only Acme/B fetched, got %v", *fetched)
	}
}
