package bcb

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
	return New(vcrtest.Client(t, "bcb", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
}

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 7))
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

func TestFetchRequiresAfter(t *testing.T) {
	if _, err := newAdapter(t).Fetch(context.Background(), time.Time{}, adapter.Date(2026, 3, 7)); err == nil {
		t.Error("want an error without an after date")
	}
}

func TestParseNormalResponse(t *testing.T) {
	rates, err := parse([]byte(`{"value":[{"cotacaoCompra":5.1995,"cotacaoVenda":5.2001,`+
		`"dataHoraCotacao":"2026-03-02 13:09:26.433","tipoBoletim":"Fechamento"}]}`), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "USD" || r.Quote != "BRL" {
		t.Errorf("got %s/%s, want USD/BRL", r.Base, r.Quote)
	}
	if math.Abs(r.Rate-5.2001) > 0.001 {
		t.Errorf("rate = %v, want 5.2001", r.Rate)
	}
}

func TestParseExtractsDate(t *testing.T) {
	rates, err := parse([]byte(`{"value":[{"cotacaoVenda":5.2001,`+
		`"dataHoraCotacao":"2026-03-04 13:09:26.433","tipoBoletim":"Fechamento"}]}`), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || !rates[0].Date.Equal(adapter.Date(2026, 3, 4)) {
		t.Errorf("rates = %+v, want one dated 2026-03-04", rates)
	}
}

func TestParseEmptyResponse(t *testing.T) {
	rates, err := parse([]byte(`{"value":[]}`), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseSkipsMissingRate(t *testing.T) {
	rates, err := parse([]byte(`{"value":[{"cotacaoCompra":5.1995,"cotacaoVenda":null,`+
		`"dataHoraCotacao":"2026-03-02 13:09:26.433","tipoBoletim":"Fechamento"}]}`), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 7))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
