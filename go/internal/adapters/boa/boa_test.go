package boa

import (
	"context"
	"encoding/xml"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

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

func TestParseSheetSkipsHeadersBlanksAndZeros(t *testing.T) {
	var ws worksheet
	err := xml.Unmarshal([]byte(`<worksheet><sheetData>
		<row><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
		<row><c r="A2"><v>46140</v></c><c r="B2"><v>82.8</v></c></row>
		<row><c r="A3"><v>46141.0</v></c><c r="B3"><v>0</v></c></row>
		<row><c r="A4"><v>46142</v></c><c r="B4"><v></v></c></row>
		<row><c r="A5"><v>46143</v></c></row>
		<row><c r="A6"><v>46144.9</v></c><c r="B6"><v>83</v></c></row>
		<row><c r="A7"><v>46145</v></c><c r="B7"><v>n/a</v></c></row>
	</sheetData></worksheet>`), &ws)
	if err != nil {
		t.Fatal(err)
	}
	got := parseSheet(ws, "JPY", time.Time{}, time.Time{})
	want := []adapter.Rate{
		{Date: adapter.Date(2026, 4, 28), Base: "JPY", Quote: "DZD", Rate: 0.828},
		{Date: adapter.Date(2026, 5, 2), Base: "JPY", Quote: "DZD", Rate: 0.83},
	}
	if !slices.Equal(got, want) {
		t.Errorf("parseSheet = %v, want %v", got, want)
	}
}

func TestParseSheetBoundsAreInclusive(t *testing.T) {
	var ws worksheet
	err := xml.Unmarshal([]byte(`<worksheet><sheetData>
		<row><c r="A1"><v>46139</v></c><c r="B1"><v>1</v></c></row>
		<row><c r="A2"><v>46140</v></c><c r="B2"><v>2</v></c></row>
		<row><c r="A3"><v>46141</v></c><c r="B3"><v>3</v></c></row>
		<row><c r="A4"><v>46142</v></c><c r="B4"><v>4</v></c></row>
	</sheetData></worksheet>`), &ws)
	if err != nil {
		t.Fatal(err)
	}
	got := parseSheet(ws, "USD", adapter.Date(2026, 4, 28), adapter.Date(2026, 4, 29))
	if len(got) != 2 || got[0].Rate != 2 || got[1].Rate != 3 {
		t.Errorf("parseSheet = %v, want rates 2 and 3", got)
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
