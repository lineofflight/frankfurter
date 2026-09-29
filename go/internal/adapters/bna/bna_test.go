package bna

import (
	"context"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "bna", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 21))
	if err != nil {
		t.Fatal(err)
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
	first := rates[0].Date
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 1 {
		t.Errorf("got %d rates on %s, want more than 1", n, first.Format("2006-01-02"))
	}
}

func TestFetchAOAAsQuote(t *testing.T) {
	rates := fetch(t)
	if rates[0].Quote != "AOA" {
		t.Errorf("quote = %q, want AOA", rates[0].Quote)
	}
	if slices.ContainsFunc(rates, func(r adapter.Rate) bool { return r.Base == "AOA" }) {
		t.Error("AOA appears as a base")
	}
}

func TestParseMidRatesSkipsNonMid(t *testing.T) {
	rates, err := parse([]byte(`{"genericResponse":[
	  {"taxa":663.11983,"descricaoTipoCambio":"Taxa de Referência - MEDIO","tipoCambio":"M",
	   "data":"2026-05-21","designacaoMoeda":"DOLAR CANADENSE","codigoMoeda":"CAD"},
	  {"taxa":660.10,"descricaoTipoCambio":"Taxa de referencia - VENDA","tipoCambio":"B",
	   "data":"2026-05-21","designacaoMoeda":"DOLAR CANADENSE","codigoMoeda":"CAD"},
	  {"taxa":665.10,"descricaoTipoCambio":"Taxa de Referência - COMPRA","tipoCambio":"G",
	   "data":"2026-05-21","designacaoMoeda":"DOLAR CANADENSE","codigoMoeda":"CAD"}
	],"success":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "CAD" || r.Quote != "AOA" {
		t.Errorf("pair = %s/%s, want CAD/AOA", r.Base, r.Quote)
	}
	if math.Abs(r.Rate-663.11983) > 0.00001 {
		t.Errorf("rate = %v, want 663.11983", r.Rate)
	}
	if !r.Date.Equal(adapter.Date(2026, 5, 21)) {
		t.Errorf("date = %v, want 2026-05-21", r.Date)
	}
}

func TestParseSkips(t *testing.T) {
	tests := map[string]string{
		"zero rates": `{"genericResponse":[
		  {"taxa":0.0,"descricaoTipoCambio":"Taxa de Referência - MEDIO","tipoCambio":"M",
		   "data":"2026-05-21","designacaoMoeda":"DOLAR AMERICANO","codigoMoeda":"USD"}
		],"success":true}`,
		"non-ISO currency codes": `{"genericResponse":[
		  {"taxa":1.234,"descricaoTipoCambio":"Taxa de Referência - MEDIO","tipoCambio":"M",
		   "data":"2026-05-21","designacaoMoeda":"SDR USD","codigoMoeda":"XDRUSD"}
		],"success":true}`,
		"empty response": `{"genericResponse":[],"success":true}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			rates, err := parse([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			if len(rates) != 0 {
				t.Errorf("got %d rates, want none", len(rates))
			}
		})
	}
}

func TestParseRaisesOnUnsuccessfulResponse(t *testing.T) {
	_, err := parse([]byte(`{"success":false,"message":"Erro"}`))
	if err == nil || err.Error() != "series request failed: Erro" {
		t.Errorf("err = %v, want series request failed: Erro", err)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 21))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func stubClient(fn func(*http.Request) string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(fn(r))),
			Request:    r,
		}, nil
	})}
}

func TestFetchEmptyWindowMakesNoRequests(t *testing.T) {
	a := New(stubClient(func(r *http.Request) string {
		t.Errorf("unexpected request %s", r.URL)
		return ""
	}))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 22), adapter.Date(2026, 5, 21))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestFetchDefaultsAndListFiltering(t *testing.T) {
	var queried []string
	a := New(stubClient(func(r *http.Request) string {
		if r.URL.Path == "/service/rest/taxas/get/lista/moedas" {
			return `{"genericResponse":[{"codigoMoeda":"AOA"},{"codigoMoeda":"XAU"},{"codigoMoeda":"XDRUSD"},
			  {"codigoMoeda":"usd"},{"codigoMoeda":"CAD"}],"success":true}`
		}
		q := r.URL.Query()
		if q.Get("datainicio") != "2000-01-01" || q.Get("datafim") != "2026-05-21" || q.Get("tipocambio") != "M" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		queried = append(queried, q.Get("moeda"))
		return `{"genericResponse":[{"taxa":"663.5","tipoCambio":"M","data":"2026-05-21","codigoMoeda":"CAD"}],
		  "success":true}`
	}))
	a.Now = func() time.Time { return time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC) }
	rates, err := a.Fetch(context.Background(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(queried, []string{"CAD"}) {
		t.Errorf("queried %v, want [CAD]", queried)
	}
	if len(rates) != 1 || rates[0].Rate != 663.5 || rates[0].Base != "CAD" || rates[0].Quote != "AOA" {
		t.Errorf("rates = %+v, want one CAD/AOA at 663.5", rates)
	}
}
