package fbil

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

func TestFetchesRates(t *testing.T) {
	a := New(vcrtest.Client(t, "fbil", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 17), adapter.Date(2026, 3, 21))
	if err != nil {
		t.Fatal(err)
	}

	dates := map[string]bool{}
	var currencies []string
	for _, r := range rates {
		dates[r.Date.Format("2006-01-02")] = true
		if !slices.Contains(currencies, r.Base) {
			currencies = append(currencies, r.Base)
		}
	}
	if len(dates) != 3 {
		t.Errorf("got %d dates, want 3", len(dates))
	}
	for _, c := range []string{"USD", "EUR"} {
		if !slices.Contains(currencies, c) {
			t.Errorf("currencies %v missing %s", currencies, c)
		}
	}
	if len(currencies) != 6 {
		t.Errorf("got %d currencies, want 6", len(currencies))
	}
}

func TestParseCorrectBaseAndQuote(t *testing.T) {
	rates, err := parse([]byte(`[{"processRunDate": "2026-03-17 00:00:00", "subProdName": "INR / 1 USD",
		"displayTime": "2026-03-17 13:00:00", "rate": 92.457, "comments": ""}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 17), Base: "USD", Quote: "INR", Rate: 92.457}
	if rates[0] != want {
		t.Errorf("rate = %+v, want %+v", rates[0], want)
	}
}

func TestParseAdjustsByUnitMultiplier(t *testing.T) {
	tests := []struct {
		name, subProd string
		rate          string
		base          string
		want, delta   float64
	}{
		{"JPY", "INR / 100 JPY", "57.99", "JPY", 0.5799, 0.0001},
		{"IDR", "INR / 10000 IDR", "54.4093", "IDR", 0.00544093, 0.00000001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rates, err := parse([]byte(`[{"processRunDate": "2026-03-17 00:00:00", "subProdName": "` + tt.subProd +
				`", "displayTime": "2026-03-17 13:00:00", "rate": ` + tt.rate + `, "comments": ""}]`))
			if err != nil {
				t.Fatal(err)
			}
			if len(rates) == 0 {
				t.Fatal("no rates")
			}
			if rates[0].Base != tt.base {
				t.Errorf("base = %s, want %s", rates[0].Base, tt.base)
			}
			if math.Abs(rates[0].Rate-tt.want) > tt.delta {
				t.Errorf("rate = %v, want %v", rates[0].Rate, tt.want)
			}
		})
	}
}

func TestParseSkips(t *testing.T) {
	tests := []struct{ name, subProd, rate string }{
		{"zero rate", `"INR / 1 USD"`, `0.0`},
		{"missing subProdName", `null`, `92.457`},
		{"non-numeric rate", `"INR / 1 USD"`, `"N/A"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rates, err := parse([]byte(`[{"processRunDate": "2026-03-17 00:00:00", "subProdName": ` + tt.subProd +
				`, "displayTime": "2026-03-17 13:00:00", "rate": ` + tt.rate + `, "comments": ""}]`))
			if err != nil {
				t.Fatal(err)
			}
			if len(rates) != 0 {
				t.Errorf("got %d rates, want none", len(rates))
			}
		})
	}
}

func TestParseRejectsNonArray(t *testing.T) {
	for _, body := range []string{`{"error": true}`, `null`} {
		if _, err := parse([]byte(body)); err == nil {
			t.Errorf("want an error for %s", body)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchSendsWindow(t *testing.T) {
	tests := []struct {
		name        string
		after, upto time.Time
		from, to    string
	}{
		{"bounded", adapter.Date(2026, 3, 17), adapter.Date(2026, 3, 21), "2026-03-17", "2026-03-21"},
		{"open", time.Time{}, time.Time{}, "", "2026-09-29"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got *http.Request
			a := New(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				got = r
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("[]")), Header: http.Header{}}, nil
			})})
			a.Now = func() time.Time { return time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC) }
			if _, err := a.Fetch(context.Background(), tt.after, tt.upto); err != nil {
				t.Fatal(err)
			}
			if got.URL.Host != "www.fbil.org.in" || got.URL.Path != "/wasdm/refrates/fetchfiltered" {
				t.Errorf("url = %s", got.URL)
			}
			q := got.URL.Query()
			if !q.Has("fromDate") || q.Get("fromDate") != tt.from || q.Get("toDate") != tt.to || q.Get("authenticated") != "false" {
				t.Errorf("query = %v", q)
			}
		})
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 17), adapter.Date(2026, 3, 21))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
