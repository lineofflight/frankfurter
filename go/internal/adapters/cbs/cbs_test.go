package cbs

import (
	"archive/zip"
	"bytes"
	"context"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
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

// workbook builds a minimal XLSX from shared-string items and sheet row XML.
func workbook(t *testing.T, strs string, sheets map[string]string) []byte {
	t.Helper()
	files := map[string]string{}
	if strs != "" {
		files["xl/sharedStrings.xml"] = `<?xml version="1.0"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` + strs + `</sst>`
	}
	for name, rows := range sheets {
		files[name] = `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` + rows + `</sheetData></worksheet>`
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestParseSections(t *testing.T) {
	// 0 MAY, 1 DATE, 2 TALA/USD, 3 tala/euro, 4 June, 5 OTHER, 6 n/a
	strs := `<si><t>MAY</t></si><si><t>DATE</t></si><si><t>TALA/USD</t></si><si><t>tala/euro</t></si>` +
		`<si><t>June</t></si><si><t>OTHER</t></si><si><t>n/a</t></si>`
	sheet := `<row r="1"><c r="B1" t="s"><v>0</v></c></row>` +
		`<row r="2"><c r="B2" t="s"><v>1</v></c><c r="C2" t="s"><v>2</v></c><c r="D2" t="s"><v>3</v></c><c r="E2" t="s"><v>5</v></c></row>` +
		// USD and EUR are kept; the OTHER column is unmapped.
		`<row r="3"><c r="B3"><v>46143</v></c><c r="C3"><v>0.36847</v></c><c r="D3"><v>0.315</v></c><c r="E3"><v>9</v></c></row>` +
		// Zero, negative, blank and string rates are dropped.
		`<row r="4"><c r="B4"><v>46144</v></c><c r="C4"><v>0</v></c><c r="D4"><v>-1</v></c></row>` +
		`<row r="5"><c r="B5"><v>46145</v></c><c r="C5"><v> </v></c><c r="D5" t="s"><v>6</v></c><c r="F5"><v>1</v></c></row>` +
		// Out-of-range serials and string labels are not dates.
		`<row r="6"><c r="B6"><v>30000</v></c><c r="C6"><v>0.4</v></c></row>` +
		`<row r="7"><c r="B7" t="s"><v>5</v></c><c r="C7"><v>0.4</v></c></row>` +
		// A month banner resets the header map, so the next row has no columns.
		`<row r="8"><c r="B8" t="s"><v>4</v></c></row>` +
		`<row r="9"><c r="B9"><v>46146.5</v></c><c r="C9"><v>0.4</v></c></row>` +
		`<row r="10"><c r="B10" t="s"><v>1</v></c><c r="C10" t="s"><v>2</v></c></row>` +
		// Fractional serials truncate to the day.
		`<row r="11"><c r="B11"><v>46147.75</v></c><c r="C11"><v>0.37</v></c></row>`
	rates, err := parse(workbook(t, strs, map[string]string{"xl/worksheets/sheet1.xml": sheet}))
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

func TestParseSheetsInNameOrder(t *testing.T) {
	strs := `<si><t>DATE</t></si><si><t>TALA/USD</t></si>`
	sheet := func(serial, rate string) string {
		return `<row r="1"><c r="B1" t="s"><v>0</v></c><c r="C1" t="s"><v>1</v></c></row>` +
			`<row r="2"><c r="B2"><v>` + serial + `</v></c><c r="C2"><v>` + rate + `</v></c></row>`
	}
	rates, err := parse(workbook(t, strs, map[string]string{
		"xl/worksheets/sheet2.xml":  sheet("46143", "0.2"),
		"xl/worksheets/sheet10.xml": sheet("46144", "0.3"),
		"xl/worksheets/other.xml":   sheet("46145", "0.4"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	got := uniq(rates, func(r adapter.Rate) float64 { return r.Rate })
	if !slices.Equal(got, []float64{0.3, 0.2}) {
		t.Errorf("rates = %v, want [0.3 0.2]", got)
	}
}

func TestParseErrorsWithoutSharedStrings(t *testing.T) {
	if _, err := parse(workbook(t, "", map[string]string{"xl/worksheets/sheet1.xml": ""})); err == nil {
		t.Error("expected an error")
	}
}
