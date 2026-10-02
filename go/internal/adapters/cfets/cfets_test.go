package cfets

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func newAdapter(t *testing.T) *Adapter {
	return New(vcrtest.Client(t, "cfets", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
}

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	rates, err := newAdapter(t).Fetch(context.Background(), after, upto)
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
	return dates
}

func fixture(label, value string) []byte {
	return fmt.Appendf(nil, `{"data":{"head":[%q],"total":1,"pageTotal":1},
 "records":[{"date":"2026-09-08","values":[%q]}]}`, label, value)
}

func mustParse(t *testing.T, data []byte) []adapter.Rate {
	t.Helper()
	rates, err := parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchesRatesWithDateRange(t *testing.T) {
	dates := uniqueDates(fetch(t, adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 4)))
	want := []time.Time{adapter.Date(2026, 9, 4), adapter.Date(2026, 9, 3), adapter.Date(2026, 9, 2), adapter.Date(2026, 9, 1)}
	if !slices.EqualFunc(dates, want, time.Time.Equal) {
		t.Fatalf("dates = %v, want %v", dates, want)
	}
}

func TestFetchesMultipleCurrenciesPerDate(t *testing.T) {
	n := 0
	for _, r := range fetch(t, adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 4)) {
		if r.Date.Equal(adapter.Date(2026, 9, 4)) {
			n++
		}
	}
	if n != 25 {
		t.Fatalf("got %d rates on 2026-09-04, want 25", n)
	}
}

func TestPagesThroughLongRanges(t *testing.T) {
	dates := uniqueDates(fetch(t, adapter.Date(2026, 6, 1), adapter.Date(2026, 8, 31)))
	if len(dates) <= pageSize {
		t.Fatalf("got %d dates, want more than %d", len(dates), pageSize)
	}
	minDate := slices.MinFunc(dates, time.Time.Compare)
	maxDate := slices.MaxFunc(dates, time.Time.Compare)
	if !minDate.Equal(adapter.Date(2026, 6, 1)) {
		t.Errorf("min date = %v", minDate)
	}
	if !maxDate.Equal(adapter.Date(2026, 8, 31)) {
		t.Errorf("max date = %v", maxDate)
	}
}

func TestParsesForeignPerCNYPairs(t *testing.T) {
	rates := mustParse(t, fixture("USD/CNY", "6.7804"))
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if r := rates[0]; r.Base != "USD" || r.Quote != "CNY" || r.Rate != 6.7804 {
		t.Fatalf("got %+v", r)
	}
}

func TestParsesCNYPerForeignPairs(t *testing.T) {
	rates := mustParse(t, fixture("CNY/THB", "4.8863"))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if r := rates[0]; r.Base != "CNY" || r.Quote != "THB" || r.Rate != 4.8863 {
		t.Fatalf("got %+v", r)
	}
}

func TestNormalizesPer100Units(t *testing.T) {
	rates := mustParse(t, fixture("100JPY/CNY", "4.3706"))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if r := rates[0]; r.Base != "JPY" || math.Abs(r.Rate-0.043706) > 1e-9 {
		t.Fatalf("got %+v", r)
	}
}

func TestParseSkips(t *testing.T) {
	tests := []struct{ name, label, value string }{
		{"missing values", "CNY/MOP", "---"},
		{"zero rates", "USD/CNY", "0"},
		{"unrecognized labels", "USD", "6.78"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rates := mustParse(t, fixture(tt.label, tt.value)); len(rates) != 0 {
				t.Fatalf("got %+v, want none", rates)
			}
		})
	}
}

func TestRaisesWhenEndpointRefusesQuery(t *testing.T) {
	json := `{"data":{"head":["USD/CNY"],"flagMessage":"只提供一年历史数据查询及下载"}}`
	_, err := parse([]byte(json))
	if err == nil {
		t.Fatal("want error")
	}
	// Ruby prefixes the message with "CFETS"; in Go the caller adds the
	// provider key, so check the flag message.
	if want := "只提供一年历史数据查询及下载"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not include %q", err, want)
	}
}

func TestGolden(t *testing.T) {
	tests := []struct {
		file        string
		after, upto time.Time
	}{
		{"testdata/golden/fetch.json", adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 4)},
		{"testdata/golden/paged.json", adapter.Date(2026, 6, 1), adapter.Date(2026, 8, 31)},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			g := golden.Load(t, tt.file)
			rates, err := New(g.Client(t)).Fetch(context.Background(), tt.after, tt.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}

func TestParseSkipsUnusableLabelsAndMissingValues(t *testing.T) {
	json := `{"data":{"head":["USD/CNY","CNY/100JPY","0JPY/CNY",7,"EUR/CNY"],"pageTotal":1},
 "records":[{"date":"2026-09-08","values":["6.78","1.0","1.0","1.0"]}]}`
	rates := mustParse(t, []byte(json))
	if len(rates) != 1 || rates[0].Base != "USD" || rates[0].Rate != 6.78 {
		t.Fatalf("got %+v, want only USD/CNY", rates)
	}
}

func TestToIntMatchesRubyToI(t *testing.T) {
	tests := []struct {
		in   any
		want int
	}{
		{float64(3), 3}, {"2", 2}, {" 2", 2}, {"+2", 2}, {"-2", -2}, {"2.5", 2}, {"3abc", 3}, {"", 0}, {"abc", 0}, {nil, 0},
	}
	for _, tt := range tests {
		if got := toInt(tt.in); got != tt.want {
			t.Errorf("toInt(%#v) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
