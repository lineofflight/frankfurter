package nbrm

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "nbrm", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host), vcrtest.AllowPlaybackRepeats))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 5, 30))
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

func TestFetchExcludesMKDToMKD(t *testing.T) {
	for _, r := range fetch(t) {
		if r.Base == "MKD" && r.Quote == "MKD" {
			t.Fatalf("found MKD/MKD row: %+v", r)
		}
	}
}

func TestParse(t *testing.T) {
	rates, err := parse([]byte(`[
		{"oznaka": "EUR", "sreden": "61.5", "datum": "2026-03-01T00:00:00", "nomin": "1"},
		{"oznaka": "USD", "sreden": "57.2", "datum": "2026-03-01T00:00:00", "nomin": "1"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 1), Base: "EUR", Quote: "MKD", Rate: 61.5}
	if rates[0] != want {
		t.Errorf("first rate = %+v, want %+v", rates[0], want)
	}
}

func TestParseNormalizesNomin(t *testing.T) {
	rates, err := parse([]byte(`[
		{"oznaka": "JPY", "sreden": "38.8", "datum": "2010-01-04T00:00:00", "nomin": "100"},
		{"oznaka": "EUR", "sreden": "61.5", "datum": "2010-01-04T00:00:00", "nomin": "1"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, r := range rates {
		got[r.Base] = r.Rate
	}
	if jpy, ok := got["JPY"]; !ok || math.Abs(jpy-0.388) > 0.001 {
		t.Errorf("JPY = %v, want 0.388", jpy)
	}
	if eur, ok := got["EUR"]; !ok || eur != 61.5 {
		t.Errorf("EUR = %v, want 61.5", eur)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	// Ruby VCR, with allow_playback_repeats, serves unused interactions before repeating one, so the two chunk
	// requests got the two recorded responses. vcrtest's repeat mode always replays the first host match, so replay
	// by exact URI instead, which picks the same interactions Ruby did.
	a := New(vcrtest.Client(t, "nbrm", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 5, 30))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

type recorder []*url.URL

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	*r = append(*r, req.URL)
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("[]")), Request: req}, nil
}

func TestFetchRequestsInclusiveChunks(t *testing.T) {
	var reqs recorder
	a := New(&http.Client{Transport: &reqs})
	if _, err := a.Fetch(context.Background(), adapter.Date(2026, 1, 1), adapter.Date(2026, 5, 30)); err != nil {
		t.Fatal(err)
	}
	want := [][2]string{{"01.01.2026", "31.03.2026"}, {"01.04.2026", "30.05.2026"}}
	if len(reqs) != len(want) {
		t.Fatalf("got %d requests, want %d", len(reqs), len(want))
	}
	for i, u := range reqs {
		q := u.Query()
		if u.Host != "www.nbrm.mk" || u.Path != "/KLServiceNOV/GetExchangeRate" || q.Get("format") != "json" ||
			q.Get("StartDate") != want[i][0] || q.Get("EndDate") != want[i][1] {
			t.Errorf("request %d = %s, want %s to %s", i, u, want[i][0], want[i][1])
		}
	}
}

func TestParseSkipsAndNumbers(t *testing.T) {
	rates, err := parse([]byte(`[
		{"oznaka": " USD ", "sreden": 57.2, "datum": "2026-03-01T00:00:00", "nomin": 1},
		{"oznaka": "MKD", "sreden": "1", "datum": "2026-03-01T00:00:00", "nomin": "1"},
		{"oznaka": "XDRX", "sreden": "80", "datum": "2026-03-01T00:00:00", "nomin": "1"},
		{"oznaka": null, "sreden": "80", "datum": "2026-03-01T00:00:00", "nomin": "1"},
		{"oznaka": "RSD", "sreden": "0", "datum": "2026-03-01T00:00:00", "nomin": "100"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 1), Base: "USD", Quote: "MKD", Rate: 57.2}
	if len(rates) != 1 || rates[0] != want {
		t.Errorf("got %+v, want only %+v", rates, want)
	}
}

func TestParseErrors(t *testing.T) {
	for name, body := range map[string]string{
		"not an array": `{"error": "x"}`,
		"bad sreden":   `[{"oznaka": "EUR", "sreden": "", "datum": "2026-03-01T00:00:00", "nomin": "1"}]`,
		"bad nomin":    `[{"oznaka": "EUR", "sreden": "61.5", "datum": "2026-03-01T00:00:00", "nomin": "1.5"}]`,
		"bad date":     `[{"oznaka": "EUR", "sreden": "61.5", "datum": "", "nomin": "1"}]`,
	} {
		if _, err := parse([]byte(body)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
