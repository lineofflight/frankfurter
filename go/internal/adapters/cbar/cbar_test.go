package cbar

import (
	"context"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "cbar", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func valute(code string, nominal any, value string) string {
	return fmt.Sprintf(`<Valute Code="%s"><Nominal>%v</Nominal><Name>%s</Name><Value>%s</Value></Valute>`, code, nominal, code, value)
}

func bulletin(date, body string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<ValCurs Date="` + date + `" Name="AZN məzənnələri">
  <ValType Type="Bank metalları">
    ` + valute("XAU", "1 t.u.", "7541.608") + `
    ` + valute("XPD", "1 t.u.", "") + `
  </ValType>
  <ValType Type="Xarici valyutalar">
    ` + body + `
  </ValType>
</ValCurs>
`)
}

func mustParse(t *testing.T, data []byte) []adapter.Rate {
	t.Helper()
	rates, err := parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func bases(rates []adapter.Rate) []string {
	var out []string
	for _, r := range rates {
		out = append(out, r.Base)
	}
	return out
}

func uniqQuotes(rates []adapter.Rate) []string {
	var out []string
	for _, r := range rates {
		if !slices.Contains(out, r.Quote) {
			out = append(out, r.Quote)
		}
	}
	return out
}

func find(rates []adapter.Rate, base string) adapter.Rate {
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == base })
	if i < 0 {
		return adapter.Rate{}
	}
	return rates[i]
}

func TestFetchWithDateRange(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 4), adapter.Date(2026, 9, 8))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if got := uniqQuotes(rates); !slices.Equal(got, []string{"AZN"}) {
		t.Errorf("quotes = %v, want [AZN]", got)
	}
}

func TestFetchDedupesWeekendFilesOnBulletinDate(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 4), adapter.Date(2026, 9, 8))
	var dates []time.Time
	for _, r := range rates {
		if !slices.ContainsFunc(dates, r.Date.Equal) {
			dates = append(dates, r.Date)
		}
	}
	want := []time.Time{adapter.Date(2026, 9, 4), adapter.Date(2026, 9, 7), adapter.Date(2026, 9, 8)}
	if !slices.EqualFunc(dates, want, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", dates, want)
	}
}

func TestFetchStartsAtFirstFile(t *testing.T) {
	// A full backfill opens the day before coverage starts. URLs before the
	// 26.11.1993 file redirect.
	rates := fetch(t, adapter.Date(1993, 11, 24), adapter.Date(1993, 11, 26))
	var dates []time.Time
	for _, r := range rates {
		if !slices.ContainsFunc(dates, r.Date.Equal) {
			dates = append(dates, r.Date)
		}
	}
	if want := []time.Time{adapter.Date(1993, 11, 25)}; !slices.EqualFunc(dates, want, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", dates, want)
	}
}

func TestFetchCurrenciesAndMetalsPerDate(t *testing.T) {
	got := bases(fetch(t, adapter.Date(2026, 9, 8), adapter.Date(2026, 9, 8)))
	if len(got) <= 30 {
		t.Errorf("got %d bases, want more than 30", len(got))
	}
	for _, b := range []string{"USD", "XAU", "XDR"} {
		if !slices.Contains(got, b) {
			t.Errorf("missing %s", b)
		}
	}
}

func TestParseCurrenciesWithCorrectBaseAndQuote(t *testing.T) {
	usd := find(mustParse(t, bulletin("08.09.2026", valute("USD", 1, "1.7"))), "USD")
	if !usd.Date.Equal(adapter.Date(2026, 9, 8)) {
		t.Errorf("date = %v", usd.Date)
	}
	if usd.Quote != "AZN" {
		t.Errorf("quote = %q", usd.Quote)
	}
	if usd.Rate != 1.7 {
		t.Errorf("rate = %v, want 1.7", usd.Rate)
	}
}

func TestParseNormalizesRateByNominal(t *testing.T) {
	jpy := find(mustParse(t, bulletin("08.09.2026", valute("JPY", 100, "1.0608"))), "JPY")
	if math.Abs(jpy.Rate-0.010608) > 1e-9 {
		t.Errorf("rate = %v, want 0.010608", jpy.Rate)
	}
}

func TestParseQuotesMetalsPerTroyOunceAndSkipsEmptyValues(t *testing.T) {
	rates := mustParse(t, bulletin("08.09.2026", ""))
	if got := bases(rates); !slices.Equal(got, []string{"XAU"}) {
		t.Fatalf("bases = %v, want [XAU]", got)
	}
	if rates[0].Rate != 7541.608 {
		t.Errorf("rate = %v, want 7541.608", rates[0].Rate)
	}
}

func TestParseMapsSDRToXDR(t *testing.T) {
	rates := mustParse(t, bulletin("08.09.2026", valute("SDR", 1, "2.3335")))
	if !slices.Contains(bases(rates), "XDR") {
		t.Errorf("bases = %v, want XDR", bases(rates))
	}
}

func TestParseStoresPre2006BulletinsAsOldManat(t *testing.T) {
	rates := mustParse(t, bulletin("29.12.2005", valute("USD", 1, "4593")))
	if got := uniqQuotes(rates); !slices.Equal(got, []string{"AZM"}) {
		t.Errorf("quotes = %v, want [AZM]", got)
	}
}

func TestParseRestoresPredecessorCodesBeforeRedenomination(t *testing.T) {
	rows := valute("RUB", 1, "0.65") + "\n" + valute("TRY", 1, "19") + "\n" + valute("USD", 1, "3888")
	rates := mustParse(t, bulletin("30.12.1997", rows))
	if got := bases(rates); !slices.Equal(got, []string{"XAU", "RUR", "TRL", "USD"}) {
		t.Errorf("bases = %v", got)
	}
	if got := find(rates, "RUR").Rate; got != 0.65 {
		t.Errorf("RUR = %v, want 0.65", got)
	}
	if got := find(rates, "TRL").Rate; got != 0.019 {
		t.Errorf("TRL = %v, want 0.019", got)
	}
}

func TestParseKeepsCurrentCodesFromCutoverDate(t *testing.T) {
	rows := valute("BYN", 1, "0.7674") + "\n" + valute("TRY", 1, "0.5352")
	rates := mustParse(t, bulletin("01.07.2016", rows))
	if got := bases(rates); !slices.Equal(got, []string{"XAU", "BYN", "TRY"}) {
		t.Errorf("bases = %v", got)
	}
	if got := uniqQuotes(rates); !slices.Equal(got, []string{"AZN"}) {
		t.Errorf("quotes = %v, want [AZN]", got)
	}
}

func TestParseRaisesOnUnexpectedDocument(t *testing.T) {
	if _, err := parse([]byte("<html><body>moved</body></html>")); err == nil {
		t.Error("want error")
	}
}

func TestParseSkipsRowsWithoutNominal(t *testing.T) {
	rates := mustParse(t, bulletin("08.09.2026", valute("USD", "n/a", "1.7")))
	if got := bases(rates); !slices.Equal(got, []string{"XAU"}) {
		t.Errorf("bases = %v, want [XAU]", got)
	}
}

func TestFetchNeedsStartDate(t *testing.T) {
	if _, err := New(nil).Fetch(context.Background(), time.Time{}, adapter.Date(2026, 9, 8)); err == nil {
		t.Error("want error")
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"testdata/golden/fetch.json", adapter.Date(2026, 9, 4), adapter.Date(2026, 9, 8)},
		{"testdata/golden/single_day.json", adapter.Date(2026, 9, 8), adapter.Date(2026, 9, 8)},
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
