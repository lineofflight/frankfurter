package xls

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

// testdata/generate.rb writes the workbook with the gem and records what the gem reads back from it. Its shared
// strings run across CONTINUE records, split mid-string, in both compressed and UTF-16 form.
func TestOpenMatchesTheGemAcrossContinueRecords(t *testing.T) {
	data, err := os.ReadFile("testdata/workbook.xls")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := workbookStream(data)
	if err != nil {
		t.Fatal(err)
	}
	globals, err := substream(stream, 0)
	if err != nil {
		t.Fatal(err)
	}
	continues := 0
	for _, r := range globals {
		if r.typ == recContinue {
			continues++
		}
	}
	if continues < 2 {
		t.Fatalf("fixture has %d CONTINUE records, want the SST to span several", continues)
	}

	var want []Sheet
	b, err := os.ReadFile("testdata/workbook.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	got, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("%d sheets, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i].Name || len(got[i].Rows) != len(want[i].Rows) {
			t.Errorf("sheet %d = %q with %d rows, want %q with %d", i, got[i].Name, len(got[i].Rows), want[i].Name, len(want[i].Rows))
			continue
		}
		for r := range want[i].Rows {
			if !reflect.DeepEqual(got[i].Rows[r], want[i].Rows[r]) {
				t.Errorf("sheet %q row %d = %+v, want %+v", want[i].Name, r, got[i].Rows[r], want[i].Rows[r])
			}
		}
	}
}

func TestReadSSTFollowsTheContinueRules(t *testing.T) {
	segs := [][]byte{
		cat(
			u32(4), u32(4), // total, unique
			[]byte{3, 0, 0x08}, u16(1), []byte("abc"), make([]byte, 4), // rich text: one formatting run after the characters
			[]byte{4, 0, 0x04}, u32(6), []byte("de"), // phonetic data; the characters break off here
		),
		cat(
			[]byte{0x01}, utf16le("fg"), // the rest of the characters restate their flags, now UTF-16
			make([]byte, 2), // the phonetic data runs on without a flags byte
		),
		cat(
			make([]byte, 4),
			[]byte{1, 0, 0x01}, utf16le("ж"),
			[]byte{2, 0, 0x00}, // a header that fills its record
		),
		cat([]byte{0x00}, []byte("hi")),
	}
	got, err := readSST(segs)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"abc", "defg", "ж", "hi"}; !reflect.DeepEqual(got, want) {
		t.Errorf("readSST = %q, want %q", got, want)
	}

	if _, err := readSST(segs[:3]); !errors.Is(err, errTruncated) {
		t.Errorf("readSST without the last CONTINUE: err = %v, want errTruncated", err)
	}
}

// The gem never writes LABEL records, error values or formulas, so these rows are built by hand.
func TestReadRowsRecordsTheGemDoesNotWrite(t *testing.T) {
	wb := &workbook{}
	cell := func(r, c int) []byte { return cat(u16(r), u16(c), u16(0)) }
	rows, err := wb.readRows([]record{
		{recLabel, cat(cell(0, 0), u16(10), []byte{0}, []byte("Cotizaci\xf3n"))},
		{recLabel, cat(cell(0, 1), u16(4), []byte{1}, utf16le("Курс"))},
		{recBoolErr, cat(cell(1, 0), []byte{0x2A, 1})},
		{recBoolErr, cat(cell(1, 1), []byte{1, 0})},
		{0x0006, cat(cell(1, 2), make([]byte, 16))}, // FORMULA
		{recBlank, cell(2, 3)},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range rows {
		rows[i] = trimBlank(rows[i])
	}
	want := []Row{
		{{Kind: String, String: "Cotización"}, {Kind: String, String: "Курс"}},
		{{Kind: Error, String: "#N/A"}, {Kind: Bool, Number: 1}},
		{},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("readRows = %+v, want %+v", rows, want)
	}
}

func TestSubstreamStepsOverNestedSubstreams(t *testing.T) {
	stream := cat(
		rec(recBOF, u16(biff8)...), rec(recBOF, u16(biff8)...), rec(recEOF), // an embedded chart
		rec(recBlank, make([]byte, 6)...), rec(recEOF),
	)
	recs, err := substream(stream, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 5 || recs[3].typ != recBlank {
		t.Errorf("substream = %+v, want all five records", recs)
	}
	if _, err := substream(stream[:len(stream)-4], 0); !errors.Is(err, errTruncated) {
		t.Errorf("substream without its EOF: err = %v, want errTruncated", err)
	}
}

func TestOpenRejectsOtherFiles(t *testing.T) {
	if _, err := Open([]byte("Period,Currency\n")); err == nil {
		t.Error("Open(csv) succeeded")
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

func cat(parts ...[]byte) []byte {
	var b []byte
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}

func u16(v int) []byte { return le.AppendUint16(nil, uint16(v)) }
func u32(v int) []byte { return le.AppendUint32(nil, uint32(v)) }

func utf16le(s string) []byte {
	var b []byte
	for _, r := range s {
		b = le.AppendUint16(b, uint16(r))
	}
	return b
}

func rec(typ uint16, data ...byte) []byte {
	return cat(u16(int(typ)), u16(len(data)), data)
}
