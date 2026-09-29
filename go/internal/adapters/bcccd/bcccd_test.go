package bcccd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func newAdapter(t *testing.T) *Adapter {
	t.Helper()
	return New(vcrtest.Client(t, "bcccd", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI), vcrtest.AllowPlaybackRepeats))
}

func dataset(t *testing.T) []adapter.Rate {
	t.Helper()
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 9, 2), adapter.Date(2026, 9, 3))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParseDay(t *testing.T, html string, date time.Time) []adapter.Rate {
	t.Helper()
	rates, err := parseDay([]byte(html), date)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParseHistory(t *testing.T, payload string) []adapter.Rate {
	t.Helper()
	rates, err := parseHistory([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchWithDateRange(t *testing.T) {
	var dates []time.Time
	for _, r := range dataset(t) {
		if !slices.ContainsFunc(dates, r.Date.Equal) {
			dates = append(dates, r.Date)
		}
	}
	want := []time.Time{adapter.Date(2026, 9, 2), adapter.Date(2026, 9, 3)}
	if !slices.EqualFunc(dates, want, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", dates, want)
	}
}

func TestFetchQuotesWholeBasketAgainstCDF(t *testing.T) {
	var bases []string
	for _, r := range dataset(t) {
		if !r.Date.Equal(adapter.Date(2026, 9, 3)) {
			continue
		}
		if r.Quote != "CDF" {
			t.Errorf("quote = %q, want CDF", r.Quote)
		}
		bases = append(bases, r.Base)
	}
	for _, code := range []string{"USD", "RWF"} {
		if !slices.Contains(bases, code) {
			t.Errorf("bases %v lack %s", bases, code)
		}
	}
	if len(bases) <= 10 {
		t.Errorf("sample size = %d, want > 10", len(bases))
	}
}

func TestFetchPrefersDatedPageForOverlappingPairs(t *testing.T) {
	date := adapter.Date(2026, 9, 3)
	rates := dataset(t)
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Date.Equal(date) && r.Base == "USD" })
	if i < 0 {
		t.Fatal("no USD row on 2026-09-03")
	}
	usd := rates[i]

	a := newAdapter(t)
	html, err := a.fetchDay(context.Background(), date)
	if err != nil {
		t.Fatal(err)
	}
	day := mustParseDay(t, string(html), date)
	j := slices.IndexFunc(day, func(r adapter.Rate) bool { return r.Base == "USD" })
	if j < 0 {
		t.Fatal("no USD on the dated page")
	}
	if usd.Rate != day[j].Rate {
		t.Errorf("rate = %v, want %v", usd.Rate, day[j].Rate)
	}
}

func TestFetchSortedByDate(t *testing.T) {
	rates := dataset(t)
	if !slices.IsSortedFunc(rates, func(x, y adapter.Rate) int { return x.Date.Compare(y.Date) }) {
		t.Error("rates not sorted by date")
	}
}

func TestParseDay(t *testing.T) {
	html := `<dl>
  <div><dt>USD (cours moyen)</dt><dd>2265.71</dd></div>
  <div><dt>EUR (cours moyen)</dt><dd>2 630,6300</dd></div>
  <div><dt>Autre</dt><dd>1</dd></div>
</dl>`
	records := mustParseDay(t, html, adapter.Date(2026, 9, 8))
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 9, 8), Base: "USD", Quote: "CDF", Rate: 2265.71}
	if records[0] != want {
		t.Errorf("first = %+v, want %+v", records[0], want)
	}
	if records[1].Rate != 2630.63 {
		t.Errorf("last rate = %v, want 2630.63", records[1].Rate)
	}
}

func TestParseDayWithoutRows(t *testing.T) {
	for _, html := range []string{"<article><p>05 janvier 2021</p></article>", ""} {
		if got := mustParseDay(t, html, adapter.Date(2021, 1, 5)); len(got) != 0 {
			t.Errorf("parseDay(%q) = %v, want empty", html, got)
		}
	}
}

func TestParseHistory(t *testing.T) {
	payload := `"series":{"USD":[1980.6]},"history":{"USD":[{"date":"2021-02-24","buy":1941.025,"average":1980.6377,"sell":2020.2505,"unit":1}],"RWF":[{"date":"2021-02-24","buy":1.5,"average":1.54,"sell":1.57,"unit":1}]},"other":1
`
	records := mustParseHistory(t, payload)
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	want := adapter.Rate{Date: adapter.Date(2021, 2, 24), Base: "USD", Quote: "CDF", Rate: 1980.6377}
	if records[0] != want {
		t.Errorf("first = %+v, want %+v", records[0], want)
	}
	if records[1].Base != "RWF" {
		t.Errorf("last base = %q, want RWF", records[1].Base)
	}
}

func TestParseHistoryNormalizesByUnitAndSkipsEmpty(t *testing.T) {
	payload := `"history":{"JPY":[{"date":"2026-09-09","average":1471,"unit":100},` +
		`{"date":"2026-09-08","average":null,"unit":1}]}`
	records := mustParseHistory(t, payload)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].Rate != 14.71 {
		t.Errorf("rate = %v, want 14.71", records[0].Rate)
	}
}

// Ruby stubs fetch_history and fetch_day; here a local server plays the landing page and 404s every dated page, which
// fetchDay turns into the same empty body.
func TestFetchLaterHistoryEntryWins(t *testing.T) {
	payload := `"history":{"USD":[{"date":"2025-11-03","average":2193.4902,"unit":1},` +
		`{"date":"2025-11-03","average":2261.1793,"unit":1}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()
	old := pageURL
	pageURL = srv.URL
	defer func() { pageURL = old }()

	rates, err := New(srv.Client()).Fetch(context.Background(), adapter.Date(2025, 11, 3), adapter.Date(2025, 11, 3))
	if err != nil {
		t.Fatal(err)
	}
	var got []float64
	for _, r := range rates {
		got = append(got, r.Rate)
	}
	if !slices.Equal(got, []float64{2261.1793}) {
		t.Errorf("rates = %v, want [2261.1793]", got)
	}
}

func TestParseHistoryMissing(t *testing.T) {
	if _, err := parseHistory([]byte("<html></html>")); err == nil {
		t.Error("want an error")
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 9, 2), adapter.Date(2026, 9, 3))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
