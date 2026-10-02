package nbu

import (
	"context"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

// newAdapter pins today to the cassette's end date; Ruby's spec relies on
// host-only matching instead.
func newAdapter(t *testing.T) *Adapter {
	a := New(vcrtest.Client(t, "nbu", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	a.Now = func() time.Time { return time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC) }
	return a
}

func TestFetchesRatesSinceDate(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 3, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("expected rates")
	}
}

func TestFetchesMultipleCurrenciesPerDate(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 3, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("expected rates")
	}
	first := rates[0].Date
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 1 {
		t.Errorf("got %d rates on %s, want more than 1", n, first.Format(time.DateOnly))
	}
}

func TestParseRestoresTajikistaniRubleBeforeSomoni(t *testing.T) {
	rates, err := parse([]byte(`[
		{"exchangedate": "01.09.2000", "cc": "TJS", "units": 1000, "rate": 2.7776},
		{"exchangedate": "02.12.2002", "cc": "TJS", "units": 1, "rate": 1.805263}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 || rates[0].Base != "TJR" || rates[1].Base != "TJS" {
		t.Fatalf("got %+v, want bases TJR, TJS", rates)
	}
	if math.Abs(rates[0].Rate-0.0027776) > 1e-9 {
		t.Errorf("rate = %v, want 0.0027776", rates[0].Rate)
	}
}

func TestParseRelabelsSuccessorsPublishedUnderRetiredCodes(t *testing.T) {
	rates, err := parse([]byte(`[
		{"exchangedate": "31.12.1997", "cc": "RUR", "units": 10000, "rate": 3.19},
		{"exchangedate": "05.01.1998", "cc": "RUR", "units": 10, "rate": 3.19},
		{"exchangedate": "01.07.1999", "cc": "BGL", "units": 1000, "rate": 2.1073},
		{"exchangedate": "02.08.1999", "cc": "BGL", "units": 1000, "rate": 2259.4804}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		date, base string
		rate       float64
	}{
		{"1997-12-31", "RUR", 0.000319},
		{"1998-01-05", "RUB", 0.319},
		{"1999-07-01", "BGL", 0.0021073},
		{"1999-08-02", "BGN", 2.2594804},
	}
	if len(rates) != len(want) {
		t.Fatalf("got %d rates, want %d", len(rates), len(want))
	}
	for i, w := range want {
		r := rates[i]
		if r.Date.Format(time.DateOnly) != w.date || r.Base != w.base || math.Abs(r.Rate-w.rate) > w.rate*1e-9 {
			t.Errorf("rate %d = %s %s %v, want %s %s %v", i, r.Date.Format(time.DateOnly), r.Base, r.Rate, w.date,
				w.base, w.rate)
		}
	}
}

func TestParseReadsSuccessorsQuotedPerHundredUnderStaleUnits(t *testing.T) {
	rates, err := parse([]byte(`[
		{"exchangedate": "03.01.2000", "cc": "BGL", "units": 1000, "rate": 271.2867},
		{"exchangedate": "05.01.2005", "cc": "TRL", "units": 10000, "rate": 0.0376},
		{"exchangedate": "27.06.2005", "cc": "TRL", "units": 10000, "rate": 372.9741},
		{"exchangedate": "30.06.2005", "cc": "ROL", "units": 10000, "rate": 1.7401},
		{"exchangedate": "01.07.2005", "cc": "ROL", "units": 10000, "rate": 169.0657},
		{"exchangedate": "05.01.2006", "cc": "AZM", "units": 10000, "rate": 10.995},
		{"exchangedate": "06.01.2006", "cc": "AZM", "units": 10000, "rate": 549.8693},
		{"exchangedate": "05.01.2009", "cc": "TMM", "units": 10000, "rate": 5.4035},
		{"exchangedate": "06.01.2009", "cc": "TMM", "units": 10000, "rate": 270.1754},
		{"exchangedate": "04.04.2014", "cc": "TRY", "units": 100, "rate": 541.6837}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	bases := []string{"BGN", "TRL", "TRY", "ROL", "RON", "AZM", "AZN", "TMM", "TMT", "TRY"}
	want := []float64{
		2.712867, 0.00000376, 3.729741, 0.00017401, 1.690657, 0.0010995, 5.498693, 0.00054035, 2.701754, 5.416837,
	}
	if len(rates) != len(want) {
		t.Fatalf("got %d rates, want %d", len(rates), len(want))
	}
	for i, r := range rates {
		if r.Base != bases[i] || math.Abs(r.Rate-want[i]) > want[i]*1e-9 {
			t.Errorf("rate %d = %s %v, want %s %v", i, r.Base, r.Rate, bases[i], want[i])
		}
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchRequestsDateRange(t *testing.T) {
	var got *http.Request
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r
		return &http.Response{StatusCode: 200, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(`[]`)), Request: r}, nil
	})}
	a := New(client)
	a.Now = func() time.Time { return time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC) }
	if _, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), time.Time{}); err != nil {
		t.Fatal(err)
	}
	q := got.URL.Query()
	if u := got.URL.Scheme + "://" + got.URL.Host + got.URL.Path; u != baseURL {
		t.Errorf("url = %s, want %s", u, baseURL)
	}
	for k, want := range map[string]string{"start": "20260301", "end": "20260316", "sort": "exchangedate", "order": "asc"} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	if _, ok := q["json"]; !ok {
		t.Error("missing json param")
	}
}

func TestFetchRequiresAfter(t *testing.T) {
	if _, err := New(http.DefaultClient).Fetch(context.Background(), time.Time{}, adapter.Date(2026, 3, 4)); err == nil {
		t.Error("want error for zero after")
	}
}

func TestParseSkipsAndScales(t *testing.T) {
	rates, err := parse([]byte(`[
		{"exchangedate": "07.03.2026", "cc": "USD", "rate": 41.5},
		{"exchangedate": "09.03.2026", "cc": "XDR", "rate": 56.1},
		{"exchangedate": "09.03.2026", "cc": "usd", "rate": 41.5},
		{"exchangedate": "09.03.2026", "cc": "EUR", "rate": 0},
		{"exchangedate": "09.03.2026", "cc": "GBP", "rate": null},
		{"exchangedate": "09.03.2026", "cc": "CHF", "units": null, "rate": 47.2},
		{"exchangedate": "09.03.2026", "cc": "PLN", "units": 0, "rate": 10.9},
		{"exchangedate": "09.03.2026", "cc": "JPY", "units": 100, "rate": 27.8},
		{"exchangedate": "9.3.2026", "cc": "CAD", "units": "1", "rate": "30.1abc"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 3 {
		t.Fatalf("got %+v, want XDR, JPY and CAD", rates)
	}
	want := []struct {
		base string
		rate float64
	}{{"XDR", 56.1}, {"JPY", 0.278}, {"CAD", 30.1}}
	for i, w := range want {
		r := rates[i]
		if r.Base != w.base || r.Quote != "UAH" || math.Abs(r.Rate-w.rate) > 1e-12 || !r.Date.Equal(adapter.Date(2026, 3, 9)) {
			t.Errorf("rates[%d] = %+v, want %s %v UAH on 2026-03-09", i, r, w.base, w.rate)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, body := range []string{
		`{"exchangedate": "09.03.2026"}`,
		`[{"cc": "USD", "rate": 41.5}]`,
		`[{"exchangedate": "09.03.2026", "rate": 41.5}]`,
		`[{"exchangedate": "09.03.2026", "cc": "USD"}]`,
		`[{"exchangedate": "31.02.2026", "cc": "USD", "rate": 41.5}]`,
		`[{"exchangedate": "09.03.2026", "cc": "USD", "rate": true}]`,
	} {
		if _, err := parse([]byte(body)); err == nil {
			t.Errorf("parse(%s): want error", body)
		}
	}
}
