package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

type rawRecord struct {
	ID      string `json:"id"`
	ShortID string `json:"shortId"`
	Status  string `json:"status"`
}

type rowRecord struct {
	ShortID string `header:"SHORT_ID"`
	Status  string `header:"STATUS"`
}

func TestPrintRowsOrRaw_JSON_EmitsRaw(t *testing.T) {
	var buf bytes.Buffer
	p := New(FormatJSON, false, true, &buf)

	raw := []rawRecord{{ID: "11111111-2222-3333-4444-555555555555", ShortID: "11111111", Status: "running"}}
	rows := []rowRecord{{ShortID: "11111111", Status: "running"}}

	if err := p.PrintRowsOrRaw(rows, raw); err != nil {
		t.Fatalf("PrintRowsOrRaw: %v", err)
	}

	var got []rawRecord
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\noutput: %s", err, buf.String())
	}
	if len(got) != 1 || got[0].ID != raw[0].ID {
		t.Fatalf("expected raw record with full GUID, got %+v", got)
	}
}

func TestPrintRowsOrRaw_Table_EmitsRows(t *testing.T) {
	var buf bytes.Buffer
	p := New(FormatTable, false, true, &buf)

	raw := []rawRecord{{ID: "11111111-2222-3333-4444-555555555555", ShortID: "11111111", Status: "running"}}
	rows := []rowRecord{{ShortID: "11111111", Status: "running"}}

	if err := p.PrintRowsOrRaw(rows, raw); err != nil {
		t.Fatalf("PrintRowsOrRaw: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "SHORT_ID") || !strings.Contains(out, "STATUS") {
		t.Fatalf("expected row headers in table output, got: %s", out)
	}
	if strings.Contains(out, "11111111-2222-3333-4444-555555555555") {
		t.Fatalf("table output should not include raw full GUID, got: %s", out)
	}
}

func TestPrintRowsOrRaw_CSV_EmitsRows(t *testing.T) {
	var buf bytes.Buffer
	p := New(FormatCSV, false, true, &buf)

	raw := []rawRecord{{ID: "11111111-2222-3333-4444-555555555555", ShortID: "11111111", Status: "running"}}
	rows := []rowRecord{{ShortID: "11111111", Status: "running"}}

	if err := p.PrintRowsOrRaw(rows, raw); err != nil {
		t.Fatalf("PrintRowsOrRaw: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "SHORT_ID") || !strings.Contains(out, "STATUS") {
		t.Fatalf("expected row headers in CSV output, got: %s", out)
	}
}
