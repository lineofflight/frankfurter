package nb

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

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "nb", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 16), adapter.Date(2026, 3, 24))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, csv string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchWithDateRange(t *testing.T) {
	if len(fetch(t)) == 0 {
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
		t.Errorf("got %d rates on %s, want more than 1", n, first.Format("2006-01-02"))
	}
}

func TestParseBaseAndQuote(t *testing.T) {
	rates := mustParse(t, `FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
B,USD,NOK,SP,2026-03-16,10.5432,0
`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 16), Base: "USD", Quote: "NOK", Rate: 10.5432}
	if rates[0] != want {
		t.Errorf("got %+v, want %+v", rates[0], want)
	}
}

func TestParseAdjustsByUnitMult(t *testing.T) {
	rates := mustParse(t, `FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
B,JPY,NOK,SP,2026-03-16,7.1234,2
`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if math.Abs(rates[0].Rate-0.071234) > 0.000001 {
		t.Errorf("rate = %v, want 0.071234", rates[0].Rate)
	}
}

func TestParseFiltersNonBusinessDayRows(t *testing.T) {
	rates := mustParse(t, `FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
M,USD,NOK,SP,2026-03-16,10.5432,0
B,EUR,NOK,SP,2026-03-16,11.2345,0
`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if rates[0].Base != "EUR" {
		t.Errorf("base = %q, want EUR", rates[0].Base)
	}
}

func TestParseIndexCodesAlongsideCurrencies(t *testing.T) {
	// Preserve the provider's index observations alongside its currency rates.
	rates := mustParse(t, `FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
B,I44,NOK,SP,2026-03-16,120.5432,0
B,TWI,NOK,SP,2026-03-16,115.432,0
B,USD,NOK,SP,2026-03-16,10.5432,0
`)
	if len(rates) != 3 {
		t.Errorf("got %d rates, want 3", len(rates))
	}
}

func TestParseSkipsBlankBaseAndDefaultsUnitMult(t *testing.T) {
	// No UNIT_MULT column means no scaling; a row without BASE_CUR is dropped.
	rates := mustParse(t, `FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE
B,,NOK,SP,2026-03-16,1.5
B,SEK,NOK,SP,2026-03-16,0.9876
`)
	want := []adapter.Rate{{Date: adapter.Date(2026, 3, 16), Base: "SEK", Quote: "NOK", Rate: 0.9876}}
	if len(rates) != 1 || rates[0] != want[0] {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseRejectsBadValues(t *testing.T) {
	const header = "FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT\n"
	for _, row := range []string{
		"B,USD,NOK,SP,2026-03-16,,0",
		"B,USD,NOK,SP,2026-03-16,NaN,0",
		"B,USD,NOK,SP,2026-03-16,10.5,x",
		"B,USD,NOK,SP,not-a-date,10.5,0",
		"B,USD,NOK,SP,2026-03-16",
	} {
		if _, err := parse([]byte(header + row + "\n")); err == nil {
			t.Errorf("want an error for %q", row)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchSendsWindow(t *testing.T) {
	tests := []struct {
		name        string
		after, upto time.Time
		want        string
	}{
		{"bounded", adapter.Date(2026, 3, 16), adapter.Date(2026, 3, 24), "endPeriod=2026-03-24&format=csvdata&startPeriod=2026-03-16"},
		{"open", time.Time{}, time.Time{}, "format=csvdata"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got *http.Request
			a := New(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				got = r
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
			})})
			if _, err := a.Fetch(context.Background(), tt.after, tt.upto); err != nil {
				t.Fatal(err)
			}
			if got.Method != http.MethodGet || got.URL.Host != "data.norges-bank.no" || got.URL.Path != "/api/data/EXR/B..NOK.SP" {
				t.Errorf("request = %s %s", got.Method, got.URL)
			}
			if q := got.URL.Query().Encode(); q != tt.want {
				t.Errorf("query = %s, want %s", q, tt.want)
			}
		})
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 16), adapter.Date(2026, 3, 24))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
