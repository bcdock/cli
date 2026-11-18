package cli

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	queryEnvID    = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	queryPassword = "Adm1nP@ss-notthisone"
	queryWsKey    = "wskey-THIS-IS-THE-ONE"
)

// bcStub stands in for the environment's BC OData endpoint. It records every request so a
// test can assert on the METHOD and the URL, not only on what came back: "read-only" is a
// claim about what was sent.
type bcStub struct {
	*httptest.Server
	methods []string
	urls    []string
	auths   []string
	status  int
	body    string
}

func newBcStub(t *testing.T) *bcStub {
	t.Helper()
	b := &bcStub{status: http.StatusOK, body: `{"@odata.context":"x","value":[{"No":"10000","Name":"Adatum Corporation"}]}`}
	b.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.methods = append(b.methods, r.Method)
		b.urls = append(b.urls, r.URL.RequestURI())
		b.auths = append(b.auths, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(b.status)
		_, _ = w.Write([]byte(b.body))
	}))
	return b
}

// basicPassword returns the password half of a Basic auth header, or "" if absent.
func basicPassword(header string) string {
	const p = "Basic "
	if !strings.HasPrefix(header, p) {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, p))
	if err != nil {
		return ""
	}
	_, pass, found := strings.Cut(string(raw), ":")
	if !found {
		return ""
	}
	return pass
}

// queryPlatform serves the platform side: the detail read (no secrets, per SEC-039) and the
// audited reveal. Both are counted, because "one reveal per invocation" is an invariant of
// this verb and only a count can see it.
type queryPlatform struct {
	*httptest.Server
	detailCalls int
	revealCalls int
	odataRoot   string
}

func newQueryPlatform(t *testing.T, odataRoot string) *queryPlatform {
	t.Helper()
	q := &queryPlatform{odataRoot: odataRoot}
	q.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/environments/" + queryEnvID:
			q.detailCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          queryEnvID,
				"shortId":     "aaaaaaaa",
				"name":        "myenv-aaaaaaaa",
				"displayName": "myenv",
				"status":      "running",
				"oDataUrl":    q.odataRoot,
				"username":    "admin",
				"multiTenant": true,
				"createdAt":   "2026-05-01T10:00:00Z",
			})
		case "/api/v1/environments/" + queryEnvID + "/credentials":
			q.revealCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"environmentId":       queryEnvID,
				"username":            "admin",
				"password":            queryPassword,
				"webServiceAccessKey": queryWsKey,
			})
		default:
			t.Errorf("unexpected platform request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	return q
}

func runQuery(t *testing.T, plat *queryPlatform, args ...string) (string, string, error) {
	t.Helper()
	full := append([]string{"--api-url", plat.URL, "--token", "bdk_test", "env", "query"}, args...)
	return RunCmd(t, full...)
}

func TestEnvQuery_JSON_ReturnsTheRows(t *testing.T) {
	bc := newBcStub(t)
	defer bc.Close()
	plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
	defer plat.Close()

	out, _, err := runQuery(t, plat, queryEnvID, "customers", "--top", "3", "-o", "json")
	if err != nil {
		t.Fatalf("env query: %v", err)
	}

	var rows []map[string]any
	if uerr := json.Unmarshal([]byte(out), &rows); uerr != nil {
		t.Fatalf("stdout is not a JSON array: %v\n%s", uerr, out)
	}
	if len(rows) != 1 || rows[0]["Name"] != "Adatum Corporation" {
		t.Fatalf("want the stub's single row, got: %s", out)
	}
	if len(bc.urls) != 1 {
		t.Fatalf("want exactly one BC call, got %d: %v", len(bc.urls), bc.urls)
	}
	if !strings.Contains(bc.urls[0], "/BC-rest/customers") {
		t.Errorf("path not appended to the OData root: %s", bc.urls[0])
	}
	for _, want := range []string{"$top=3", "tenant=default"} {
		if !strings.Contains(bc.urls[0], want) {
			t.Errorf("missing %q in request URI: %s", want, bc.urls[0])
		}
	}
}

// The credential BC actually accepts on OData is the web-service access key, not the admin
// password (the al-cli skill records this as live-validated: OData/SOAP 401 on the password).
// Reaching for `requireBcAdmin` here would compile, pass a smoke against a stub that checks
// nothing, and 401 against every real environment. This test is the one that says which.
func TestEnvQuery_AuthenticatesWithTheAccessKeyNotThePassword(t *testing.T) {
	bc := newBcStub(t)
	defer bc.Close()
	plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
	defer plat.Close()

	if _, _, err := runQuery(t, plat, queryEnvID, "customers"); err != nil {
		t.Fatalf("env query: %v", err)
	}
	if len(bc.auths) != 1 {
		t.Fatalf("want one authenticated call, got %d", len(bc.auths))
	}
	got := basicPassword(bc.auths[0])
	if got != queryWsKey {
		t.Errorf("Basic auth secret = %q, want the web-service access key %q", got, queryWsKey)
	}
	if got == queryPassword {
		t.Errorf("the admin password was sent to OData; BC rejects it there")
	}
}

// The SEC-039 shape, applied to this verb: neither secret may appear in either stream.
// Both are asserted, not just the password - the key is the one this verb actually holds,
// so a test that only watched the password would be watching the safer of the two.
func TestEnvQuery_NeitherSecretReachesEitherStream(t *testing.T) {
	bc := newBcStub(t)
	defer bc.Close()
	plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
	defer plat.Close()

	for _, args := range [][]string{
		{queryEnvID, "customers"},
		{queryEnvID, "customers", "-o", "json"},
		{queryEnvID, "--company", "CRONUS AU", "customers", "--top", "3"},
	} {
		out, errOut, err := runQuery(t, plat, args...)
		if err != nil {
			t.Fatalf("env query %v: %v", args, err)
		}
		for _, secret := range []string{queryPassword, queryWsKey} {
			if strings.Contains(out, secret) {
				t.Errorf("%v: secret in stdout:\n%s", args, out)
			}
			if strings.Contains(errOut, secret) {
				t.Errorf("%v: secret in stderr:\n%s", args, errOut)
			}
		}
	}
}

// The path nobody writes a test for: BC answers with an error body that quotes the
// credential back. Nothing observed does this, which is why the guard is in the code rather
// than in a comment - the invariant must not depend on a remote server's manners.
func TestEnvQuery_RedactsASecretEchoedBackByBc(t *testing.T) {
	bc := newBcStub(t)
	defer bc.Close()
	bc.status = http.StatusBadRequest
	bc.body = `{"error":{"message":"bad credential ` + queryWsKey + ` supplied"}}`

	plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
	defer plat.Close()

	out, errOut, err := runQuery(t, plat, queryEnvID, "customers")
	if err == nil {
		t.Fatal("want an error for HTTP 400")
	}
	// The error text is what cobra prints to stderr, so assert on it directly too.
	combined := out + errOut + err.Error()
	if strings.Contains(combined, queryWsKey) {
		t.Errorf("the echoed access key was not redacted:\n%s", combined)
	}
	if !strings.Contains(combined, "[redacted]") {
		t.Errorf("want the redaction marker, got:\n%s", combined)
	}
	if !strings.Contains(combined, "400") {
		t.Errorf("want the status code preserved, got:\n%s", combined)
	}
}

// Refusals, and the assertion that makes them mean something: ZERO calls to BC and zero to
// the reveal. "Refused" and "refused before any call" are different claims, and only the
// counters can tell them apart. The final case is the positive control: with the same
// harness a legitimate path DOES reach BC, so a passing refusal is not just a broken stub.
func TestEnvQuery_RefusesWriteShapedAndOffHostPaths(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"absolute url", "https://evil.example/steal", "absolute URL"},
		{"protocol relative", "//evil.example/steal", "another host"},
		{"root escape", "../BC-dev/dev/packages", "escapes"},
		{"batch", "$batch", "write-shaped"},
		{"batch nested", "Company('X')/$BATCH", "write-shaped"},
		{"own query string", "customers?$top=99", "query string"},
		{"empty", "", "empty OData path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bc := newBcStub(t)
			defer bc.Close()
			plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
			defer plat.Close()

			_, _, err := runQuery(t, plat, queryEnvID, tc.path)
			if err == nil {
				t.Fatalf("path %q was accepted", tc.path)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not say why: %v", err)
			}
			if len(bc.urls) != 0 {
				t.Errorf("refused path still reached BC: %v", bc.urls)
			}
			if plat.revealCalls != 0 {
				t.Errorf("refused path still revealed credentials (%d reveals) and wrote an audit row",
					plat.revealCalls)
			}
		})
	}

	t.Run("positive control: a legitimate path reaches BC", func(t *testing.T) {
		bc := newBcStub(t)
		defer bc.Close()
		plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
		defer plat.Close()

		if _, _, err := runQuery(t, plat, queryEnvID, "customers"); err != nil {
			t.Fatalf("env query: %v", err)
		}
		if len(bc.urls) != 1 || plat.revealCalls != 1 {
			t.Fatalf("control did not exercise the path: %d BC calls, %d reveals",
				len(bc.urls), plat.revealCalls)
		}
	})
}

// Read-only is enforced by the absence of a method flag, so the refusal happens in cobra
// before RunE is entered. Asserting the error alone would not distinguish that from a
// runtime check, so the call counts are asserted too.
func TestEnvQuery_HasNoMethodFlag(t *testing.T) {
	bc := newBcStub(t)
	defer bc.Close()
	plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
	defer plat.Close()

	_, _, err := runQuery(t, plat, queryEnvID, "customers", "--method", "POST")
	if err == nil {
		t.Fatal("--method was accepted")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("want an unknown-flag error, got: %v", err)
	}
	if len(bc.methods) != 0 || plat.detailCalls != 0 {
		t.Errorf("a rejected flag still made calls: %v / %d", bc.methods, plat.detailCalls)
	}
}

func TestEnvQuery_OnlyEverIssuesGet(t *testing.T) {
	bc := newBcStub(t)
	defer bc.Close()
	plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
	defer plat.Close()

	for _, args := range [][]string{
		{queryEnvID, "customers"},
		{queryEnvID, "customers(10000)"},
		{queryEnvID, "companies", "--select", "Name"},
	} {
		if _, _, err := runQuery(t, plat, args...); err != nil {
			t.Fatalf("env query %v: %v", args, err)
		}
	}
	for i, m := range bc.methods {
		if m != http.MethodGet {
			t.Errorf("call %d used %s", i, m)
		}
	}
	if len(bc.methods) != 3 {
		t.Fatalf("want 3 BC calls, got %d", len(bc.methods))
	}
}

func TestEnvQuery_OneRevealPerInvocation(t *testing.T) {
	bc := newBcStub(t)
	defer bc.Close()
	plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
	defer plat.Close()

	for i := 0; i < 3; i++ {
		if _, _, err := runQuery(t, plat, queryEnvID, "customers"); err != nil {
			t.Fatalf("env query: %v", err)
		}
	}
	if plat.revealCalls != 3 {
		t.Errorf("reveals = %d, want one per invocation (3)", plat.revealCalls)
	}
}

func TestBuildODataURL(t *testing.T) {
	const root = "https://e.dev.bcdock.io/BC-rest/"
	cases := []struct {
		name    string
		company string
		path    string
		tenant  string
		filter  string
		sel     string
		top     int
		want    string
	}{
		{
			name: "bare entity set", path: "customers",
			want: "https://e.dev.bcdock.io/BC-rest/customers",
		},
		{
			// The delimiters stay literal. Percent-encoded sub-delims are legal per RFC 3986
			// but BC parses the raw path, so Company%28%27X%27%29 does not resolve.
			name: "company with a space", company: "CRONUS AU", path: "customers",
			want: "https://e.dev.bcdock.io/BC-rest/Company('CRONUS%20AU')/customers",
		},
		{
			name: "quote in the company name is doubled", company: "O'Brien Ltd", path: "customers",
			want: "https://e.dev.bcdock.io/BC-rest/Company('O''Brien%20Ltd')/customers",
		},
		{
			name: "multi segment path keeps its separators", path: "customers(10000)/salesOrders",
			want: "https://e.dev.bcdock.io/BC-rest/customers(10000)/salesOrders",
		},
		{
			name: "leading slash is tolerated", path: "/customers",
			want: "https://e.dev.bcdock.io/BC-rest/customers",
		},
		{
			// $ stays literal in the KEY: url.Values.Encode would emit %24filter, which relies
			// on the server decoding query keys before dispatch.
			name: "read options", path: "customers", tenant: "default",
			filter: "Name eq 'Adatum'", sel: "No,Name", top: 3,
			want: "https://e.dev.bcdock.io/BC-rest/customers?tenant=default&$filter=Name+eq+%27Adatum%27&$select=No%2CName&$top=3",
		},
		{
			name: "top zero is omitted", path: "customers", top: 0,
			want: "https://e.dev.bcdock.io/BC-rest/customers",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildODataURL(root, tc.company, tc.path, tc.tenant, tc.filter, tc.sel, tc.top)
			if err != nil {
				t.Fatalf("buildODataURL: %v", err)
			}
			if got != tc.want {
				t.Errorf("\n got %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestEnvQuery_TableNamesTheColumnsItDropped(t *testing.T) {
	bc := newBcStub(t)
	defer bc.Close()
	bc.body = `{"value":[{"No":"10000","Name":"Adatum","@odata.etag":"W/\"x\"","address":{"city":"Sydney"},"tags":["a"]}]}`

	plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
	defer plat.Close()

	out, errOut, err := runQuery(t, plat, queryEnvID, "customers")
	if err != nil {
		t.Fatalf("env query: %v", err)
	}
	for _, want := range []string{"NAME", "NO", "Adatum", "10000"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in table:\n%s", want, out)
		}
	}
	if strings.Contains(out, "@odata.etag") {
		t.Errorf("transport metadata leaked into the table:\n%s", out)
	}
	// The loss must be visible. A table that silently drops columns is the failure mode.
	for _, want := range []string{"address", "tags", "-o json"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr does not name the dropped column %q:\n%s", want, errOut)
		}
	}
}

func TestEnvQuery_NoODataUrlIsAClearError(t *testing.T) {
	bc := newBcStub(t)
	defer bc.Close()
	plat := newQueryPlatform(t, "")
	defer plat.Close()

	_, _, err := runQuery(t, plat, queryEnvID, "customers")
	if err == nil {
		t.Fatal("want an error when the environment has no OData URL")
	}
	if !strings.Contains(err.Error(), "no OData URL") {
		t.Errorf("unclear error: %v", err)
	}
	if plat.revealCalls != 0 {
		t.Errorf("credentials were revealed for an environment that cannot be queried")
	}
}

// ---------------------------------------------------------------------------
// Findings from Ravi's review of #289 @ 1b10c12. Each of these covers a path the
// original suite did not enter, which is why none of the seven mutations reached them.
// ---------------------------------------------------------------------------

// A single-entity or $count body parses, so it took the branch that skipped the redaction
// the function's own signature advertised. This is the plain 200 - the path not thought
// about - and the earlier redaction test could not reach it because it asserts on a 400.
func TestEnvQuery_RedactsASecretInANonCollectionBody(t *testing.T) {
	for _, format := range []string{"table", "json", "csv"} {
		t.Run(format, func(t *testing.T) {
			bc := newBcStub(t)
			defer bc.Close()
			bc.body = `{"No":"10000","Note":"key is ` + queryWsKey + `"}`

			plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
			defer plat.Close()

			out, errOut, err := runQuery(t, plat, queryEnvID, "Company('CRONUS')", "-o", format)
			if err != nil {
				t.Fatalf("env query: %v", err)
			}
			if strings.Contains(out+errOut, queryWsKey) {
				t.Errorf("secret survived a parseable non-collection body:\n%s", out+errOut)
			}
			if !strings.Contains(out, "[redacted]") {
				t.Errorf("want the redaction marker in the body:\n%s", out)
			}
			// A Go map dump under the header VALUE is not an answer; JSON is.
			if strings.Contains(out, "map[") {
				t.Errorf("non-collection body rendered as a Go map:\n%s", out)
			}
		})
	}
}

// A secret in an ordinary row of an ordinary collection, in every format. The invariant is
// "no credential in either stream" and it must not depend on which renderer ran.
func TestEnvQuery_RedactsASecretInACollectionRow(t *testing.T) {
	for _, format := range []string{"table", "json", "csv"} {
		t.Run(format, func(t *testing.T) {
			bc := newBcStub(t)
			defer bc.Close()
			bc.body = `{"value":[{"Name":"Adatum","Note":"` + queryWsKey + `"}]}`

			plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
			defer plat.Close()

			out, errOut, err := runQuery(t, plat, queryEnvID, "Company", "-o", format)
			if err != nil {
				t.Fatalf("env query: %v", err)
			}
			if strings.Contains(out+errOut, queryWsKey) {
				t.Errorf("secret survived a %s render:\n%s", format, out+errOut)
			}
		})
	}
}

// io.LimitReader does not error when it truncates, so a cut response and a complete one
// shared an exit code, an empty stderr and a stream. For a verb whose whole point is an
// agent reading data it will not eyeball, that is the one failure that must be loud.
func TestEnvQuery_AnOversizeResponseFailsRatherThanTruncating(t *testing.T) {
	bc := newBcStub(t)
	defer bc.Close()
	// One byte past the cap, inside a well-formed envelope so nothing else can be blamed.
	filler := strings.Repeat("x", maxResponseBytes)
	bc.body = `{"value":[{"Name":"` + filler + `"}]}`

	plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
	defer plat.Close()

	out, _, err := runQuery(t, plat, queryEnvID, "Company", "-o", "json")
	if err == nil {
		t.Fatalf("an oversize response succeeded; %d bytes reached stdout", len(out))
	}
	for _, want := range []string{"NOT read to the end", "--top"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not say what happened or what to do: %v", err)
		}
	}
}

// A dial failure used to be wrapped with %w after being redacted, which re-appended the
// original: the message doubled and the redaction on the first half was inert.
func TestEnvQuery_ATransportErrorIsNotDoubled(t *testing.T) {
	bc := newBcStub(t)
	root := bc.URL + "/BC-rest/"
	bc.Close() // nothing is listening now, so the GET fails at dial

	plat := newQueryPlatform(t, root)
	defer plat.Close()

	_, _, err := runQuery(t, plat, queryEnvID, "Company")
	if err == nil {
		t.Fatal("want a transport error")
	}
	if n := strings.Count(err.Error(), `Get "`); n != 1 {
		t.Errorf("the transport error appears %d times, want 1 (a %%w wrap re-appends the "+
			"unredacted original after the redacted copy): %v", n, err)
	}
}

// -o csv is a documented global format. It used to fall through to the tab table, which is
// the quiet kind of wrong: the caller asked for one thing and got another with no signal.
func TestEnvQuery_CsvIsCommaSeparatedNotTheTable(t *testing.T) {
	bc := newBcStub(t)
	defer bc.Close()
	bc.body = `{"value":[{"No":"10000","Name":"Adatum, Inc."}]}`

	plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
	defer plat.Close()

	out, _, err := runQuery(t, plat, queryEnvID, "Company", "-o", "csv")
	if err != nil {
		t.Fatalf("env query: %v", err)
	}
	if !strings.Contains(out, "Name,No") {
		t.Errorf("want a comma-separated header, got:\n%s", out)
	}
	// The comma inside the value must be quoted, or the CSV is malformed.
	if !strings.Contains(out, `"Adatum, Inc."`) {
		t.Errorf("a comma in a value was not quoted:\n%s", out)
	}
}

// A key scalar in one row and nested in another used to land in BOTH the printed columns
// and the omitted list, so the table and its own footnote disagreed about the same name.
func TestEnvQuery_AMixedKeyIsDroppedNotBoth(t *testing.T) {
	bc := newBcStub(t)
	defer bc.Close()
	bc.body = `{"value":[{"Name":"A","Extra":"scalar here"},{"Name":"B","Extra":{"nested":1}}]}`

	plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
	defer plat.Close()

	out, errOut, err := runQuery(t, plat, queryEnvID, "Company")
	if err != nil {
		t.Fatalf("env query: %v", err)
	}
	if strings.Contains(out, "EXTRA") {
		t.Errorf("a column reported as omitted was also printed:\n%s", out)
	}
	if !strings.Contains(errOut, "Extra") {
		t.Errorf("the mixed column was not reported as omitted:\n%s", errOut)
	}
	if !strings.Contains(out, "NAME") {
		t.Errorf("the genuinely scalar column was dropped too:\n%s", out)
	}
}

func TestEnvQuery_RefusesANegativeTop(t *testing.T) {
	bc := newBcStub(t)
	defer bc.Close()
	plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
	defer plat.Close()

	_, _, err := runQuery(t, plat, queryEnvID, "Company", "--top", "-1")
	if err == nil {
		t.Fatal("--top -1 was accepted")
	}
	if len(bc.urls) != 0 || plat.revealCalls != 0 {
		t.Errorf("a bad flag value still made calls: %v / %d reveals", bc.urls, plat.revealCalls)
	}
}

// The three nits from the approve at a7a40dd. Each asserts the property, not the wording,
// except where the wording IS the defect: an error describing output that was never emitted.
func TestEnvQuery_OversizeErrorDoesNotDescribeOutputThatWasNotPrinted(t *testing.T) {
	bc := newBcStub(t)
	defer bc.Close()
	bc.body = `{"value":[{"Name":"` + strings.Repeat("x", maxResponseBytes) + `"}]}`

	plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
	defer plat.Close()

	out, _, err := runQuery(t, plat, queryEnvID, "Company", "-o", "json")
	if err == nil {
		t.Fatal("want an error for an oversize response")
	}
	// The claim and the stream have to agree: nothing was printed, so nothing is "above".
	if len(out) != 0 {
		t.Fatalf("want no stdout, got %d bytes", len(out))
	}
	if strings.Contains(err.Error(), "above") {
		t.Errorf("the error points at output that was never emitted: %v", err)
	}
	if !strings.Contains(err.Error(), "no rows were printed") {
		t.Errorf("the error does not say that nothing was printed: %v", err)
	}
}

// An empty collection must look like absence in every format. csv used to emit a bare
// newline, which a parser reads as one empty record.
func TestEnvQuery_ZeroRowsIsAbsenceInEveryFormat(t *testing.T) {
	for _, format := range []string{"table", "json", "csv"} {
		t.Run(format, func(t *testing.T) {
			bc := newBcStub(t)
			defer bc.Close()
			bc.body = `{"value":[]}`

			plat := newQueryPlatform(t, bc.URL+"/BC-rest/")
			defer plat.Close()

			out, errOut, err := runQuery(t, plat, queryEnvID, "Company", "-o", format)
			if err != nil {
				t.Fatalf("env query: %v", err)
			}
			if format == "json" {
				// An empty JSON array is a value that says "none", so it stands on its own.
				if strings.TrimSpace(out) != "[]" {
					t.Errorf("want [], got %q", out)
				}
				return
			}
			if strings.TrimSpace(out) != "" {
				t.Errorf("%s emitted %q for zero rows; a parser reads that as a record", format, out)
			}
			if !strings.Contains(errOut, "no rows") {
				t.Errorf("%s did not say there were no rows: %q", format, errOut)
			}
		})
	}
}
