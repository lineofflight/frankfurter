package bbk

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

const header = "DATAFLOW;BBK_STD_FREQ;BBK_STD_CURRENCY;BBK_ERX_PARTNER_CURRENCY;BBK_ERX_SERIES_TYPE;BBK_ERX_RATE_TYPE;BBK_ERX_SUFFIX;TIME_PERIOD;OBS_VALUE;TIME_FORMAT;BBK_DECIMALS;BBK_ID;BBK_UNIT;BBK_UNIT_MULT;BBK_TITLE;WEB_CATEGORY;BBK_COMM_GEN;BBK_DIFF;OBS_STATUS\n"

func newAdapter(t *testing.T) *Adapter {
	return New(vcrtest.Client(t, "bbk", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
}

func mustParse(t *testing.T, rows ...string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(header + strings.Join(rows, "\n") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func find(rates []adapter.Rate, base string) *adapter.Rate {
	for i := range rates {
		if rates[i].Base == base {
			return &rates[i]
		}
	}
	return nil
}

func TestParseForeignBaseDEMQuote(t *testing.T) {
	rates := mustParse(t,
		"BBK:BBEX3(1.0);D;USD;DEM;AA;AC;000;1998-12-30;1.6730;P1D;4;BBEX3.D.USD.DEM.AA.AC.000;DEM;0;Devisenkurse der Frankfurter Börse / 1 USD = ... DEM / Vereinigte Staaten;WEDE;;0.0;")

	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(1998, 12, 30), Base: "USD", Quote: "DEM", Rate: 1.6730}
	if rates[0] != want {
		t.Errorf("rate = %+v, want %+v", rates[0], want)
	}
}

func TestParseScalesByMultiplier(t *testing.T) {
	rates := mustParse(t,
		"BBK:BBEX3(1.0);D;ATS;DEM;AA;AC;000;1998-12-30;14.214;P1D;3;BBEX3.D.ATS.DEM.AA.AC.000;DEM;0;Devisenkurse der Frankfurter Börse / 100 ATS = ... DEM / Österreich;WEDE;;0.0;",
		"BBK:BBEX3(1.0);D;ITL;DEM;AA;AC;000;1998-12-30;1.0100;P1D;4;BBEX3.D.ITL.DEM.AA.AC.000;DEM;0;Devisenkurse der Frankfurter Börse / 1 000 ITL = ... DEM / Italien;WEDE;;0.0;")

	ats, itl := find(rates, "ATS"), find(rates, "ITL")
	if ats == nil || itl == nil {
		t.Fatalf("missing ATS or ITL in %+v", rates)
	}
	if math.Abs(ats.Rate-0.14214) > 0.00001 {
		t.Errorf("ATS rate = %v, want ~0.14214", ats.Rate)
	}
	if math.Abs(itl.Rate-0.00101) > 0.000001 {
		t.Errorf("ITL rate = %v, want ~0.00101", itl.Rate)
	}
}

func TestParseSkipsNonDaily(t *testing.T) {
	rates := mustParse(t,
		"BBK:BBEX3(1.0);M;USD;DEM;AA;AC;A02;1998-12;1.6700;P1M;4;BBEX3.M.USD.DEM.AA.AC.A02;DEM;0;Devisenkurse der Frankfurter Börse / 1 USD = ... DEM / Vereinigte Staaten;WEDE;;0.0;")
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseSkipsMissingObsValue(t *testing.T) {
	rates := mustParse(t,
		"BBK:BBEX3(1.0);D;USD;DEM;AA;AC;000;1998-12-24;.;P1D;4;BBEX3.D.USD.DEM.AA.AC.000;DEM;0;Devisenkurse der Frankfurter Börse / 1 USD = ... DEM / Vereinigte Staaten;WEDE;;;K")
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestFetchHistoricalRange(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(1998, 12, 21), adapter.Date(1998, 12, 30))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Quote != "DEM" {
			t.Fatalf("quote = %q, want DEM", r.Quote)
		}
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(1998, 12, 21), adapter.Date(1998, 12, 30))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	first, n := rates[0].Date, 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 5 {
		t.Errorf("got %d rates on %s, want more than 5", n, first.Format("2006-01-02"))
	}
}

func TestFetchUSDPlausibleLate1998(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(1998, 12, 29), adapter.Date(1998, 12, 30))
	if err != nil {
		t.Fatal(err)
	}
	var usd *adapter.Rate
	for i, r := range rates {
		if r.Base == "USD" && r.Quote == "DEM" && r.Date.Equal(adapter.Date(1998, 12, 30)) {
			usd = &rates[i]
			break
		}
	}
	if usd == nil {
		t.Fatal("no USD/DEM rate on 1998-12-30")
	}
	if math.Abs(usd.Rate-1.67) > 0.1 {
		t.Errorf("USD rate = %v, want ~1.67", usd.Rate)
	}
}

func TestParseRejectsTitleMultiplierMismatch(t *testing.T) {
	_, err := parse([]byte(header +
		"BBK:BBEX3(1.0);D;USD;DEM;AA;AC;000;1998-12-30;1.6730;P1D;4;BBEX3.D.USD.DEM.AA.AC.000;DEM;0;Devisenkurse der Frankfurter Börse / 100 USD = ... DEM / Vereinigte Staaten;WEDE;;0.0;\n"))
	if err == nil {
		t.Error("want an error when the title contradicts the multiplier table")
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto [3]int
	}{
		{"testdata/golden/fetch.json", [3]int{1998, 12, 21}, [3]int{1998, 12, 30}},
		{"testdata/golden/late.json", [3]int{1998, 12, 29}, [3]int{1998, 12, 30}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, tc.file)
			a := New(g.Client(t))
			after := adapter.Date(tc.after[0], time.Month(tc.after[1]), tc.after[2])
			upto := adapter.Date(tc.upto[0], time.Month(tc.upto[1]), tc.upto[2])
			rates, err := a.Fetch(context.Background(), after, upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
