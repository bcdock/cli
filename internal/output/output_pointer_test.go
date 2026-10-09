package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// #591: table and CSV cells used fmt's %v, which prints a pointer field as its ADDRESS.

type nestedRec struct {
	Count *int   `json:"count"`
	Label string `json:"label"`
}

type pointerRec struct {
	Set    *int       `json:"set"     header:"SET"`
	Unset  *int       `json:"unset"   header:"UNSET"`
	Name   *string    `json:"name"    header:"NAME"`
	Nested *nestedRec `json:"nested"  header:"NESTED"`
	Value  nestedRec  `json:"value"   header:"VALUE"`
	When   time.Time  `json:"when"    header:"WHEN"`
	Tags   []string   `json:"tags"    header:"TAGS"`
	NoNest *nestedRec `json:"noNest"  header:"NO_NEST"`
}

func pointerFixture() pointerRec {
	three, seven := 3, 7
	name := "sub_123"
	return pointerRec{
		Set: &three, Name: &name,
		Nested: &nestedRec{Count: &seven, Label: "x"},
		Value:  nestedRec{Label: "y"},
		When:   time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
		Tags:   []string{"a", "b"},
	}
}

func render(t *testing.T, format string, v any) string {
	t.Helper()
	var b bytes.Buffer
	p := &Printer{Format: format, W: &b}
	if err := p.Print(v); err != nil {
		t.Fatalf("print %s: %v", format, err)
	}
	return b.String()
}

func TestTable_PointerFields_AreValues_NilIsDash(t *testing.T) {
	out := render(t, FormatTable, pointerFixture())
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("table =\n%s", out)
	}
	got := strings.Join(strings.Fields(lines[1]), " ")
	want := "3 - sub_123 {7 x} {- y} 2026-10-07 12:00:00 +0000 UTC [a b] -"
	if got != want {
		t.Errorf("row = %q\nwant  %q", got, want)
	}
	if strings.Contains(out, "0x") || strings.Contains(out, "&{") || strings.Contains(out, "<nil>") {
		t.Errorf("an address or a raw pointer leaked:\n%s", out)
	}
}

func TestCSV_PointerFields_AreValues_NilIsDash(t *testing.T) {
	out := render(t, FormatCSV, []pointerRec{pointerFixture()})
	if !strings.Contains(out, "3,-,sub_123,{7 x},{- y},") {
		t.Errorf("csv = %q", out)
	}
	if strings.Contains(out, "0x") || strings.Contains(out, "&{") {
		t.Errorf("an address leaked:\n%s", out)
	}
}

// JSON never went through the cell renderer and must not change: pointers as values, nil as null.
func TestJSON_PointerFields_Unchanged(t *testing.T) {
	var got map[string]any
	if err := json.Unmarshal([]byte(render(t, FormatJSON, pointerFixture())), &got); err != nil {
		t.Fatalf("json: %v", err)
	}
	if got["set"] != float64(3) || got["unset"] != nil || got["name"] != "sub_123" || got["noNest"] != nil {
		t.Errorf("json = %v", got)
	}
}
