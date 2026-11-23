package cli

import (
	"strconv"
	"strings"
)

// knownAppMove is an app Microsoft moved or retired from sandbox envs in a given
// BC version, so a dependency that still names it is not found there (#1001).
// The CLI only ever HINTS: it never fetches the replacement under a name the
// user did not declare, and app.json stays theirs to change.
type knownAppMove struct {
	Publisher string
	OldName   string
	NewName   string
	SinceBC   int // first BC major where the move applies; older envs still have the old app
	Hint      string
}

// knownAppMoves is the one place these live. Each entry says where it was
// verified; add an entry only for a move you can show in real packages.
var knownAppMoves = []knownAppMove{
	{
		// Verified 2026-11-23 against the BC 28.5.54151.56140 (AU) and 29.0.47162.0 (US)
		// sandbox artifacts. "Tests-TestLibraries" keeps its id (5d86850b) but is
		// Target=OnPrem and absent from the sandbox Extensions set; its cloud libraries
		// (Library - Sales, Library - ERM, ...) are in the new Target=Cloud
		// "Application Test Library" (id d852d5d2), which Tests-TestLibraries now depends on.
		Publisher: "Microsoft",
		OldName:   "Tests-TestLibraries",
		NewName:   "Application Test Library",
		SinceBC:   28,
		Hint: `"Tests-TestLibraries" is OnPrem-only from BC 28 and not installed on sandbox envs; ` +
			`its cloud test libraries moved to "Application Test Library" - depend on that in app.json`,
	},
}

// appMoveHint returns the hint for a dependency the env did not have, or "" when
// the app is not a known move on this env's BC version (the error is then exactly
// what it was before). An env whose version cannot be read gets no hint: pointing a
// BC 27 user at an app their env does not have is worse than saying nothing.
func appMoveHint(publisher, name, envBcVersion string) string {
	major, ok := bcMajor(envBcVersion)
	if !ok {
		return ""
	}
	for _, m := range knownAppMoves {
		if major >= m.SinceBC && strings.EqualFold(m.Publisher, publisher) && strings.EqualFold(m.OldName, name) {
			return m.Hint
		}
	}
	return ""
}

// bcMajor reads the major from an env's BC version ("28.5", "28.5.54151.56140", "28").
func bcMajor(v string) (int, bool) {
	head, _, _ := strings.Cut(strings.TrimSpace(v), ".")
	n, err := strconv.Atoi(head)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
