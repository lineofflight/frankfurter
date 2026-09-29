package bsp

import (
	"context"
	"math"
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
	a := New(vcrtest.Client(t, "bsp", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchOnlyReferenceRateAsUSDOverPHP(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 27), adapter.Date(2026, 5, 29))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Base != "USD" || r.Quote != "PHP" {
			t.Errorf("pair %s/%s, want USD/PHP", r.Base, r.Quote)
		}
	}
}

func TestFetchEmitsReferenceRateNotReutersEquivalent(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 29), adapter.Date(2026, 5, 29))
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == "USD" })
	if i < 0 {
		t.Fatal("no USD rate")
	}
	// BSP Reference Rate is 61.600; the USD row's peso equivalent is 61.6540.
	if got := rates[i].Rate; math.Abs(got-61.600) > 0.001 {
		t.Errorf("USD = %v, want 61.600", got)
	}
}

func TestFetchFiltersByDateRange(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 27), adapter.Date(2026, 5, 29))
	for _, r := range rates {
		if r.Date.Before(adapter.Date(2026, 5, 27)) || r.Date.After(adapter.Date(2026, 5, 29)) {
			t.Errorf("date %s outside 2026-05-27..2026-05-29", r.Date.Format(time.DateOnly))
		}
	}
}

const bulletin = `   1 UNITED STATES                       DOLLAR             USD             0.858222     1.000000      61.6540
   2 JAPAN                               YEN                JPY             0.005390     0.006280       0.3872
  16 EUROPEAN MONETARY UNION             EURO               EUR             1.000000     1.165200      71.8392
     BSP Buying Rate (T/T)PHP            61.350      GOLD BUYING:   $      4,495.00
     BSP Selling Rate (T/TPHP            61.850      SILVER BUYING: $         75.80
     BSP Reference Rate:  PHP            61.600
     SDR Rate:            $               1.36668    /SDR
`

func parseBulletin(t *testing.T) []adapter.Rate {
	t.Helper()
	rates, err := parseText(bulletin, adapter.Date(2026, 5, 29))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func bases(rates []adapter.Rate) []string {
	var bs []string
	for _, r := range rates {
		bs = append(bs, r.Base)
	}
	return bs
}

func TestParseTextEmitsOnlyReferenceRate(t *testing.T) {
	want := []adapter.Rate{{Date: adapter.Date(2026, 5, 29), Base: "USD", Quote: "PHP", Rate: 61.600}}
	got := parseBulletin(t)
	if !slices.EqualFunc(got, want, func(x, y adapter.Rate) bool {
		return x.Date.Equal(y.Date) && x.Base == y.Base && x.Quote == y.Quote && x.Rate == y.Rate &&
			x.Bid == nil && x.Ask == nil && x.Mid == nil
	}) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseTextSkipsLSEGTable(t *testing.T) {
	bs := bases(parseBulletin(t))
	for _, code := range []string{"JPY", "EUR"} {
		if slices.Contains(bs, code) {
			t.Errorf("relayed %s", code)
		}
	}
}

func TestParseTextSkipsSDRAndMetals(t *testing.T) {
	bs := bases(parseBulletin(t))
	for _, code := range []string{"XDR", "XAU", "XAG"} {
		if slices.Contains(bs, code) {
			t.Errorf("relayed %s", code)
		}
	}
}

func TestParseTextSkipsUSDRowReutersEquivalent(t *testing.T) {
	for _, r := range parseBulletin(t) {
		if r.Rate == 61.6540 {
			t.Errorf("relayed the USD row's peso equivalent")
		}
	}
}

func TestParseTextImageOnlyScan(t *testing.T) {
	rates, err := parseText("  \n ", adapter.Date(2016, 5, 29))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseTextMissingReferenceRate(t *testing.T) {
	_, err := parseText("no reference rate in this text", adapter.Date(2026, 5, 29))
	if err == nil || !strings.Contains(err.Error(), "Reference Rate line missing") {
		t.Errorf("err = %v, want Reference Rate line missing", err)
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"testdata/golden/fetch.json", adapter.Date(2026, 5, 27), adapter.Date(2026, 5, 29)},
		{"testdata/golden/single_day.json", adapter.Date(2026, 5, 29), adapter.Date(2026, 5, 29)},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, tc.file)
			rates, err := New(g.Client(t)).Fetch(context.Background(), tc.after, tc.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
