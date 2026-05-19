package cli

import (
	"archive/zip"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bcdock/cli/internal/output"
	"github.com/spf13/cobra"
)

var alCmd = &cobra.Command{
	Use:   "al",
	Short: "Compile AL projects using the alc that matches the target BC env",
	Long: `Run alc.exe versioned to a specific BC environment, by extracting the
ALLanguage.vsix that BcContainerHelper bundled with that env's BC platform.

The vsix is served anonymously by every BC container on its downloads endpoint
(env.downloadsUrl/ALLanguage.vsix) - same binary BcContainerHelper unpacks
during container setup, so it's always version-matched to the env's platform.

Exit codes:
  0   ok
  1   general error`,
	Example: `  bcdock al compile --env my-env`,
}

var alCompileCmd = &cobra.Command{
	Use:   "compile",
	Short: "Compile an AL project using the env-matched alc",
	Long: `Download the env's bundled AL Language extension (cached), extract the
matching alc binary, and run it against the AL project.

Caching: the vsix is keyed by the env's platformVersion (falling back to
bcVersion when null) and stored under ${BCDOCK_AL_CACHE:-$XDG_CACHE_HOME/bcdock/al-vsix}.
Re-use across envs on the same BC platform is automatic. Use --refresh to
force re-download.

Args after a literal '--' are forwarded verbatim to alc, so anything alc
supports (e.g. /generatecode, /analyzer, /ruleset) goes through unmodified.

Exit codes:
  0   ok
  1   general error (compile failed, download failed, env not running)
  3   auth failure (missing or invalid token)
  5   environment not found`,
	Example: `  bcdock al compile --env my-env
  bcdock al compile --env my-env --out build/MyApp_1.0.0.0.app
  bcdock al compile --env my-env --refresh
  bcdock al compile --env my-env -- /generatecode+ /errorlog:build/diag.log`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		envArg, _ := cmd.Flags().GetString("env")
		project, _ := cmd.Flags().GetString("project")
		packageCache, _ := cmd.Flags().GetString("package-cache")
		out, _ := cmd.Flags().GetString("out")
		refresh, _ := cmd.Flags().GetBool("refresh")
		insecure, _ := cmd.Flags().GetBool("insecure")
		timeout, _ := cmd.Flags().GetDuration("timeout")

		if envArg == "" {
			return fmt.Errorf("--env is required (the env whose BC platform's alc to use)")
		}

		id, err := resolveEnvID(cmd.Context(), r.Client, envArg)
		if err != nil {
			return err
		}
		var env environment
		if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/environments/"+id, nil, &env); err != nil {
			return err
		}
		if env.DownloadsUrl == nil || *env.DownloadsUrl == "" {
			return fmt.Errorf("environment %q has no downloads URL (status: %s) - env must be running",
				envArg, env.Status)
		}

		alcPath, err := ensureEnvALC(cmd.Context(), r.Printer, env, refresh, insecure, timeout)
		if err != nil {
			return err
		}

		alcArgs := buildALCArgs(project, packageCache, out, args)
		r.Printer.Info("Running %s %s", alcPath, strings.Join(alcArgs, " "))

		c := exec.CommandContext(cmd.Context(), alcPath, alcArgs...)
		// Route through the printer so test capture and `--output` plumbing work.
		// On a TTY these are os.Stdout/os.Stderr (set by cobra), so streaming is real-time.
		c.Stdout = r.Printer.W
		c.Stderr = r.Printer.Err
		c.Stdin = os.Stdin
		return c.Run()
	},
}

// buildALCArgs assembles the alc.exe invocation. Anything after `--` (passthrough)
// is appended verbatim so users can reach alc flags we don't model directly.
func buildALCArgs(project, packageCache, out string, passthrough []string) []string {
	a := []string{}
	if project != "" {
		a = append(a, "/project:"+project)
	}
	if packageCache != "" {
		a = append(a, "/packagecachepath:"+packageCache)
	}
	if out != "" {
		a = append(a, "/out:"+out)
	}
	a = append(a, passthrough...)
	return a
}

// ensureEnvALC downloads + extracts ALLanguage.vsix for the given env (cached)
// and returns the absolute path to the alc executable for the current OS.
func ensureEnvALC(ctx context.Context, p *output.Printer, env environment, refresh, insecure bool, timeout time.Duration) (string, error) {
	cacheKey := derefStr(env.PlatformVersion)
	if cacheKey == "" {
		cacheKey = env.BcVersion
	}
	if cacheKey == "" {
		return "", fmt.Errorf("env has neither platformVersion nor bcVersion - cannot key cache")
	}

	cacheDir := alVsixCacheDir(cacheKey)
	alcPath := filepath.Join(cacheDir, "extension", alcSubpath())

	if !refresh {
		if _, err := os.Stat(alcPath); err == nil {
			return alcPath, nil
		}
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}

	vsixURL := strings.TrimRight(*env.DownloadsUrl, "/") + "/ALLanguage.vsix"
	vsixPath := filepath.Join(cacheDir, "ALLanguage.vsix")

	p.Info("Fetching env-matched alc (BC %s) from %s", cacheKey, vsixURL)
	if err := downloadFile(ctx, vsixURL, vsixPath, insecure, timeout); err != nil {
		return "", fmt.Errorf("download vsix: %w", err)
	}

	p.Info("Extracting %s → %s", filepath.Base(vsixPath), cacheDir)
	if err := extractZip(vsixPath, cacheDir); err != nil {
		return "", fmt.Errorf("extract vsix: %w", err)
	}

	if _, err := os.Stat(alcPath); err != nil {
		return "", fmt.Errorf("alc not found at %s after extraction (vsix layout changed?): %w", alcPath, err)
	}

	// vsix entries don't preserve POSIX exec bits - set them on the unix binaries.
	if runtime.GOOS != "windows" {
		_ = os.Chmod(alcPath, 0o755)
	}

	return alcPath, nil
}

// alcSubpath returns the platform-specific path inside the unpacked vsix.
// Layout (from the AL Language extension): extension/bin/{linux|darwin|win32}/alc[.exe]
func alcSubpath() string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join("bin", "win32", "alc.exe")
	case "darwin":
		return filepath.Join("bin", "darwin", "alc")
	default: // linux + bsd-likes
		return filepath.Join("bin", "linux", "alc")
	}
}

func alVsixCacheDir(version string) string {
	if env := os.Getenv("BCDOCK_AL_CACHE"); env != "" {
		return filepath.Join(env, version)
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = filepath.Join(os.TempDir(), "bcdock-cache")
	}
	return filepath.Join(base, "bcdock", "al-vsix", version)
}

func downloadFile(ctx context.Context, url, dest string, insecure bool, timeout time.Duration) error {
	tr := &http.Transport{}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // gated by --insecure
	}
	client := &http.Client{Transport: tr, Timeout: timeout}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("GET %s: %d %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

func extractZip(src, destDir string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		// Reject path-traversal entries that would write outside destDir.
		path := filepath.Join(destDir, f.Name)
		if !strings.HasPrefix(path, filepath.Clean(destDir)+string(os.PathSeparator)) && path != filepath.Clean(destDir) {
			return fmt.Errorf("zip entry escapes destination: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		w, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			_ = w.Close()
			return err
		}
		if _, err := io.Copy(w, rc); err != nil { //nolint:gosec // file size bounded by zip entry; CLI-only context
			_ = w.Close()
			_ = rc.Close()
			return err
		}
		_ = rc.Close()
		_ = w.Close()
	}
	return nil
}

func init() {
	alCompileCmd.Flags().String("env", "", "Env whose BC platform's alc to use (required)")
	alCompileCmd.Flags().String("project", ".", "AL project directory passed to alc as /project:")
	alCompileCmd.Flags().String("package-cache", ".alpackages", "Symbol cache directory passed as /packagecachepath:")
	alCompileCmd.Flags().String("out", "", "Output .app file passed as /out: (alc derives the default from app.json if empty)")
	alCompileCmd.Flags().Bool("refresh", false, "Re-download the vsix even if the cached copy exists")
	alCompileCmd.Flags().Bool("insecure", false, "Skip TLS verification for the downloads endpoint (self-signed certs)")
	alCompileCmd.Flags().Duration("timeout", 5*time.Minute, "vsix download timeout")

	alCmd.AddCommand(alCompileCmd)
	RootCmd.AddCommand(alCmd)
}
