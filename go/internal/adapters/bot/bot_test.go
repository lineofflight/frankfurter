package bot

import (
	"context"
	"math"
	"slices"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	vcrtest.SetSecrets(t)
	a := New(vcrtest.Client(t, "bot", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 3))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetch(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchReturnsMultipleCurrencies(t *testing.T) {
	var currencies []string
	for _, r := range fetch(t) {
		if !slices.Contains(currencies, r.Base) {
			currencies = append(currencies, r.Base)
		}
	}
	if len(currencies) <= 10 {
		t.Errorf("got %d currencies, want more than 10", len(currencies))
	}
	for _, c := range []string{"USD", "EUR"} {
		if !slices.Contains(currencies, c) {
			t.Errorf("missing %s", c)
		}
	}
}

func TestFetchQuotesInTHB(t *testing.T) {
	for _, r := range fetch(t) {
		if r.Quote != "THB" {
			t.Errorf("quote = %s, want THB", r.Quote)
		}
	}
}

func TestParseNormalisesPerUnitRates(t *testing.T) {
	records, err := parse([]byte(`{"result":{"data":{"data_detail":[
		{"period":"2026-04-03","currency_id":"JPY","currency_name_eng":"JAPAN : YEN (100 YEN) (JPY)","mid_rate":"20.4832000"},
		{"period":"2026-04-03","currency_id":"IDR","currency_name_eng":"INDONESIA : RUPIAH (1,000 RUPIAH) (IDR)","mid_rate":"1.9296000"},
		{"period":"2026-04-03","currency_id":"USD","currency_name_eng":"USA : DOLLAR (USD)","mid_rate":"32.6448000"}
	]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	rates := map[string]float64{}
	for _, r := range records {
		rates[r.Base] = r.Rate
	}
	for _, c := range []struct {
		base        string
		want, delta float64
	}{
		{"JPY", 0.204832, 0.0001},
		{"IDR", 0.0019296, 0.00001},
		{"USD", 32.6448, 0.01},
	} {
		got, ok := rates[c.base]
		if !ok || math.Abs(got-c.want) > c.delta {
			t.Errorf("%s = %v, want %v", c.base, got, c.want)
		}
	}
}

func TestGolden(t *testing.T) {
	vcrtest.SetSecrets(t)
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 3))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
