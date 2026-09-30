package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bcdock/cli/internal/output"
	"github.com/spf13/cobra"
)

// env query is the agent's read path into a BC environment (DEV-088).
//
// Before it existed, an agent that wanted to read business data pulled the admin password
// out of `env get` and called OData raw, which put a live credential into its transcript.
// The verb exists so that reading data and handling a secret are no longer the same act.
//
// READ-ONLY IS BY CONSTRUCTION, NOT BY CONVENTION. The method below is the literal
// http.MethodGet and there is no flag that changes it, so `--method POST` is refused by
// cobra as an unknown flag before any network call happens. validateODataPath then refuses
// the path shapes that could route a GET somewhere it should not go.

// odataDelims restores the three characters url.PathEscape encodes that OData servers
// expect to see literally in a path segment. Percent-encoded sub-delims are legal per
// RFC 3986 but BC parses the raw path, so `Company%28%27X%27%29` does not resolve.
var odataDelims = strings.NewReplacer("%28", "(", "%29", ")", "%27", "'")

// escapeODataPath percent-encodes each segment of an OData path while leaving the segment
// separators and OData's own literal delimiters intact. Escaping the whole string in one
// call would turn `Company('X')/customers` into a single segment by encoding the slash.
func escapeODataPath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = odataDelims.Replace(url.PathEscape(s))
	}
	return strings.Join(segs, "/")
}

// validateODataPath refuses, before any call is made, the paths a read-only verb must not
// follow. Each refusal names the thing it protects:
//
//   - an absolute or protocol-relative URL would send the environment's web-service access
//     key, as HTTP Basic auth, to a host of the caller's choosing. This is the refusal that
//     matters most and it is not about writes at all.
//   - a `..` segment escapes the OData root. `../BC-dev/dev/packages` reaches the dev
//     endpoint, which is a different surface with different credentials.
//   - `$batch` is OData's write multiplexer. A GET to it answers 405, so refusing here buys
//     a clear error rather than a confusing one - and it says the intent is out of scope.
//   - a `?` or `#` would let query options in through the path and collide with the ones
//     this verb builds. The read options have flags; say so instead of merging silently.
func validateODataPath(p string) error {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return fmt.Errorf("empty OData path: give an entity set, for example 'customers'")
	}
	if i := strings.Index(trimmed, "://"); i > 0 && isURLScheme(trimmed[:i]) {
		return fmt.Errorf("path %q is an absolute URL: give a path relative to the environment's "+
			"OData root, for example 'customers'. env query authenticates with the environment's "+
			"web-service access key and will not send it to another host", p)
	}
	if strings.HasPrefix(trimmed, "//") {
		return fmt.Errorf("path %q names another host: give a path relative to the environment's "+
			"OData root, for example 'customers'", p)
	}
	if strings.ContainsAny(trimmed, "?#") {
		return fmt.Errorf("path %q carries its own query string: pass read options as flags "+
			"(--filter, --select, --top, --company, --tenant) so they are encoded once", p)
	}
	for _, seg := range strings.Split(strings.Trim(trimmed, "/"), "/") {
		if seg == ".." {
			return fmt.Errorf("path %q escapes the environment's OData root", p)
		}
		if strings.EqualFold(seg, "$batch") {
			return fmt.Errorf("path %q is write-shaped: $batch is OData's write multiplexer and "+
				"env query is read-only. There is no write verb", p)
		}
	}
	return nil
}

func isURLScheme(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && (r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.'):
		default:
			return false
		}
	}
	return true
}

// buildODataURL assembles the request URL. It sets RawPath as well as Path so the literal
// `('...')` survives into the wire form; see odataDelims.
func buildODataURL(odataRoot, company, path, tenant, filter, sel string, top int) (string, error) {
	base, err := url.Parse(odataRoot)
	if err != nil {
		return "", fmt.Errorf("environment has an unparseable OData URL %q: %w", odataRoot, err)
	}
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}

	rel := strings.TrimPrefix(strings.TrimSpace(path), "/")
	decoded := rel
	escaped := escapeODataPath(rel)
	if company != "" {
		// OData escapes a single quote inside a string literal by doubling it.
		lit := strings.ReplaceAll(company, "'", "''")
		decoded = "Company('" + lit + "')/" + decoded
		escaped = "Company('" + odataDelims.Replace(url.PathEscape(lit)) + "')/" + escaped
	}

	u := *base
	u.Path = base.Path + decoded
	u.RawPath = base.EscapedPath() + escaped

	// Built by hand rather than with url.Values so `$filter` keeps its dollar sign. Encode()
	// would emit `%24filter`, which relies on the server decoding query KEYS before dispatch.
	var q []string
	add := func(k, v string) { q = append(q, k+"="+url.QueryEscape(v)) }
	if tenant != "" {
		add("tenant", tenant)
	}
	if filter != "" {
		add("$filter", filter)
	}
	if sel != "" {
		add("$select", sel)
	}
	if top > 0 {
		add("$top", strconv.Itoa(top))
	}
	u.RawQuery = strings.Join(q, "&")
	return u.String(), nil
}

// redactSecrets removes credential literals from anything sourced from BC before it reaches
// a stream. Nothing observed does echo them, which is exactly why this is here: the verb's
// invariant is "the secret is in neither stdout nor stderr", and an invariant that holds
// only while a remote server keeps behaving is not an invariant. Cheap, and it fails closed.
func redactSecrets(s string, secrets ...string) string {
	for _, sec := range secrets {
		if len(sec) < 4 {
			continue
		}
		s = strings.ReplaceAll(s, sec, "[redacted]")
	}
	return s
}

// maxResponseBytes caps what this verb will read. The cap has to ANNOUNCE itself: this verb
// has no paging by design, so an unbounded entity set is the ordinary way to reach it, and
// an agent reading data it will not eyeball cannot tell truncated rows from all the rows.
const maxResponseBytes = 32 << 20

// runODataQuery issues the one GET and returns the raw response body.
func runODataQuery(ctx context.Context, reqURL, user, key string, timeout time.Duration, insecure bool) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(user, key)
	req.Header.Set("Accept", "application/json")

	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // gated by --insecure
	}
	httpClient := &http.Client{Timeout: timeout, Transport: tr}

	resp, err := httpClient.Do(req)
	if err != nil {
		// errors.New rather than %w: wrapping re-appends the ORIGINAL error after the
		// redacted copy, so the message doubles and the redaction on the first half is inert.
		return nil, errors.New(redactSecrets("querying the OData endpoint: "+err.Error(), key))
	}
	defer resp.Body.Close()

	// cap+1, because io.LimitReader does not error when it truncates: a silent cut and a
	// complete answer would otherwise share an exit code, an empty stderr and a stream.
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if len(body) > maxResponseBytes {
		// "the rows above" described output that does not exist: this returns before anything
		// is printed. A reader scrolling back would go looking for rows that were never emitted.
		return nil, fmt.Errorf("the response exceeds %d MB, so it was NOT read to the end and "+
			"no rows were printed. Narrow it with --top, --select or --filter",
			maxResponseBytes>>20)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("BC rejected the environment's web-service access key (HTTP 401). " +
			"OData authenticates with the access key, not the admin password; if this environment " +
			"was created before the key was stored, recreate it")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("BC returned HTTP %d: %s", resp.StatusCode,
			truncate(redactSecrets(strings.TrimSpace(string(body)), key), 2000))
	}
	if readErr != nil {
		return nil, fmt.Errorf("reading OData response: %w", readErr)
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "... (truncated)"
}

// odataEnvelope is the shape of a BC OData collection response. `value` is what a caller
// wants; the @odata.* siblings are transport metadata.
type odataEnvelope struct {
	Value []map[string]any `json:"value"`
}

var envQueryCmd = &cobra.Command{
	Use:   "query <env> <odata-path>",
	Short: "Run a read-only OData query against a BC environment",
	Long: `Read business data out of a BC environment over its OData v4 endpoint.

This is the read path for scripts and agents. It replaces the pattern of pulling the
admin password out of 'bcdock env get' and calling OData with curl, which put a live
credential into the caller's transcript.

Credentials: the CLI fetches the environment's web-service access key once per
invocation from the audited reveal endpoint (the same one 'bcdock env credentials'
uses) and sends it as HTTP Basic auth. It is never printed, never accepted as a flag,
and never reaches your shell history or the process list. BC's OData and SOAP surfaces
reject the admin password, so the access key is the credential in play here.

Read-only by construction: the verb issues GET and nothing else. There is no method
flag, so a write attempt is refused as an unknown flag before any call. A path that is
an absolute URL, escapes the OData root, or names $batch is refused for the same reason.

The path is relative to the environment's OData root and is taken literally (do not
pre-percent-encode it). OData v4 serves the entity sets your environment publishes as
web services, so WHICH NOUNS EXIST VARIES BY ENVIRONMENT. 'Company' is a BC system
entity set and is present on every environment. Everything else depends on what has
been published, so ask the environment rather than guessing:

  Company                                  the company list - start here if you do
                                           not know the company name. Note the
                                           capital and the singular
  $metadata                                every entity set this environment serves.
                                           This is the answer to "what can I query?"
  Company('CRONUS AU')/Chart_of_Accounts   a company-scoped set, or pass --company
  Company('CRONUS AU')                     a single entity by key

Measured on a BC 28.4 sandbox: 82 published entity sets, among them
Chart_of_Accounts, workflowCustomers, G_LEntries and SalesOrder. Names are
case-sensitive, and a name BC does not serve answers 404 rather than an empty list.

Output:
  -o table  (default) scalar columns; any dropped nested column is named on stderr
  -o json   the 'value' array, pretty-printed
  -o csv    the same scalar columns as the table
A response that is not a collection (a single entity, $count) is printed as JSON in
every format, because there are no rows to lay out.

Exit codes:
  0   ok
  1   general error (refused path, no OData URL, BC error response)
  3   auth failure (missing or invalid bcdock token)
  5   environment not found`,
	Example: `  bcdock env query my-env Company -o json
  bcdock env query my-env '$metadata'
  bcdock env query my-env --company "CRONUS AU" Chart_of_Accounts --select "No,Name" --top 3
  bcdock env query my-env --company "CRONUS AU" workflowCustomers --filter "startswith(Name,'Adatum')" -o json`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		envArg, path := args[0], args[1]

		company, _ := cmd.Flags().GetString("company")
		tenant, _ := cmd.Flags().GetString("tenant")
		filter, _ := cmd.Flags().GetString("filter")
		sel, _ := cmd.Flags().GetString("select")
		top, _ := cmd.Flags().GetInt("top")
		timeout, _ := cmd.Flags().GetDuration("timeout")
		insecure, _ := cmd.Flags().GetBool("insecure")

		// Refused before the environment is even resolved: a bad path should cost nothing
		// and, in particular, should not reach the reveal endpoint and write an audit row.
		if err := validateODataPath(path); err != nil {
			return err
		}
		if top < 0 {
			return fmt.Errorf("--top %d is not a row count. Omit it for the server default", top)
		}

		id, err := resolveEnvID(cmd.Context(), r.Client, envArg)
		if err != nil {
			return err
		}
		var env environment
		if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/environments/"+id, nil, &env); err != nil {
			return err
		}
		if env.ODataUrl == nil || *env.ODataUrl == "" {
			return fmt.Errorf("environment %q has no OData URL (status: %s)", envArg, env.Status)
		}

		reqURL, err := buildODataURL(*env.ODataUrl, company, path, tenant, filter, sel, top)
		if err != nil {
			return err
		}

		// SEC-039: one reveal per invocation, audited server-side. A caller that loops over
		// this verb produces one audit row per query, which is the intended reading.
		creds, err := revealEnvCredentials(cmd.Context(), r.Client, id)
		if err != nil {
			return err
		}
		if creds.Username == nil || creds.WebServiceAccessKey == nil {
			return fmt.Errorf("environment %q has no web-service access key yet (status: %s)", envArg, env.Status)
		}
		user, key := *creds.Username, *creds.WebServiceAccessKey

		body, err := runODataQuery(cmd.Context(), reqURL, user, key, timeout, insecure)
		if err != nil {
			return err
		}

		secrets := []string{key, derefStr(creds.Password)}

		var env4 odataEnvelope
		if uerr := json.Unmarshal(body, &env4); uerr != nil || env4.Value == nil {
			// Not a collection response (a single entity, or $count). Printed as JSON in every
			// format, because there are no rows to put in a table and the alternative was a Go
			// map dump under the header VALUE.
			return printRawJSON(r.Printer, body, secrets...)
		}
		switch r.Printer.Format {
		case output.FormatJSON:
			return printODataJSON(r.Printer, env4.Value, secrets...)
		case output.FormatCSV:
			return printODataCSV(r.Printer, env4.Value, secrets...)
		default:
			return printODataRows(r.Printer, env4.Value, secrets...)
		}
	},
}

// writeRedacted is the single exit for everything this verb prints. The invariant is "no
// credential in either stream"; routing every byte through one function makes that a
// property of the code rather than of each call site remembering to ask.
func writeRedacted(w io.Writer, s string, secrets ...string) error {
	_, err := io.WriteString(w, redactSecrets(s, secrets...))
	return err
}

// printRawJSON emits a non-collection response, re-indented if it parses.
//
// The redaction used to sit only in the does-not-parse branch, which is the branch that
// never runs: every well-formed single-entity or $count body took the other one straight to
// the printer. A guard that takes the secrets as a parameter and then skips them on the
// success path is worse than no guard, because the signature says it ran.
func printRawJSON(p *output.Printer, body []byte, secrets ...string) error {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return writeRedacted(p.W, strings.TrimSpace(string(body))+"\n", secrets...)
	}
	pretty, merr := json.MarshalIndent(v, "", "  ")
	if merr != nil {
		return merr
	}
	return writeRedacted(p.W, string(pretty)+"\n", secrets...)
}

// printODataJSON emits the rows through the same redacting exit as every other format.
func printODataJSON(p *output.Printer, rows []map[string]any, secrets ...string) error {
	pretty, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	return writeRedacted(p.W, string(pretty)+"\n", secrets...)
}

// odataColumns derives the scalar column set shared by the table and csv renderers.
//
// A key that is scalar in one row and nested in another is DROPPED, not both printed and
// reported omitted: the column and the stderr note must not disagree about the same name.
// @odata.* keys are transport metadata; -o json keeps everything.
func odataColumns(rows []map[string]any) (cols, dropped []string) {
	nested := map[string]bool{}
	seen := map[string]bool{}
	var order []string
	for _, row := range rows {
		for k, v := range row {
			if strings.HasPrefix(k, "@") {
				continue
			}
			if !isScalar(v) {
				nested[k] = true
			}
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		}
	}
	for _, k := range order {
		if nested[k] {
			dropped = append(dropped, k)
		} else {
			cols = append(cols, k)
		}
	}
	sort.Strings(cols)
	sort.Strings(dropped)
	return cols, dropped
}

// printODataCSV renders the same scalar columns as the table. Routed explicitly rather than
// left to fall through: -o csv is a documented global format, and silently serving the tab
// table for it is the quiet kind of wrong.
func printODataCSV(p *output.Printer, rows []map[string]any, secrets ...string) error {
	// Same early return as the table. csv is the format most likely to be piped straight
	// into a parser, and a lone header-less newline is one empty record where absence was
	// meant - the two renderers must not disagree about what "no rows" looks like.
	if len(rows) == 0 {
		p.Info("no rows")
		return nil
	}

	cols, dropped := odataColumns(rows)
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(cols); err != nil {
		return err
	}
	for _, row := range rows {
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = scalarString(row[c])
		}
		if err := w.Write(cells); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	if err := writeRedacted(p.W, buf.String(), secrets...); err != nil {
		return err
	}
	noteDropped(p, dropped)
	return nil
}

func noteDropped(p *output.Printer, dropped []string) {
	if len(dropped) == 0 {
		return
	}
	p.Info("omitted %d nested column(s): %s (use -o json to see them)",
		len(dropped), strings.Join(dropped, ", "))
}

// printODataRows renders the rows as a table. Only scalar columns can be a cell; the names
// of any dropped nested columns go to stderr so the loss is visible rather than silent.
func printODataRows(p *output.Printer, rows []map[string]any, secrets ...string) error {
	if len(rows) == 0 {
		p.Info("no rows")
		return nil
	}

	cols, dropped := odataColumns(rows)

	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(upperAll(cols), "\t"))
	for _, row := range rows {
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = scalarString(row[c])
		}
		fmt.Fprintln(w, strings.Join(cells, "\t"))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := writeRedacted(p.W, buf.String(), secrets...); err != nil {
		return err
	}
	noteDropped(p, dropped)
	return nil
}

func upperAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToUpper(s)
	}
	return out
}

func isScalar(v any) bool {
	switch v.(type) {
	case nil, bool, float64, string, json.Number:
		return true
	}
	return false
}

func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case string:
		return t
	default:
		return ""
	}
}
