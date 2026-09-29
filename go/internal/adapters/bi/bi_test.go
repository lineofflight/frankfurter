package bi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

const aedTable = `<table id="foo_gvSearchResult2">
<tr><th>Value</th><th>Sell</th><th>Buy</th><th>Date</th></tr>
<tr><td>1.00</td><td>4,627.79</td><td>4,580.24</td><td>5 Mar 2026</td></tr>
</table>
`

// vcrClient replays the bi cassette with VCR's allow_playback_repeats rule: unused matches play first, in order, and
// once they run out the most recently used match repeats. vcrtest.AllowPlaybackRepeats (go-vcr) always replays the
// first match instead, which would answer every currency search with the AED table. The cassette's three searches
// are matched on method and host, so Ruby answers AED, AUD, then BND for every remaining currency.
func vcrClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: &repeatLast{
		next: vcrtest.Client(t, "bi", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)).Transport,
		last: map[string]*recorded{},
	}}
}

type recorded struct {
	resp *http.Response
	body []byte
}

type repeatLast struct {
	next http.RoundTripper
	last map[string]*recorded // by method; every request goes to the same host
}

func (r *repeatLast) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.next.RoundTrip(req)
	if err != nil {
		rec, ok := r.last[req.Method]
		if !ok {
			return nil, err
		}
		copied := *rec.resp
		copied.Header = rec.resp.Header.Clone()
		copied.Body = io.NopCloser(bytes.NewReader(rec.body))
		copied.Request = req
		return &copied, nil
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	r.last[req.Method] = &recorded{resp, body}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

func mustParse(t *testing.T, html, currency string) []adapter.Rate {
	t.Helper()
	rates, err := parse(html, currency)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchesRates(t *testing.T) {
	a := New(vcrClient(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestParseForeignBaseIDRQuote(t *testing.T) {
	r := mustParse(t, aedTable, "AED  ")[0]
	if r.Base != "AED" || r.Quote != "IDR" {
		t.Errorf("got %s/%s, want AED/IDR", r.Base, r.Quote)
	}
}

func TestParseComputesMidFromBuyAndSell(t *testing.T) {
	r := mustParse(t, aedTable, "AED  ")[0]
	if r.Rate != 4604.015 {
		t.Errorf("rate = %v, want 4604.015", r.Rate)
	}
	if !r.Date.Equal(adapter.Date(2026, 3, 5)) || *r.Bid != 4580.24 || *r.Ask != 4627.79 {
		t.Errorf("got %+v", r)
	}
}

func TestParseThousandsSeparators(t *testing.T) {
	html := `<table id="foo_gvSearchResult2">
<tr><th>Value</th><th>Sell</th><th>Buy</th><th>Date</th></tr>
<tr><td>1.00</td><td>19,743.74</td><td>19,543.91</td><td>3 Mar 2026</td></tr>
</table>
`
	r := mustParse(t, html, "EUR  ")[0]
	if r.Rate != 19643.825 {
		t.Errorf("rate = %v, want 19643.825", r.Rate)
	}
}

func TestParseEmptyWhenSearchFormWithoutTable(t *testing.T) {
	html := `<html><body><input name="ctl00$PlaceHolderMain$g$btnSearch1"></body></html>`
	if rates := mustParse(t, html, "USD  "); len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseErrorsWithoutTableOrForm(t *testing.T) {
	_, err := parse("<html><body>No data</body></html>", "USD  ")
	if !errors.Is(err, errNoTable) {
		t.Fatalf("err = %v, want %v", err, errNoTable)
	}
}

type pageTransport string

func (p pageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body := io.NopCloser(bytes.NewBufferString(string(p)))
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body, Request: req}, nil
}

func TestFetchErrorsWithoutFormPrefix(t *testing.T) {
	page := `<select name="x$ddlmatauang1"><option value="USD  ">USD</option></select>`
	a := New(&http.Client{Transport: pageTransport(page)})
	_, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 5))
	if !errors.Is(err, errNoPrefix) {
		t.Fatalf("err = %v, want %v", err, errNoPrefix)
	}
}

func TestFetchRequiresAfter(t *testing.T) {
	a := New(&http.Client{Transport: pageTransport("")})
	if _, err := a.Fetch(context.Background(), time.Time{}, adapter.Date(2026, 3, 5)); err == nil {
		t.Fatal("want error for zero after")
	}
}

func TestParseErrorsOnUnparseableDate(t *testing.T) {
	html := `<table id="foo_gvSearchResult2"><tr><td>1.00</td><td>2.00</td><td>1.00</td><td>not a date</td></tr></table>`
	if _, err := parse(html, "USD  "); err == nil {
		t.Fatal("want error for unparseable date")
	}
}

func TestParseSkipsNonPositiveAndBlankPrices(t *testing.T) {
	html := `<table id="foo_gvSearchResult2">
<tr><td>1.00</td><td>0.00</td><td>1.00</td><td>5 Mar 2026</td></tr>
<tr><td>1.00</td><td>-</td><td>1.00</td><td>5 Mar 2026</td></tr>
<tr><td>1.00</td><td>2.00</td><td></td><td>5 Mar 2026</td></tr>
<tr><td>1.00</td><td><span>2.00</span></td><td>1.00</td><td>05 March 2026</td></tr>
</table>`
	rates := mustParse(t, html, "USD  ")
	if len(rates) != 1 || rates[0].Rate != 1.5 || !rates[0].Date.Equal(adapter.Date(2026, 3, 5)) {
		t.Fatalf("got %+v, want one USD rate 1.5 on 2026-03-05", rates)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	if g.Cassette != "bi" || !slices.Equal(g.MatchRequestsOn, []string{"method", "host"}) || !g.AllowPlaybackRepeats {
		t.Fatalf("golden recorded with %s %v repeats=%v; vcrClient replays bi on method,host with repeats",
			g.Cassette, g.MatchRequestsOn, g.AllowPlaybackRepeats)
	}
	a := New(vcrClient(t)) // not g.Client: see vcrClient
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 5))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
