package rbv

import (
	"context"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "rbv", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, html string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(html))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func bases(rates []adapter.Rate) []string {
	var codes []string
	for _, r := range rates {
		if !slices.Contains(codes, r.Base) {
			codes = append(codes, r.Base)
		}
	}
	slices.Sort(codes)
	return codes
}

func TestFetchNewestFirst(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 22))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	var seen []time.Time
	for _, r := range rates {
		if !slices.ContainsFunc(seen, r.Date.Equal) {
			seen = append(seen, r.Date)
		}
	}
	if !slices.IsSortedFunc(seen, func(a, b time.Time) int { return b.Compare(a) }) {
		t.Errorf("dates not in descending order: %v", seen)
	}
}

func TestFetchAllBasketCurrencies(t *testing.T) {
	got := bases(fetch(t, adapter.Date(2026, 5, 21), adapter.Date(2026, 5, 22)))
	if want := []string{"AUD", "EUR", "GBP", "JPY", "NZD", "USD"}; !slices.Equal(got, want) {
		t.Errorf("bases = %v, want %v", got, want)
	}
}

func TestParseQuotesVUV(t *testing.T) {
	rates := mustParse(t, `<table>
  <tr class="fabrik_row">
    <td class="exchange_rates___date fabrik_element">22 May 2026</td>
    <td class="exchange_rates___usd fabrik_element decimal">116.28</td>
    <td class="exchange_rates___jpy fabrik_element decimal">0.7316</td>
    <td class="exchange_rates___nzd fabrik_element decimal">68.37</td>
    <td class="exchange_rates___GBP fabrik_element decimal">156.19</td>
    <td class="exchange_rates___aud fabrik_element decimal">83.16</td>
    <td class="exchange_rates___eur fabrik_element decimal">135.14</td>
  </tr>
</table>`)
	if len(rates) != 6 {
		t.Fatalf("got %d rates, want 6", len(rates))
	}
	usd := rates[slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == "USD" })]
	if usd.Quote != "VUV" || math.Abs(usd.Rate-116.28) > 0.001 || !usd.Date.Equal(adapter.Date(2026, 5, 22)) {
		t.Errorf("USD = %+v", usd)
	}
}

func TestParseShortDateFormat(t *testing.T) {
	rates := mustParse(t, `<table>
  <tr class="fabrik_row">
    <td class="exchange_rates___date fabrik_element">26-Aug-25</td>
    <td class="exchange_rates___usd fabrik_element decimal">119.06</td>
    <td class="exchange_rates___jpy fabrik_element decimal">0.8058</td>
    <td class="exchange_rates___nzd fabrik_element decimal">69.63</td>
    <td class="exchange_rates___GBP fabrik_element decimal">160.23</td>
    <td class="exchange_rates___aud fabrik_element decimal">77.17</td>
    <td class="exchange_rates___eur fabrik_element decimal">138.33</td>
  </tr>
</table>`)
	if len(rates) == 0 || !rates[0].Date.Equal(adapter.Date(2025, 8, 26)) {
		t.Errorf("rates = %+v, want 2025-08-26", rates)
	}
}

func TestParseSkipsNonPositiveRates(t *testing.T) {
	rates := mustParse(t, `<table>
  <tr class="fabrik_row">
    <td class="exchange_rates___date fabrik_element">22 May 2026</td>
    <td class="exchange_rates___usd fabrik_element decimal">0</td>
    <td class="exchange_rates___jpy fabrik_element decimal">0.7316</td>
    <td class="exchange_rates___nzd fabrik_element decimal"></td>
    <td class="exchange_rates___GBP fabrik_element decimal">156.19</td>
    <td class="exchange_rates___aud fabrik_element decimal">83.16</td>
    <td class="exchange_rates___eur fabrik_element decimal">135.14</td>
  </tr>
</table>`)
	if got, want := bases(rates), []string{"AUD", "EUR", "GBP", "JPY"}; !slices.Equal(got, want) {
		t.Errorf("bases = %v, want %v", got, want)
	}
}

func TestParseSkipsUnparseableDates(t *testing.T) {
	rates := mustParse(t, `<table>
  <tr class="fabrik_row">
    <td class="exchange_rates___date fabrik_element">not a date</td>
    <td class="exchange_rates___usd fabrik_element decimal">116.28</td>
  </tr>
</table>`)
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 22))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
