package cbs

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "cbs", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func uniq[T comparable](rates []adapter.Rate, f func(adapter.Rate) T) []T {
	var out []T
	for _, r := range rates {
		if v := f(r); !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func TestFetchArchiveWorkbook(t *testing.T) {
	if rates := fetch(t, adapter.Date(2026, 5, 1), adapter.Date(2026, 5, 22)); len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchEmitsWSTBase(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 1), adapter.Date(2026, 5, 22))
	got := uniq(rates, func(r adapter.Rate) string { return r.Base })
	if !slices.Equal(got, []string{"WST"}) {
		t.Errorf("bases = %v, want [WST]", got)
	}
}

func TestFetchCoversNineQuotes(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 1), adapter.Date(2026, 5, 22))
	quotes := uniq(rates, func(r adapter.Rate) string { return r.Quote })
	for _, iso := range []string{"AUD", "CNH", "CNY", "EUR", "FJD", "GBP", "JPY", "NZD", "USD"} {
		if !slices.Contains(quotes, iso) {
			t.Errorf("quotes %v missing %s", quotes, iso)
		}
	}
}

func TestFetchRespectsBounds(t *testing.T) {
	after, upto := adapter.Date(2026, 5, 14), adapter.Date(2026, 5, 17)
	rates := fetch(t, after, upto)
	dates := uniq(rates, func(r adapter.Rate) time.Time { return r.Date })
	if len(dates) == 0 {
		t.Fatal("no rates")
	}
	lo := slices.MinFunc(dates, time.Time.Compare)
	hi := slices.MaxFunc(dates, time.Time.Compare)
	if lo.Before(after) {
		t.Errorf("min date %v before %v", lo, after)
	}
	if hi.After(upto) {
		t.Errorf("max date %v after %v", hi, upto)
	}
}

func TestFetchUSDPlausible(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 1), adapter.Date(2026, 5, 22))
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Quote == "USD" })
	if i < 0 {
		t.Fatal("no USD row")
	}
	if usd := rates[i].Rate; usd <= 0.2 || usd >= 0.6 {
		t.Errorf("USD rate = %v, want between 0.2 and 0.6", usd)
	}
}

func TestFetchRowPerCurrencyEachDate(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 22))
	for _, date := range uniq(rates, func(r adapter.Rate) time.Time { return r.Date }) {
		var quotes []string
		for _, r := range rates {
			if r.Date.Equal(date) {
				quotes = append(quotes, r.Quote)
			}
		}
		for _, iso := range []string{"USD", "EUR"} {
			if !slices.Contains(quotes, iso) {
				t.Errorf("%v: quotes %v missing %s", date, quotes, iso)
			}
		}
	}
}

func TestArchiveURL(t *testing.T) {
	tests := []struct {
		name, html, want string
	}{
		{
			"resolves the date-stamped workbook link",
			`<ul class="downloads">
  <li><a href="/media/Historical-Daily-Rates-June032026.xlsx">Historical rates</a></li>
</ul>`,
			"https://cbs.gov.ws/media/Historical-Daily-Rates-June032026.xlsx",
		},
		{
			"percent-encodes spaces in the filename",
			`<ul class="downloads">
  <li><a href="/media/Historical Daily Rates-220626.xlsx">Historical rates</a></li>
</ul>`,
			"https://cbs.gov.ws/media/Historical%20Daily%20Rates-220626.xlsx",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := archiveURL(tt.html)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("archiveURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestArchiveURLErrorsWithoutLink(t *testing.T) {
	if _, err := archiveURL("<html><body>no link here</body></html>"); err == nil {
		t.Error("expected an error")
	}
}

func TestParseMapsLabelsToISO(t *testing.T) {
	for label, want := range map[string]string{
		"TALA/USD":  "USD",
		"TALA/EURO": "EUR",
		"TALA/YEN":  "JPY",
		"TALA/CNY":  "CNY",
		"TALA/CNH":  "CNH",
	} {
		if got := currencies[label]; got != want {
			t.Errorf("currencies[%q] = %q, want %q", label, got, want)
		}
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 5, 1), adapter.Date(2026, 5, 22))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

func TestGoldenArchive(t *testing.T) {
	g := golden.Load(t, "testdata/golden/archive.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
