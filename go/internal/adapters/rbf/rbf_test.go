package rbf

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
	"github.com/xuri/excelize/v2"
)

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "rbf", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func find(rates []adapter.Rate, quote string) *adapter.Rate {
	for i := range rates {
		if rates[i].Quote == quote {
			return &rates[i]
		}
	}
	return nil
}

func TestFetchFJDBase(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 22))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Base != "FJD" {
			t.Fatalf("base = %s, want FJD", r.Base)
		}
	}
}

func TestFetchCoversAllEightQuotes(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 22))
	var quotes []string
	for _, r := range rates {
		if !slices.Contains(quotes, r.Quote) {
			quotes = append(quotes, r.Quote)
		}
	}
	slices.Sort(quotes)
	want := []string{"AUD", "CHF", "EUR", "GBP", "JPY", "NZD", "USD", "XDR"}
	if !slices.Equal(quotes, want) {
		t.Errorf("quotes = %v, want %v", quotes, want)
	}
}

func TestFetchRelabelsSDRAsXDR(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 22), adapter.Date(2026, 5, 22))
	xdr := find(rates, "XDR")
	if xdr == nil {
		t.Fatal("no XDR rate")
	}
	if xdr.Rate <= 0 {
		t.Errorf("XDR rate = %v, want > 0", xdr.Rate)
	}
}

func TestFetchUSDInPlausibleRange(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 22), adapter.Date(2026, 5, 22))
	usd := find(rates, "USD")
	if usd == nil {
		t.Fatal("no USD rate")
	}
	if usd.Rate <= 0.3 || usd.Rate >= 0.6 {
		t.Errorf("USD rate = %v, want between 0.3 and 0.6", usd.Rate)
	}
}

func TestFetchRespectsBounds(t *testing.T) {
	after, upto := adapter.Date(2026, 5, 20), adapter.Date(2026, 5, 21)
	rates := fetch(t, after, upto)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Date.Before(after) || r.Date.After(upto) {
			t.Errorf("date %s outside %s..%s", r.Date.Format(time.DateOnly), after.Format(time.DateOnly), upto.Format(time.DateOnly))
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchErrorsWhenLinkMissing(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("<html><body>no link here</body></html>")),
			Request:    r,
		}, nil
	})}
	if _, err := New(client).Fetch(context.Background(), time.Time{}, time.Time{}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestGolden(t *testing.T) {
	tests := []struct {
		file        string
		after, upto time.Time
	}{
		{"fetch", adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 22)},
		{"day", adapter.Date(2026, 5, 22), adapter.Date(2026, 5, 22)},
		{"bounds", adapter.Date(2026, 5, 20), adapter.Date(2026, 5, 21)},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			g := golden.Load(t, "testdata/golden/"+tt.file+".json")
			rates, err := New(g.Client(t)).Fetch(context.Background(), tt.after, tt.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}

// xlsx builds a workbook shaped like RBF's: the rates on the first sheet under
// title rows, and an empty second sheet. Each row is written from column A; nil
// leaves a cell blank.
func xlsx(t *testing.T, rows [][]any) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", "Exchange Rates"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.NewSheet("Sheet1"); err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("Exchange Rates", cell, &r); err != nil {
			t.Fatal(err)
		}
	}
	return save(t, f)
}

func save(t *testing.T, f *excelize.File) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Covers title rows above the header, padded labels, fractional date serials,
// text cells in data rows, zero, negative and blank rates, unmapped columns,
// rows without a date, and the inclusive after bound.
func TestParseFiltersCells(t *testing.T) {
	data := xlsx(t, [][]any{
		{"DAILY EXCHANGE RATES"},
		{"(RBF Mid-Rate Per Fiji Dollar)"},
		{"Period", " US$ ", "EURO", "XYZ"},
		{},
		{46163, 0.43},
		{46164.75, 0.44, 0, 9},
		{46165, "n/a", nil},
		{46166, -1, 0.38},
		{nil, 0.5},
		{46167, 0.45},
	})

	rates, err := parse(data, adapter.Date(2026, 5, 22), adapter.Date(2026, 5, 24))
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{
		{Date: adapter.Date(2026, 5, 22), Base: "FJD", Quote: "USD", Rate: 0.44},
		{Date: adapter.Date(2026, 5, 24), Base: "FJD", Quote: "EUR", Rate: 0.38},
	}
	if !slices.Equal(rates, want) {
		t.Errorf("rates = %+v, want %+v", rates, want)
	}
}

func TestParseReadsFirstSheetOnly(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	if _, err := f.NewSheet("Other"); err != nil {
		t.Fatal(err)
	}
	for sheet, rate := range map[string]float64{"Sheet1": 0.45, "Other": 9.99} {
		if err := f.SetSheetRow(sheet, "A1", &[]any{"Period", "US$"}); err != nil {
			t.Fatal(err)
		}
		if err := f.SetSheetRow(sheet, "A2", &[]any{46164, rate}); err != nil {
			t.Fatal(err)
		}
	}

	rates, err := parse(save(t, f), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{{Date: adapter.Date(2026, 5, 22), Base: "FJD", Quote: "USD", Rate: 0.45}}
	if !slices.Equal(rates, want) {
		t.Errorf("rates = %+v, want %+v", rates, want)
	}
}

func TestParseErrorsOnHTMLPage(t *testing.T) {
	if _, err := parse([]byte("<html><body>Service unavailable</body></html>"), time.Time{}, time.Time{}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestParseErrorsWithoutHeader(t *testing.T) {
	for name, rows := range map[string][][]any{
		"empty":     nil,
		"no labels": {{"Period", "Rate"}, {46164, 0.45}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parse(xlsx(t, rows), time.Time{}, time.Time{}); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
