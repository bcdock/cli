package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DEV-024 v1: declarative environment recipes.
//
// A manifest describes an environment the way a Dockerfile describes an image: the
// BC version to build on and the apps to publish into it, checked into the caller's
// git repo. `bcdock env create --manifest` is CLI orchestration over verbs that
// already exist - there is no API change and no new endpoint.
//
// WHY A SCHEMA FIELD. The format will grow (data seeding, appsource apps, post
// scripts are all v1.1). A manifest written for a later schema must be REFUSED by
// an older CLI rather than silently half-applied: a v1 binary that ignored an
// unknown `data:` block would create the environment and skip the seed, which
// looks like success. Unknown versions and unknown keys are both hard errors.

// manifestSchemaV1 is the only schema this binary accepts.
const manifestSchemaV1 = "bcdock-env/v1"

// Manifest is a parsed and validated bcdock.manifest.yaml.
type Manifest struct {
	Schema       string        `yaml:"schema"`
	BCVersion    string        `yaml:"bcVersion"`
	Country      string        `yaml:"country"`
	ArtifactType string        `yaml:"artifactType"`
	Name         string        `yaml:"name"`
	Region       string        `yaml:"region"`
	MultiTenant  *bool         `yaml:"multiTenant"`
	Apps         []ManifestApp `yaml:"apps"`

	// Dir is the directory containing the manifest. Relative `file:` paths resolve
	// against it, NOT against the process working directory - a recipe checked into
	// a repo must behave the same regardless of where the caller runs bcdock from.
	Dir string `yaml:"-"`
	// Path is the manifest path as supplied, for error messages.
	Path string `yaml:"-"`
}

// ManifestApp is one entry of the apps: list. Exactly one source must be set.
type ManifestApp struct {
	// File is a path to a .app on disk, relative to the manifest's directory.
	File string `yaml:"file"`
	// GitHub is "owner/repo@tag" and requires Asset.
	GitHub string `yaml:"github"`
	// Asset is the release asset filename to download from the GitHub release.
	Asset string `yaml:"asset"`
}

// Source returns a short human description of where the app comes from, for
// progress output and error messages.
func (a ManifestApp) Source() string {
	if a.File != "" {
		return a.File
	}
	return a.GitHub + " (" + a.Asset + ")"
}

// ResolvedFile returns the absolute path of a file: app, resolved against the
// manifest directory. Empty for github apps.
func (a ManifestApp) ResolvedFile(dir string) string {
	if a.File == "" {
		return ""
	}
	if filepath.IsAbs(a.File) {
		return a.File
	}
	return filepath.Join(dir, a.File)
}

// LoadManifest reads, parses and fully validates a manifest.
//
// Everything that can be checked without the network is checked HERE, before the
// caller makes any API call. A manifest naming a .app that does not exist must fail
// with nothing created - the expensive failure mode is an environment that provisions
// for twenty-five minutes and then cannot publish.
func LoadManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read manifest %s: %w", path, err)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		// Fall back to the supplied path; only affects relative-path resolution.
		abs = path
	}

	var m Manifest
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	// Reject unknown keys. A typo'd `bcversion:` would otherwise leave BCVersion
	// empty and produce a confusing "bcVersion is required" against a file that
	// visibly contains it.
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("cannot parse manifest %s: %w", path, err)
	}

	m.Path = path
	m.Dir = filepath.Dir(abs)

	if err := m.validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// validate enforces the v1 contract. Errors name the manifest and the offending
// field so the message is actionable without opening the file.
func (m *Manifest) validate() error {
	if m.Schema == "" {
		return fmt.Errorf("%s: schema is required (expected %q)", m.Path, manifestSchemaV1)
	}
	if m.Schema != manifestSchemaV1 {
		return fmt.Errorf("%s: unsupported schema %q - this bcdock supports %q. Upgrade the CLI or change the manifest",
			m.Path, m.Schema, manifestSchemaV1)
	}
	if strings.TrimSpace(m.BCVersion) == "" {
		return fmt.Errorf("%s: bcVersion is required", m.Path)
	}
	if strings.TrimSpace(m.Country) == "" {
		return fmt.Errorf("%s: country is required", m.Path)
	}
	if m.ArtifactType != "" {
		switch strings.ToLower(m.ArtifactType) {
		case "sandbox", "onprem":
		default:
			return fmt.Errorf("%s: artifactType must be Sandbox or OnPrem, got %q", m.Path, m.ArtifactType)
		}
	}

	for i, a := range m.Apps {
		// Position is 1-based in messages: users count apps from one, and the
		// publish progress output uses the same numbering.
		n := i + 1
		hasFile := strings.TrimSpace(a.File) != ""
		hasGitHub := strings.TrimSpace(a.GitHub) != ""

		switch {
		case hasFile && hasGitHub:
			return fmt.Errorf("%s: apps[%d] sets both file: and github: - use one", m.Path, n)
		case !hasFile && !hasGitHub:
			return fmt.Errorf("%s: apps[%d] needs either file: or github:", m.Path, n)
		}

		if hasFile {
			if a.Asset != "" {
				return fmt.Errorf("%s: apps[%d] sets asset: with file: - asset: belongs to github:", m.Path, n)
			}
			// The check that matters: a manifest can be structurally valid and still
			// name a .app that is not there. Catch it now, not after provisioning.
			resolved := a.ResolvedFile(m.Dir)
			// Stat, then insist it is a REGULAR file: os.Stat succeeds on a
			// directory, so `file: ./build` would validate here and fail later at
			// os.ReadFile, past the point where nothing has been created
			// (Ravi, #280).
			info, err := os.Stat(resolved)
			if err != nil {
				return fmt.Errorf("%s: apps[%d] file %q not found (resolved to %s)", m.Path, n, a.File, resolved)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%s: apps[%d] file %q is not a regular file (resolved to %s)", m.Path, n, a.File, resolved)
			}
			continue
		}

		owner, repo, tag, err := parseGitHubRef(a.GitHub)
		if err != nil {
			return fmt.Errorf("%s: apps[%d] %w", m.Path, n, err)
		}
		_, _, _ = owner, repo, tag
		if strings.TrimSpace(a.Asset) == "" {
			return fmt.Errorf("%s: apps[%d] github: requires asset: (the release asset filename, e.g. MyApp.app)", m.Path, n)
		}
	}

	return nil
}

// parseGitHubRef splits "owner/repo@tag". The tag is required rather than defaulting
// to the latest release: a manifest is a recipe, and a recipe that resolves to a
// different artifact tomorrow is not reproducible.
func parseGitHubRef(ref string) (owner, repo, tag string, err error) {
	at := strings.LastIndex(ref, "@")
	if at < 0 {
		return "", "", "", fmt.Errorf("github %q must be owner/repo@tag", ref)
	}
	repoPart, tag := ref[:at], ref[at+1:]
	if strings.TrimSpace(tag) == "" {
		return "", "", "", fmt.Errorf("github %q must be owner/repo@tag (tag is empty)", ref)
	}
	slash := strings.Index(repoPart, "/")
	if slash < 0 {
		return "", "", "", fmt.Errorf("github %q must be owner/repo@tag", ref)
	}
	owner, repo = repoPart[:slash], repoPart[slash+1:]
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(repo) == "" || strings.Contains(repo, "/") {
		return "", "", "", fmt.Errorf("github %q must be owner/repo@tag", ref)
	}
	return owner, repo, tag, nil
}
