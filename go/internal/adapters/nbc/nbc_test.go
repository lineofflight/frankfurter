package nbc

import (
	"context"
	"errors"
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

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "nbc", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, html string, date time.Time) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(html), date)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func row(name, symbol, unit, bid, ask, average string) string {
	return `<table>
  <tr>
    <td>` + name + `</td>
    <td align='center'>` + symbol + `</td>
    <td align='center'>` + unit + `</td>
    <td align='right'>` + bid + `</td>
    <td align='right'>` + ask + `</td>
    <td align='right'>` + average + `</td>
  </tr>
</table>`
}

// stubClient answers like WebMock.stub_request: one canned response per method, anything else fails the test.
func stubClient(t *testing.T, responses map[string]*http.Response) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "www.nbc.gov.kh" {
			t.Fatalf("unexpected host %s", r.URL.Host)
		}
		resp, ok := responses[r.Method]
		if !ok {
			t.Fatalf("unexpected %s %s", r.Method, r.URL)
		}
		return resp, nil
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestFetchDateRange(t *testing.T) {
	if rates := fetch(t, adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 20)); len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 20), adapter.Date(2026, 5, 20))
	var dates []time.Time
	for _, r := range rates {
		if !slices.ContainsFunc(dates, r.Date.Equal) {
			dates = append(dates, r.Date)
		}
	}
	if len(dates) != 1 {
		t.Errorf("dates = %v, want 1", dates)
	}
	if len(rates) <= 10 {
		t.Errorf("got %d rates, want more than 10", len(rates))
	}
}

func TestFetchIncludesUSDFromHeadline(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 20), adapter.Date(2026, 5, 20))
	if !slices.ContainsFunc(rates, func(r adapter.Rate) bool { return r.Base == "USD" && r.Quote == "KHR" }) {
		t.Error("no USD/KHR rate")
	}
}

func TestParseUnits(t *testing.T) {
	tests := []struct {
		name, html, base string
		rate             float64
	}{
		{"unit-1 rows directly", row("European Euro", "EUR/KHR", "1", "4678", "4725", "4701.50"), "EUR", 4701.5},
		{"per-100 unit rows", row("Japanese Yen", "JPY/KHR", "100", "2529", "2554", "2541.50"), "JPY", 25.415},
		{"per-1000 unit rows", row("Vietnamese Dong", "VND/KHR", "1000", "153", "154", "153.50"), "VND", 0.1535},
		{"rewrites SDR to XDR", row("Special Drawing Right", "SDR/KHR", "1", "5498", "5553", "5525.50"), "XDR", 5525.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rates := mustParse(t, tt.html, adapter.Date(2026, 5, 20))
			if len(rates) != 1 {
				t.Fatalf("got %d rates, want 1", len(rates))
			}
			r := rates[0]
			if r.Base != tt.base || r.Quote != "KHR" {
				t.Errorf("pair = %s/%s, want %s/KHR", r.Base, r.Quote, tt.base)
			}
			if math.Abs(r.Rate-tt.rate) > 0.0001 {
				t.Errorf("rate = %v, want %v", r.Rate, tt.rate)
			}
		})
	}
}

func TestParseUSDFromHeadline(t *testing.T) {
	html := `<table>
  <tr><td>Official Exchange Rate : <font color="#FF3300">4022</font> KHR / USD</td></tr>
</table>
` + row("European Euro", "EUR/KHR", "1", "4678", "4725", "4701.50")
	rates := mustParse(t, html, adapter.Date(2026, 5, 20))
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == "USD" })
	if i < 0 {
		t.Fatal("no USD rate")
	}
	if rates[i].Quote != "KHR" {
		t.Errorf("quote = %s, want KHR", rates[i].Quote)
	}
	if rates[i].Rate != 4022.0 {
		t.Errorf("rate = %v, want 4022", rates[i].Rate)
	}
}

func TestParseEmptyForNoData(t *testing.T) {
	html := `<table>
  <tr><td colspan='6'>There is no data available.</td></tr>
</table>`
	if rates := mustParse(t, html, adapter.Date(2026, 5, 17)); len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestFetchRaisesWhenPostBlocked(t *testing.T) {
	client := stubClient(t, map[string]*http.Response{
		http.MethodGet:  response(200, "<input name='tk' value='abc'>"),
		http.MethodPost: response(403, "<html><body>Request blocked</body></html>"),
	})
	_, err := New(client).Fetch(context.Background(), adapter.Date(2026, 5, 20), adapter.Date(2026, 5, 20))
	var status *adapter.StatusError
	if !errors.As(err, &status) {
		t.Fatalf("err = %v, want StatusError", err)
	}
}

func TestFetchRaisesWhenLandingPageBlocked(t *testing.T) {
	client := stubClient(t, map[string]*http.Response{http.MethodGet: response(403, "blocked")})
	_, err := New(client).Fetch(context.Background(), adapter.Date(2026, 5, 20), adapter.Date(2026, 5, 20))
	var status *adapter.StatusError
	if !errors.As(err, &status) {
		t.Fatalf("err = %v, want StatusError", err)
	}
}

func TestFetchSkipsSundays(t *testing.T) {
	// 2026-05-17 is a Sunday: skipped without any request.
	if rates := fetch(t, adapter.Date(2026, 5, 17), adapter.Date(2026, 5, 17)); len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 20))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
