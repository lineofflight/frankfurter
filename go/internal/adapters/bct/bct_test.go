package bct

import (
	"context"
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

// The Ruby spec allows playback repeats, but VCR still prefers unused interactions, so each day's POST gets its own
// response. go-vcr's replayable mode always answers with the first match, so we replay play-once here, which gives
// the same pairing for these three requests.
func newAdapter(t *testing.T) *Adapter {
	t.Helper()
	return New(vcrtest.Client(t, "bct", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
}

var may20 = adapter.Date(2026, 5, 20)

func mustParse(t *testing.T, html string, date time.Time) []adapter.Rate {
	t.Helper()
	rates, err := parse(html, date)
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

func TestFetchesRatesWithDateRange(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 5, 18), may20)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("empty dataset")
	}
	var dates []time.Time
	for _, r := range rates {
		if !slices.ContainsFunc(dates, r.Date.Equal) {
			dates = append(dates, r.Date)
		}
	}
	slices.SortFunc(dates, time.Time.Compare)
	want := []time.Time{adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 19), may20}
	if !slices.EqualFunc(dates, want, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", dates, want)
	}
}

func TestParseStoresForeignCurrencyAsBase(t *testing.T) {
	html := `<h3 class='bct-mod-hdr' id='1'>Cours Moyens des Devises Cotées</h3>
<h5>Journée du 20/05/2026</h5>
<table>
  <tr>
    <td>DOLLAR DES USA</td>
    <td>USD</td>
    <td><div align='right'>1</div></td>
    <td><div align='right'>  2,9048</div></td>
  </tr>
</table>
`
	rates := mustParse(t, html, may20)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "USD" || r.Quote != "TND" {
		t.Errorf("pair = %s/%s, want USD/TND", r.Base, r.Quote)
	}
	if math.Abs(r.Rate-2.9048) > 0.0001 {
		t.Errorf("rate = %v", r.Rate)
	}
}

func TestParseNormalizesPer1000Rates(t *testing.T) {
	html := `<h3>Cours Moyens des Devises Cotées</h3>
<h5>Journée du 20/05/2026</h5>
<table>
  <tr>
    <td>YEN JAPONAIS</td>
    <td>JPY</td>
    <td><div align='right'>1000</div></td>
    <td><div align='right'> 18,4230</div></td>
  </tr>
</table>
`
	rates := mustParse(t, html, may20)
	if len(rates) == 0 || rates[0].Base != "JPY" {
		t.Fatalf("rates = %v", rates)
	}
	if math.Abs(rates[0].Rate-0.01842) > 0.00001 {
		t.Errorf("rate = %v", rates[0].Rate)
	}
}

func TestParseNormalizesPer100AndPer10Rates(t *testing.T) {
	html := `<h3>Cours Moyens des Devises Cotées</h3>
<h5>Journée du 20/05/2026</h5>
<table>
  <tr>
    <td>COURONNE DANOISE</td>
    <td>DKK</td>
    <td><div align='right'>100</div></td>
    <td><div align='right'> 45,4665</div></td>
  </tr>
  <tr>
    <td>DIRHAM DES EAU</td>
    <td>AED</td>
    <td><div align='right'>10</div></td>
    <td><div align='right'>  7,9781</div></td>
  </tr>
</table>
`
	rates := mustParse(t, html, may20)
	dkk, aed := find(rates, "DKK"), find(rates, "AED")
	if dkk == nil || aed == nil {
		t.Fatalf("rates = %v", rates)
	}
	if math.Abs(dkk.Rate-0.454665) > 0.000001 {
		t.Errorf("DKK = %v", dkk.Rate)
	}
	if math.Abs(aed.Rate-0.79781) > 0.00001 {
		t.Errorf("AED = %v", aed.Rate)
	}
}

func TestParseIgnoresManualExchangeTable(t *testing.T) {
	html := `<h3>Cours Moyens des Devises Cotées</h3>
<h5>Journée du 20/05/2026</h5>
<table>
  <tr>
    <td>DOLLAR DES USA</td>
    <td>USD</td>
    <td><div align='right'>1</div></td>
    <td><div align='right'>  2,9048</div></td>
  </tr>
</table>
<!-- 2eme module-->
<table>
  <tr>
    <td>DOLLAR DES USA</td>
    <td>USD</td>
    <td><div align='right'>1</div></td>
    <td><div align='right'>  2,924</div></td>
  </tr>
</table>
`
	var usd []adapter.Rate
	for _, r := range mustParse(t, html, may20) {
		if r.Base == "USD" {
			usd = append(usd, r)
		}
	}
	if len(usd) != 1 {
		t.Fatalf("got %d USD rates, want 1", len(usd))
	}
	if math.Abs(usd[0].Rate-2.9048) > 0.0001 {
		t.Errorf("rate = %v", usd[0].Rate)
	}
}

func TestParseRejectsMismatchedEchoedDate(t *testing.T) {
	html := `<h3>Cours Moyens des Devises Cotées</h3>
<h5>Journée du 18/05/2026</h5>
<table>
  <tr>
    <td>DOLLAR DES USA</td>
    <td>USD</td>
    <td><div align='right'>1</div></td>
    <td><div align='right'>  2,9048</div></td>
  </tr>
</table>
`
	if rates := mustParse(t, html, adapter.Date(2026, 5, 23)); len(rates) != 0 {
		t.Errorf("rates = %v, want none", rates)
	}
}

func TestParseReturnsEmptyOnExhaustedResultset(t *testing.T) {
	html := "<h3>Cours Moyens des Devises Cotées</h3>Un problème rencontré!!!\nExhausted Resultset"
	if rates := mustParse(t, html, may20); len(rates) != 0 {
		t.Errorf("rates = %v, want none", rates)
	}
}

func TestParseErrorsOnUndatedResponse(t *testing.T) {
	_, err := parse("<html>Maintenance</html>", may20)
	if err == nil || !strings.Contains(err.Error(), "no dated rates page") {
		t.Errorf("err = %v", err)
	}
}

type captureTransport struct{ userAgent string }

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.userAgent = req.Header.Get("User-Agent")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("Exhausted Resultset")),
		Request:    req,
	}, nil
}

func TestSendsBaseClientUserAgent(t *testing.T) {
	tr := &captureTransport{}
	if _, err := New(&http.Client{Transport: tr}).Fetch(context.Background(), may20, may20); err != nil {
		t.Fatal(err)
	}
	if want := "Mozilla/5.0 (compatible; Frankfurter; +https://frankfurter.dev)"; tr.userAgent != want {
		t.Errorf("User-Agent = %q, want %q", tr.userAgent, want)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 5, 18), may20)
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

func TestDecodeTranscodesLatin1WhenNotUTF8(t *testing.T) {
	if got := decode([]byte("Journ\xe9e du 20/05/2026")); got != "Journée du 20/05/2026" {
		t.Errorf("latin-1 decode = %q", got)
	}
	if got := decode([]byte("Journée")); got != "Journée" {
		t.Errorf("utf-8 decode = %q", got)
	}
}

func TestParseSkipsZeroAndBlankRates(t *testing.T) {
	html := `<h5>Journée du 20/05/2026</h5>
<table>
  <tr><td>A</td><td>USD</td><td>1</td><td>0,000</td></tr>
  <tr><td>B</td><td>EUR</td><td>0</td><td>3,1</td></tr>
  <tr><td>C</td><td>GBP</td><td>1</td><td>n/a</td></tr>
  <tr><td>D</td><td>CHF</td><td>1</td><td>3,5</td></tr>
</table>`
	rates := mustParse(t, html, may20)
	if len(rates) != 1 || rates[0].Base != "CHF" {
		t.Errorf("rates = %v, want only CHF", rates)
	}
}
