package boi

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

const header = "SERIES_CODE,FREQ,BASE_CURRENCY,COUNTER_CURRENCY,UNIT_MEASURE,DATA_TYPE,DATA_SOURCE,TIME_COLLECT,CONF_STATUS,PUB_WEBSITE,UNIT_MULT,COMMENTS,TIME_PERIOD,OBS_VALUE,RELEASE_STATUS\n"

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "boi", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 20))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, row string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(header + row + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	return rates
}

func TestFetchWithDateRange(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	first := rates[0].Date
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 1 {
		t.Errorf("got %d rates on %s, want more than 1", n, first.Format(time.DateOnly))
	}
}

func TestParseBaseAndQuote(t *testing.T) {
	rates := mustParse(t, "RER_USD_ILS,D,USD,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2026-03-02,3.073,YP")
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 2), Base: "USD", Quote: "ILS", Rate: 3.073}
	if rates[0] != want {
		t.Errorf("got %+v, want %+v", rates[0], want)
	}
}

func TestParseUnitMult(t *testing.T) {
	tests := []struct {
		name        string
		row         string
		want, delta float64
	}{
		{"adjusts rate by UNIT_MULT", "RER_JPY_ILS,D,JPY,ILS,ILS,OF00,BOI_MRKT,V,F,Y,2,,2026-03-02,1.971,YP", 0.01971, 0.00001},
		{"handles LBP with UNIT_MULT 1", "RER_LBP_ILS,D,LBP,ILS,ILS,OF00,BOI_MRKT,V,F,Y,1,,2026-03-02,0.0003,YP", 0.00003, 0.000001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustParse(t, tt.row)[0].Rate
			if math.Abs(got-tt.want) > tt.delta {
				t.Errorf("rate = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 20))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

func TestParseSkipsIncompleteAndZeroRows(t *testing.T) {
	rows := []string{
		"RER_USD_ILS,D,,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2026-03-02,3.073,YP", // no base
		"RER_USD_ILS,D,USD,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,,3.073,YP",        // no date
		"RER_USD_ILS,D,USD,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2026-03-02,,YP",   // no value
		"RER_USD_ILS,D,USD,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2026-03-02,0,YP",  // zero
		"RER_EUR_ILS,D,EUR,ILS,ILS,OF00,BOI_MRKT,V,F,Y,,,2026-03-02,3.5,YP", // blank UNIT_MULT leaves rate as is
	}
	rates, err := parse([]byte(header + strings.Join(rows, "\n") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{{Date: adapter.Date(2026, 3, 2), Base: "EUR", Quote: "ILS", Rate: 3.5}}
	if !slices.Equal(rates, want) {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseRejectsBadNumbers(t *testing.T) {
	for _, row := range []string{
		"RER_USD_ILS,D,USD,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2026-03-02,n/a,YP",
		"RER_USD_ILS,D,USD,ILS,ILS,OF00,BOI_MRKT,V,F,Y,x,,2026-03-02,3.073,YP",
	} {
		if _, err := parse([]byte(header + row + "\n")); err == nil {
			t.Errorf("parse(%q) returned no error", row)
		}
	}
}

func TestParseSkipsCurrencyBasketAndOtherNonCurrencySeries(t *testing.T) {
	rates, err := parse([]byte(header +
		"RER_CBK_ILS,D,CBK_L,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2000-01-03,4.387,YP\n" +
		"RER_USD_ILS,D,USD,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2000-01-03,4.124,YP\n"))
	if err != nil {
		t.Fatal(err)
	}
	var bases []string
	for _, r := range rates {
		bases = append(bases, r.Base)
	}
	if !slices.Equal(bases, []string{"USD"}) {
		t.Errorf("bases = %v, want [USD]", bases)
	}
}

func TestParseNormalizesPreEuroSeriesPublishedPer10100Or1000Units(t *testing.T) {
	rates, err := parse([]byte(header +
		"RER_ATS_ILS,D,ATS,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2000-01-03,3.0234,YP\n" +
		"RER_BEL_ILS,D,BEL,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2000-01-03,1.0313,YP\n" +
		"RER_ESP_ILS,D,ESP,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2000-01-03,2.5004,YP\n" +
		"RER_ITL_ILS,D,ITL,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2000-01-03,2.1486,YP\n" +
		"RER_DEM_ILS,D,DEM,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2000-01-03,2.1271,YP\n"))
	if err != nil {
		t.Fatal(err)
	}
	var bases []string
	records := map[string]float64{}
	for _, r := range rates {
		bases = append(bases, r.Base)
		records[r.Base] = r.Rate
	}
	if want := []string{"ATS", "BEF", "ESP", "ITL", "DEM"}; !slices.Equal(bases, want) {
		t.Fatalf("bases = %v, want %v", bases, want)
	}
	for code, want := range map[string]float64{"ATS": 0.30234, "BEF": 0.10313, "ESP": 0.025004, "ITL": 0.0021486} {
		if math.Abs(records[code]-want) > 1e-12 {
			t.Errorf("%s = %v, want %v", code, records[code], want)
		}
	}
	if records["DEM"] != 2.1271 {
		t.Errorf("DEM = %v, want 2.1271", records["DEM"])
	}
}
