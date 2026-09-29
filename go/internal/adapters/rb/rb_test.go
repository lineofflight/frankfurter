package rb

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func bases(rates []adapter.Rate) []string {
	var out []string
	for _, r := range rates {
		out = append(out, r.Base)
	}
	return out
}

func mustParse(t *testing.T, data string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetch(t *testing.T) {
	a := New(vcrtest.Client(t, "rb", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 24), adapter.Date(2026, 3, 28))
	if err != nil {
		t.Fatal(err)
	}
	dates := map[string]bool{}
	currencies := map[string]bool{}
	for _, r := range rates {
		dates[r.Date.Format("2006-01-02")] = true
		currencies[r.Base] = true
	}
	if len(dates) < 3 {
		t.Errorf("got %d dates, want at least 3", len(dates))
	}
	if !currencies["EUR"] || !currencies["USD"] {
		t.Errorf("currencies %v missing EUR or USD", currencies)
	}
	if len(currencies) < 20 {
		t.Errorf("got %d currencies, want at least 20", len(currencies))
	}
}

func TestParseByGroupResponse(t *testing.T) {
	rates := mustParse(t, `[
		{"seriesId": "SEKEURPMI", "date": "2026-03-24", "value": 10.8238},
		{"seriesId": "SEKUSDPMI", "date": "2026-03-24", "value": 9.9421}
	]`)
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	if rates[0].Base != "EUR" || rates[0].Quote != "SEK" || rates[0].Rate != 10.8238 {
		t.Errorf("first rate = %+v", rates[0])
	}
	if rates[1].Base != "USD" {
		t.Errorf("last base = %s, want USD", rates[1].Base)
	}
}

func TestParseRelabelsPredecessors(t *testing.T) {
	tests := []struct {
		name, data string
		want       []string
	}{
		{"pre-1999 EUR as the ECU", `[
			{"seriesId": "SEKEURPMI", "date": "1998-12-30", "value": 9.4685},
			{"seriesId": "SEKEURPMI", "date": "1999-01-04", "value": 9.3060}
		]`, []string{"XEU", "EUR"}},
		{"pre-1998 RUB as the old ruble", `[
			{"seriesId": "SEKRUBPMI", "date": "1997-12-30", "value": 0.0013},
			{"seriesId": "SEKRUBPMI", "date": "1998-01-02", "value": 1.326}
		]`, []string{"RUR", "RUB"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bases(mustParse(t, tt.data)); !slices.Equal(got, tt.want) {
				t.Errorf("bases = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseFiltersIdentityRate(t *testing.T) {
	rates := mustParse(t, `[
		{"seriesId": "SEKETT", "date": "2026-03-24", "value": 1.0},
		{"seriesId": "SEKEURPMI", "date": "2026-03-24", "value": 10.8238}
	]`)
	if got := bases(rates); !slices.Equal(got, []string{"EUR"}) {
		t.Errorf("bases = %v, want [EUR]", got)
	}
}

func TestParseSkipsZeroRate(t *testing.T) {
	if rates := mustParse(t, `[{"seriesId": "SEKEURPMI", "date": "2026-03-24", "value": 0}]`); len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseSkipsMissingValues(t *testing.T) {
	if rates := mustParse(t, `[{"seriesId": "SEKEURPMI", "date": "2026-03-24", "value": null}]`); len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseRejectsInvalidJSON(t *testing.T) {
	if _, err := parse([]byte("Not found")); err == nil {
		t.Error("want an error for invalid JSON")
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 24), adapter.Date(2026, 3, 28))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

func TestParseSkipsFalseValue(t *testing.T) {
	if rates := mustParse(t, `[{"seriesId": "SEKEURPMI", "date": "2026-03-24", "value": false}]`); len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseStringValue(t *testing.T) {
	rates := mustParse(t, `[{"seriesId": "SEKEURPMI", "date": "2026-03-24", "value": "10.8238"}]`)
	if len(rates) != 1 || rates[0].Rate != 10.8238 {
		t.Errorf("rates = %+v, want one at 10.8238", rates)
	}
}

func TestParseErrors(t *testing.T) {
	for name, data := range map[string]string{
		"not an array":  `{"seriesId": "SEKEURPMI"}`,
		"bad value":     `[{"seriesId": "SEKEURPMI", "date": "2026-03-24", "value": "n/a"}]`,
		"boolean value": `[{"seriesId": "SEKEURPMI", "date": "2026-03-24", "value": true}]`,
		"bad date":      `[{"seriesId": "SEKEURPMI", "date": "24/03/2026", "value": 1.5}]`,
	} {
		if _, err := parse([]byte(data)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

type recorder struct{ urls []string }

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.urls = append(r.urls, req.URL.String())
	return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}, Request: req}, nil
}

func TestFetchURL(t *testing.T) {
	tests := []struct {
		name        string
		after, upto time.Time
		want        string
	}{
		{"window", adapter.Date(2026, 3, 24), adapter.Date(2026, 3, 28), baseURL + "/2026-03-24/2026-03-28"},
		{"open ends", time.Time{}, time.Time{}, baseURL + "//2026-09-29"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{}
			a := New(&http.Client{Transport: rec})
			a.Now = func() time.Time { return time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC) }
			// An empty body is not JSON, so the fetch fails after the request is made.
			a.Fetch(context.Background(), tt.after, tt.upto)
			if len(rec.urls) != 1 || rec.urls[0] != tt.want {
				t.Errorf("urls = %v, want [%s]", rec.urls, tt.want)
			}
		})
	}
}
