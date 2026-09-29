package boa

import (
	"slices"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

// worksheet and parseSheet let the XML-fixture tests drive parseRow, dropping string and value-less cells as parse
// does.
type worksheet struct {
	Rows []struct {
		Cells []struct {
			Ref  string  `xml:"r,attr"`
			Type string  `xml:"t,attr"`
			V    *string `xml:"v"`
		} `xml:"c"`
	} `xml:"sheetData>row"`
}

func parseSheet(ws worksheet, base string, after, upto time.Time) []adapter.Rate {
	var rates []adapter.Rate
	for _, row := range ws.Rows {
		var cols []string
		for _, c := range row.Cells {
			col, _, err := excelize.SplitCellName(c.Ref)
			if err != nil || c.Type == "s" || c.V == nil {
				continue
			}
			n, _ := excelize.ColumnNameToNumber(col)
			for len(cols) < n {
				cols = append(cols, "")
			}
			cols[n-1] = *c.V
		}
		if r, ok := parseRow(cols, base, after, upto); ok {
			rates = append(rates, r)
		}
	}
	return rates
}

func TestParseSkipsNumericLookingStringCells(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", "USD - DZD"); err != nil {
		t.Fatal(err)
	}
	for cell, v := range map[string]any{"A1": "46140", "B1": 1.5, "A2": 46141, "B2": "2.5", "A3": 46142, "B3": 3.5} {
		if err := f.SetCellValue("USD - DZD", cell, v); err != nil {
			t.Fatal(err)
		}
	}
	if typ, _ := f.GetCellType("USD - DZD", "A1"); typ != excelize.CellTypeSharedString {
		t.Fatalf("A1 type = %v, want shared string", typ)
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	got, err := parse(buf.Bytes(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{{Date: adapter.Date(2026, 4, 30), Base: "USD", Quote: "DZD", Rate: 3.5}}
	if !slices.Equal(got, want) {
		t.Errorf("parse = %v, want %v", got, want)
	}
}
