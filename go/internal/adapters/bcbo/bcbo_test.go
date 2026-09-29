package bcbo

import (
	"context"
	"os"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func newAdapter(t *testing.T) *Adapter {
	return New(vcrtest.Client(t, "bcbo", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI), vcrtest.AllowPlaybackRepeats))
}

// fixture reads a workbook written by testdata/fixtures/generate.rb, the spec's build_xls and build_daily_xls.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/fixtures/" + name + ".xls")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func bases(rates []adapter.Rate) []string {
	var out []string
	for _, r := range rates {
		if !slices.Contains(out, r.Base) {
			out = append(out, r.Base)
		}
	}
	return out
}

func sameRate(a, b adapter.Rate) bool {
	eq := func(x, y *float64) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	return a.Date.Equal(b.Date) && a.Base == b.Base && a.Quote == b.Quote && a.Rate == b.Rate &&
		eq(a.Bid, b.Bid) && eq(a.Ask, b.Ask) && eq(a.Mid, b.Mid)
}

func mustInclude(t *testing.T, rates []adapter.Rate, want adapter.Rate) {
	t.Helper()
	if !slices.ContainsFunc(rates, func(r adapter.Rate) bool { return sameRate(r, want) }) {
		t.Errorf("missing %+v in %+v", want, rates)
	}
}

func TestFetchHistoricalPre2008(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2007, 12, 27), adapter.Date(2007, 12, 28))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Base != "USD" || r.Quote != "BOB" {
			t.Errorf("got %s/%s, want USD/BOB", r.Base, r.Quote)
		}
		if r.Date.Before(adapter.Date(2007, 12, 27)) || r.Date.After(adapter.Date(2007, 12, 28)) {
			t.Errorf("date %s outside 2007-12-27..2007-12-28", r.Date.Format(time.DateOnly))
		}
	}
}

func TestFetchDailyPost2008(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 7, 13), adapter.Date(2026, 7, 14))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	got := bases(rates)
	for _, want := range []string{"USD", "EUR", "XAU", "XAG", "XDR"} {
		if !slices.Contains(got, want) {
			t.Errorf("bases %v missing %s", got, want)
		}
	}
}

func TestParseYearlyAveragesVentaAndCompra(t *testing.T) {
	rates, err := parseYearly(fixture(t, "yearly_mid"), 2024)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "USD" || r.Quote != "BOB" || r.Rate != 6.91 || !r.Date.Equal(adapter.Date(2024, 1, 1)) {
		t.Errorf("got %+v, want USD/BOB 6.91 on 2024-01-01", r)
	}
}

func TestParseYearlyMapsMonthBlocks(t *testing.T) {
	// January in columns 1-2, March in columns 5-6.
	rates, err := parseYearly(fixture(t, "yearly_months"), 2024)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(rates, func(i, j int) bool { return rates[i].Date.Before(rates[j].Date) })
	var dates []time.Time
	var values []float64
	for _, r := range rates {
		dates = append(dates, r.Date)
		values = append(values, r.Rate)
	}
	wantDates := []time.Time{adapter.Date(2024, 1, 2), adapter.Date(2024, 3, 2)}
	if !slices.EqualFunc(dates, wantDates, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", dates, wantDates)
	}
	if want := []float64{6.91, 6.95}; !slices.Equal(values, want) {
		t.Errorf("rates = %v, want %v", values, want)
	}
}

func TestParseYearlySkipsNonNumericDays(t *testing.T) {
	rates, err := parseYearly(fixture(t, "yearly_prom"), 2024)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if !rates[0].Date.Equal(adapter.Date(2024, 1, 1)) {
		t.Errorf("date = %v, want 2024-01-01", rates[0].Date)
	}
}

func TestParseYearlySkipsMonthsWithoutRate(t *testing.T) {
	// Day 31 exists in January but not in February; the February pair is nil.
	rates, err := parseYearly(fixture(t, "yearly_missing_month"), 2024)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if !rates[0].Date.Equal(adapter.Date(2024, 1, 31)) {
		t.Errorf("date = %v, want 2024-01-31", rates[0].Date)
	}
}

func TestParseYearlySkipsInvalidDates(t *testing.T) {
	// Day 30 in February (column pair 3-4) is not a real date and is dropped.
	rates, err := parseYearly(fixture(t, "yearly_invalid_date"), 2024)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestParseDailyLegacy(t *testing.T) {
	date := adapter.Date(2026, 6, 11)
	rates, err := parseDaily(fixture(t, "daily_legacy"), date)
	if err != nil {
		t.Fatal(err)
	}
	mustInclude(t, rates, adapter.Rate{Date: date, Base: "USD", Quote: "BOB", Rate: 6.91,
		Bid: adapter.Float(6.86), Ask: adapter.Float(6.96)})
	mustInclude(t, rates, adapter.Rate{Date: date, Base: "EUR", Quote: "BOB", Rate: 7.91})
	mustInclude(t, rates, adapter.Rate{Date: date, Base: "XDR", Quote: "USD", Rate: 1.36})
	mustInclude(t, rates, adapter.Rate{Date: date, Base: "XAU", Quote: "USD", Rate: 4082.56})
	mustInclude(t, rates, adapter.Rate{Date: date, Base: "XAG", Quote: "USD", Rate: 63.73})

	for _, r := range rates {
		if r.Base == "USD" && r.Rate == 6.86 {
			t.Errorf("Ecuador's USD row leaked: %+v", r)
		}
	}
}

func TestParseDailyCurrent(t *testing.T) {
	date := adapter.Date(2026, 7, 14)
	rates, err := parseDaily(fixture(t, "daily_current"), date)
	if err != nil {
		t.Fatal(err)
	}
	mustInclude(t, rates, adapter.Rate{Date: date, Base: "USD", Quote: "BOB", Rate: 10.5})
	mustInclude(t, rates, adapter.Rate{Date: date, Base: "EUR", Quote: "BOB", Rate: 11.95314})
	mustInclude(t, rates, adapter.Rate{Date: date, Base: "JPY", Quote: "BOB", Rate: 0.06464})
	mustInclude(t, rates, adapter.Rate{Date: date, Base: "XAU", Quote: "USD", Rate: 3999.28})
	mustInclude(t, rates, adapter.Rate{Date: date, Base: "XAG", Quote: "USD", Rate: 57.4583})
	mustInclude(t, rates, adapter.Rate{Date: date, Base: "XDR", Quote: "USD", Rate: 1.35904})

	// UFV (code "Bs/UFV") and SOFR are not currency rates. Check their values, since a UFV row would be keyed
	// "Bs/UFV", not "UFV".
	for _, r := range rates {
		if r.Rate == 3.30736 || r.Rate == 0.0355 {
			t.Errorf("non-currency row leaked: %+v", r)
		}
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"yearly.json", adapter.Date(2007, 12, 27), adapter.Date(2007, 12, 28)},
		{"daily.json", adapter.Date(2026, 7, 13), adapter.Date(2026, 7, 14)},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, "testdata/golden/"+tc.file)
			rates, err := New(g.Client(t)).Fetch(context.Background(), tc.after, tc.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
