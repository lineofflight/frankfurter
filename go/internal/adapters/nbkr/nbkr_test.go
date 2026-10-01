package nbkr

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func find(rates []adapter.Rate, base string) (adapter.Rate, bool) {
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == base })
	if i < 0 {
		return adapter.Rate{}, false
	}
	return rates[i], true
}

// Live XML feed

func fetchLive(t *testing.T, after time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "nbkr_live", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI), vcrtest.AllowPlaybackRepeats))
	// The day after the cassette's daily feed, so a set after scrapes nothing.
	a.Now = func() time.Time { return time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC) }
	rates, err := a.Fetch(context.Background(), after, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchLiveBothEndpointsWhenUptoUnset(t *testing.T) {
	if rates := fetchLive(t, time.Time{}); len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchLiveForeignBaseKGSQuote(t *testing.T) {
	usd, ok := find(fetchLive(t, time.Time{}), "USD")
	if !ok {
		t.Fatal("no USD")
	}
	if usd.Quote != "KGS" {
		t.Errorf("quote = %s, want KGS", usd.Quote)
	}
	if usd.Rate <= 50 {
		t.Errorf("rate = %v, want > 50", usd.Rate)
	}
}

func TestFetchLiveIncludesDailyAndWeekly(t *testing.T) {
	rates := fetchLive(t, time.Time{})
	for _, code := range []string{"USD", "EUR", "RUB", "GBP", "JPY", "CHF"} {
		if _, ok := find(rates, code); !ok {
			t.Errorf("missing %s", code)
		}
	}
}

func TestFetchLiveFiltersByAfter(t *testing.T) {
	if rates := fetchLive(t, adapter.Date(2026, 5, 25)); len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

// Historical HTML scrape

var (
	historicalAfter = adapter.Date(2005, 6, 1)
	historicalUpto  = adapter.Date(2005, 6, 30)
)

func fetchHistorical(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "nbkr_historical", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI), vcrtest.AllowPlaybackRepeats))
	a.currencies = []currency{{15, "USD", 1}, {20, "EUR", 1}}
	rates, err := a.Fetch(context.Background(), historicalAfter, historicalUpto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchHistoricalPastWindow(t *testing.T) {
	rates := fetchHistorical(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	usd, ok := find(rates, "USD")
	if !ok {
		t.Fatal("no USD")
	}
	if usd.Quote != "KGS" {
		t.Errorf("quote = %s, want KGS", usd.Quote)
	}
	// USD/KGS in mid-2000s was roughly 40-45.
	if math.Abs(usd.Rate-41.0) > 5.0 {
		t.Errorf("rate = %v, want 41 +/- 5", usd.Rate)
	}
}

func TestFetchHistoricalConstrainsToWindow(t *testing.T) {
	rates := fetchHistorical(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Date.Before(historicalAfter) || r.Date.After(historicalUpto) {
			t.Errorf("date %v outside window", r.Date)
		}
	}
}

// parse

func TestParseDailyCommaDecimals(t *testing.T) {
	rates, err := parse([]byte(`<?xml version="1.0" encoding="windows-1251" ?>
<CurrencyRates Name="Daily Exchange Rates" Date="23.05.2026">
  <Currency ISOCode="USD"><Nominal>1</Nominal><Value>87,4500</Value></Currency>
  <Currency ISOCode="EUR"><Nominal>1</Nominal><Value>101,5076</Value></Currency>
</CurrencyRates>
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	usd, _ := find(rates, "USD")
	if !usd.Date.Equal(adapter.Date(2026, 5, 23)) {
		t.Errorf("date = %v", usd.Date)
	}
	if usd.Quote != "KGS" {
		t.Errorf("quote = %s", usd.Quote)
	}
	if math.Abs(usd.Rate-87.45) > 0.0001 {
		t.Errorf("rate = %v", usd.Rate)
	}
}

func TestParseNormalizesByNominal(t *testing.T) {
	rates, err := parse([]byte(`<?xml version="1.0" encoding="windows-1251" ?>
<CurrencyRates Name="Weekly Exchange Rates" Date="23.05.2026">
  <Currency ISOCode="JPY"><Nominal>10</Nominal><ValidFor>7</ValidFor><Value>5,4969</Value></Currency>
  <Currency ISOCode="BYR"><Nominal>100</Nominal><ValidFor>7</ValidFor><Value>0,3402</Value></Currency>
</CurrencyRates>
`))
	if err != nil {
		t.Fatal(err)
	}
	jpy, _ := find(rates, "JPY")
	byr, _ := find(rates, "BYR")
	if math.Abs(jpy.Rate-0.54969) > 0.00001 {
		t.Errorf("JPY = %v", jpy.Rate)
	}
	if math.Abs(byr.Rate-0.003402) > 0.000001 {
		t.Errorf("BYR = %v", byr.Rate)
	}
}

func TestParseSkipsInvalidOrEmpty(t *testing.T) {
	rates, err := parse([]byte(`<?xml version="1.0" encoding="windows-1251" ?>
<CurrencyRates Name="Daily Exchange Rates" Date="23.05.2026">
  <Currency ISOCode="USD"><Nominal>1</Nominal><Value>0,0000</Value></Currency>
  <Currency ISOCode="EUR"><Nominal>1</Nominal><Value></Value></Currency>
  <Currency ISOCode="XX"><Nominal>1</Nominal><Value>1,2345</Value></Currency>
</CurrencyRates>
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %v, want none", rates)
	}
}

// parseHistorical

func TestParseHistoricalExtractsPairs(t *testing.T) {
	rates, err := parseHistorical([]byte(`<table><tr>
  <td><!--date-->04.06.2005<!--date--></td>
  <td><!--value-->40,9879<!--value--></td>
</tr><tr>
  <td><!--date-->28.05.2005<!--date--></td>
  <td><!--value-->40,9577<!--value--></td>
</tr></table>
`), "USD", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	first := rates[0]
	if !first.Date.Equal(adapter.Date(2005, 6, 4)) || first.Base != "USD" || first.Quote != "KGS" {
		t.Errorf("first = %+v", first)
	}
	if math.Abs(first.Rate-40.9879) > 0.0001 {
		t.Errorf("rate = %v", first.Rate)
	}
}

func TestParseHistoricalNormalizesByNominal(t *testing.T) {
	html := []byte(`<tr><td><!--date-->15.01.1999<!--date--></td><td><!--value-->2,5816<!--value--></td></tr>
<tr><td><!--date-->01.01.1999<!--date--></td><td><!--value-->0,3402<!--value--></td></tr>
`)
	jpy, err := parseHistorical(html, "JPY", 10)
	if err != nil {
		t.Fatal(err)
	}
	byr, err := parseHistorical(html, "BYR", 100)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(jpy[0].Rate-0.25816) > 0.00001 {
		t.Errorf("JPY = %v", jpy[0].Rate)
	}
	if math.Abs(byr[len(byr)-1].Rate-0.003402) > 0.000001 {
		t.Errorf("BYR = %v", byr[len(byr)-1].Rate)
	}
}

func TestParseHistoricalSkipsZero(t *testing.T) {
	rates, err := parseHistorical([]byte(`<tr><td><!--date-->01.01.1999<!--date--></td><td><!--value-->0,0000<!--value--></td></tr>
`), "USD", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %v, want none", rates)
	}
}

func TestGoldenLive(t *testing.T) {
	g := golden.Load(t, "testdata/golden/live.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

func TestGoldenHistorical(t *testing.T) {
	g := golden.Load(t, "testdata/golden/historical.json")
	a := New(g.Client(t))
	a.currencies = []currency{{15, "USD", 1}, {20, "EUR", 1}}
	rates, err := a.Fetch(context.Background(), historicalAfter, historicalUpto)
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// recordingAdapter answers every request with an empty feed and records the
// requested paths.
func recordingAdapter(paths *[]string) *Adapter {
	return queryRecordingAdapter(paths, nil)
}

// queryRecordingAdapter is recordingAdapter that also records each query.
func queryRecordingAdapter(paths *[]string, queries *[]url.Values) *Adapter {
	a := New(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		*paths = append(*paths, r.URL.Path)
		if queries != nil {
			*queries = append(*queries, r.URL.Query())
		}
		body := `<CurrencyRates Date="23.05.2026"></CurrencyRates>`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}, nil
	})})
	a.currencies = []currency{{15, "USD", 1}}
	a.Now = func() time.Time { return time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC) }
	return a
}

func TestFetchDispatchesByWindow(t *testing.T) {
	today := adapter.Date(2026, 5, 23)
	live := []string{"/XML/daily.xml", "/XML/weekly.xml"}
	both := []string{"/index1.jsp", "/XML/daily.xml", "/XML/weekly.xml"}
	tests := []struct {
		name        string
		after, upto time.Time
		want        []string
	}{
		{"open window", time.Time{}, time.Time{}, live},
		{"after only", today.AddDate(0, 0, -10), time.Time{}, both},
		{"upto only in past", time.Time{}, today.AddDate(0, 0, -1), live},
		{"upto today", today.AddDate(0, 0, -10), today, both},
		{"upto in future", today.AddDate(0, 0, -10), today.AddDate(0, 0, 1), both},
		{"upto yesterday", today.AddDate(0, 0, -10), today.AddDate(0, 0, -1), []string{"/index1.jsp"}},
		{"after two days ago", today.AddDate(0, 0, -2), time.Time{}, both},
		{"after yesterday", today.AddDate(0, 0, -1), time.Time{}, live},
		{"after today", today, time.Time{}, live},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var paths []string
			if _, err := recordingAdapter(&paths).Fetch(context.Background(), tt.after, tt.upto); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(paths, tt.want) {
				t.Errorf("paths = %v, want %v", paths, tt.want)
			}
		})
	}
}

func TestFetchScrapesHistoryBeforeTheLiveSnapshot(t *testing.T) {
	var paths []string
	var queries []url.Values
	a := queryRecordingAdapter(&paths, &queries)
	if _, err := a.Fetch(context.Background(), adapter.Date(2026, 1, 10), time.Time{}); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 3 || paths[0] != "/index1.jsp" {
		t.Fatalf("paths = %v, want the historical page then the live feed", paths)
	}
	q := queries[0]
	from := q.Get("beg_year") + "-" + q.Get("beg_month") + "-" + q.Get("beg_day")
	to := q.Get("end_year") + "-" + q.Get("end_month") + "-" + q.Get("end_day")
	if from != "2026-01-11" || to != "2026-05-22" {
		t.Errorf("scraped %s..%s, want 2026-01-11..2026-05-22 (after exclusive, through yesterday)", from, to)
	}
}

func TestFetchOpenWindowReturnsHistoryAndTheLiveSnapshot(t *testing.T) {
	a := New(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `<CurrencyRates Date="23.05.2026"><Currency ISOCode="USD"><Nominal>1</Nominal><Value>87,45</Value></Currency></CurrencyRates>`
		if r.URL.Path == "/index1.jsp" {
			body = `<tr><td><!--date-->22.05.2026<!--date--></td><td><!--value-->87,40<!--value--></td></tr>
<tr><td><!--date-->21.05.2026<!--date--></td><td><!--value-->87,35<!--value--></td></tr>`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}, nil
	})})
	a.currencies = []currency{{15, "USD", 1}}
	a.Now = func() time.Time { return time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC) }
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 20), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rates {
		if r.Base == "USD" {
			got = append(got, r.Date.Format(time.DateOnly))
		}
	}
	// Two USD rows from the live feed: daily and weekly answer alike here.
	want := []string{"2026-05-22", "2026-05-21", "2026-05-23", "2026-05-23"}
	if !slices.Equal(got, want) {
		t.Errorf("USD dates = %v, want %v", got, want)
	}
}

func TestFetchRoutineRunRequestsOnlyTheLiveFeed(t *testing.T) {
	var paths []string
	a := recordingAdapter(&paths)
	// Backfill resumes from the newest stored day: yesterday on a routine run.
	if _, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 22), time.Time{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"/XML/daily.xml", "/XML/weekly.xml"}; !slices.Equal(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestParseErrorsWithoutDate(t *testing.T) {
	if _, err := parse([]byte(`<CurrencyRates><Currency ISOCode="USD"><Nominal>1</Nominal><Value>87,45</Value></Currency></CurrencyRates>`)); err == nil {
		t.Error("want error for missing Date attribute")
	}
}

func TestParseErrorsWithoutRoot(t *testing.T) {
	if _, err := parse([]byte(`<Other Date="23.05.2026"></Other>`)); err == nil {
		t.Error("want error for missing CurrencyRates root")
	}
}

func TestParseSkipsZeroOrMissingNominal(t *testing.T) {
	rates, err := parse([]byte(`<CurrencyRates Date="23.05.2026">
  <Currency ISOCode="USD"><Nominal>0</Nominal><Value>87,45</Value></Currency>
  <Currency ISOCode="EUR"><Value>101,50</Value></Currency>
</CurrencyRates>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %v, want none", rates)
	}
}

func TestParseHistoricalErrorsOnImpossibleDate(t *testing.T) {
	if _, err := parseHistorical([]byte(`<!--date-->31.02.2005<!--date--><!--value-->40,1<!--value-->`), "USD", 1); err == nil {
		t.Error("want error for 31.02.2005")
	}
}
