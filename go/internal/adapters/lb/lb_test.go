package lb

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

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "lb", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2014, 12, 29), adapter.Date(2014, 12, 31))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, xml string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func bases(rates []adapter.Rate) []string {
	var bs []string
	for _, r := range rates {
		bs = append(bs, r.Base)
	}
	return bs
}

func TestFetchPreEURRates(t *testing.T) {
	rates := fetch(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if rates[0].Quote != "LTL" {
		t.Errorf("quote = %s, want LTL", rates[0].Quote)
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
		t.Errorf("got %d rates on %v, want several", n, first)
	}
}

func TestParseLTType(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8"?>
<FxRates xmlns="http://www.lb.lt/WebServices/FxRates">
  <FxRate>
    <Tp>LT</Tp>
    <Dt>2014-12-30</Dt>
    <CcyAmt>
      <Ccy>LTL</Ccy>
      <Amt>7.6881</Amt>
    </CcyAmt>
    <CcyAmt>
      <Ccy>AED</Ccy>
      <Amt>10</Amt>
    </CcyAmt>
  </FxRate>
</FxRates>`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "AED" || r.Quote != "LTL" {
		t.Errorf("pair = %s/%s, want AED/LTL", r.Base, r.Quote)
	}
	if math.Abs(r.Rate-0.76881) > 0.00001 {
		t.Errorf("rate = %v, want 0.76881", r.Rate)
	}
	if !r.Date.Equal(adapter.Date(2014, 12, 30)) {
		t.Errorf("date = %v, want 2014-12-30", r.Date)
	}
}

func TestParseRestoresOldManat(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8"?>
<FxRates xmlns="http://www.lb.lt/WebServices/FxRates">
  <FxRate>
    <Tp>LT</Tp>
    <Dt>2005-12-30</Dt>
    <CcyAmt><Ccy>LTL</Ccy><Amt>0.63014</Amt></CcyAmt>
    <CcyAmt><Ccy>AZN</Ccy><Amt>1000</Amt></CcyAmt>
  </FxRate>
  <FxRate>
    <Tp>LT</Tp>
    <Dt>2006-01-09</Dt>
    <CcyAmt><Ccy>LTL</Ccy><Amt>3.1077</Amt></CcyAmt>
    <CcyAmt><Ccy>AZN</Ccy><Amt>1</Amt></CcyAmt>
  </FxRate>
</FxRates>`)
	if got := bases(rates); !slices.Equal(got, []string{"AZM", "AZN"}) {
		t.Fatalf("bases = %v, want [AZM AZN]", got)
	}
	if math.Abs(rates[0].Rate-0.00063014) > 1e-9 {
		t.Errorf("rate = %v, want 0.00063014", rates[0].Rate)
	}
}

func TestParseRestoresOldTurkmenManat(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8"?>
<FxRates xmlns="http://www.lb.lt/WebServices/FxRates">
  <FxRate>
    <Tp>LT</Tp>
    <Dt>2008-12-31</Dt>
    <CcyAmt><Ccy>LTL</Ccy><Amt>1.7354</Amt></CcyAmt>
    <CcyAmt><Ccy>TMT</Ccy><Amt>10000</Amt></CcyAmt>
  </FxRate>
  <FxRate>
    <Tp>LT</Tp>
    <Dt>2009-01-01</Dt>
    <CcyAmt><Ccy>LTL</Ccy><Amt>8.6770</Amt></CcyAmt>
    <CcyAmt><Ccy>TMT</Ccy><Amt>10</Amt></CcyAmt>
  </FxRate>
</FxRates>`)
	if got := bases(rates); !slices.Equal(got, []string{"TMM", "TMT"}) {
		t.Fatalf("bases = %v, want [TMM TMT]", got)
	}
	if math.Abs(rates[0].Rate-0.00017354) > 1e-9 {
		t.Errorf("rate = %v, want 0.00017354", rates[0].Rate)
	}
}

func TestParseEUType(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8"?>
<FxRates xmlns="http://www.lb.lt/WebServices/FxRates">
  <FxRate>
    <Tp>EU</Tp>
    <Dt>2025-03-17</Dt>
    <CcyAmt>
      <Ccy>EUR</Ccy>
      <Amt>1</Amt>
    </CcyAmt>
    <CcyAmt>
      <Ccy>AUD</Ccy>
      <Amt>1.7160</Amt>
    </CcyAmt>
  </FxRate>
</FxRates>`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "EUR" || r.Quote != "AUD" {
		t.Errorf("pair = %s/%s, want EUR/AUD", r.Base, r.Quote)
	}
	if math.Abs(r.Rate-1.7160) > 0.0001 {
		t.Errorf("rate = %v, want 1.7160", r.Rate)
	}
	if !r.Date.Equal(adapter.Date(2025, 3, 17)) {
		t.Errorf("date = %v, want 2025-03-17", r.Date)
	}
}

func TestParseNormalizesByQuantity(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8"?>
<FxRates xmlns="http://www.lb.lt/WebServices/FxRates">
  <FxRate>
    <Tp>LT</Tp>
    <Dt>2014-12-30</Dt>
    <CcyAmt>
      <Ccy>LTL</Ccy>
      <Amt>4.8611</Amt>
    </CcyAmt>
    <CcyAmt>
      <Ccy>AFN</Ccy>
      <Amt>100</Amt>
    </CcyAmt>
  </FxRate>
</FxRates>`)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if math.Abs(rates[0].Rate-0.048611) > 0.000001 {
		t.Errorf("rate = %v, want 0.048611", rates[0].Rate)
	}
}

func TestParseEmptyResponse(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8"?>
<FxRates xmlns="http://www.lb.lt/WebServices/FxRates" />`)
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseSkipsBlankFields(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8"?>
<FxRates xmlns="http://www.lb.lt/WebServices/FxRates">
  <FxRate>
    <Tp>LT</Tp>
    <Dt>2014-12-30</Dt>
    <CcyAmt><Ccy>LTL</Ccy><Amt>7.6881</Amt></CcyAmt>
    <CcyAmt><Ccy>AED</Ccy><Amt> </Amt></CcyAmt>
  </FxRate>
  <FxRate>
    <Tp>LT</Tp>
    <Dt>2014-12-30</Dt>
    <CcyAmt><Ccy>LTL</Ccy><Amt>0</Amt></CcyAmt>
    <CcyAmt><Ccy>AFN</Ccy><Amt>100</Amt></CcyAmt>
  </FxRate>
  <FxRate>
    <Tp>EU</Tp>
    <Dt>2025-03-17</Dt>
    <CcyAmt><Ccy>EUR</Ccy><Amt>1</Amt></CcyAmt>
  </FxRate>
  <FxRate>
    <Tp>EU</Tp>
    <Dt>2025-03-17</Dt>
    <CcyAmt><Ccy>EUR</Ccy><Amt>1</Amt></CcyAmt>
    <CcyAmt><Ccy>USD</Ccy><Amt>1.09</Amt></CcyAmt>
  </FxRate>
</FxRates>`)
	if len(rates) != 1 || rates[0].Quote != "USD" {
		t.Errorf("rates = %+v, want only EUR/USD", rates)
	}
}

func TestParseRejectsBadAmount(t *testing.T) {
	_, err := parse([]byte(`<FxRates><FxRate><Tp>EU</Tp><Dt>2025-03-17</Dt>
<CcyAmt><Ccy>EUR</Ccy><Amt>1</Amt></CcyAmt><CcyAmt><Ccy>USD</Ccy><Amt>n/a</Amt></CcyAmt></FxRate></FxRates>`))
	if err == nil {
		t.Error("want an error for a non-numeric amount")
	}
}

type recorder struct{ reqs []*http.Request }

func (rt *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.reqs = append(rt.reqs, req)
	body := io.NopCloser(strings.NewReader(`<FxRates/>`))
	return &http.Response{StatusCode: http.StatusOK, Body: body, Header: http.Header{}, Request: req}, nil
}

func TestFetchRequestsEachWeekdayWithType(t *testing.T) {
	rt := &recorder{}
	a := New(&http.Client{Transport: rt})
	// Wednesday 2014-12-31 through Monday 2015-01-05, crossing euro adoption and a weekend.
	if _, err := a.Fetch(context.Background(), adapter.Date(2014, 12, 31), adapter.Date(2015, 1, 5)); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, req := range rt.reqs {
		if u := req.URL.Scheme + "://" + req.URL.Host + req.URL.Path; u != baseURL {
			t.Errorf("url = %s", req.URL)
		}
		q := req.URL.Query()
		got = append(got, q.Get("tp")+" "+q.Get("dt"))
	}
	want := []string{"LT 2014-12-31", "EU 2015-01-01", "EU 2015-01-02", "EU 2015-01-05"}
	if !slices.Equal(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestFetchNeedsStartDate(t *testing.T) {
	a := New(&http.Client{Transport: &recorder{}})
	if _, err := a.Fetch(context.Background(), time.Time{}, adapter.Date(2015, 1, 5)); err == nil {
		t.Error("want an error without a start date")
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2014, 12, 29), adapter.Date(2014, 12, 31))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
