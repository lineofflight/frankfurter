package cbk

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func mustParse(t *testing.T, data string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetch(t *testing.T) {
	a := New(vcrtest.Client(t, "cbk", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host), vcrtest.AllowPlaybackRepeats))
	rates, err := a.Fetch(context.Background(), adapter.Date(2025, 1, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestParseStoresForeignBaseAndKESQuote(t *testing.T) {
	r := mustParse(t, `{"data": [["20/03/2026", "US DOLLAR", "129.5012"]]}`)[0]
	if r.Base != "USD" || r.Quote != "KES" || r.Rate != 129.5012 {
		t.Errorf("got %+v", r)
	}
}

func TestParseLegacyTableWithMeanRate(t *testing.T) {
	r := mustParse(t, `{"data": [["15/06/2020", "EURO", "85.1234", "84.0000", "86.0000"]]}`)[0]
	if r.Base != "EUR" || r.Quote != "KES" || r.Rate != 85.1234 {
		t.Errorf("got %+v", r)
	}
}

func TestParseResolvesEastAfricanCrossRateNames(t *testing.T) {
	rates := mustParse(t, `{"data": [
		["20/03/2026", "KES / USHS", "27.5000"],
		["20/03/2026", "KES / TSHS", "20.1000"]
	]}`)
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	if rates[0].Quote != "UGX" || rates[1].Quote != "TZS" {
		t.Errorf("got %+v", rates)
	}
}

func TestParseRecordsEastAfricanCrossRatesAsPublished(t *testing.T) {
	rates := mustParse(t, `{"data": [
		["20/03/2026", "KES / TSHS", "21.6800"],
		["20/03/2026", "KES / USHS", "27.5000"],
		["20/03/2026", "KEN SHILLING / RWF", "8.5000"]
	]}`)
	if len(rates) != 3 {
		t.Fatalf("got %d rates, want 3", len(rates))
	}
	want := []struct {
		quote string
		rate  float64
	}{{"TZS", 21.68}, {"UGX", 27.5}, {"RWF", 8.5}}
	for i, w := range want {
		// 1 KES = 21.68 TZS is stored as published, not as its reciprocal.
		if r := rates[i]; r.Base != "KES" || r.Quote != w.quote || r.Rate != w.rate {
			t.Errorf("rates[%d] = %+v, want KES/%s %v", i, r, w.quote, w.rate)
		}
	}
}

func TestParseDividesByUnitMarker(t *testing.T) {
	rates := mustParse(t, `{"data": [["20/03/2026", "JPY (100)", "109.3764"]]}`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "JPY" || r.Quote != "KES" || math.Abs(r.Rate-109.3764/100) > 0.0001 {
		t.Errorf("got %+v", r)
	}
}

func TestParseSkipsUnmappedCurrencies(t *testing.T) {
	rates := mustParse(t, `{"data": [
		["20/03/2026", "UNKNOWN CURRENCY", "1.0000"],
		["20/03/2026", "US DOLLAR", "129.5012"]
	]}`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if rates[0].Base != "USD" {
		t.Errorf("got %+v", rates[0])
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2025, 1, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
