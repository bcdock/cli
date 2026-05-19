package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"text/tabwriter"
)

const (
	FormatTable = "table"
	FormatJSON  = "json"
	FormatCSV   = "csv"
)

type Printer struct {
	Format  string
	Quiet   bool
	NoColor bool
	W       io.Writer // primary output (stdout)
	Err     io.Writer // status / progress / errors (stderr)
}

func New(format string, quiet, noColor bool, w io.Writer) *Printer {
	if w == nil {
		w = os.Stdout
	}
	return &Printer{Format: format, Quiet: quiet, NoColor: noColor, W: w, Err: os.Stderr}
}

// NewWithErr is like New but also accepts an explicit stderr writer; used by
// tests so that Info/Error output can be captured through cobra's SetErr.
func NewWithErr(format string, quiet, noColor bool, w, errW io.Writer) *Printer {
	p := New(format, quiet, noColor, w)
	if errW != nil {
		p.Err = errW
	}
	return p
}

// Print renders v according to the configured format.
// v should be a struct, slice, or map — whatever the command produces.
func (p *Printer) Print(v any) error {
	switch p.Format {
	case FormatJSON:
		return p.printJSON(v)
	case FormatCSV:
		return p.printCSV(v)
	default:
		return p.printTable(v)
	}
}

// PrintRowsOrRaw renders the table-friendly `rows` view for table/csv and
// the raw API record `raw` for json. Used by list/get commands so callers
// of `-o json` get the underlying record (full GUIDs, every field, nested
// fields preserved) instead of the truncated row struct.
//
// CSV stays on `rows` because the printer's reflection-based row-flattener
// can't render pointer fields (`*string`, `*time.Time`) cleanly — addresses
// leak instead of values. JSON encodes pointers fine.
func (p *Printer) PrintRowsOrRaw(rows, raw any) error {
	switch p.Format {
	case FormatJSON:
		return p.printJSON(raw)
	case FormatCSV:
		return p.printCSV(rows)
	default:
		return p.printTable(rows)
	}
}

func (p *Printer) printJSON(v any) error {
	enc := json.NewEncoder(p.W)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// printTable renders a slice of structs (or a single struct) as a tab-aligned table.
// Uses struct field names as headers unless a "header" struct tag is present.
func (p *Printer) printTable(v any) error {
	w := tabwriter.NewWriter(p.W, 0, 0, 2, ' ', 0)
	rows, headers := toRows(v)
	if len(rows) == 0 {
		fmt.Fprintln(w, "(none)")
		return w.Flush()
	}
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	return w.Flush()
}

func (p *Printer) printCSV(v any) error {
	rows, headers := toRows(v)
	w := csv.NewWriter(p.W)
	if err := w.Write(headers); err != nil {
		return err
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// Info prints an informational message to stderr unless --quiet.
func (p *Printer) Info(format string, args ...any) {
	if p.Quiet {
		return
	}
	w := p.Err
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, format+"\n", args...)
}

// Error always prints to stderr.
func (p *Printer) Error(format string, args ...any) {
	w := p.Err
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, "error: "+format+"\n", args...)
}

// toRows converts a struct or slice of structs to (rows, headers) for table/CSV output.
// Falls back to JSON representation for non-struct types.
func toRows(v any) ([][]string, []string) {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.Slice:
		if rv.Len() == 0 {
			return nil, nil
		}
		elem := rv.Index(0)
		headers := structHeaders(elem)
		rows := make([][]string, rv.Len())
		for i := range rv.Len() {
			rows[i] = structValues(rv.Index(i))
		}
		return rows, headers

	case reflect.Struct:
		return [][]string{structValues(rv)}, structHeaders(rv)

	default:
		return [][]string{{fmt.Sprintf("%v", v)}}, []string{"VALUE"}
	}
}

func structHeaders(v reflect.Value) []string {
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	t := v.Type()
	var headers []string
	for i := range t.NumField() {
		f := t.Field(i)
		if name, ok := f.Tag.Lookup("header"); ok {
			headers = append(headers, name)
		} else {
			headers = append(headers, strings.ToUpper(f.Name))
		}
	}
	return headers
}

func structValues(v reflect.Value) []string {
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	var vals []string
	for i := range v.NumField() {
		vals = append(vals, fmt.Sprintf("%v", v.Field(i).Interface()))
	}
	return vals
}
