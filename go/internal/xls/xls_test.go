package xls

import (
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"

	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func open(t *testing.T, name string, index int) []Sheet {
	t.Helper()
	c, err := cassette.Load(filepath.Join(vcrtest.CassetteDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	sheets, err := Open([]byte(c.Interactions[index].Response.Body))
	if err != nil {
		t.Fatal(err)
	}
	return sheets
}

// The expected cells are what the spreadsheet gem reads from the same workbooks.
func TestOpenTypesCellsLikeTheSpreadsheetGem(t *testing.T) {
	tests := []struct {
		cassette    string
		interaction int
		row, col    int
		want        Cell
	}{
		{"bdl", 0, 1, 0, Cell{Kind: Date, Date: time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)}},
		{"bdl", 0, 1, 1, Cell{Kind: String, String: "USD"}},
		{"bdl", 0, 1, 4, Cell{Kind: Number, Number: 89500}},
		{"bcv", 1, 7, 3, Cell{Kind: String, String: "(a) Cotización M.E./US$"}},
		{"bcbo", 0, 10, 1, Cell{Kind: Number, Number: 8.03}},
	}
	for _, tt := range tests {
		sheets := open(t, tt.cassette, tt.interaction)
		if got := sheets[0].Rows[tt.row].At(tt.col); got != tt.want {
			t.Errorf("%s row %d col %d = %+v, want %+v", tt.cassette, tt.row, tt.col, got, tt.want)
		}
	}
}

func TestRowAtPastTheEnd(t *testing.T) {
	if got := (Row{{Kind: Number, Number: 1}}).At(5); got.Kind != Empty {
		t.Errorf("At(5) = %+v, want empty", got)
	}
}

func TestIsDate(t *testing.T) {
	for format, want := range map[string]bool{
		"M/D/YY": true, "dd/mm/yyyy": true, "[$-409]d-mmm-yy": true, "h:mm": true,
		"GENERAL": false, "0.00": false, "#,##0.0000": false, "@": false,
	} {
		if got := isDate(format); got != want {
			t.Errorf("isDate(%q) = %v, want %v", format, got, want)
		}
	}
}

func TestSerialDate(t *testing.T) {
	if got := serialDate(46272); !got.Equal(time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("serialDate(46272) = %v", got)
	}
	if got := serialDate(46272.75); !got.Equal(time.Date(2026, 9, 7, 18, 0, 0, 0, time.UTC)) {
		t.Errorf("serialDate(46272.75) = %v", got)
	}
}
