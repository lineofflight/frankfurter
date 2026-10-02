package cbs

import (
	"bytes"
	"context"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
	"github.com/xuri/excelize/v2"
)

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "cbs", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func uniq[T comparable](rates []adapter.Rate, f func(adapter.Rate) T) []T {
	var out []T
	for _, r := range rates {
		if v := f(r); !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func TestFetchArchiveWorkbook(t *testing.T) {
	if rates := fetch(t, adapter.Date(2026, 5, 1), adapter.Date(2026, 5, 22)); len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchEmitsWSTBase(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 1), adapter.Date(2026, 5, 22))
	got := uniq(rates, func(r adapter.Rate) string { return r.Base })
	if !slices.Equal(got, []string{"WST"}) {
		t.Errorf("bases = %v, want [WST]", got)
	}
}

func TestFetchCoversNineQuotes(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 1), adapter.Date(2026, 5, 22))
	quotes := uniq(rates, func(r adapter.Rate) string { return r.Quote })
	for _, iso := range []string{"AUD", "CNH", "CNY", "EUR", "FJD", "GBP", "JPY", "NZD", "USD"} {
		if !slices.Contains(quotes, iso) {
			t.Errorf("quotes %v missing %s", quotes, iso)
		}
	}
}

func TestFetchRespectsBounds(t *testing.T) {
	after, upto := adapter.Date(2026, 5, 14), adapter.Date(2026, 5, 17)
	rates := fetch(t, after, upto)
	dates := uniq(rates, func(r adapter.Rate) time.Time { return r.Date })
	if len(dates) == 0 {
		t.Fatal("no rates")
	}
	lo := slices.MinFunc(dates, time.Time.Compare)
	hi := slices.MaxFunc(dates, time.Time.Compare)
	if lo.Before(after) {
		t.Errorf("min date %v before %v", lo, after)
	}
	if hi.After(upto) {
		t.Errorf("max date %v after %v", hi, upto)
	}
}

func TestFetchUSDPlausible(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 1), adapter.Date(2026, 5, 22))
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Quote == "USD" })
	if i < 0 {
		t.Fatal("no USD row")
	}
	if usd := rates[i].Rate; usd <= 0.2 || usd >= 0.6 {
		t.Errorf("USD rate = %v, want between 0.2 and 0.6", usd)
	}
}

func TestFetchRowPerCurrencyEachDate(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 22))
	for _, date := range uniq(rates, func(r adapter.Rate) time.Time { return r.Date }) {
		var quotes []string
		for _, r := range rates {
			if r.Date.Equal(date) {
				quotes = append(quotes, r.Quote)
			}
		}
		for _, iso := range []string{"USD", "EUR"} {
			if !slices.Contains(quotes, iso) {
				t.Errorf("%v: quotes %v missing %s", date, quotes, iso)
			}
		}
	}
}

func TestArchiveURL(t *testing.T) {
	tests := []struct {
		name, html, want string
	}{
		{
			"resolves the date-stamped workbook link",
			`<ul class="downloads">
  <li><a href="/media/Historical-Daily-Rates-June032026.xlsx">Historical rates</a></li>
</ul>`,
			"https://cbs.gov.ws/media/Historical-Daily-Rates-June032026.xlsx",
		},
		{
			"percent-encodes spaces in the filename",
			`<ul class="downloads">
  <li><a href="/media/Historical Daily Rates-220626.xlsx">Historical rates</a></li>
</ul>`,
			"https://cbs.gov.ws/media/Historical%20Daily%20Rates-220626.xlsx",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := archiveURL(tt.html)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("archiveURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestArchiveURLErrorsWithoutLink(t *testing.T) {
	if _, err := archiveURL("<html><body>no link here</body></html>"); err == nil {
		t.Error("expected an error")
	}
}

func TestParseMapsLabelsToISO(t *testing.T) {
	for label, want := range map[string]string{
		"TALA/USD":  "USD",
		"TALA/EURO": "EUR",
		"TALA/YEN":  "JPY",
		"TALA/CNY":  "CNY",
		"TALA/CNH":  "CNH",
	} {
		if got := currencies[label]; got != want {
			t.Errorf("currencies[%q] = %q, want %q", label, got, want)
		}
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 5, 1), adapter.Date(2026, 5, 22))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

func TestGoldenArchive(t *testing.T) {
	g := golden.Load(t, "testdata/golden/archive.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

// workbook builds an XLSX with one sheet per entry, writing each row from
// column A.
func workbook(t *testing.T, sheets map[string][][]any) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	for name, rows := range sheets {
		if _, err := f.NewSheet(name); err != nil {
			t.Fatal(err)
		}
		for i, row := range rows {
			cell, _ := excelize.CoordinatesToCellName(1, i+1)
			if err := f.SetSheetRow(name, cell, &row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, ok := sheets["Sheet1"]; !ok {
		if err := f.DeleteSheet("Sheet1"); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestParseSections(t *testing.T) {
	rows := [][]any{
		{nil, "MAY"},
		{nil, "DATE", "TALA/USD", "tala/euro", "OTHER"},
		// USD and EUR are kept; the OTHER column is unmapped.
		{nil, 46143, 0.36847, 0.315, 9},
		// Zero, negative, blank and text rates are dropped.
		{nil, 46144, 0, -1},
		{nil, 46145, " ", "n/a", 1},
		// Out-of-range serials and text labels are not dates.
		{nil, 30000, 0.4},
		{nil, "OTHER", 0.4},
		{nil, "NaN", 0.4},
		// A month banner resets the header map, so the next row has no columns.
		{nil, "June"},
		{nil, 46146.5, 0.4},
		{nil, "DATE", "TALA/USD"},
		// Fractional serials truncate to the day.
		{nil, 46147.75, 0.37},
	}
	rates, err := parse(workbook(t, map[string][][]any{"2026": rows}))
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{
		{Date: adapter.Date(2026, 5, 1), Base: "WST", Quote: "USD", Rate: 0.36847},
		{Date: adapter.Date(2026, 5, 1), Base: "WST", Quote: "EUR", Rate: 0.315},
		{Date: adapter.Date(2026, 5, 5), Base: "WST", Quote: "USD", Rate: 0.37},
	}
	if !slices.Equal(rates, want) {
		t.Errorf("parse = %v, want %v", rates, want)
	}
}

func TestParseReadsEverySheet(t *testing.T) {
	sheet := func(serial int, rate float64) [][]any {
		return [][]any{{nil, "DATE", "TALA/USD"}, {nil, serial, rate}}
	}
	rates, err := parse(workbook(t, map[string][][]any{
		"2025":  sheet(46143, 0.2),
		"2026":  sheet(46144, 0.3),
		"Notes": {{nil, "Rates are per tala"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	got := uniq(rates, func(r adapter.Rate) float64 { return r.Rate })
	slices.Sort(got)
	if !slices.Equal(got, []float64{0.2, 0.3}) {
		t.Errorf("rates = %v, want [0.2 0.3]", got)
	}
}

func TestParseErrorsOnHTMLPage(t *testing.T) {
	if _, err := parse([]byte("<html><body>Page not found</body></html>")); err == nil {
		t.Error("expected an error")
	}
}

func TestParseErrorsWithoutRates(t *testing.T) {
	if _, err := parse(workbook(t, map[string][][]any{"Sheet1": nil})); err == nil {
		t.Error("expected an error")
	}
}
