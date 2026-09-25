package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// CLI-041. `env create --wait` and `env resume` used to print envRow - a DISPLAY struct with
// only header: tags - for -o json, so encoding/json emitted Go field names (Name, ShortID)
// and no id at all. The same command returned two schemas depending on --wait. These pin
// that -o json is the environment record, the same schema `env get -o json` emits.

// assertEnvRecordJSON decodes stdout and checks it is the record, not the row: camelCase id
// and shortId present, and none of envRow's Go field names.
func assertEnvRecordJSON(t *testing.T, stdout, wantID string) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not a JSON object: %v\n%s", err, stdout)
	}
	if got["id"] != wantID {
		t.Errorf(`"id" = %v, want %s - scripts read .id (jq -e '.id')`, got["id"], wantID)
	}
	if got["shortId"] == nil {
		t.Errorf(`"shortId" missing: %v`, got)
	}
	// Fields only the RECORD names this way. The wrong fix - json: tags plus an ID field on
	// envRow - would produce id and camelCase too, but would name these "version"/"region".
	for _, k := range []string{"bcVersion", "location"} {
		if got[k] == nil {
			t.Errorf("record field %q missing - this is not the environment record: %v", k, got)
		}
	}
	// envRow's field names. Any of these means the display struct leaked into JSON.
	for _, k := range []string{"Name", "ShortID", "Status", "Version", "Region", "Created"} {
		if _, present := got[k]; present {
			t.Errorf("key %q present - that is envRow, the display struct, not the record: %v", k, got)
		}
	}
}

func TestEnvCreate_Wait_JSON_EmitsTheRecordWithID(t *testing.T) {
	apiURL, _, _ := createHarness(t, nil)

	stdout, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test", "-o", "json",
		"env", "create", "--version", "27.1.41600.0", "--country", "au", "--region", "westus2", "--wait")
	if err != nil {
		t.Fatalf("env create --wait -o json: %v", err)
	}
	assertEnvRecordJSON(t, stdout, manifestEnvID)
}

// The path the code comment calls out: publish failed, the environment is real, running and
// billable, and what is printed is "the only handle the caller has". A script parsing that
// output must get an id.
func TestEnvCreate_PublishFails_JSON_StillEmitsTheRecordWithID(t *testing.T) {
	apiURL, _, _ := createHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("app is broken"))
	})
	path := writeManifest(t, `
schema: bcdock-env/v1
bcVersion: 27.1.41600.0
country: au
region: westus2
apps:
  - file: ./one.app
`, "one.app")

	stdout, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test", "-o", "json",
		"env", "create", "--manifest", path)
	if err == nil {
		t.Fatal("want a non-zero exit when the publish fails")
	}
	assertEnvRecordJSON(t, stdout, manifestEnvID)
}

// Control: the human view is unchanged - the compact row with its header. If this moves,
// the fix went into the table path instead of only the JSON branch.
func TestEnvCreate_Wait_Table_KeepsTheCompactRow(t *testing.T) {
	apiURL, _, _ := createHarness(t, nil)

	stdout, _, err := RunCmd(t, "--api-url", apiURL, "--token", "bdk_test",
		"env", "create", "--version", "27.1.41600.0", "--country", "au", "--region", "westus2", "--wait")
	if err != nil {
		t.Fatalf("env create --wait: %v", err)
	}
	if !strings.Contains(stdout, "SHORT_ID") || !strings.Contains(stdout, "88888888") {
		t.Fatalf("table output lost its row shape:\n%s", stdout)
	}
	if strings.Contains(stdout, `"id"`) {
		t.Fatalf("table output must not be JSON:\n%s", stdout)
	}
}

func TestEnvResume_Wait_JSON_EmitsTheRecordWithID(t *testing.T) {
	const id = "44444444-5555-6666-7777-888888888888"
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/environments/"+id+"/resume", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("/api/v1/environments/"+id, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": id, "shortId": "44444444", "name": "rs-env", "status": "running",
			"bcVersion": "27.1.41600.0", "country": "au", "location": "westus2",
			"createdAt": "2026-09-25T00:00:00Z",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	stdout, _, err := RunCmd(t, "--api-url", srv.URL, "--token", "bdk_test", "-o", "json",
		"env", "resume", id, "--wait")
	if err != nil {
		t.Fatalf("env resume --wait -o json: %v", err)
	}
	assertEnvRecordJSON(t, stdout, id)
}
