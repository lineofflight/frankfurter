package ecb

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func TestFetchRates(t *testing.T) {
	a := New(vcrtest.Client(t, "ecb_historical", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2025, 1, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
}

// No Ruby counterpart: covers the row filters in parse_row.
func TestParseSkipsNonDailyAndInvalidRows(t *testing.T) {
	rates, err := parse([]byte("FREQ,CURRENCY,TIME_PERIOD,OBS_VALUE\n" +
		"D,USD,2025-01-02,1.0321\n" +
		"M,USD,2025-01,1.03\n" +
		"D,usd,2025-01-02,1.0321\n" +
		"D,JPY,2025-01-02,\n" +
		"D,GBP,2025-01-02,0.8294"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2: %+v", len(rates), rates)
	}
	want := adapter.Rate{Date: adapter.Date(2025, 1, 2), Base: "EUR", Quote: "USD", Rate: 1.0321}
	if r := rates[0]; !r.Date.Equal(want.Date) || r.Base != want.Base || r.Quote != want.Quote || r.Rate != want.Rate {
		t.Errorf("rates[0] = %+v, want %+v", r, want)
	}
	if rates[1].Quote != "GBP" || rates[1].Rate != 0.8294 {
		t.Errorf("rates[1] = %+v", rates[1])
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2025, 1, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// No Ruby counterpart: the cassette matches on host only, so nothing else pins
// the query.
func TestFetchRequestParams(t *testing.T) {
	tests := []struct {
		after, upto time.Time
		want        string
	}{
		{time.Time{}, time.Time{}, "format=csvdata"},
		{adapter.Date(2025, 1, 1), time.Time{}, "format=csvdata&startPeriod=2025-01-01"},
		{adapter.Date(2025, 1, 1), adapter.Date(2025, 1, 31), "endPeriod=2025-01-31&format=csvdata&startPeriod=2025-01-01"},
	}
	for _, tt := range tests {
		var got *http.Request
		client := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
			got = r
			body := "FREQ,CURRENCY,TIME_PERIOD,OBS_VALUE\nD,USD,2025-01-02,1.0321\n"
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
		})}
		rates, err := New(client).Fetch(context.Background(), tt.after, tt.upto)
		if err != nil {
			t.Fatal(err)
		}
		if len(rates) != 1 {
			t.Errorf("got %d rates, want 1", len(rates))
		}
		if u := got.URL; u.Scheme+"://"+u.Host+u.Path != sdmxURL || u.RawQuery != tt.want {
			t.Errorf("url = %s, want %s?%s", u, sdmxURL, tt.want)
		}
	}
}

// No Ruby counterpart: Float(value) and Date.parse raise on junk.
func TestParseRejectsInvalidValues(t *testing.T) {
	for _, row := range []string{"D,USD,2025-01-02,n/a", "D,USD,2025-01-02,NaN", "D,USD,not-a-date,1.03"} {
		if _, err := parse([]byte("FREQ,CURRENCY,TIME_PERIOD,OBS_VALUE\n" + row)); err == nil {
			t.Errorf("parse(%q) returned no error", row)
		}
	}
}
