package rba

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func newAdapter(t *testing.T) *Adapter {
	return New(vcrtest.Client(t, "rba", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
}

func hasQuote(rates []adapter.Rate, quote string) bool {
	return slices.ContainsFunc(rates, func(r adapter.Rate) bool { return r.Quote == quote })
}

func TestFetch(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2025, 1, 1), adapter.Date(2026, 3, 20))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if !hasQuote(rates, "FXRTWI") {
		t.Error("want FXRTWI rows")
	}
	if !hasQuote(rates, "XDR") {
		t.Error("want XDR rows")
	}
	if hasQuote(rates, "SDR") {
		t.Error("want no SDR rows")
	}
}

func TestParseNormalizesSDRToXDR(t *testing.T) {
	rates, err := parse([]byte(`F11.1  EXCHANGE RATES
Title,A$1=USD,A$1=SDR
Description,AUD/USD Exchange Rate,AUD/SDR Exchange Rate
Frequency,Daily,Daily
Type,Indicative,Indicative
Units,USD,SDR


Source,WM/Reuters,IMF
Publication date,20-Mar-2026,20-Mar-2026
Series ID,FXRUSD,FXRSDR
03-Jan-2023,0.6828,0.5131
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{
		{Date: adapter.Date(2023, 1, 3), Base: "AUD", Quote: "USD", Rate: 0.6828},
		{Date: adapter.Date(2023, 1, 3), Base: "AUD", Quote: "XDR", Rate: 0.5131},
	}
	if !reflect.DeepEqual(rates, want) {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParsePreservesQuotesAndIdentifiesTradeWeightedIndex(t *testing.T) {
	rates, err := parse([]byte(`F11.1  EXCHANGE RATES
Title,A$1=USD,Trade-weighted Index May 1970 = 100,A$1=JPY
Description,AUD/USD Exchange Rate,Australian Dollar Trade-weighted Index,AUD/JPY Exchange Rate
Frequency,Daily,Daily,Daily
Type,Indicative,Indicative,Indicative
Units,USD,Index,JPY


Source,WM/Reuters,RBA,RBA
Publication date,20-Mar-2026,20-Mar-2026,20-Mar-2026
Series ID,FXRUSD,FXRTWI,FXRJY
03-Jan-2023,0.6828,61.40,88.48
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 3 {
		t.Fatalf("got %d rates, want 3", len(rates))
	}
	if first := rates[0]; first.Base != "AUD" || first.Quote != "USD" || first.Rate != 0.6828 {
		t.Errorf("first = %+v", first)
	}
	want := adapter.Rate{Date: adapter.Date(2023, 1, 3), Base: "AUD", Quote: "FXRTWI", Rate: 61.4}
	if rates[1] != want {
		t.Errorf("second = %+v, want %+v", rates[1], want)
	}
	if last := rates[2]; last.Quote != "JPY" || last.Rate != 88.48 {
		t.Errorf("last = %+v", last)
	}
}

const header = "F11.1  EXCHANGE RATES\nTitle,A$1=USD,A$1=JPY\nDescription,a,b\nFrequency,Daily,Daily\n" +
	"Type,Indicative,Indicative\nUnits,USD,JPY\n\n\nSource,a,b\nPublication date,20-Mar-2026,20-Mar-2026\n" +
	"Series ID,FXRUSD,FXRJY\n"

func TestParseRejectsMalformedLine(t *testing.T) {
	// Ruby's CSV.parse_line raises on a malformed data line rather than
	// skipping it.
	for _, line := range []string{`03-Jan-2023,0"6828,88.48`, `03-Jan-2023,"0.6828,88.48`} {
		if _, err := parse([]byte(header + line + "\n")); err == nil {
			t.Errorf("%s: want error", line)
		}
	}
}

func TestParseSkipsBlankValuesAndNonDateRows(t *testing.T) {
	rates, err := parse([]byte(header + "03-Jan-2023, ,88.48\n\nNotes,x\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{{Date: adapter.Date(2023, 1, 3), Base: "AUD", Quote: "JPY", Rate: 88.48}}
	if !reflect.DeepEqual(rates, want) {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseRequiresHeaderRows(t *testing.T) {
	if _, err := parse([]byte("Series ID,FXRUSD\n")); err == nil {
		t.Error("want error without a Units row")
	}
	if _, err := parse([]byte("Units,USD\n")); err == nil {
		t.Error("want error without a Series ID row")
	}
	if _, err := parse([]byte(header + "03-Jan-2023,abc,88.48\n")); err == nil {
		t.Error("want error for a non-numeric rate")
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2025, 1, 1), adapter.Date(2026, 3, 20))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
