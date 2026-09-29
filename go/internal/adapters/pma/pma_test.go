package pma

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

// The POST body carries a per-session anti-forgery token, so match on method and URI only.
func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "pma", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI), vcrtest.AllowPlaybackRepeats))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func pairs(rates []adapter.Rate) [][2]string {
	var ps [][2]string
	for _, r := range rates {
		ps = append(ps, [2]string{r.Base, r.Quote})
	}
	return ps
}

func TestFetchDateRange(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 8))
	var dates []time.Time
	for _, r := range rates {
		if !slices.ContainsFunc(dates, r.Date.Equal) {
			dates = append(dates, r.Date)
		}
	}
	slices.SortFunc(dates, time.Time.Compare)
	if len(dates) == 0 {
		t.Fatal("no rates")
	}
	if !dates[0].Equal(adapter.Date(2026, 9, 1)) {
		t.Errorf("min date %v", dates[0])
	}
	if !dates[len(dates)-1].Equal(adapter.Date(2026, 9, 8)) {
		t.Errorf("max date %v", dates[len(dates)-1])
	}
	for _, d := range []time.Time{adapter.Date(2026, 9, 4), adapter.Date(2026, 9, 5)} { // Friday, Saturday
		if slices.ContainsFunc(dates, d.Equal) {
			t.Errorf("dates include %v", d)
		}
	}
}

func TestFetchAll25PairsPerDate(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 8))
	var sample []adapter.Rate
	for _, r := range rates {
		if r.Date.Equal(adapter.Date(2026, 9, 8)) {
			sample = append(sample, r)
		}
	}
	if len(sample) != 25 {
		t.Errorf("got %d rates, want 25", len(sample))
	}
	ps := pairs(sample)
	for _, p := range [][2]string{{"USD", "ILS"}, {"EUR", "USD"}, {"JOD", "ILS"}} {
		if !slices.Contains(ps, p) {
			t.Errorf("missing %v", p)
		}
	}
}

func TestFetchUSDILSPlausible(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 8))
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool {
		return r.Date.Equal(adapter.Date(2026, 9, 8)) && r.Base == "USD" && r.Quote == "ILS"
	})
	if i < 0 {
		t.Fatal("no USD/ILS")
	}
	if r := rates[i].Rate; r <= 2.5 || r >= 4.5 {
		t.Errorf("USD/ILS = %v", r)
	}
}

func TestFetchInvertedWindow(t *testing.T) {
	if rates := fetch(t, adapter.Date(2026, 9, 8), adapter.Date(2026, 9, 1)); len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseReadsPublishedMid(t *testing.T) {
	rates, err := parse(workbook(t, [][]string{{"2026/09/01", "USD/JOD", "0.7080", "0.7100", "0.709"}}))
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{{Date: adapter.Date(2026, 9, 1), Base: "USD", Quote: "JOD", Rate: 0.709}}
	if len(rates) != 1 || !rates[0].Date.Equal(want[0].Date) || rates[0].Base != "USD" || rates[0].Quote != "JOD" ||
		rates[0].Rate != 0.709 || rates[0].Bid != nil || rates[0].Ask != nil || rates[0].Mid != nil {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseKeepsPublishedDirection(t *testing.T) {
	rates, err := parse(workbook(t, [][]string{
		{"2026/09/01", "USD/ILS", "2.9903", "2.9944", "2.9924"},
		{"2026/09/01", "GBP/USD", "1.3546", "1.3547", "1.3547"},
		{"2026/09/01", "EGP/ILS", "0.0587", "0.0589", "0.0588"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]string{{"USD", "ILS"}, {"GBP", "USD"}, {"EGP", "ILS"}}
	if got := pairs(rates); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseStripsSeparatorsAndWhitespace(t *testing.T) {
	rates, err := parse(workbook(t, [][]string{
		{"2026/09/01", "USD/LBP", "89,446.50", "89,800.00", "89623.25"},
		{"2025/12/30", "XAU/USD", " 4,367.06 ", " 4,368.17 ", " 4,367.62 "},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var got []float64
	for _, r := range rates {
		got = append(got, r.Rate)
	}
	if want := []float64{89623.25, 4367.62}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseSkipsUnparseableRows(t *testing.T) {
	rates, err := parse(workbook(t, [][]string{
		{"التاريخ", "العملة", "الشراء", "البيع", "الوسطي"},
		{"2026/09/01", "USD", "1", "1", "1"},
		{"2026/09/01", "USD/ILS", "", "", ""},
		{"2026/09/01", "USD/ILS", "0", "0", "0"},
		{"2026/13/01", "USD/ILS", "3", "3", "3"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

// Ruby's messages start with "PMA:"; Go leaves the provider prefix to the caller (PORTING.md), so the missing-sheet
// case asserts on the part name instead.
func TestParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		entries map[string]string
		want    string
	}{
		{"no sheetData", map[string]string{
			"xl/worksheets/sheet1.xml": `<worksheet><dimension ref="A1"/></worksheet>`,
			"xl/sharedStrings.xml":     `<sst></sst>`,
		}, "sheetData"},
		{"shared strings missing", map[string]string{
			"xl/worksheets/sheet1.xml": `<worksheet><sheetData/></worksheet>`,
		}, "sharedStrings"},
		{"no sheet", map[string]string{"[Content_Types].xml": ""}, "sheet1.xml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parse(zipOf(t, tt.entries))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got error %v, want one mentioning %q", err, tt.want)
			}
		})
	}
}

func TestParseInlineAndPlainCells(t *testing.T) {
	sheet := `<worksheet><sheetData><row r="2">` +
		`<c r="A2" t="inlineStr"><is><t>2026/09/01</t></is></c>` +
		`<c r="B2" t="s"><v>0</v></c>` +
		`<c r="E2"><v>2.9924</v></c>` +
		`</row></sheetData></worksheet>`
	rates, err := parse(zipOf(t, map[string]string{
		"xl/worksheets/sheet1.xml": sheet,
		"xl/sharedStrings.xml":     `<sst><si><t>USD/ILS</t></si></sst>`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || !rates[0].Date.Equal(adapter.Date(2026, 9, 1)) || rates[0].Base != "USD" ||
		rates[0].Quote != "ILS" || rates[0].Rate != 2.9924 {
		t.Errorf("got %+v", rates)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 8))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

// workbook builds a workbook shaped like the export: every cell a shared string.
func workbook(t *testing.T, rows [][]string) []byte {
	t.Helper()
	var strs []string
	for _, row := range rows {
		for _, v := range row {
			if !slices.Contains(strs, v) {
				strs = append(strs, v)
			}
		}
	}
	var sheetRows strings.Builder
	for n, row := range rows {
		r := n + 2
		fmt.Fprintf(&sheetRows, `<row r="%d">`, r)
		for i, v := range row {
			fmt.Fprintf(&sheetRows, `<c r="%c%d" t="s"><v>%d</v></c>`, 'A'+i, r, slices.Index(strs, v))
		}
		sheetRows.WriteString(`</row>`)
	}
	var si strings.Builder
	for _, s := range strs {
		fmt.Fprintf(&si, "<si><t>%s</t></si>", s)
	}
	return zipOf(t, map[string]string{
		"xl/worksheets/sheet1.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <sheetData>` + sheetRows.String() + `</sheetData>
</worksheet>`,
		"xl/sharedStrings.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  ` + si.String() + `
</sst>`,
	})
}

func zipOf(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
