package rbf

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "rbf", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func find(rates []adapter.Rate, quote string) *adapter.Rate {
	for i := range rates {
		if rates[i].Quote == quote {
			return &rates[i]
		}
	}
	return nil
}

func TestFetchFJDBase(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 22))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Base != "FJD" {
			t.Fatalf("base = %s, want FJD", r.Base)
		}
	}
}

func TestFetchCoversAllEightQuotes(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 22))
	var quotes []string
	for _, r := range rates {
		if !slices.Contains(quotes, r.Quote) {
			quotes = append(quotes, r.Quote)
		}
	}
	slices.Sort(quotes)
	want := []string{"AUD", "CHF", "EUR", "GBP", "JPY", "NZD", "USD", "XDR"}
	if !slices.Equal(quotes, want) {
		t.Errorf("quotes = %v, want %v", quotes, want)
	}
}

func TestFetchRelabelsSDRAsXDR(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 22), adapter.Date(2026, 5, 22))
	xdr := find(rates, "XDR")
	if xdr == nil {
		t.Fatal("no XDR rate")
	}
	if xdr.Rate <= 0 {
		t.Errorf("XDR rate = %v, want > 0", xdr.Rate)
	}
}

func TestFetchUSDInPlausibleRange(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 22), adapter.Date(2026, 5, 22))
	usd := find(rates, "USD")
	if usd == nil {
		t.Fatal("no USD rate")
	}
	if usd.Rate <= 0.3 || usd.Rate >= 0.6 {
		t.Errorf("USD rate = %v, want between 0.3 and 0.6", usd.Rate)
	}
}

func TestFetchRespectsBounds(t *testing.T) {
	after, upto := adapter.Date(2026, 5, 20), adapter.Date(2026, 5, 21)
	rates := fetch(t, after, upto)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Date.Before(after) || r.Date.After(upto) {
			t.Errorf("date %s outside %s..%s", r.Date.Format(time.DateOnly), after.Format(time.DateOnly), upto.Format(time.DateOnly))
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchErrorsWhenLinkMissing(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("<html><body>no link here</body></html>")),
			Request:    r,
		}, nil
	})}
	if _, err := New(client).Fetch(context.Background(), time.Time{}, time.Time{}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestGolden(t *testing.T) {
	tests := []struct {
		file        string
		after, upto time.Time
	}{
		{"fetch", adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 22)},
		{"day", adapter.Date(2026, 5, 22), adapter.Date(2026, 5, 22)},
		{"bounds", adapter.Date(2026, 5, 20), adapter.Date(2026, 5, 21)},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			g := golden.Load(t, "testdata/golden/"+tt.file+".json")
			rates, err := New(g.Client(t)).Fetch(context.Background(), tt.after, tt.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
