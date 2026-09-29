package cbi

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "cbi", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchWithIQDAsQuote(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 2, 1), adapter.Date(2026, 2, 28))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Quote != "IQD" {
			t.Fatalf("quote = %q, want IQD", r.Quote)
		}
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 2, 1), adapter.Date(2026, 2, 28))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	first := rates[0].Date
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 5 {
		t.Errorf("got %d rates on %s, want more than 5", n, first.Format(time.DateOnly))
	}
}

func TestFetchUSDAroundOfficialPeg(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 2, 10), adapter.Date(2026, 2, 11))
	for _, r := range rates {
		if r.Base == "USD" && r.Date.Equal(adapter.Date(2026, 2, 11)) {
			if math.Abs(r.Rate-1310) > 30 {
				t.Errorf("USD/IQD = %v, want 1310 +/- 30", r.Rate)
			}
			return
		}
	}
	t.Fatal("no USD rate on 2026-02-11")
}

func TestFetchFiltersByDateRange(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 2, 5), adapter.Date(2026, 2, 10))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if !r.Date.After(adapter.Date(2026, 2, 5)) || r.Date.After(adapter.Date(2026, 2, 10)) {
			t.Errorf("date %s outside (2026-02-05, 2026-02-10]", r.Date.Format(time.DateOnly))
		}
	}
}

func TestDiscoverFileURLPicksGoldLink(t *testing.T) {
	html := `
<a href="https://cbi.iq/static/uploads/up/file-111.xlsx">الدينار تجاه الدولار</a>
<a href="https://cbi.iq/static/uploads/up/file-222.xlsx">العملات الرئيسية والذهب</a>
<a href="https://cbi.iq/static/uploads/up/file-333.xlsx">العملات الأجنبية تجاه الدولار</a>
`
	got, err := discoverFileURL([]byte(html))
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://cbi.iq/static/uploads/up/file-222.xlsx"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDiscoverFileURLFailsWithoutGoldLink(t *testing.T) {
	html := `<a href="https://cbi.iq/static/uploads/up/file-111.xlsx">USD only</a>`
	if _, err := discoverFileURL([]byte(html)); err == nil {
		t.Error("want an error when no xlsx link mentions gold")
	}
}

func TestExtractCode(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"S.FR", "CHF"},
		{"UAE", "AED"},
		{"Gold", "XAU"},
		{"SDR", "XDR"},
		{"Saudi Arabian Riyal SAR", "SAR"},
	} {
		if got := extractCode(tc.text); got != tc.want {
			t.Errorf("extractCode(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

// workbook builds an XLSX with one sheet per name, each filled row by row from A1.
func workbook(t *testing.T, sheets map[string][][]any) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	for name, rows := range sheets {
		if _, err := f.NewSheet(name); err != nil {
			t.Fatal(err)
		}
		for i, row := range rows {
			if err := f.SetSheetRow(name, fmt.Sprintf("A%d", i+1), &row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := f.DeleteSheet("Sheet1"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestParseTwoAndThreeColumnLayouts(t *testing.T) {
	data := workbook(t, map[string][][]any{
		"2009": {
			{"", "USD", "", "Saudi Arabian Riyal SAR"},
			{"Date", "Buy", "Sell", "Buy", "Sell"},
			{"July 2009"},
			{1, 1170, 1180, 310, 320},
			{2, 0, 1180, "n/a", 320}, // zero and text cells are skipped
			{31, 1171, 1181},
		},
		"2025": {
			{"", "", "S.FR", "", "", "Gold"},
			{"Date", "Buy", "Sell", "Sell 2", "Buy", "Sell", "Sell 2"},
			{"Spet.2025"},
			{"Average"},
			{31, 1600, 1620, 1630, 100, 110, 120}, // no 31 September
			{30, 1600, 1620, 1630, 100, 110, 120},
		},
		"Notes": {{"Buy", "Sell"}, {"x"}},
	})
	rates, err := parse(data)
	if err != nil {
		t.Fatal(err)
	}
	type key struct {
		date, base string
		rate       float64
	}
	var got []key
	for _, r := range rates {
		if r.Quote != "IQD" {
			t.Errorf("quote = %q", r.Quote)
		}
		if r.Base == "XAU" && (r.Bid == nil || *r.Bid != 100 || r.Ask == nil || *r.Ask != 110) {
			t.Errorf("XAU bid/ask = %v/%v, want 100/110 (Sell 2 ignored)", r.Bid, r.Ask)
		}
		got = append(got, key{r.Date.Format(time.DateOnly), r.Base, r.Rate})
	}
	want := []key{
		{"2009-07-01", "SAR", 315},
		{"2009-07-01", "USD", 1175},
		{"2009-07-31", "USD", 1176},
		{"2025-09-30", "CHF", 1610},
		{"2025-09-30", "XAU", 105},
	}
	slices.SortFunc(got, func(a, b key) int { return strings.Compare(a.date+a.base, b.date+b.base) })
	if !slices.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestParseExcelDateSerialMonth(t *testing.T) {
	data := workbook(t, map[string][][]any{
		"2010": {
			{"", "USD"},
			{"", "Buy", "Sell"},
			{40299}, // 2010-05-01
			{3, 1170, 1180},
		},
	})
	rates, err := parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || !rates[0].Date.Equal(adapter.Date(2010, 5, 3)) {
		t.Errorf("got %v, want one rate on 2010-05-03", rates)
	}
}

func TestParseToleratesEmptyYear(t *testing.T) {
	rates, err := parse(workbook(t, map[string][][]any{"2027": nil}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseFailsWithoutBuySellLayout(t *testing.T) {
	_, err := parse(workbook(t, map[string][][]any{"2026": {{"Date", "USD"}, {1, 1310}}}))
	if err == nil {
		t.Error("want an error when a year sheet has no Buy/Sell header")
	}
}

func TestParseFailsOnNonWorkbook(t *testing.T) {
	if _, err := parse([]byte("<html><body>Service unavailable</body></html>")); err == nil {
		t.Error("want an error when the download is not a workbook")
	}
}

func TestMonthFromLabel(t *testing.T) {
	for _, tc := range []struct {
		label string
		want  time.Month
	}{
		{"Jan. 2026", 1},
		{"Jan.", 1},
		{"Feb.2026", 2},
		{"*Mar. 2020", 3}, // footnote marker
		{"40299", 5},      // Excel date serial
		{"June 2013", 6},
		{"July 2009", 7},
		{"Sept.2026", 9},
		{"Spet.2016", 9}, // historical typo
		{"Dec, 2009", 12},
	} {
		got, ok := monthFromLabel(tc.label)
		if !ok || got != tc.want {
			t.Errorf("monthFromLabel(%q) = %v, %v; want %v", tc.label, got, ok, tc.want)
		}
	}
}

func TestMonthFromLabelRejectsNonMonths(t *testing.T) {
	for _, label := range []string{"Average", "2026", "1"} {
		if got, ok := monthFromLabel(label); ok {
			t.Errorf("monthFromLabel(%q) = %v, want none", label, got)
		}
	}
}

// Ruby's \b is Unicode-aware, so a code glued to Arabic text is not a token.
func TestExtractCodeUnicodeBoundary(t *testing.T) {
	if got := extractCode("الدولارUSD"); got != "" {
		t.Errorf("got %q, want no code", got)
	}
	if got := extractCode("ABCDE"); got != "" {
		t.Errorf("got %q, want no code", got)
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"testdata/golden/fetch.json", adapter.Date(2026, 2, 1), adapter.Date(2026, 2, 28)},
		{"testdata/golden/all.json", time.Time{}, time.Time{}},
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

// Ruby's strip leaves U+00A0 in place, so "Gold " is not the Gold alias and " Jan." is not a month.
func TestStripKeepsNonASCIISpace(t *testing.T) {
	if got := extractCode("Gold "); got != "" {
		t.Errorf("extractCode = %q, want no code", got)
	}
	if got, ok := monthFromLabel(" Jan."); ok {
		t.Errorf("monthFromLabel = %v, want none", got)
	}
	if got := extractCode(" Gold\n"); got != "XAU" {
		t.Errorf("extractCode = %q, want XAU", got)
	}
}
