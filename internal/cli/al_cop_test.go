package cli

import (
	"archive/zip"
	"bytes"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// vsixAnalyzerFiles are the analyzer entries of a real ALLanguage.vsix (BC 27.0 to
// 28.0, listed from the extracted cache): the four cops plus the common assembly
// they load through, all under extension/bin/Analyzers.
var vsixAnalyzerFiles = []string{
	"extension/bin/Analyzers/Microsoft.Dynamics.Nav.Analyzers.Common.dll",
	"extension/bin/Analyzers/Microsoft.Dynamics.Nav.CodeCop.dll",
	"extension/bin/Analyzers/Microsoft.Dynamics.Nav.AppSourceCop.dll",
	"extension/bin/Analyzers/Microsoft.Dynamics.Nav.PerTenantExtensionCop.dll",
	"extension/bin/Analyzers/Microsoft.Dynamics.Nav.UICop.dll",
}

// makeFakeVsixWithAnalyzers is makeFakeVsix plus the analyzer layout above.
func makeFakeVsixWithAnalyzers(t *testing.T) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(makeFakeVsix(t)), int64(len(makeFakeVsix(t))))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		w, _ := zw.Create(f.Name)
		rc, _ := f.Open()
		var b bytes.Buffer
		_, _ = b.ReadFrom(rc)
		_ = rc.Close()
		_, _ = w.Write(b.Bytes())
	}
	for _, name := range vsixAnalyzerFiles {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte("MZ fake analyzer"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func analyzerArg(cacheDir, dll string) string {
	return "/analyzer:" + filepath.Join(cacheDir, "27.1.41698.0", "extension", "bin", "Analyzers", dll)
}

func TestALCompile_Cop_ResolvesEachNameToItsAnalyzerInTheVsix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only stub")
	}
	for _, tc := range []struct{ flag, dll string }{
		{"CodeCop", "Microsoft.Dynamics.Nav.CodeCop.dll"},
		{"appsourcecop", "Microsoft.Dynamics.Nav.AppSourceCop.dll"},
		{"PerTenantExtensionCop", "Microsoft.Dynamics.Nav.PerTenantExtensionCop.dll"},
		{"UICOP", "Microsoft.Dynamics.Nav.UICop.dll"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			cacheDir := withAlCacheDir(t)
			apiURL, _ := alHarness(t, makeFakeVsixWithAnalyzers(t))
			stdout, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
				"al", "compile", "--env", alEnvID, "--out", "/tmp/o.app", "--cop", tc.flag)
			if err != nil {
				t.Fatalf("al compile: %v", err)
			}
			want := analyzerArg(cacheDir, "Microsoft.Dynamics.Nav.Analyzers.Common.dll") + " " + analyzerArg(cacheDir, tc.dll)
			if !strings.Contains(stdout, want) {
				t.Errorf("want %q in alc args, got: %s", want, stdout)
			}
			if n := strings.Count(stdout, "/analyzer:"); n != 2 {
				t.Errorf("want exactly 2 /analyzer args (common + %s), got %d: %s", tc.dll, n, stdout)
			}
		})
	}
}

func TestALCompile_Cop_AllFourInStableOrderBeforePassthrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only stub")
	}
	cacheDir := withAlCacheDir(t)
	apiURL, _ := alHarness(t, makeFakeVsixWithAnalyzers(t))
	stdout, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"al", "compile", "--env", alEnvID, "--out", "/tmp/o.app",
		"--cop", "UICop,CodeCop", "--cop", "PerTenantExtensionCop", "--cop", "AppSourceCop", "--cop", "codecop",
		"--", "/ruleset:my.ruleset.json")
	if err != nil {
		t.Fatalf("al compile: %v", err)
	}
	want := strings.Join([]string{
		analyzerArg(cacheDir, "Microsoft.Dynamics.Nav.Analyzers.Common.dll"),
		analyzerArg(cacheDir, "Microsoft.Dynamics.Nav.CodeCop.dll"),
		analyzerArg(cacheDir, "Microsoft.Dynamics.Nav.AppSourceCop.dll"),
		analyzerArg(cacheDir, "Microsoft.Dynamics.Nav.PerTenantExtensionCop.dll"),
		analyzerArg(cacheDir, "Microsoft.Dynamics.Nav.UICop.dll"),
		"/ruleset:my.ruleset.json",
	}, " ")
	if !strings.Contains(stdout, want) {
		t.Errorf("want\n%s\ngot\n%s", want, stdout)
	}
}

func TestALCompile_Cop_UnknownNameRefusedBeforeDownload(t *testing.T) {
	withAlCacheDir(t)
	apiURL, calls := alHarness(t, makeFakeVsixWithAnalyzers(t))
	_, stderr, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"al", "compile", "--env", alEnvID, "--cop", "LinterCop")
	if err == nil {
		t.Fatal("expected an unknown cop to be refused")
	}
	if msg := err.Error() + stderr; !strings.Contains(msg, `unknown --cop "LinterCop"`) || !strings.Contains(msg, "PerTenantExtensionCop") {
		t.Errorf("want the unknown name and the valid list, got: %s", msg)
	}
	if *calls != 0 {
		t.Errorf("want no vsix download for a refused command, got %d", *calls)
	}
}

func TestALCompile_Cop_NoneAsked_AddsNoAnalyzer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only stub")
	}
	withAlCacheDir(t)
	apiURL, _ := alHarness(t, makeFakeVsixWithAnalyzers(t))
	stdout, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"al", "compile", "--env", alEnvID, "--out", "/tmp/o.app")
	if err != nil {
		t.Fatalf("al compile: %v", err)
	}
	if strings.Contains(stdout, "/analyzer:") {
		t.Errorf("want no /analyzer without --cop, got: %s", stdout)
	}
	if !strings.Contains(stdout, "FAKE_ALC /project:. /packagecachepath:.alpackages /out:/tmp/o.app\n") {
		t.Errorf("want the unchanged argument line, got: %s", stdout)
	}
}

func TestALCompile_Cop_MissingAnalyzerIsAnError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only stub")
	}
	withAlCacheDir(t)
	apiURL, _ := alHarness(t, makeFakeVsix(t)) // no analyzers in this vsix
	_, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"al", "compile", "--env", alEnvID, "--cop", "CodeCop")
	if err == nil || !strings.Contains(err.Error(), "Microsoft.Dynamics.Nav.CodeCop.dll not found") {
		t.Fatalf("want a not-found error naming the analyzer, got: %v", err)
	}
}
