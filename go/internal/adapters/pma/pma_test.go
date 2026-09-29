package pma

import (
	"bytes"
	"context"
	"slices"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

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

// A session hiccup can serve an HTML page or nothing at all instead of the workbook.
func TestParseErrors(t *testing.T) {
	for name, data := range map[string][]byte{
		"html error page": []byte("<!DOCTYPE html><html><body>Session expired</body></html>"),
		"empty body":      nil,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parse(data); err == nil {
				t.Error("got no error")
			}
		})
	}
}

func TestParseEmptyWorkbook(t *testing.T) {
	rates, err := parse(workbook(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

// The export uses shared strings throughout, but numeric and rich-text cells read the same.
func TestParseNumericAndRichTextCells(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	sh := f.GetSheetName(0)
	if err := f.SetSheetRow(sh, "A2", &[]any{"2026/09/01", "USD/ILS", 2.9903, 2.9944, 2.9924}); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellStr(sh, "A3", "2026/09/01"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellRichText(sh, "B3", []excelize.RichTextRun{{Text: "GBP/"}, {Text: "USD"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellValue(sh, "E3", 1.3547); err != nil {
		t.Fatal(err)
	}
	rates, err := parse(write(t, f))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 || !rates[0].Date.Equal(adapter.Date(2026, 9, 1)) || rates[0].Base != "USD" ||
		rates[0].Rate != 2.9924 || rates[1].Base != "GBP" || rates[1].Quote != "USD" || rates[1].Rate != 1.3547 {
		t.Errorf("got %+v", rates)
	}
}

func TestParseReadsFirstSheetOnly(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetRow("Sheet1", "A1", &[]any{"2026/09/01", "USD/ILS", "", "", "2.9924"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.NewSheet("Other"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetSheetRow("Other", "A1", &[]any{"2026/09/01", "EUR/USD", "", "", "1.1"}); err != nil {
		t.Fatal(err)
	}
	rates, err := parse(write(t, f))
	if err != nil {
		t.Fatal(err)
	}
	if got := pairs(rates); !slices.Equal(got, [][2]string{{"USD", "ILS"}}) {
		t.Errorf("got %v", got)
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

// workbook builds a workbook shaped like the export: every cell a string, data from row 2.
func workbook(t *testing.T, rows [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	sh := f.GetSheetName(0)
	for n, row := range rows {
		for i, v := range row {
			ref, err := excelize.CoordinatesToCellName(i+1, n+2)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellStr(sh, ref, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	return write(t, f)
}

func write(t *testing.T, f *excelize.File) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
