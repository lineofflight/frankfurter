package boja

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func newAdapter(t *testing.T) *Adapter {
	return New(vcrtest.Client(t, "boja", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host), vcrtest.AllowPlaybackRepeats))
}

func mustParse(t *testing.T, data string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	return rates
}

func TestFetch(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 3, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestParseStoresForeignBaseAndJMDQuote(t *testing.T) {
	r := mustParse(t, `{"data": [["20 Mar 2026", "U.S. DOLLAR", "155.0000", "150.0000", "", "156.0000"]]}`)[0]
	if r.Base != "USD" || r.Quote != "JMD" || r.Rate != 152.5 {
		t.Errorf("got %+v, want USD/JMD 152.5", r)
	}
}

func TestParseComputesMidFromSellAndBuy(t *testing.T) {
	r := mustParse(t, `{"data": [["20 Mar 2026", "EURO", "186.2883", "176.9700", "156.9700", "191.1503"]]}`)[0]
	if r.Base != "EUR" || r.Quote != "JMD" {
		t.Errorf("got %s/%s, want EUR/JMD", r.Base, r.Quote)
	}
	if math.Abs(r.Rate-181.6292) > 0.001 {
		t.Errorf("rate = %v, want about 181.6292", r.Rate)
	}
}

func TestParseSkipsUnmappedCurrencies(t *testing.T) {
	rates := mustParse(t, `{"data": [
		["20 Mar 2026", "UNKNOWN CURRENCY", "1.0000", "0.9000", "", "1.1000"],
		["20 Mar 2026", "U.S. DOLLAR", "155.0000", "150.0000", "", "156.0000"]
	]}`)
	if len(rates) != 1 || rates[0].Base != "USD" {
		t.Errorf("got %+v, want one USD row", rates)
	}
}

func TestParseHandlesEmptyBuyRate(t *testing.T) {
	r := mustParse(t, `{"data": [["20 Mar 2026", "BELIZE DOLLAR", "74.9819", "67.4800", "", "77.1539"]]}`)[0]
	if r.Base != "BZD" {
		t.Errorf("base = %s, want BZD", r.Base)
	}
	if math.Abs(r.Rate-71.2310) > 0.001 {
		t.Errorf("rate = %v, want about 71.2310", r.Rate)
	}
}

func TestParseRejectsMissingData(t *testing.T) {
	if _, err := parse([]byte(`{"error": true}`)); err == nil {
		t.Error("want an error when the data array is missing")
	}
}

func TestParseZeroBuyUsesSell(t *testing.T) {
	r := mustParse(t, `{"data": [["20 Mar 2026", "EURO", "186.2883", "0.0000"]]}`)[0]
	if r.Rate != 186.2883 || r.Bid == nil || *r.Bid != 0 {
		t.Errorf("got %+v, want rate 186.2883 with zero bid", r)
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"testdata/golden/fetch.json", adapter.Date(2026, 3, 1), time.Time{}},
		{"testdata/golden/inclusive.json", adapter.Date(2026, 3, 19), adapter.Date(2026, 3, 19)},
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
