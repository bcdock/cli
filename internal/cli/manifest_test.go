package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeManifest drops a manifest (and any named .app fixtures) into a fresh temp
// dir and returns the manifest path. Fixtures are created in the manifest's dir so
// relative-path resolution is exercised against a real file.
func writeManifest(t *testing.T, body string, appFixtures ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range appFixtures {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir fixture: %v", err)
		}
		if err := os.WriteFile(p, []byte("not-a-real-app"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	path := filepath.Join(dir, "bcdock.manifest.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

func TestLoadManifest_Valid_ParsesAllFields(t *testing.T) {
	path := writeManifest(t, `
schema: bcdock-env/v1
bcVersion: 27.1.41600.0
country: au
artifactType: Sandbox
multiTenant: false
apps:
  - file: ./out/MyApp.app
  - github: acme/bc-tools@v1.4.0
    asset: BcTools.app
`, "out/MyApp.app")

	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.BCVersion != "27.1.41600.0" || m.Country != "au" || m.ArtifactType != "Sandbox" {
		t.Fatalf("fields not parsed: %+v", m)
	}
	if m.MultiTenant == nil || *m.MultiTenant {
		t.Fatalf("multiTenant should parse as explicit false, got %v", m.MultiTenant)
	}
	if len(m.Apps) != 2 {
		t.Fatalf("want 2 apps, got %d", len(m.Apps))
	}
}

// multiTenant absent must be distinguishable from multiTenant: false, because the
// precedence rule is env var > flag > manifest > default. A plain bool would make
// an unset manifest silently assert single-tenant and beat the built-in default.
func TestLoadManifest_MultiTenantUnset_IsNilNotFalse(t *testing.T) {
	path := writeManifest(t, `
schema: bcdock-env/v1
bcVersion: 27.1.41600.0
country: au
`)
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.MultiTenant != nil {
		t.Fatalf("unset multiTenant must stay nil, got %v", *m.MultiTenant)
	}
}

// THE PATH THAT PRODUCED THE NEED. A manifest can be structurally perfect and still
// name a .app that is not on disk. If that is only discovered at publish time the
// user has already waited out a provision. It must fail at load.
func TestLoadManifest_FileAppMissing_FailsAtLoad(t *testing.T) {
	path := writeManifest(t, `
schema: bcdock-env/v1
bcVersion: 27.1.41600.0
country: au
apps:
  - file: ./out/Missing.app
`)
	_, err := LoadManifest(path)
	if err == nil {
		t.Fatal("want an error for a missing .app, got nil")
	}
	if !strings.Contains(err.Error(), "Missing.app") {
		t.Fatalf("error must name the missing file, got: %v", err)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error must say the file was not found, got: %v", err)
	}
}

// Relative paths resolve against the MANIFEST's directory, not the process cwd -
// a recipe checked into a repo must behave the same from any working directory.
// This test would pass spuriously if resolution used cwd and the test happened to
// run from the manifest dir, so it deliberately chdirs somewhere else first.
func TestLoadManifest_RelativePath_ResolvesFromManifestDirNotCwd(t *testing.T) {
	path := writeManifest(t, `
schema: bcdock-env/v1
bcVersion: 27.1.41600.0
country: au
apps:
  - file: ./out/MyApp.app
`, "out/MyApp.app")

	// Run from a directory that does NOT contain out/MyApp.app.
	elsewhere := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(elsewhere); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest from a different cwd: %v", err)
	}
	got := m.Apps[0].ResolvedFile(m.Dir)
	if !strings.HasPrefix(got, filepath.Dir(path)) {
		t.Fatalf("resolved %q is not under the manifest dir %q", got, filepath.Dir(path))
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("resolved path does not exist: %v", err)
	}
}

// Publish order is the manifest's order - v1 does not resolve AL dependencies, so
// the author controls sequencing by listing apps in dependency order.
func TestLoadManifest_AppOrderPreserved(t *testing.T) {
	path := writeManifest(t, `
schema: bcdock-env/v1
bcVersion: 27.1.41600.0
country: au
apps:
  - file: ./a.app
  - file: ./b.app
  - file: ./c.app
`, "a.app", "b.app", "c.app")

	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	want := []string{"./a.app", "./b.app", "./c.app"}
	for i, w := range want {
		if m.Apps[i].File != w {
			t.Fatalf("app %d: want %q, got %q - manifest order must be preserved", i, w, m.Apps[i].File)
		}
	}
}

func TestLoadManifest_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantSub string
	}{
		{
			name:    "unknown schema version",
			body:    "schema: bcdock-env/v2\nbcVersion: 27.1.41600.0\ncountry: au\n",
			wantSub: "unsupported schema",
		},
		{
			name:    "missing schema",
			body:    "bcVersion: 27.1.41600.0\ncountry: au\n",
			wantSub: "schema is required",
		},
		{
			name:    "missing bcVersion",
			body:    "schema: bcdock-env/v1\ncountry: au\n",
			wantSub: "bcVersion is required",
		},
		{
			name:    "missing country",
			body:    "schema: bcdock-env/v1\nbcVersion: 27.1.41600.0\n",
			wantSub: "country is required",
		},
		{
			name:    "bad artifactType",
			body:    "schema: bcdock-env/v1\nbcVersion: 27.1.41600.0\ncountry: au\nartifactType: Container\n",
			wantSub: "artifactType must be Sandbox or OnPrem",
		},
		{
			// A typo'd key must not silently leave the field empty and report the
			// field as missing against a file that visibly contains it.
			name:    "unknown key",
			body:    "schema: bcdock-env/v1\nbcversion: 27.1.41600.0\ncountry: au\n",
			wantSub: "cannot parse manifest",
		},
		{
			name:    "app with neither source",
			body:    "schema: bcdock-env/v1\nbcVersion: 27.1.41600.0\ncountry: au\napps:\n  - asset: X.app\n",
			wantSub: "needs either file: or github:",
		},
		{
			name:    "app with both sources",
			body:    "schema: bcdock-env/v1\nbcVersion: 27.1.41600.0\ncountry: au\napps:\n  - file: ./a.app\n    github: o/r@v1\n",
			wantSub: "sets both file: and github:",
		},
		{
			name:    "github without asset",
			body:    "schema: bcdock-env/v1\nbcVersion: 27.1.41600.0\ncountry: au\napps:\n  - github: acme/tools@v1\n",
			wantSub: "requires asset:",
		},
		{
			name:    "github without tag",
			body:    "schema: bcdock-env/v1\nbcVersion: 27.1.41600.0\ncountry: au\napps:\n  - github: acme/tools\n    asset: X.app\n",
			wantSub: "owner/repo@tag",
		},
		{
			name:    "github without owner",
			body:    "schema: bcdock-env/v1\nbcVersion: 27.1.41600.0\ncountry: au\napps:\n  - github: tools@v1\n    asset: X.app\n",
			wantSub: "owner/repo@tag",
		},
		{
			name:    "github with empty tag",
			body:    "schema: bcdock-env/v1\nbcVersion: 27.1.41600.0\ncountry: au\napps:\n  - github: acme/tools@\n    asset: X.app\n",
			wantSub: "tag is empty",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeManifest(t, tc.body)
			_, err := LoadManifest(path)
			if err == nil {
				t.Fatalf("want an error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("want error containing %q, got: %v", tc.wantSub, err)
			}
		})
	}
}

func TestParseGitHubRef(t *testing.T) {
	owner, repo, tag, err := parseGitHubRef("acme/bc-tools@v1.4.0")
	if err != nil {
		t.Fatalf("parseGitHubRef: %v", err)
	}
	if owner != "acme" || repo != "bc-tools" || tag != "v1.4.0" {
		t.Fatalf("got %q/%q@%q", owner, repo, tag)
	}
	// A tag may itself contain '@' in theory; we split on the LAST one so the repo
	// part stays intact.
	_, _, tag, err = parseGitHubRef("acme/bc-tools@release@2")
	if err != nil {
		t.Fatalf("parseGitHubRef with @ in tag: %v", err)
	}
	if tag != "2" {
		t.Fatalf("want tag %q, got %q", "2", tag)
	}
}

// Ravi, #280: os.Stat succeeds on a DIRECTORY, so `file: ./build` validated here
// and failed later at os.ReadFile - past the point where nothing had been created,
// which is the whole property this check exists to hold.
func TestLoadManifest_FileAppIsADirectory_Rejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "build"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "bcdock.manifest.yaml")
	if err := os.WriteFile(path, []byte(`
schema: bcdock-env/v1
bcVersion: 27.1.41600.0
country: au
apps:
  - file: ./build
`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := LoadManifest(path)
	if err == nil {
		t.Fatal("want an error for a directory in file:, got nil")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error must say it is not a regular file, got: %v", err)
	}
}
