package bota

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "bota", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 19))
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
		codes = append(codes, r.Base)
	}
	return codes
}

func TestFetchWithDateRange(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
	first := rates[0].Date
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 1 {
		t.Errorf("got %d rates on %s, want more than 1", n, first)
	}
}

func TestFetchAntiforgeryTokenFlow(t *testing.T) {
	rates := fetch(t)
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool {
		return r.Date.Equal(adapter.Date(2026, 5, 19)) && r.Base == "USD"
	})
	if i < 0 {
		t.Fatal("no USD rate on 2026-05-19")
	}
	usd := rates[i]
	if usd.Quote != "TZS" {
		t.Errorf("quote = %q, want TZS", usd.Quote)
	}
	if usd.Rate != 2602.0545 {
		t.Errorf("rate = %v, want 2602.0545", usd.Rate)
	}
}

func TestParseBaseAndQuote(t *testing.T) {
	rates := mustParse(t, `<html><body>
<table><tbody>
<tr>
  <td>1</td><td>USD</td><td>2568.72</td><td>2594.41</td><td>2581.57</td><td>24-Mar-26</td>
</tr>
</tbody></table>
</body></html>`)
	want := []adapter.Rate{{Date: adapter.Date(2026, 3, 24), Base: "USD", Quote: "TZS", Rate: 2581.57}}
	if !reflect.DeepEqual(rates, want) {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseNormalizesSDRToXDR(t *testing.T) {
	rates := mustParse(t, `<table><tbody>
<tr><td>35</td><td>SDR</td><td>3589.4684</td><td>3625.3631</td><td>3607.4158</td><td>18-Sep-26</td></tr>
</tbody></table>`)
	want := []adapter.Rate{{Date: adapter.Date(2026, 9, 18), Base: "XDR", Quote: "TZS", Rate: 3607.4158}}
	if !reflect.DeepEqual(rates, want) {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseMapsMXMLabelToOldMetical(t *testing.T) {
	rates := mustParse(t, `<table><tbody>
<tr><td>757</td><td>MXM</td><td>0.0599</td><td>0.0605</td><td>0.0602</td><td>01-Jul-99</td></tr>
</tbody></table>`)
	want := []adapter.Rate{{Date: adapter.Date(1999, 7, 1), Base: "MZM", Quote: "TZS", Rate: 0.0602}}
	if !reflect.DeepEqual(rates, want) {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseExcludesGoldAndDefunctCurrencies(t *testing.T) {
	rates := mustParse(t, `<html><body>
<table><tbody>
<tr><td>1</td><td>USD</td><td>2568.72</td><td>2594.41</td><td>2581.57</td><td>24-Mar-26</td></tr>
<tr><td>2</td><td>GOLD</td><td>1000.00</td><td>2000.00</td><td>1500.00</td><td>24-Mar-26</td></tr>
<tr><td>3</td><td>ATS</td><td>100.00</td><td>200.00</td><td>150.00</td><td>24-Mar-26</td></tr>
<tr><td>4</td><td>NLG</td><td>100.00</td><td>200.00</td><td>150.00</td><td>24-Mar-26</td></tr>
<tr><td>5</td><td>MZM</td><td>100.00</td><td>200.00</td><td>150.00</td><td>24-Mar-26</td></tr>
<tr><td>6</td><td>ZWD</td><td>100.00</td><td>200.00</td><td>150.00</td><td>24-Mar-26</td></tr>
<tr><td>7</td><td>CUC</td><td>100.00</td><td>200.00</td><td>150.00</td><td>24-Mar-26</td></tr>
<tr><td>8</td><td>EUR</td><td>2950.95</td><td>2980.46</td><td>2965.70</td><td>24-Mar-26</td></tr>
</tbody></table>
</body></html>`)
	codes := bases(rates)
	for _, c := range []string{"USD", "EUR"} {
		if !slices.Contains(codes, c) {
			t.Errorf("missing %s in %v", c, codes)
		}
	}
	for _, c := range []string{"GOLD", "ATS", "NLG", "MZM", "ZWD", "CUC"} {
		if slices.Contains(codes, c) {
			t.Errorf("unexpected %s in %v", c, codes)
		}
	}
	if len(rates) != 2 {
		t.Errorf("got %d rates, want 2", len(rates))
	}
}

func TestParseCommasInNumbers(t *testing.T) {
	rates := mustParse(t, `<html><body>
<table><tbody>
<tr><td>1</td><td>GOLD</td><td>11,759,124.79</td><td>11,876,716.04</td><td>11,817,920.42</td><td>24-Mar-26</td></tr>
<tr><td>2</td><td>JPY</td><td>17.1234</td><td>17.2946</td><td>17.2090</td><td>24-Mar-26</td></tr>
</tbody></table>
</body></html>`)
	// GOLD is excluded
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if rates[0].Base != "JPY" || rates[0].Rate != 17.209 {
		t.Errorf("got %+v, want JPY at 17.209", rates[0])
	}
}

func TestFetchWithoutTokenRaises(t *testing.T) {
	a := New(vcrtest.Client(t, "bota_no_token", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	_, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 19))
	if err == nil || !strings.Contains(err.Error(), "antiforgery") {
		t.Fatalf("err = %v, want a missing antiforgery token error", err)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 19))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

// The cassette records the form body and Cookie header Ruby sent; matching on
// both checks the token flow itself.
func TestFetchSendsTokenFormAndCookie(t *testing.T) {
	cookie := func(r *http.Request, _ []byte, rec cassette.Request) bool {
		return r.Header.Get("Cookie") == rec.Headers.Get("Cookie")
	}
	a := New(vcrtest.Client(t, "bota", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI, vcrtest.Body, cookie)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 19))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestParseWithoutTableFails(t *testing.T) {
	if _, err := parse([]byte("<html><body><p>No rates</p></body></html>")); err == nil {
		t.Fatal("want an error for a page without a rates table")
	}
}

func TestParseSkipsShortRowsAndZeroRates(t *testing.T) {
	rates := mustParse(t, `<table><tbody>
<tr><td colspan="6">No data</td></tr>
<tr><td>1</td><td>KES</td><td>0.00</td><td>0.00</td><td>0.00</td><td>24-Mar-26</td></tr>
<tr><td>2</td><td>USD</td><td>2568.72</td><td>2594.41</td><td>2581.57</td><td>24-Mar-26</td></tr>
</tbody></table>`)
	if got := bases(rates); !reflect.DeepEqual(got, []string{"USD"}) {
		t.Errorf("got %v, want [USD]", got)
	}
}

func TestParseNonNumericMeanFails(t *testing.T) {
	_, err := parse([]byte(`<table><tbody>
<tr><td>1</td><td>USD</td><td>2568.72</td><td>2594.41</td><td>n/a</td><td>24-Mar-26</td></tr>
</tbody></table>`))
	if err == nil {
		t.Fatal("want an error for a non-numeric mean, as Ruby's Float() raises")
	}
}
