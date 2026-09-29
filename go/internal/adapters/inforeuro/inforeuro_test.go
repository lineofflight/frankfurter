package inforeuro

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

// newAdapter pins today so the specs that rely on the real date stay deterministic.
func newAdapter(t *testing.T, today time.Time) *Adapter {
	a := New(vcrtest.Client(t, "inforeuro", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	a.Now = func() time.Time { return today.Add(12 * time.Hour) }
	return a
}

func fetch(t *testing.T, a *Adapter, after, upto time.Time) []adapter.Rate {
	t.Helper()
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

var today = adapter.Date(2026, 9, 26)

// dates returns the distinct dates in order of appearance.
func dates(rates []adapter.Rate) []time.Time {
	var ds []time.Time
	for _, r := range rates {
		if !slices.ContainsFunc(ds, r.Date.Equal) {
			ds = append(ds, r.Date)
		}
	}
	return ds
}

func wantDates(t *testing.T, rates []adapter.Rate, want ...time.Time) {
	t.Helper()
	if got := dates(rates); !slices.EqualFunc(got, want, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", got, want)
	}
}

func bases(rates []adapter.Rate, date time.Time) []string {
	var bs []string
	for _, r := range rates {
		if r.Date.Equal(date) && !slices.Contains(bs, r.Base) {
			bs = append(bs, r.Base)
		}
	}
	return bs
}

func quoteRates(rates []adapter.Rate, quote string) []float64 {
	var out []float64
	for _, r := range rates {
		if r.Quote == quote {
			out = append(out, r.Rate)
		}
	}
	return out
}

func hasQuote(rates []adapter.Rate, quote string) bool {
	return slices.ContainsFunc(rates, func(r adapter.Rate) bool { return r.Quote == quote })
}

func TestFetchDistinctMonthlyObservationsInEUROrientation(t *testing.T) {
	rates := fetch(t, newAdapter(t, today), adapter.Date(2026, 8, 1), adapter.Date(2026, 9, 1))

	wantDates(t, rates, adapter.Date(2026, 8, 1), adapter.Date(2026, 9, 1))
	type key struct {
		date  time.Time
		quote string
	}
	seen := map[key]bool{}
	for _, r := range rates {
		if r.Base != "EUR" {
			t.Errorf("base = %s, want EUR", r.Base)
		}
		seen[key{r.Date, r.Quote}] = true
	}
	if len(seen) != len(rates) {
		t.Errorf("%d distinct date/quote pairs, want %d", len(seen), len(rates))
	}
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool {
		return r.Date.Equal(adapter.Date(2026, 9, 1)) && r.Quote == "USD"
	})
	if i < 0 || rates[i].Rate != 1.1643 {
		t.Errorf("September USD missing or wrong: %v", i)
	}
	if hasQuote(rates, "EUR") {
		t.Error("EUR quoted against itself")
	}
}

func TestFetchClipsBoundsToEffectiveDates(t *testing.T) {
	a := newAdapter(t, today)
	rates := fetch(t, a, adapter.Date(2026, 8, 2), adapter.Date(2026, 9, 15))
	wantDates(t, rates, adapter.Date(2026, 9, 1))

	if rates := fetch(t, a, adapter.Date(2026, 8, 2), adapter.Date(2026, 8, 31)); len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestFetchClipsToArchiveAndSkipsNulls(t *testing.T) {
	rates := fetch(t, newAdapter(t, today), adapter.Date(1994, 1, 1), adapter.Date(1994, 3, 1))

	if len(rates) != 155 {
		t.Errorf("got %d rates, want 155", len(rates))
	}
	for _, r := range rates {
		if r.Base != "XEU" || !r.Date.Equal(adapter.Date(1994, 3, 1)) {
			t.Errorf("unexpected row %+v", r)
		}
	}
	if got := quoteRates(rates, "USD"); len(got) == 0 || got[0] != 1.12892 {
		t.Errorf("USD = %v, want 1.12892", got)
	}
	if got := quoteRates(rates, "RUR"); len(got) == 0 || got[0] != 1789.34 {
		t.Errorf("RUR = %v, want 1789.34", got)
	}
	if hasQuote(rates, "KGS") {
		t.Error("null KGS kept")
	}
}

func TestFetchDoesNotRequestUnpublishedFutureMonths(t *testing.T) {
	rates := fetch(t, newAdapter(t, adapter.Date(2026, 9, 26)), adapter.Date(2026, 9, 1), adapter.Date(2027, 1, 1))
	wantDates(t, rates, adapter.Date(2026, 9, 1))
}

// Ruby runs RateValidation.reject! first; it only relabels or drops rows, so the base per date is checked directly.
func TestFetchStepsIntoJanuaryAndChangesECUToEUR(t *testing.T) {
	rates := fetch(t, newAdapter(t, today), adapter.Date(1998, 12, 1), adapter.Date(1999, 1, 1))

	if got := bases(rates, adapter.Date(1998, 12, 1)); !slices.Equal(got, []string{"XEU"}) {
		t.Errorf("December 1998 bases = %v, want [XEU]", got)
	}
	if got := bases(rates, adapter.Date(1999, 1, 1)); !slices.Equal(got, []string{"EUR"}) {
		t.Errorf("January 1999 bases = %v, want [EUR]", got)
	}
}

// Provider["INFOREURO"].blends? is false because the seed's frequency is not daily.
func TestKeepsMonthlyObservationsOutOfBlends(t *testing.T) {
	data, err := os.ReadFile("../../../../db/seeds/providers/inforeuro.json")
	if err != nil {
		t.Fatal(err)
	}
	var seed struct {
		Frequency string `json:"frequency"`
	}
	if err := json.Unmarshal(data, &seed); err != nil {
		t.Fatal(err)
	}
	if seed.Frequency == "" || seed.Frequency == "daily" {
		t.Errorf("frequency = %q, want a non-daily frequency so INFOREURO never blends", seed.Frequency)
	}
}

// Ruby backfills and reads the rate back through the API; ingestion and the API belong to core, so this checks that
// the adapter hands over every published digit.
func TestPreservesLongPublishedRates(t *testing.T) {
	rates := fetch(t, newAdapter(t, adapter.Date(2019, 1, 2)), adapter.Date(2019, 1, 1), time.Time{})

	if got := quoteRates(rates, "VEF"); len(got) != 1 || got[0] != 57698326.62525 {
		t.Errorf("VEF = %v, want [57698326.62525]", got)
	}
}

func TestMapsHistoricalSourceLabels(t *testing.T) {
	a := newAdapter(t, today)
	congo := fetch(t, a, adapter.Date(1999, 2, 1), adapter.Date(1999, 3, 1))
	angola := fetch(t, a, adapter.Date(2000, 1, 1), adapter.Date(2000, 3, 1))

	if got := quoteRates(congo, "CDF"); !slices.Equal(got, []float64{2.89237, 2.74715}) {
		t.Errorf("CDF = %v", got)
	}
	if got := quoteRates(angola, "AOA"); !slices.Equal(got, []float64{5.54714, 5.78925, 5.68025}) {
		t.Errorf("AOA = %v", got)
	}
	if hasQuote(congo, "FRC") {
		t.Error("FRC kept")
	}
	if hasQuote(angola, "AOK") {
		t.Error("AOK kept")
	}
}

var parseDate = adapter.Date(2026, 9, 1)

func TestParseKeepsDigitsAndMapsZimbabweGold(t *testing.T) {
	rates, err := parse([]byte(`[{"isoA3Code":"ZIG","value":30.5535},{"isoA3Code":"VES","value":350.08868}]`), parseDate)
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{
		{Date: parseDate, Base: "EUR", Quote: "ZWG", Rate: 30.5535},
		{Date: parseDate, Base: "EUR", Quote: "VES", Rate: 350.08868},
	}
	if !slices.Equal(rates, want) {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseRetainsPredecessorAndSuccessor(t *testing.T) {
	rates, err := parse([]byte(`[{"isoA3Code":"BYN","value":2.0378},{"isoA3Code":"BYR","value":20378}]`),
		adapter.Date(2017, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 || rates[0].Quote != "BYN" || rates[0].Rate != 2.0378 ||
		rates[1].Quote != "BYR" || rates[1].Rate != 20378 {
		t.Errorf("got %+v", rates)
	}
}

func TestParseSkipsNullAndNonPositive(t *testing.T) {
	rates, err := parse([]byte(`[{"isoA3Code":"TRY","value":null},{"isoA3Code":"USD","value":0},`+
		`{"isoA3Code":"GBP","value":-1}]`), parseDate)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestParseRejectsSemanticError(t *testing.T) {
	for _, body := range []string{`{"error":"unavailable"}`, `[]`, `[{"isoA3Code":"USD"}]`} {
		if _, err := parse([]byte(body), parseDate); err == nil {
			t.Errorf("parse(%s) returned no error", body)
		}
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"testdata/golden/recent.json", adapter.Date(2026, 8, 1), adapter.Date(2027, 1, 1)},
		{"testdata/golden/archive.json", adapter.Date(1994, 1, 1), adapter.Date(1994, 3, 1)},
		{"testdata/golden/euro_inception.json", adapter.Date(1998, 12, 1), adapter.Date(1999, 3, 1)},
		{"testdata/golden/angola.json", adapter.Date(2000, 1, 1), adapter.Date(2000, 3, 1)},
		{"testdata/golden/vef.json", adapter.Date(2019, 1, 1), adapter.Date(2019, 1, 1)},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, tc.file)
			a := New(g.Client(t))
			a.Now = g.Now(t)
			rates, err := a.Fetch(context.Background(), tc.after, tc.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}

// Not in the Ruby spec: AOK keeps its label outside January and February 2000, and ECU-era rows drop an XEU quote.
func TestParseAOKWindowAndECUSelfQuote(t *testing.T) {
	body := []byte(`[{"isoA3Code":"AOK","value":5.5},{"isoA3Code":"XEU","value":1},{"isoA3Code":"FRC","value":2.5}]`)
	for _, tc := range []struct {
		date time.Time
		want []string
	}{
		{adapter.Date(1998, 12, 1), []string{"AOK", "CDF"}},
		{adapter.Date(2000, 1, 1), []string{"AOA", "XEU", "CDF"}},
		{adapter.Date(2000, 3, 1), []string{"AOK", "XEU", "CDF"}},
	} {
		rates, err := parse(body, tc.date)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range rates {
			got = append(got, r.Quote)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s quotes = %v, want %v", tc.date.Format(time.DateOnly), got, tc.want)
		}
	}
}
