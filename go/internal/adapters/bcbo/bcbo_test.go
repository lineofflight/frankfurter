package bcbo

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
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

type recorder struct {
	urls   []string
	bodies map[string][]byte
}

func (rt *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.urls = append(rt.urls, req.URL.String())
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(rt.bodies[req.URL.Path])),
		Header:     http.Header{},
		Request:    req,
	}, nil
}

// Not covered by the Ruby spec: the request plan across the 2008 switch from yearly archives to daily sheets.
func TestFetchRequestPlan(t *testing.T) {
	rt := &recorder{bodies: map[string][]byte{
		"/tiposDeCambioHistorico/xls.php":                     fixture(t, "yearly_months"),
		"/librerias/indicadores/otras/otras_imprimir2XLS.php": fixture(t, "daily_current"),
	}}
	rates, err := New(&http.Client{Transport: rt}).Fetch(context.Background(),
		adapter.Date(2007, 3, 2), adapter.Date(2008, 1, 7))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{yearURL + "?anio=2007"}
	for _, day := range []int{1, 2, 3, 4, 7} { // 5 and 6 January 2008 fall on a weekend
		want = append(want, fmt.Sprintf("%s?qaa=2008&qdd=%d&qmm=1", dailyURL, day))
	}
	if !slices.Equal(rt.urls, want) {
		t.Errorf("requests = %v, want %v", rt.urls, want)
	}

	// The yearly fixture carries 2 January and 2 March; only the latter is inside the window. Each daily sheet adds 6.
	if len(rates) != 1+5*6 {
		t.Fatalf("got %d rates, want 31", len(rates))
	}
	if !rates[0].Date.Equal(adapter.Date(2007, 3, 2)) || rates[0].Rate != 6.95 {
		t.Errorf("first rate = %+v, want 6.95 on 2007-03-02", rates[0])
	}
}

func TestFetchEmptyWindowBeforeCoverage(t *testing.T) {
	rt := &recorder{}
	rates, err := New(&http.Client{Transport: rt}).Fetch(context.Background(), time.Time{}, adapter.Date(1999, 12, 31))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 || len(rt.urls) != 0 {
		t.Errorf("got %d rates and requests %v, want none", len(rates), rt.urls)
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
