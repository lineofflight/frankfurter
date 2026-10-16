package boa

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
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
	a := New(vcrtest.Client(t, "boa", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func find(rates []adapter.Rate, base string, date time.Time) *adapter.Rate {
	for i, r := range rates {
		if r.Base == base && (date.IsZero() || r.Date.Equal(date)) {
			return &rates[i]
		}
	}
	return nil
}

func TestFetchUsesDZDAsQuote(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 4, 28), adapter.Date(2026, 4, 30))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Quote != "DZD" {
			t.Fatalf("quote = %s, want DZD", r.Quote)
		}
	}
}

func TestFetchCoversMultipleBases(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 4, 28), adapter.Date(2026, 4, 30))
	var bases []string
	for _, r := range rates {
		bases = append(bases, r.Base)
	}
	for _, want := range []string{"USD", "EUR", "GBP", "JPY"} {
		if !slices.Contains(bases, want) {
			t.Errorf("bases missing %s", want)
		}
	}
}

func TestFetchMapsEuroSheetToEUR(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 4, 28), adapter.Date(2026, 4, 30))
	eur := find(rates, "EUR", adapter.Date(2026, 4, 30))
	if eur == nil {
		t.Fatal("no EUR rate on 2026-04-30")
	}
	if math.Abs(eur.Rate-154.76) > 0.5 {
		t.Errorf("EUR rate = %v, want about 154.76", eur.Rate)
	}
}

func TestFetchNormalisesJPYPerUnit(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 4, 28), adapter.Date(2026, 4, 30))
	jpy := find(rates, "JPY", adapter.Date(2026, 4, 30))
	if jpy == nil {
		t.Fatal("no JPY rate on 2026-04-30")
	}
	if math.Abs(jpy.Rate-0.828) > 0.05 {
		t.Errorf("JPY rate = %v, want about 0.828", jpy.Rate)
	}
}

func TestFetchFiltersByDateRange(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 4, 28), adapter.Date(2026, 4, 30))
	lo, hi := rates[0].Date, rates[0].Date
	for _, r := range rates {
		if r.Date.Before(lo) {
			lo = r.Date
		}
		if r.Date.After(hi) {
			hi = r.Date
		}
	}
	if !lo.Equal(adapter.Date(2026, 4, 28)) {
		t.Errorf("min date = %s, want 2026-04-28", lo)
	}
	if !hi.Equal(adapter.Date(2026, 4, 30)) {
		t.Errorf("max date = %s, want 2026-04-30", hi)
	}
}

func TestFetchUSDInPlausibleRange(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 4, 30), adapter.Date(2026, 4, 30))
	usd := find(rates, "USD", time.Time{})
	if usd == nil {
		t.Fatal("no USD rate")
	}
	if math.Abs(usd.Rate-132.5) > 5.0 {
		t.Errorf("USD rate = %v, want about 132.5", usd.Rate)
	}
}

type stubTransport string

func (s stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(string(s))),
		Request:    req,
	}, nil
}

// The Ruby spec stubs download to return a hub page without the link.
func TestFetchFailsWhenArchiveLinkMissingFromHub(t *testing.T) {
	a := New(&http.Client{Transport: stubTransport("<html><body>no link here</body></html>")})
	_, err := a.Fetch(context.Background(), time.Time{}, time.Time{})
	if err == nil || !strings.Contains(err.Error(), "archive XLSX link not found") {
		t.Fatalf("err = %v, want archive link error", err)
	}
}

func TestSheetCurrency(t *testing.T) {
	for name, want := range map[string]string{
		"USD - DZD":  "USD",
		"EURO - DZD": "EUR",
		"gbp/DZD":    "GBP",
		" JPY-DZD":   "JPY",
		"Feuil1":     "",
		"":           "",
		" - DZD":     "",
	} {
		got, ok := sheetCurrency(name)
		if !ok {
			got = ""
		}
		if got != want {
			t.Errorf("sheetCurrency(%q) = %q, want %q", name, got, want)
		}
	}
}

type sheet struct {
	name string
	rows [][]any
}

// workbook builds an XLSX with the given sheets in order, writing each row from
// column A. Go strings become shared strings, numbers become numeric cells and
// nil becomes a cell with no value.
func workbook(t *testing.T, sheets ...sheet) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	for i, s := range sheets {
		if i == 0 {
			if err := f.SetSheetName("Sheet1", s.name); err != nil {
				t.Fatal(err)
			}
		} else if _, err := f.NewSheet(s.name); err != nil {
			t.Fatal(err)
		}
		for r, row := range s.rows {
			cell, err := excelize.CoordinatesToCellName(1, r+1)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetSheetRow(s.name, cell, &row); err != nil {
				t.Fatal(err)
			}
		}
	}
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func parseWorkbook(t *testing.T, data []byte, after, upto time.Time) []adapter.Rate {
	t.Helper()
	rates, err := parse(data, after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestParseSkipsHeadersBlanksAndZeros(t *testing.T) {
	data := workbook(t,
		sheet{"JPY - DZD", [][]any{
			{"Date", "Cours"},
			{46140, 82.8},
			{46141.0, 0},
			{46142, nil},
			{46143},
			{46144.9, 83},
			{46145, "n/a"},
		}},
		// Sheets not named after a currency are ignored, numbers and all.
		sheet{"Feuil1", [][]any{{46140, 99}}},
	)
	got := parseWorkbook(t, data, time.Time{}, time.Time{})
	want := []adapter.Rate{
		{Date: adapter.Date(2026, 4, 28), Base: "JPY", Quote: "DZD", Rate: 0.828},
		{Date: adapter.Date(2026, 5, 2), Base: "JPY", Quote: "DZD", Rate: 0.83},
	}
	if !slices.Equal(got, want) {
		t.Errorf("parse = %v, want %v", got, want)
	}
}

func TestParseBoundsAreInclusive(t *testing.T) {
	data := workbook(t, sheet{"USD - DZD", [][]any{{46139, 1}, {46140, 2}, {46141, 3}, {46142, 4}}})
	got := parseWorkbook(t, data, adapter.Date(2026, 4, 28), adapter.Date(2026, 4, 29))
	if len(got) != 2 || got[0].Rate != 2 || got[1].Rate != 3 {
		t.Errorf("parse = %v, want rates 2 and 3", got)
	}
}

func TestParseSkipsNumericLookingStringCells(t *testing.T) {
	data := workbook(t, sheet{"USD - DZD", [][]any{{"46140", 1.5}, {46141, "2.5"}, {46142, 3.5}}})
	got := parseWorkbook(t, data, time.Time{}, time.Time{})
	want := []adapter.Rate{{Date: adapter.Date(2026, 4, 30), Base: "USD", Quote: "DZD", Rate: 3.5}}
	if !slices.Equal(got, want) {
		t.Errorf("parse = %v, want %v", got, want)
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"fetch.json", adapter.Date(2026, 4, 28), adapter.Date(2026, 4, 30)},
		{"fetch_one_day.json", adapter.Date(2026, 4, 30), adapter.Date(2026, 4, 30)},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, "testdata/golden/"+tc.file)
			rates, err := New(g.Client(t)).Fetch(context.Background(), tc.after, tc.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
