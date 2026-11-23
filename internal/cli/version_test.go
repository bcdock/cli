package cli

import (
	"encoding/json"
	"testing"
)

// #517: `version` carries the build's commit AND that commit's date, as stamped at build time.
func TestVersion_JSON_CarriesCommitAndCommitDate(t *testing.T) {
	oldV, oldC, oldD := version, commit, commitDate
	t.Cleanup(func() { version, commit, commitDate = oldV, oldC, oldD })
	version, commit, commitDate = "0.0.0-test", "abc1234", "2026-10-01T19:57:18+10:00"

	stdout, _, err := RunCmd(t, "version", "-o", "json")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("decode %q: %v", stdout, err)
	}
	want := map[string]string{"version": "0.0.0-test", "commit": "abc1234", "commitDate": "2026-10-01T19:57:18+10:00", "api": "v1"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (body %s)", k, got[k], v, stdout)
		}
	}
}

// Unstamped (a plain go build): both say "unknown" rather than an empty or invented value.
func TestVersion_Unstamped_SaysUnknown(t *testing.T) {
	stdout, _, err := RunCmd(t, "version", "-o", "json")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	var got map[string]string
	_ = json.Unmarshal([]byte(stdout), &got)
	if got["commit"] != "unknown" || got["commitDate"] != "unknown" {
		t.Errorf("unstamped test binary: commit %q, commitDate %q, want both unknown", got["commit"], got["commitDate"])
	}
}
