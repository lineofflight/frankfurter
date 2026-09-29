package bccr

import (
	"context"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

// The Ruby spec allows playback repeats, but go-vcr's replayable mode returns the first match even when it has
// been used, so the token interaction would also answer the data request (both match on method and host). Each
// interaction plays exactly once here, which is what VCR does in practice.
func newAdapter(t *testing.T) *Adapter {
	return New(vcrtest.Client(t, "bccr", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
}

func TestFetchesRates(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 3, 16), adapter.Date(2026, 3, 24))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestParseCorrectStructure(t *testing.T) {
	rates, err := parse([]byte(`{
		"columnas": [
			{"field": "nombre", "tituloIngles": "Indicators"},
			{"field": "serie1", "tituloIngles": "20 mar 2026"},
			{"field": "serie2", "tituloIngles": "21 mar 2026"}
		],
		"indicadoresRaiz": [
			{"idIndicador": 317, "nombreIngles": "Buy", "series": {"serie1Ingles": "463.24", "serie2Ingles": "463.50"}},
			{"idIndicador": 318, "nombreIngles": "Sell", "series": {"serie1Ingles": "469.49", "serie2Ingles": "468.29"}}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 20), Base: "USD", Quote: "CRC", Rate: 469.49}
	if rates[0] != want {
		t.Errorf("first rate = %+v, want %+v", rates[0], want)
	}
}

func TestParseSkipsEmptySeriesValues(t *testing.T) {
	rates, err := parse([]byte(`{
		"columnas": [
			{"field": "nombre", "tituloIngles": "Indicators"},
			{"field": "serie1", "tituloIngles": "20 mar 2026"},
			{"field": "serie2", "tituloIngles": "21 mar 2026"}
		],
		"indicadoresRaiz": [
			{"idIndicador": 318, "nombreIngles": "Sell", "series": {"serie1Ingles": "469.49", "serie2Ingles": ""}}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 16), adapter.Date(2026, 3, 24))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
