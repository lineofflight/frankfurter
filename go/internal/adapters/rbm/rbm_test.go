package rbm

import (
	"context"
	"math"
	"reflect"
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
	client := vcrtest.Client(t, "rbm", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host))
	rates, err := New(client).Fetch(context.Background(), after, upto)
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

func mustParse(t *testing.T, html string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(html))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchAcrossRequestedDateRange(t *testing.T) {
	rates := fetch(t, adapter.Date(2024, 1, 2), adapter.Date(2024, 1, 5))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, d := range []time.Time{adapter.Date(2024, 1, 2), adapter.Date(2024, 1, 5)} {
		if !slices.ContainsFunc(rates, func(r adapter.Rate) bool { return r.Date.Equal(d) }) {
			t.Errorf("missing date %s", d.Format(time.DateOnly))
		}
	}
}

func TestFetchStoresForeignAsBaseAndMWKAsQuote(t *testing.T) {
	rates := fetch(t, adapter.Date(2024, 1, 2), adapter.Date(2024, 1, 5))
	usd := find(rates, "USD", adapter.Date(2024, 1, 2))
	if usd == nil {
		t.Fatal("no USD on 2024-01-02")
	}
	if usd.Quote != "MWK" || usd.Rate <= 1000 {
		t.Errorf("USD = %+v", usd)
	}
}

func TestFetchCoversBroadSetOfQuoteCurrencies(t *testing.T) {
	rates := fetch(t, adapter.Date(2024, 1, 2), adapter.Date(2024, 1, 5))
	for _, code := range []string{"USD", "EUR", "GBP", "ZAR", "ZMW", "MZN", "XDR"} {
		if find(rates, code, time.Time{}) == nil {
			t.Errorf("missing %s", code)
		}
	}
}

func TestFetchKeepsCMDSeparateFromUSD(t *testing.T) {
	rates := fetch(t, adapter.Date(2024, 1, 2), adapter.Date(2024, 1, 2))
	cmd := find(rates, "CMD", time.Time{})
	usd := find(rates, "USD", time.Time{})
	if cmd == nil || usd == nil {
		t.Fatalf("CMD = %v, USD = %v", cmd, usd)
	}
	if cmd.Rate != 1683.3663 || usd.Rate != 1683.3663 {
		t.Errorf("CMD = %v, USD = %v, want 1683.3663", cmd.Rate, usd.Rate)
	}
}

func TestParseCodeMiddleRateAndDate(t *testing.T) {
	rates := mustParse(t, `<table id="exchange-rates" class="table table-striped table-bordered">
  <tr>
    <td><strong>USD</strong></td>
    <td>1,665.0099</td>
    <td>1,683.3700</td>
    <td>1,701.7300</td>
    <td style="color: #2670a2;"><span>Jan 02&nbsp;&nbsp;</span><span>2024</span></td>
  </tr>
</table>`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if !r.Date.Equal(adapter.Date(2024, 1, 2)) || r.Base != "USD" || r.Quote != "MWK" || math.Abs(r.Rate-1683.37) > 0.001 {
		t.Errorf("rate = %+v", r)
	}
}

func TestParseNormalizesSDRToXDR(t *testing.T) {
	rates := mustParse(t, `<table id="exchange-rates">
  <tr>
    <td><strong>SDR</strong></td>
    <td>2,200.0000</td>
    <td>2,250.0000</td>
    <td>2,300.0000</td>
    <td><span>Jan 02&nbsp;&nbsp;</span><span>2024</span></td>
  </tr>
</table>`)
	want := []adapter.Rate{{Date: adapter.Date(2024, 1, 2), Base: "XDR", Quote: "MWK", Rate: 2250.0}}
	if !reflect.DeepEqual(rates, want) {
		t.Errorf("rates = %+v, want %+v", rates, want)
	}
}

func TestParseSkipsRowsWithoutCurrencyCode(t *testing.T) {
	rates := mustParse(t, `<table id="exchange-rates">
  <tr>
    <td><strong>Currency</strong></td>
    <td>Buying</td>
    <td>Middle</td>
    <td>Selling</td>
    <td>Date</td>
  </tr>
  <tr>
    <td><strong>USD</strong></td>
    <td>1,665.0099</td>
    <td>1,683.3700</td>
    <td>1,701.7300</td>
    <td><span>Jan 02&nbsp;&nbsp;</span><span>2024</span></td>
  </tr>
</table>`)
	if len(rates) != 1 || rates[0].Base != "USD" {
		t.Errorf("rates = %+v, want one USD", rates)
	}
}

func TestParseSkipsMissingOrZeroMiddle(t *testing.T) {
	rates := mustParse(t, `<table id="exchange-rates">
  <tr>
    <td><strong>USD</strong></td>
    <td>1,665.0099</td>
    <td>0</td>
    <td>1,701.7300</td>
    <td><span>Jan 02&nbsp;&nbsp;</span><span>2024</span></td>
  </tr>
  <tr>
    <td><strong>EUR</strong></td>
    <td>1,800</td>
    <td></td>
    <td>1,900</td>
    <td><span>Jan 02&nbsp;&nbsp;</span><span>2024</span></td>
  </tr>
</table>`)
	if len(rates) != 0 {
		t.Errorf("rates = %+v, want none", rates)
	}
}

func TestParseErrorsWithoutTable(t *testing.T) {
	_, err := parse([]byte("<html><body>no data</body></html>"))
	if err == nil || !strings.Contains(err.Error(), "rates table not found") {
		t.Errorf("err = %v, want rates table not found", err)
	}
}

func TestFetchEmptyWhenStartAfterUpto(t *testing.T) {
	rates := fetch(t, adapter.Date(2024, 1, 10), adapter.Date(2024, 1, 5))
	if len(rates) != 0 {
		t.Errorf("rates = %+v, want none", rates)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2024, 1, 2), adapter.Date(2024, 1, 5))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
