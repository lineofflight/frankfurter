package cbsl

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetchRange(t *testing.T) []adapter.Rate {
	t.Helper()
	client := vcrtest.Client(t, "cbsl", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host), vcrtest.AllowPlaybackRepeats)
	rates, err := New(client).Fetch(context.Background(), adapter.Date(2025, 5, 15), adapter.Date(2025, 5, 19))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchesRatesWithDateRange(t *testing.T) {
	rates := fetchRange(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if rates[0].Quote != "LKR" {
		t.Errorf("quote = %s, want LKR", rates[0].Quote)
	}
	if rates[0].Rate <= 0 {
		t.Errorf("rate = %v, want > 0", rates[0].Rate)
	}
}

func TestEmitsMultipleCurrenciesAsBaseAgainstLKR(t *testing.T) {
	var bases []string
	for _, r := range fetchRange(t) {
		bases = append(bases, r.Base)
	}
	for _, want := range []string{"USD", "EUR", "GBP"} {
		if !slices.Contains(bases, want) {
			t.Errorf("bases missing %s", want)
		}
	}
}

func TestFiltersDatesToRequestedRange(t *testing.T) {
	lo, hi := adapter.Date(2025, 5, 15), adapter.Date(2025, 5, 19)
	for _, r := range fetchRange(t) {
		if r.Date.Before(lo) || r.Date.After(hi) {
			t.Errorf("date %s outside range", r.Date.Format("2006-01-02"))
		}
	}
}

func TestEmitsXAUPerTroyOunce(t *testing.T) {
	var xau []adapter.Rate
	for _, r := range fetchRange(t) {
		if r.Base == "XAU" {
			xau = append(xau, r)
		}
	}
	if len(xau) == 0 {
		t.Fatal("no XAU rows")
	}
	// Per troy ounce is hundreds of thousands of LKR; per gram would be tens of
	// thousands.
	if xau[0].Rate <= 100_000 {
		t.Errorf("XAU rate = %v, want > 100000", xau[0].Rate)
	}
}

func TestParseHTMLTablesWithCurrencyCodeFromHeader(t *testing.T) {
	html := `<h2>US Dollar</h2>
<table>
  <thead><tr>
    <th>Date</th>
    <th>1  USD  -&gt; LKR</th>
    <th>1 LKR -&gt;  USD </th>
  </tr></thead>
  <tbody>
    <tr><td> 2025-05-19 </td><td> 298.8165 </td><td> 0.0033 </td></tr>
    <tr><td> 2025-05-16 </td><td> 298.5101 </td><td> 0.0033 </td></tr>
  </tbody>
</table>
<h2>Euro</h2>
<table>
  <thead><tr>
    <th>Date</th>
    <th>1  EUR  -&gt; LKR</th>
    <th>1 LKR -&gt;  EUR </th>
  </tr></thead>
  <tbody>
    <tr><td> 2025-05-19 </td><td> 334.3159 </td><td> 0.003 </td></tr>
  </tbody>
</table>
`
	rates, err := parse([]byte(html))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 3 {
		t.Fatalf("got %d rates, want 3", len(rates))
	}
	var usd []adapter.Rate
	var eur *adapter.Rate
	for i, r := range rates {
		switch r.Base {
		case "USD":
			usd = append(usd, r)
		case "EUR":
			if eur == nil {
				eur = &rates[i]
			}
		}
	}
	if len(usd) != 2 {
		t.Fatalf("got %d USD rates, want 2", len(usd))
	}
	if usd[0].Quote != "LKR" {
		t.Errorf("quote = %s, want LKR", usd[0].Quote)
	}
	if usd[0].Rate != 298.8165 {
		t.Errorf("rate = %v, want 298.8165", usd[0].Rate)
	}
	if !usd[0].Date.Equal(adapter.Date(2025, 5, 19)) {
		t.Errorf("date = %v, want 2025-05-19", usd[0].Date)
	}
	if eur == nil || eur.Rate != 334.3159 {
		t.Errorf("EUR = %+v, want rate 334.3159", eur)
	}
}

func TestParseSkipsEmptyTbodySections(t *testing.T) {
	html := `<h2>US Dollar</h2>
<table>
  <thead><tr><th>Date</th><th>1  USD  -&gt; LKR</th><th>1 LKR -&gt;  USD </th></tr></thead>
  <tbody>0 results</tbody>
</table>
`
	rates, err := parse([]byte(html))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseSkipsZeroRates(t *testing.T) {
	html := `<h2>US Dollar</h2>
<table>
  <thead><tr><th>Date</th><th>1  USD  -&gt; LKR</th><th>1 LKR -&gt;  USD </th></tr></thead>
  <tbody>
    <tr><td> 2025-05-19 </td><td> 0.0000 </td><td> 0 </td></tr>
  </tbody>
</table>
`
	rates, err := parse([]byte(html))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2025, 5, 15), adapter.Date(2025, 5, 19))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

func TestParseSkipsCellsPaddedWithNoBreakSpace(t *testing.T) {
	html := `<table>
  <thead><tr><th>Date</th><th>1 USD -&gt; LKR</th></tr></thead>
  <tbody>
    <tr><td>&nbsp;2025-05-19</td><td> 298.8165 </td></tr>
    <tr><td> 2025-05-16 </td><td>298.5101&nbsp;</td></tr>
    <tr><td> 2025-05-15 </td><td> 297.1 </td></tr>
  </tbody>
</table>
`
	rates, err := parse([]byte(html))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || !rates[0].Date.Equal(adapter.Date(2025, 5, 15)) || rates[0].Rate != 297.1 {
		t.Errorf("got %+v, want only the 2025-05-15 row", rates)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchPostsFormWithCurrenciesAndDates(t *testing.T) {
	var form url.Values
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `<input name="chk_cur[]" value="USD~United States Dollar"><input name="chk_cur[]" value="EUR~Euro">`
		if r.Method == http.MethodPost {
			if r.URL.String() != "https://www.cbsl.gov.lk/cbsl_custom/exrates/exrates_results.php" {
				t.Errorf("POST %s", r.URL)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			form = r.PostForm
			body = ""
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	a := New(client)
	a.Now = func() time.Time { return time.Date(2025, 5, 19, 12, 0, 0, 0, time.UTC) }
	if _, err := a.Fetch(context.Background(), time.Time{}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	want := url.Values{
		"lookupPage":    {"lookup_daily_exchange_rates.php"},
		"rangeType":     {"dates"},
		"txtStart":      {""},
		"txtEnd":        {"2025-05-19"},
		"chk_cur[]":     {"USD~United States Dollar", "EUR~Euro"},
		"submit_button": {"Submit"},
	}
	if !reflect.DeepEqual(form, want) {
		t.Errorf("form = %v, want %v", form, want)
	}
}

func TestParseErrorsOnImpossibleDate(t *testing.T) {
	html := `<table>
  <thead><tr><th>Date</th><th>1 USD -&gt; LKR</th></tr></thead>
  <tbody><tr><td>2025-13-01</td><td>298.8165</td></tr></tbody>
</table>
`
	if _, err := parse([]byte(html)); err == nil {
		t.Error("want error for 2025-13-01")
	}
}
