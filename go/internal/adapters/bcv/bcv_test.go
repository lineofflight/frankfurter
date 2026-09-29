package bcv

import (
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"os"
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
	a := New(vcrtest.Client(t, "bcv", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func fetchWeek(t *testing.T) []adapter.Rate {
	t.Helper()
	return fetch(t, adapter.Date(2026, 8, 3), adapter.Date(2026, 8, 7))
}

// parseFixture parses a workbook written by testdata/xls/build.rb, which builds the same workbooks as the Ruby spec.
func parseFixture(t *testing.T, name string) ([]adapter.Rate, error) {
	t.Helper()
	data, err := os.ReadFile("testdata/xls/" + name + ".xls")
	if err != nil {
		t.Fatal(err)
	}
	return parse(data)
}

func mustParseFixture(t *testing.T, name string) []adapter.Rate {
	t.Helper()
	rates, err := parseFixture(t, name)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func baseList(rates []adapter.Rate) []string {
	codes := make([]string, len(rates))
	for i, r := range rates {
		codes[i] = r.Base
	}
	return codes
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestFetchDateRangeWithinOneQuarter(t *testing.T) {
	rates := fetchWeek(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Quote != "VES" {
			t.Errorf("quote = %s, want VES", r.Quote)
		}
		if r.Date.Before(adapter.Date(2026, 8, 3)) || r.Date.After(adapter.Date(2026, 8, 7)) {
			t.Errorf("date %s outside 2026-08-03..2026-08-07", r.Date.Format(time.DateOnly))
		}
	}
}

func TestFetchHeadlineCurrencies(t *testing.T) {
	bases := baseList(fetchWeek(t))
	for _, iso := range []string{"USD", "EUR", "CNY", "TRY", "RUB"} {
		if !slices.Contains(bases, iso) {
			t.Errorf("missing %s in %v", iso, bases)
		}
	}
}

func TestFetchUSDInPlausibleRange(t *testing.T) {
	rates := fetchWeek(t)
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == "USD" })
	if i < 0 {
		t.Fatal("no USD row")
	}
	if usd := rates[i].Rate; usd <= 100 || usd >= 10_000 {
		t.Errorf("USD/VES = %v, want between 100 and 10000", usd)
	}
}

func TestFetchEachValueDateHasARowPerCurrency(t *testing.T) {
	byDate := map[time.Time][]string{}
	for _, r := range fetchWeek(t) {
		byDate[r.Date] = append(byDate[r.Date], r.Base)
	}
	if len(byDate) != 5 {
		t.Errorf("got %d value dates, want 5", len(byDate))
	}
	for date, bases := range byDate {
		if !slices.Contains(bases, "USD") {
			t.Errorf("%s: no USD", date.Format(time.DateOnly))
		}
		unique := slices.Compact(slices.Sorted(slices.Values(bases)))
		if len(unique) != len(bases) {
			t.Errorf("%s: duplicate bases in %v", date.Format(time.DateOnly), bases)
		}
	}
}

func TestFetchStopsPagingAndNamesMissingQuarter(t *testing.T) {
	html := `<a href="/sites/default/files/EstadisticasGeneral/2_1_2c26_smc.xls">III Trim 2026</a>`
	var pages []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		pages = append(pages, req.URL.Query().Get("page"))
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(html)), Request: req}, nil
	})}

	_, err := New(client).Fetch(context.Background(), adapter.Date(2022, 1, 3), adapter.Date(2022, 1, 7))
	if err == nil || !strings.Contains(err.Error(), "no workbook for 2021Q4") {
		t.Errorf("err = %v, want no workbook for 2021Q4", err)
	}
	if want := []string{"0", "1"}; !slices.Equal(pages, want) {
		t.Errorf("pages = %v, want %v", pages, want)
	}
}

// Not in the Ruby spec: a missing link is a not-yet only for the current quarter, and after is inclusive.
func TestFetchSkipsMissingCurrentQuarterOnly(t *testing.T) {
	html := `<a href="/sites/default/files/EstadisticasGeneral/2_1_2c26_smc.xls">III Trim 2026</a>`
	workbook, err := os.ReadFile("testdata/xls/fecha_valor.xls")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := []byte(html)
		if strings.HasSuffix(req.URL.Path, ".xls") {
			body = workbook
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body))), Request: req}, nil
	})}

	for _, tc := range []struct {
		name    string
		today   time.Time
		wantErr string
	}{
		{"current", adapter.Date(2026, 10, 1), ""},
		{"past", adapter.Date(2027, 1, 5), "no workbook for 2026Q4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := New(client)
			a.Now = func() time.Time { return tc.today }
			rates, err := a.Fetch(context.Background(), adapter.Date(2026, 9, 4), adapter.Date(2026, 10, 1))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("err = %v, want %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var dates []time.Time
			for _, r := range rates {
				dates = append(dates, r.Date)
			}
			want := []time.Time{adapter.Date(2026, 9, 7), adapter.Date(2026, 9, 4)}
			if !slices.EqualFunc(dates, want, time.Time.Equal) {
				t.Errorf("dates = %v, want %v", dates, want)
			}
		})
	}
}

func TestFetchNothingBeforeRedenominationWithoutFetching(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Errorf("unexpected request to %s", req.URL)
		return nil, errors.New("no requests expected")
	})}
	rates, err := New(client).Fetch(context.Background(), adapter.Date(2020, 4, 1), adapter.Date(2021, 9, 30))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestWorkbookLinksKeysLetteredLinksByQuarter(t *testing.T) {
	html := `
<a href="https://www.bcv.org.ve/sites/default/files/EstadisticasGeneral/2_1_2c26_smc.xls">III Trim 2026</a>
<a href="/sites/default/files/EstadisticasGeneral/2_1_2c23_smc_60.xls">III Trim 2023</a>
<a href="/sites/default/files/EstadisticasGeneral/2_1_2d21_smc.xls">IV Trim 2021</a>
<a href="/sites/default/files/EstadisticasGeneral/otra_cosa.xls">Unrelated</a>
`
	want := map[quarter]string{
		{2026, 3}: "https://www.bcv.org.ve/sites/default/files/EstadisticasGeneral/2_1_2c26_smc.xls",
		{2023, 3}: "https://www.bcv.org.ve/sites/default/files/EstadisticasGeneral/2_1_2c23_smc_60.xls",
		{2021, 4}: "https://www.bcv.org.ve/sites/default/files/EstadisticasGeneral/2_1_2d21_smc.xls",
	}
	if got := workbookLinks(html); !maps.Equal(got, want) {
		t.Errorf("links = %v, want %v", got, want)
	}
}

func TestParseEmitsBsMEAsk(t *testing.T) {
	got := mustParseFixture(t, "ask")
	want := []adapter.Rate{
		{Date: adapter.Date(2026, 9, 9), Base: "EUR", Quote: "VES", Rate: 954.02442394},
		{Date: adapter.Date(2026, 9, 9), Base: "USD", Quote: "VES", Rate: 820.1018},
	}
	if !slices.EqualFunc(got, want, func(a, b adapter.Rate) bool {
		return a.Date.Equal(b.Date) && a.Base == b.Base && a.Quote == b.Quote && a.Rate == b.Rate &&
			a.Bid == nil && a.Ask == nil && a.Mid == nil
	}) {
		t.Errorf("rates = %+v, want %+v", got, want)
	}
}

func TestParseDatesSheetsByFechaValor(t *testing.T) {
	var dates []time.Time
	for _, r := range mustParseFixture(t, "fecha_valor") {
		dates = append(dates, r.Date)
	}
	want := []time.Time{adapter.Date(2026, 9, 7), adapter.Date(2026, 9, 4)}
	if !slices.EqualFunc(dates, want, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", dates, want)
	}
}

func TestParseRelabelsMXPAsMXN(t *testing.T) {
	if got := baseList(mustParseFixture(t, "mxp")); !slices.Equal(got, []string{"MXN"}) {
		t.Errorf("bases = %v, want [MXN]", got)
	}
}

func TestParseSkipsRowsWithoutCodeOrPositiveRate(t *testing.T) {
	if got := baseList(mustParseFixture(t, "skips")); !slices.Equal(got, []string{"USD"}) {
		t.Errorf("bases = %v, want [USD]", got)
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct{ fixture, want string }{
		{"no_fecha_valor", "Fecha Valor"},
		{"no_ask", "Venta (ASK)"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			_, err := parseFixture(t, tc.fixture)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %s", err, tc.want)
			}
		})
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 8, 3), adapter.Date(2026, 8, 7))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
