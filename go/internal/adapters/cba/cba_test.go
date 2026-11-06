package cba

import (
	"context"
	"io"
	"math"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
)

func newAdapter(t *testing.T) *Adapter {
	t.Helper()
	a := New(vcrtest.Client(t, "cba", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	a.Now = func() time.Time { return time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC) }
	return a
}

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 3, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchSinceDate(t *testing.T) {
	if rates := fetch(t); len(rates) == 0 {
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
		t.Errorf("got %d rates on %v, want several", n, first)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchRequestsYearLongChunks(t *testing.T) {
	var ranges []string
	dates := regexp.MustCompile(`<DateFrom>(.*)</DateFrom>\s*<DateTo>(.*)</DateTo>`)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		if m := dates.FindSubmatch(body); m != nil {
			ranges = append(ranges, string(m[1])+".."+string(m[2]))
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("<Envelope/>"))}, nil
	})}
	if _, err := New(client).Fetch(context.Background(), adapter.Date(2024, 1, 1), adapter.Date(2025, 1, 5)); err != nil {
		t.Fatal(err)
	}
	want := []string{"2024-01-01..2024-12-30", "2024-12-31..2025-01-05"}
	if !slices.Equal(ranges, want) {
		t.Errorf("ranges = %v, want %v", ranges, want)
	}
}

func TestFetchRequiresStartDate(t *testing.T) {
	if _, err := newAdapter(t).Fetch(context.Background(), time.Time{}, time.Time{}); err == nil {
		t.Fatal("want error for open start")
	}
}

func TestParseRangeConvertsMetalsAndAmounts(t *testing.T) {
	data := []byte(`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>
<ExchangeRatesByDateRangeByISOResponse xmlns="http://www.cba.am/"><ExchangeRatesByDateRangeByISOResult>
<diffgr:diffgram xmlns:diffgr="urn:schemas-microsoft-com:xml-diffgram-v1"><DocumentElement xmlns="">
<ExchangeRatesByRange><Rate>10</Rate><Amount>100</Amount><ISO>JPY</ISO><RateDate>2026-03-02T00:00:00</RateDate></ExchangeRatesByRange>
<ExchangeRatesByRange><Rate>2</Rate><Amount>1</Amount><ISO>XAU</ISO><RateDate>2026-03-02T00:00:00</RateDate></ExchangeRatesByRange>
<ExchangeRatesByRange><Rate>2</Rate><Amount>1</Amount><RateDate>2026-03-02T00:00:00</RateDate></ExchangeRatesByRange>
</DocumentElement></diffgr:diffgram></ExchangeRatesByDateRangeByISOResult></ExchangeRatesByDateRangeByISOResponse>
</soap:Body></soap:Envelope>`)
	rates, err := parseRange(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	if !rates[0].Date.Equal(adapter.Date(2026, 3, 2)) || rates[0].Quote != "AMD" || rates[0].Rate != 0.1 {
		t.Errorf("JPY = %+v", rates[0])
	}
	if math.Abs(rates[1].Rate-2*troyOunceGrams) > 1e-12 {
		t.Errorf("XAU = %v", rates[1].Rate)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	a.Now = g.Now(t)
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

// CBA history (cba_history cassette). The cassette matches on method, URI and
// body, as in Ruby, so it pins the SOAP envelopes (codes, date range); the
// SOAPAction header is pinned too.

func fetchHistory(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	soapAction := func(r *http.Request, _ []byte, rec cassette.Request) bool {
		return r.Header.Get("SOAPAction") == rec.Headers.Get("SOAPAction")
	}
	a := New(vcrtest.Client(t, "cba_history", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI, vcrtest.Body, soapAction)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

// series maps each date of code's rows to its rate.
func series(rates []adapter.Rate, code string) map[time.Time]float64 {
	out := map[time.Time]float64{}
	for _, r := range rates {
		if r.Base == code {
			out[r.Date] = r.Rate
		}
	}
	return out
}

func TestFetchRequestsSeriesCBANoLongerQuotesUnderTheirCurrentCodes(t *testing.T) {
	day := adapter.Date(2001, 12, 28)
	rates := fetchHistory(t, day, day)
	var bases []string
	for _, r := range rates {
		bases = append(bases, r.Base)
	}

	for _, code := range []string{"DEM", "DKK", "BYR", "ARS", "BGN", "BRL", "PLN", "UZS", "XDR"} {
		if !slices.Contains(bases, code) {
			t.Errorf("want %s among bases", code)
		}
	}
	for _, code := range []string{"ARP", "BGL", "BRC", "PLZ", "SDR", "USM", "TAD", "TMM", "TMT", "TRL", "ROL"} {
		if slices.Contains(bases, code) {
			t.Errorf("unexpected base %s", code)
		}
	}
	if got := series(rates, "PLN")[day]; got != 140.83 {
		t.Errorf("PLN = %v, want 140.83", got)
	}
}

func TestFetchReadsTheSomPer10BeforeCBAsUZSSeriesTakesOver(t *testing.T) {
	rates := fetchHistory(t, adapter.Date(2006, 12, 30), adapter.Date(2007, 1, 5))
	uzs := series(rates, "UZS")

	for date, want := range map[time.Time]float64{adapter.Date(2006, 12, 30): 0.293, adapter.Date(2007, 1, 5): 0.294} {
		if got := uzs[date]; math.Abs(got-want) > 1e-9 {
			t.Errorf("UZS on %s = %v, want %v", date.Format(time.DateOnly), got, want)
		}
	}
	var dates []time.Time
	for d := range series(rates, "PLN") {
		dates = append(dates, d)
	}
	slices.SortFunc(dates, time.Time.Compare)
	if len(dates) == 0 || !dates[0].Equal(adapter.Date(2006, 12, 30)) || !dates[len(dates)-1].Equal(adapter.Date(2007, 1, 5)) {
		t.Errorf("PLN dates %v, want 2006-12-30 through 2007-01-05", dates)
	}
}

func TestFetchRestoresTheTajikRubleBeforeTheSomoni(t *testing.T) {
	rates := fetchHistory(t, adapter.Date(2000, 10, 30), adapter.Date(2000, 11, 1))

	tjr := series(rates, "TJR")
	if len(tjr) != 1 || math.Abs(tjr[adapter.Date(2000, 10, 30)]-0.2671) > 1e-9 {
		t.Errorf("TJR = %v, want 0.2671 on 2000-10-30 only", tjr)
	}
	tjs := series(rates, "TJS")
	if want := map[time.Time]float64{adapter.Date(2000, 11, 1): 250.74}; !reflect.DeepEqual(tjs, want) {
		t.Errorf("TJS = %v, want %v", tjs, want)
	}
}

func TestFetchReadsTheTengePer10BeforeCBACorrectedItsAmount(t *testing.T) {
	kzt := series(fetchHistory(t, adapter.Date(2004, 12, 30), adapter.Date(2005, 1, 4)), "KZT")

	for date, want := range map[time.Time]float64{adapter.Date(2004, 12, 30): 3.737, adapter.Date(2005, 1, 4): 3.739} {
		if got := kzt[date]; math.Abs(got-want) > 1e-9 {
			t.Errorf("KZT on %s = %v, want %v", date.Format(time.DateOnly), got, want)
		}
	}
}

func TestFetchReadsTheKronaPer10BeforeCBACorrectedItsAmount(t *testing.T) {
	isk := series(fetchHistory(t, adapter.Date(2015, 3, 6), adapter.Date(2015, 3, 9)), "ISK")

	for date, want := range map[time.Time]float64{adapter.Date(2015, 3, 6): 3.54, adapter.Date(2015, 3, 9): 3.512} {
		if got := isk[date]; math.Abs(got-want) > 1e-9 {
			t.Errorf("ISK on %s = %v, want %v", date.Format(time.DateOnly), got, want)
		}
	}
}

func TestFetchCollapsesTheSDRAndXDRDuplicates(t *testing.T) {
	day := adapter.Date(2017, 3, 17)
	n := 0
	for _, r := range fetchHistory(t, day, day) {
		if r.Base == "XDR" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("got %d XDR rows, want 1", n)
	}
}

func TestGoldenHistory(t *testing.T) {
	for _, tc := range []struct {
		name        string
		after, upto time.Time
	}{
		{"dropped", adapter.Date(2001, 12, 28), adapter.Date(2001, 12, 28)},
		{"som", adapter.Date(2006, 12, 30), adapter.Date(2007, 1, 5)},
		{"ruble", adapter.Date(2000, 10, 30), adapter.Date(2000, 11, 1)},
		{"tenge", adapter.Date(2004, 12, 30), adapter.Date(2005, 1, 4)},
		{"krona", adapter.Date(2015, 3, 6), adapter.Date(2015, 3, 9)},
		{"sdr", adapter.Date(2017, 3, 17), adapter.Date(2017, 3, 17)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := golden.Load(t, "testdata/golden/history_"+tc.name+".json")
			rates, err := New(g.Client(t)).Fetch(context.Background(), tc.after, tc.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}

func TestBackfillRangeIsOneYear(t *testing.T) {
	if got := New(nil).BackfillRange(); got != 365 {
		t.Errorf("BackfillRange() = %d, want 365", got)
	}
}
