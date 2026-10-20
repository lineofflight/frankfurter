package nrbt

import (
	"bytes"
	"context"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "nrbt", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func dateRange(rates []adapter.Rate) (lo, hi time.Time) {
	for i, r := range rates {
		if i == 0 || r.Date.Before(lo) {
			lo = r.Date
		}
		if i == 0 || r.Date.After(hi) {
			hi = r.Date
		}
	}
	return lo, hi
}

func findQuote(rates []adapter.Rate, quote string) *adapter.Rate {
	for i := range rates {
		if rates[i].Quote == quote {
			return &rates[i]
		}
	}
	return nil
}

func TestFetchWithTOPBase(t *testing.T) {
	rates := fetch(t, adapter.Date(2025, 1, 2), adapter.Date(2025, 1, 6))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Base != "TOP" {
			t.Fatalf("base = %q, want TOP", r.Base)
		}
	}
}

func TestFetchCoversAllTwelveQuotes(t *testing.T) {
	rates := fetch(t, adapter.Date(2025, 1, 2), adapter.Date(2025, 1, 6))
	var quotes []string
	for _, r := range rates {
		quotes = append(quotes, r.Quote)
	}
	slices.Sort(quotes)
	quotes = slices.Compact(quotes)
	want := []string{"AUD", "CAD", "CHF", "EUR", "FJD", "GBP", "JPY", "NZD", "SEK", "SGD", "USD", "WST"}
	if !slices.Equal(quotes, want) {
		t.Errorf("quotes = %v, want %v", quotes, want)
	}
}

func TestFetchEmitsMidColumn(t *testing.T) {
	usd := findQuote(fetch(t, adapter.Date(2025, 1, 2), adapter.Date(2025, 1, 2)), "USD")
	if usd == nil {
		t.Fatal("no USD rate")
	}
	// MID rate for 2025-01-02 is published as 0.4106 in column U (MID block,
	// USD position).
	if math.Abs(usd.Rate-0.4106) > 0.0001 {
		t.Errorf("USD = %v, want 0.4106", usd.Rate)
	}
}

func TestFetchFiltersByDateRange(t *testing.T) {
	lo, hi := dateRange(fetch(t, adapter.Date(2025, 1, 2), adapter.Date(2025, 1, 6)))
	if !lo.Equal(adapter.Date(2025, 1, 2)) || !hi.Equal(adapter.Date(2025, 1, 6)) {
		t.Errorf("range = %v..%v, want 2025-01-02..2025-01-06", lo, hi)
	}
}

func TestFetchSkipsHolidayRows(t *testing.T) {
	// 2025-01-01 is "Public Holiday: New Year's Day": the rate cells hold a
	// shared-string label, not numbers.
	for _, r := range fetch(t, adapter.Date(2024, 12, 30), adapter.Date(2025, 1, 3)) {
		if r.Date.Equal(adapter.Date(2025, 1, 1)) {
			t.Fatalf("holiday row emitted: %+v", r)
		}
	}
}

func TestFetchUSDInPlausibleRange(t *testing.T) {
	usd := findQuote(fetch(t, adapter.Date(2025, 1, 2), adapter.Date(2025, 1, 2)), "USD")
	if usd == nil {
		t.Fatal("no USD rate")
	}
	// 1 TOP buys roughly 0.40-0.45 USD in 2025.
	if math.Abs(usd.Rate-0.42) > 0.05 {
		t.Errorf("USD = %v, want about 0.42", usd.Rate)
	}
}

func TestFetchReachesStartOf2017Archive(t *testing.T) {
	rates := fetch(t, adapter.Date(2017, 1, 2), adapter.Date(2017, 1, 6))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if lo, _ := dateRange(rates); !lo.Equal(adapter.Date(2017, 1, 3)) {
		t.Errorf("earliest = %v, want 2017-01-03", lo)
	}
}

func TestColumnIndex(t *testing.T) {
	for ref, want := range map[string]int{"A1": 0, "N5": 13, "O5": 14, "Z9": 25, "AA2": 26, "AM3": 38} {
		if got, ok := columnIndex(ref); !ok || got != want {
			t.Errorf("columnIndex(%q) = %d, %v; want %d", ref, got, ok, want)
		}
	}
}

// setCells writes values keyed by cell reference to sheet. Go strings become
// shared strings, numbers become numeric cells and nil becomes a cell with no
// value.
func setCells(t *testing.T, f *excelize.File, sheet string, cells map[string]any) {
	t.Helper()
	for ref, v := range cells {
		if err := f.SetCellValue(sheet, ref, v); err != nil {
			t.Fatal(err)
		}
	}
}

func write(t *testing.T, f *excelize.File) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// workbook builds an XLSX whose only sheet holds cells.
func workbook(t *testing.T, cells map[string]any) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	setCells(t, f, "Sheet1", cells)
	return write(t, f)
}

func parseWorkbook(t *testing.T, data []byte, after, upto time.Time) []adapter.Rate {
	t.Helper()
	rates, err := parse(data, after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestParseSkipsZeroBlankAndFlags(t *testing.T) {
	// Serial 45659 is 2025-01-02; a float serial is truncated as Ruby's to_i
	// does. BUY (B) and SELL (AB) are ignored.
	data := workbook(t, map[string]any{
		"A1": "Date", "O1": "AUD",
		"A2": 45659, "B2": 9.9, "O2": 0.6626, "P2": 0, "Q2": nil, "R2": "Public Holiday", "U2": 0.4106, "AB2": 8.8,
		"A3": 45660.75, "Z3": 0.55,
		"A4": 45661, "O4": "Public Holiday: ANZAC Day",
	})
	got := parseWorkbook(t, data, time.Time{}, time.Time{})
	want := []adapter.Rate{
		{Date: adapter.Date(2025, 1, 2), Base: "TOP", Quote: "AUD", Rate: 0.6626},
		{Date: adapter.Date(2025, 1, 2), Base: "TOP", Quote: "USD", Rate: 0.4106},
		{Date: adapter.Date(2025, 1, 3), Base: "TOP", Quote: "SGD", Rate: 0.55},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}

	// Both bounds are inclusive.
	got = parseWorkbook(t, data, adapter.Date(2025, 1, 3), adapter.Date(2025, 1, 3))
	if len(got) != 1 || !got[0].Date.Equal(adapter.Date(2025, 1, 3)) {
		t.Errorf("windowed = %+v, want only 2025-01-03", got)
	}
}

func TestParseSkipsNumericLookingStrings(t *testing.T) {
	data := workbook(t, map[string]any{"A1": "45660", "O1": "1.5", "A2": 45659, "O2": 0.6626})
	got := parseWorkbook(t, data, time.Time{}, time.Time{})
	want := []adapter.Rate{{Date: adapter.Date(2025, 1, 2), Base: "TOP", Quote: "AUD", Rate: 0.6626}}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseSkipsUnresolvableSheet(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	if _, err := f.NewSheet("Orphan"); err != nil {
		t.Fatal(err)
	}
	setCells(t, f, "Sheet1", map[string]any{"A1": 45659, "O1": 0.6626})
	setCells(t, f, "Orphan", map[string]any{"A1": 45660, "O1": 0.7})

	// Move the second sheet's part to a path no relationship targets: its data
	// stays in the package, but the sheet name no longer resolves to it.
	ws, ok := f.Sheet.LoadAndDelete("xl/worksheets/sheet2.xml")
	if !ok {
		t.Fatal("no sheet2 part")
	}
	f.Sheet.Store("xl/worksheets/orphan.xml", ws)

	got := parseWorkbook(t, write(t, f), time.Time{}, time.Time{})
	want := []adapter.Rate{{Date: adapter.Date(2025, 1, 2), Base: "TOP", Quote: "AUD", Rate: 0.6626}}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"testdata/golden/fetch.json", adapter.Date(2024, 12, 30), adapter.Date(2025, 1, 6)},
		{"testdata/golden/archive.json", time.Time{}, time.Time{}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, tc.file)
			rates, err := New(g.Client(t)).Fetch(context.Background(), tc.after, tc.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
