package bom

import (
	"context"
	"slices"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "bom", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path), vcrtest.AllowPlaybackRepeats))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 22))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, data string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func find(rates []adapter.Rate, base string) *adapter.Rate {
	for i := range rates {
		if rates[i].Base == base {
			return &rates[i]
		}
	}
	return nil
}

func TestFetch(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchRespectsAfterAndUpto(t *testing.T) {
	for _, r := range fetch(t) {
		if r.Date.Before(adapter.Date(2026, 5, 19)) || r.Date.After(adapter.Date(2026, 5, 22)) {
			t.Errorf("rate dated %s outside window", r.Date.Format("2006-01-02"))
		}
	}
}

func TestParseStoresForeignAsBaseAndMNTAsQuote(t *testing.T) {
	rates := mustParse(t, `{"data": [{"RATE_DATE": "2026-05-22", "USD": "3,576.42", "EUR": "4,151.69"}]}`)
	usd := find(rates, "USD")
	if usd == nil {
		t.Fatal("no USD rate")
	}
	want := adapter.Rate{Date: adapter.Date(2026, 5, 22), Base: "USD", Quote: "MNT", Rate: 3576.42}
	if *usd != want {
		t.Errorf("USD = %+v, want %+v", *usd, want)
	}
}

func TestParseCommaThousandSeparators(t *testing.T) {
	rates := mustParse(t, `{"data": [{"RATE_DATE": "2026-05-22", "KWD": "11,657.17"}]}`)
	if rates[0].Rate != 11657.17 {
		t.Errorf("rate = %v, want 11657.17", rates[0].Rate)
	}
}

func TestParseFractionalRatesForHighDenominationCurrencies(t *testing.T) {
	rates := mustParse(t, `{"data": [{"RATE_DATE": "2026-05-22", "IDR": "0.20", "VND": "0.14", "KRW": "2.36"}]}`)
	for base, want := range map[string]float64{"IDR": 0.20, "VND": 0.14, "KRW": 2.36} {
		r := find(rates, base)
		if r == nil || r.Rate != want {
			t.Errorf("%s = %+v, want %v", base, r, want)
		}
	}
}

func TestParseRewritesSDRToXDR(t *testing.T) {
	rates := mustParse(t, `{"data": [{"RATE_DATE": "2026-05-22", "USD": "3,576.42", "SDR": "4,887.93"}]}`)
	xdr := find(rates, "XDR")
	if xdr == nil {
		t.Fatal("no XDR rate")
	}
	if xdr.Rate != 4887.93 || xdr.Quote != "MNT" {
		t.Errorf("XDR = %+v", *xdr)
	}
	if find(rates, "SDR") != nil {
		t.Error("SDR still present")
	}
}

func TestParseStoresMetalsPerTroyOunce(t *testing.T) {
	rates := mustParse(t, `{"data": [{"RATE_DATE": "2026-05-22", "XAU": "16,172,267.24", "XAG": "271,833.67"}]}`)
	xau, xag := find(rates, "XAU"), find(rates, "XAG")
	if xau == nil || xag == nil {
		t.Fatalf("missing metals: %+v", rates)
	}
	if xau.Quote != "MNT" || xau.Rate != 16172267.24 {
		t.Errorf("XAU = %+v", *xau)
	}
	if xag.Rate != 271833.67 {
		t.Errorf("XAG = %+v", *xag)
	}
}

func TestParseSkipsZeroAndEmptyValues(t *testing.T) {
	rates := mustParse(t, `{"data": [{"RATE_DATE": "2026-05-22", "USD": "3,576.42", "ZZZ": "", "AAA": "0.00"}]}`)
	var bases []string
	for _, r := range rates {
		bases = append(bases, r.Base)
	}
	if !slices.Equal(bases, []string{"USD"}) {
		t.Errorf("bases = %v, want [USD]", bases)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 22))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
