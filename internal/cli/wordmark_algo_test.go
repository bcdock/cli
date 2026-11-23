package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The AL-Go overrides module lives in the monorepo's docs-site, which the public
// bcdock/cli mirror does not carry. The test skips only when docs-site itself is
// absent; in the monorepo a moved module or a renamed banner fails it.
const algoDocsSite = "../../../../docs-site"
const algoModule = algoDocsSite + "/public/docs/al-go/overrides/bcdock/BCDock.AlGo.psm1"

func TestWordmark_MatchesAlGoBanner(t *testing.T) {
	if _, err := os.Stat(algoDocsSite); os.IsNotExist(err) {
		t.Skipf("%s not present (public mirror); the sync check runs in the monorepo", filepath.Clean(algoDocsSite))
	}
	raw, err := os.ReadFile(algoModule)
	if err != nil {
		t.Fatalf("read AL-Go module: %v", err)
	}
	// The banner is the single-quoted here-string inside Write-BcDockBanner:
	//   $lines = @'
	//   <art>
	//   '@ -split ...
	m := regexp.MustCompile(`(?s)function Write-BcDockBanner \{.*?\$lines = @'\r?\n(.*?)\r?\n'@`).FindSubmatch(raw)
	if m == nil {
		t.Fatalf("Write-BcDockBanner here-string not found in %s", algoModule)
	}
	algo := strings.ReplaceAll(string(m[1]), "\r\n", "\n")
	if algo != bcdockWordmark {
		t.Errorf("CLI wordmark differs from the AL-Go banner.\nCLI:\n%s\nAL-Go:\n%s", bcdockWordmark, algo)
	}
}
