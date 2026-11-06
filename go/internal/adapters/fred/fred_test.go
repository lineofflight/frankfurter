package fred

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	vcrtest.SetSecrets(t)
	a := New(vcrtest.Client(t, "fred", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchSinceDate(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	first := rates[0].Date
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 1 {
		t.Errorf("got %d rates on %s, want more than 1", n, first.Format("2006-01-02"))
	}
}

func TestParseSkipsMissingAndZeroValues(t *testing.T) {
	rates, err := parse([]byte(`{"observations":[
		{"date":"2026-03-02","value":"1.169"},
		{"date":"2026-03-03","value":"."},
		{"date":"2026-03-04","value":"0"}
	]}`), "USD", "EUR")
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{{Date: adapter.Date(2026, 3, 2), Base: "EUR", Quote: "USD", Rate: 1.169}}
	if len(rates) != 1 || rates[0] != want[0] {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseRejectsInvalidValue(t *testing.T) {
	if _, err := parse([]byte(`{"observations":[{"date":"2026-03-02","value":"n/a"}]}`), "BRL", "USD"); err == nil {
		t.Error("want an error for a non-numeric value")
	}
}

func TestParseWithoutObservations(t *testing.T) {
	rates, err := parse([]byte(`{"error_code":400}`), "BRL", "USD")
	if err != nil || len(rates) != 0 {
		t.Errorf("got %v, %v; want no rates, no error", rates, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchRequiresAPIKey(t *testing.T) {
	t.Setenv("FRED_API_KEY", "")
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected request to %s", r.URL)
		return nil, errors.New("no requests expected")
	})}
	_, err := New(client).Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 31))
	if err == nil || err.Error() != "no API key" {
		t.Errorf("got %v, want no API key", err)
	}
}

func TestFetchRequestsEverySeries(t *testing.T) {
	t.Setenv("FRED_API_KEY", "secret")
	var queries []url.Values
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.URL.Scheme + "://" + r.URL.Host + r.URL.Path; got != apiURL {
			t.Errorf("got URL %s, want %s", got, apiURL)
		}
		queries = append(queries, r.URL.Query())
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"observations":[]}`)), Request: r}, nil
	})}

	for _, tc := range []struct {
		after time.Time
		start string
	}{
		{time.Time{}, ""},
		{adapter.Date(2026, 3, 1), "2026-03-01"},
	} {
		queries = nil
		if _, err := New(client).Fetch(context.Background(), tc.after, adapter.Date(2026, 3, 31)); err != nil {
			t.Fatal(err)
		}
		if len(queries) != len(allSeries) {
			t.Fatalf("got %d requests, want %d", len(queries), len(allSeries))
		}
		for i, q := range queries {
			want := url.Values{"series_id": {allSeries[i].id}, "api_key": {"secret"}, "file_type": {"json"}}
			if tc.start != "" {
				want.Set("observation_start", tc.start)
			}
			if q.Encode() != want.Encode() {
				t.Errorf("request %d: got %s, want %s", i, q.Encode(), want.Encode())
			}
		}
	}
}

func TestGolden(t *testing.T) {
	vcrtest.SetSecrets(t)
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 31))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
