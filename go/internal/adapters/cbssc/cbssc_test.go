package cbssc

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
	a := New(vcrtest.Client(t, "cbssc", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func uniqueDates(rates []adapter.Rate) []time.Time {
	var dates []time.Time
	for _, r := range rates {
		if !slices.ContainsFunc(dates, r.Date.Equal) {
			dates = append(dates, r.Date)
		}
	}
	slices.SortFunc(dates, time.Time.Compare)
	return dates
}

func sortedBases(rates []adapter.Rate, unique bool) []string {
	var bases []string
	for _, r := range rates {
		bases = append(bases, r.Base)
	}
	slices.Sort(bases)
	if unique {
		bases = slices.Compact(bases)
	}
	return bases
}

func TestFetchFromWorkbook(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 8, 3), adapter.Date(2026, 8, 7))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if n := len(uniqueDates(rates)); n != 5 {
		t.Errorf("got %d dates, want 5", n)
	}
}

func TestFetchQuotesUSDEURGBPInSCR(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 8, 3), adapter.Date(2026, 8, 7))
	if got := sortedBases(rates, true); !slices.Equal(got, []string{"EUR", "GBP", "USD"}) {
		t.Errorf("bases = %v", got)
	}
	for _, r := range rates {
		if r.Quote != "SCR" {
			t.Errorf("quote = %s, want SCR", r.Quote)
		}
	}
}

func TestFetchUSDInPlausibleRange(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 8, 3), adapter.Date(2026, 8, 7))
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == "USD" })
	if i < 0 {
		t.Fatal("no USD rate")
	}
	if r := rates[i].Rate; r <= 10 || r >= 20 {
		t.Errorf("USD/SCR = %v, want between 10 and 20", r)
	}
}

func TestFetchRoundsWorkbookMidsToFourDecimals(t *testing.T) {
	for _, r := range fetch(t, adapter.Date(2026, 8, 3), adapter.Date(2026, 8, 7)) {
		if r.Rate != round4(r.Rate) {
			t.Errorf("%v %s rate %v not rounded to four decimals", r.Date, r.Base, r.Rate)
		}
	}
}

func TestFetchReachesBackToStartOfWorkbook(t *testing.T) {
	rates := fetch(t, adapter.Date(2000, 1, 4), adapter.Date(2000, 1, 4))
	if len(rates) != 3 {
		t.Fatalf("got %d rates, want 3", len(rates))
	}
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == "USD" })
	if i < 0 || rates[i].Rate != 5.3441 {
		t.Errorf("USD rate = %+v, want 5.3441", rates)
	}
}

func TestFetchRespectsBounds(t *testing.T) {
	got := uniqueDates(fetch(t, adapter.Date(2026, 8, 5), adapter.Date(2026, 8, 6)))
	want := []time.Time{adapter.Date(2026, 8, 5), adapter.Date(2026, 8, 6)}
	if !slices.EqualFunc(got, want, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", got, want)
	}
}

func TestFetchIncludesLiveDay(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 9), adapter.Date(2026, 9, 9))
	if got := sortedBases(rates, false); !slices.Equal(got, []string{"EUR", "GBP", "USD"}) {
		t.Errorf("bases = %v", got)
	}
}

func TestParseLiveCARMids(t *testing.T) {
	rates, err := parseLive([]byte(`{"car":[{"usdmid":"14.4077","currentDate":"09-Sep-2026","usdbuy":"14.2268","eursell":"17.3166",
"gbpdiff":"0.7936","gbpbuy":"19.1057","usddiff":"-0.2089","gbpmid":"20.1872","usdsell":"14.7228",
"eurdiff":"0.0284","eurmid":"16.8958","id":1,"eurbuy":"16.8603","gbpsell":"20.3083"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 3 {
		t.Fatalf("got %d rates, want 3", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 9, 9), Base: "USD", Quote: "SCR", Rate: 14.4077}
	if rates[0] != want {
		t.Errorf("first = %+v, want %+v", rates[0], want)
	}
	if rates[2].Rate != 20.1872 {
		t.Errorf("last rate = %v, want 20.1872", rates[2].Rate)
	}
}

func TestParseLiveRaisesWhenCARMissing(t *testing.T) {
	if _, err := parseLive([]byte(`{"car":[]}`)); err == nil {
		t.Error("want an error for an empty CAR block")
	}
}

func TestParseLiveRejectsInvalidMid(t *testing.T) {
	for _, mid := range []string{"NaN", "Inf", "", "n/a"} {
		json := `{"car":[{"currentDate":"09-Sep-2026","usdmid":"` + mid + `","eurmid":"16.8958","gbpmid":"20.1872"}]}`
		if _, err := parseLive([]byte(json)); err == nil {
			t.Errorf("usdmid %q: want an error", mid)
		}
	}
}

func TestParseLiveSkipsZeroAndAcceptsNumbers(t *testing.T) {
	rates, err := parseLive([]byte(`{"car":[{"currentDate":"9-Sep-2026","usdmid":14.4077,"eurmid":"0","gbpmid":"20.1872"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{
		{Date: adapter.Date(2026, 9, 9), Base: "USD", Quote: "SCR", Rate: 14.4077},
		{Date: adapter.Date(2026, 9, 9), Base: "GBP", Quote: "SCR", Rate: 20.1872},
	}
	if !slices.Equal(rates, want) {
		t.Errorf("rates = %+v, want %+v", rates, want)
	}
}

func TestParseLiveRaisesWhenMidMissing(t *testing.T) {
	if _, err := parseLive([]byte(`{"car":[{"currentDate":"09-Sep-2026","usdmid":"14.4","eurmid":"16.9"}]}`)); err == nil {
		t.Error("want an error when gbpmid is missing")
	}
}

func TestGolden(t *testing.T) {
	cases := []struct {
		file        string
		after, upto time.Time
	}{
		{"fetch.json", adapter.Date(2026, 8, 3), adapter.Date(2026, 8, 7)},
		{"archive_start.json", adapter.Date(2000, 1, 4), adapter.Date(2000, 1, 4)},
		{"live.json", adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 9)},
		{"full.json", time.Time{}, time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			g := golden.Load(t, "testdata/golden/"+c.file)
			rates, err := New(g.Client(t)).Fetch(context.Background(), c.after, c.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
