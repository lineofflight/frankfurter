package boz

import (
	"bytes"
	"context"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	client := vcrtest.Client(t, "boz", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path))
	rates, err := New(client).Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func uniq[T comparable](rates []adapter.Rate, f func(adapter.Rate) T) []T {
	var out []T
	for _, r := range rates {
		if v := f(r); !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func TestFetchesRatesFromWorkbook(t *testing.T) {
	if len(fetch(t, adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 8))) == 0 {
		t.Fatal("no rates")
	}
}

func TestEmitsZMWAsQuote(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 8))
	got := uniq(rates, func(r adapter.Rate) string { return r.Quote })
	if !slices.Equal(got, []string{"ZMW"}) {
		t.Errorf("quotes = %v, want [ZMW]", got)
	}
}

func TestCoversFourCurrencies(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 8))
	got := uniq(rates, func(r adapter.Rate) string { return r.Base })
	slices.Sort(got)
	if want := []string{"EUR", "GBP", "USD", "ZAR"}; !slices.Equal(got, want) {
		t.Errorf("bases = %v, want %v", got, want)
	}
}

func TestRespectsAfterAndUpto(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 3), adapter.Date(2026, 9, 4))
	got := uniq(rates, func(r adapter.Rate) string { return r.Date.Format(time.DateOnly) })
	slices.Sort(got)
	if want := []string{"2026-09-03", "2026-09-04"}; !slices.Equal(got, want) {
		t.Errorf("dates = %v, want %v", got, want)
	}
}

func TestUSDMidpoint(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 8), adapter.Date(2026, 9, 8))
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == "USD" })
	if i < 0 {
		t.Fatal("no USD rate")
	}
	// must_be_within_epsilon is relative.
	if got := rates[i].Rate; math.Abs(got-19.2291) > 1e-6*19.2291 {
		t.Errorf("USD rate = %v, want 19.2291", got)
	}
}

func TestLabelsRowsBeforeRebasingAsOldKwacha(t *testing.T) {
	rates := fetch(t, adapter.Date(2012, 12, 28), adapter.Date(2013, 1, 2))
	byDate := map[time.Time]adapter.Rate{}
	for _, r := range rates {
		if r.Base == "USD" {
			byDate[r.Date] = r
		}
	}
	old, ok := byDate[adapter.Date(2012, 12, 28)]
	if !ok {
		t.Fatal("no USD rate on 2012-12-28")
	}
	if old.Quote != "ZMK" || old.Rate <= 5000 {
		t.Errorf("2012-12-28 = %s %v, want ZMK > 5000", old.Quote, old.Rate)
	}
	rebased, ok := byDate[adapter.Date(2013, 1, 2)]
	if !ok {
		t.Fatal("no USD rate on 2013-01-02")
	}
	if rebased.Quote != "ZMW" || rebased.Rate >= 6 {
		t.Errorf("2013-01-02 = %s %v, want ZMW < 6", rebased.Quote, rebased.Rate)
	}
}

func TestSkipsHiddenPlaceholderRows(t *testing.T) {
	rates := fetch(t, adapter.Date(2006, 1, 1), adapter.Date(2006, 1, 12))
	got := uniq(rates, func(r adapter.Rate) time.Time { return r.Date })
	if len(got) != 1 || !got[0].Equal(adapter.Date(2006, 1, 12)) {
		t.Errorf("dates = %v, want [2006-01-12]", got)
	}
}

func TestWorkbookURLResolvesAttachedFile(t *testing.T) {
	got, err := workbookURL([]byte(`{
		"data": [{"id": "n1", "relationships": {"field_average_historical_file": {"data": {"id": "f1"}}}}],
		"included": [{"id": "f1", "attributes": {"uri": {"url": "/sites/default/files/2026-09/AVERAGE_FXRATES_3.xlsx"}}}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://www.boz.zm/sites/default/files/2026-09/AVERAGE_FXRATES_3.xlsx"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWorkbookURLErrors(t *testing.T) {
	for name, json := range map[string]string{
		"empty listing":    `{"data": []}`,
		"no file attached": `{"data": [{"id": "n1", "relationships": {}}], "included": []}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := workbookURL([]byte(json)); err == nil {
				t.Error("want an error")
			}
		})
	}
}

func TestSkipsSharedStringNumbersAcrossRowGaps(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	sh := f.GetSheetName(0)
	for ref, v := range map[string]any{
		"C3": "Dollar", "E3": "Pound",
		"B4": "Date", "C4": "Buy", "D4": "Sale", "E4": "Buy", "F4": "Sale",
		// Row 5 is absent; rows 6 and 9 hold data.
		"B6": 46000, "C6": 19.1, "D6": 19.3, "E6": "25.1", "F6": 25.3,
		"B9": 46001, "C9": "19.2", "D9": 19.4, "E9": 25.2, "F9": 25.4,
	} {
		if err := f.SetCellValue(sh, ref, v); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	rates, err := parse(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rates {
		got = append(got, r.Date.Format(time.DateOnly)+" "+r.Base)
	}
	if want := []string{"2025-12-09 USD", "2025-12-10 GBP"}; !slices.Equal(got, want) {
		t.Errorf("rates = %v, want %v", got, want)
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"recent", adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 8)},
		{"rebasing", adapter.Date(2012, 12, 28), adapter.Date(2013, 1, 2)},
		{"all", time.Time{}, time.Time{}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, "testdata/golden/"+tc.file+".json")
			rates, err := New(g.Client(t)).Fetch(context.Background(), tc.after, tc.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
