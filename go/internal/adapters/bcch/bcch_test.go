package bcch

import (
	"context"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	vcrtest.SetSecrets(t)
	a := New(vcrtest.Client(t, "bcch", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 15))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchSinceDate(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
	first := rates[0].Date
	var sample []adapter.Rate
	for _, r := range rates {
		if r.Date.Equal(first) {
			sample = append(sample, r)
		}
	}
	if len(sample) <= 1 {
		t.Errorf("got %d rates on %s, want more than 1", len(sample), first.Format(time.DateOnly))
	}
}

func TestParseSeriesResponse(t *testing.T) {
	rates, err := parse([]byte(`{"Codigo":0,"Descripcion":"Success","Series":{"Obs":[
		{"indexDateString":"10-03-2026","value":"916.36","statusCode":"OK"},
		{"indexDateString":"11-03-2026","value":"893.69","statusCode":"OK"}]}}`), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	if rates[0].Base != "USD" || rates[0].Quote != "CLP" {
		t.Errorf("got %s/%s, want USD/CLP", rates[0].Base, rates[0].Quote)
	}
	if math.Abs(rates[0].Rate-916.36) > 0.001 {
		t.Errorf("rate = %v, want 916.36", rates[0].Rate)
	}
}

func TestParseSkipsNaN(t *testing.T) {
	rates, err := parse([]byte(`{"Codigo":0,"Series":{"Obs":[
		{"indexDateString":"07-03-2026","value":"NaN","statusCode":"ND"}]}}`), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseRestoresCruzeiroReal(t *testing.T) {
	rates, err := parse([]byte(`{"Codigo":0,"Series":{"Obs":[
		{"indexDateString":"30-06-1994","value":"0.16","statusCode":"OK"},
		{"indexDateString":"01-07-1994","value":"418.34","statusCode":"OK"}]}}`), "BRL")
	if err != nil {
		t.Fatal(err)
	}
	var bases []string
	for _, r := range rates {
		bases = append(bases, r.Base)
	}
	if want := []string{"BRR", "BRL"}; !slices.Equal(bases, want) {
		t.Errorf("bases = %v, want %v", bases, want)
	}
}

func TestParseCommaFormattedNumbers(t *testing.T) {
	rates, err := parse([]byte(`{"Codigo":0,"Series":{"Obs":[
		{"indexDateString":"10-03-2026","value":"1,916.36","statusCode":"OK"}]}}`), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || math.Abs(rates[0].Rate-1916.36) > 0.001 {
		t.Errorf("rates = %+v, want one at 1916.36", rates)
	}
}

func TestGolden(t *testing.T) {
	vcrtest.SetSecrets(t)
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 15))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
