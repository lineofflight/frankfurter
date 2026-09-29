package bccr

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

// The Ruby spec allows playback repeats, but go-vcr's replayable mode returns
// the first match even when it has been used, so the token interaction would
// also answer the data request (both match on method and host). Each
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeAPI answers the token request with "tok" and the data request with body,
// recording the data request.
func fakeAPI(body string, got **http.Request) *Adapter {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		resp := "tok"
		if r.URL.String() != tokenURL {
			*got = r
			resp = body
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(resp)), Request: r}, nil
	})}
	return New(client)
}

const oneDay = `{
	"columnas": [{"field": "nombre", "tituloIngles": "Indicators"}, {"field": "serie1", "tituloIngles": "20 mar 2026"}],
	"indicadoresRaiz": [{"idIndicador": 318, "series": {"serie1Ingles": "469.49"}}]
}`

func TestFetchRequest(t *testing.T) {
	var req *http.Request
	a := fakeAPI(oneDay, &req)
	if _, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 16), adapter.Date(2026, 3, 24)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(req.URL.String(), dataURL+"?") {
		t.Errorf("url = %s", req.URL)
	}
	q := req.URL.Query()
	want := map[string]string{
		"IdGrupoVariable":        "1",
		"FechaInicio":            "2026-03-16T00:00:00",
		"FechaFin":               "2026-03-24",
		"CantidadSeriesAMostrar": "100",
	}
	if len(q) != len(want) {
		t.Errorf("query = %v", q)
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	// http.rb sends the token_csrf header as Token-Csrf.
	if h := req.Header["Token-Csrf"]; len(h) != 1 || h[0] != "tok" {
		t.Errorf("Token-Csrf = %q, want tok", h)
	}
	if h := req.Header.Get("Origin"); h != "https://sdd.bccr.fi.cr" {
		t.Errorf("Origin = %q", h)
	}
}

func TestFetchDefaults(t *testing.T) {
	var req *http.Request
	a := fakeAPI(oneDay, &req)
	a.Now = func() time.Time { return time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC) }
	if _, err := a.Fetch(context.Background(), time.Time{}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	q := req.URL.Query()
	if q.Get("FechaInicio") != "T00:00:00" || q.Get("FechaFin") != "2026-03-25" {
		t.Errorf("query = %v", q)
	}
}

func TestParseSkipsZeroAndAcceptsLooseValues(t *testing.T) {
	rates, err := parse([]byte(`{
		"columnas": [
			{"field": "nombre", "tituloIngles": "Indicators"},
			{"field": "serie1", "tituloIngles": "1 Mar 2026"},
			{"field": "serie2", "tituloIngles": "02 mar 2026"},
			{"field": "serie3", "tituloIngles": "03 mar 2026"}
		],
		"indicadoresRaiz": [{"idIndicador": 318, "series": {"serie1Ingles": " 470.5 ", "serie2Ingles": "0", "serie3Ingles": null}}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 1), Base: "USD", Quote: "CRC", Rate: 470.5}
	if len(rates) != 1 || rates[0] != want {
		t.Errorf("rates = %+v, want [%+v]", rates, want)
	}
}

func TestParseErrors(t *testing.T) {
	cols := `[{"field": "nombre", "tituloIngles": "Indicators"}, {"field": "serie1", "tituloIngles": "20 mar 2026"}]`
	for name, body := range map[string]string{
		"missing envelope":  `{"columnas": ` + cols + `}`,
		"missing indicator": `{"columnas": ` + cols + `, "indicadoresRaiz": [{"idIndicador": 317, "series": {"serie1Ingles": "1"}}]}`,
		"missing series":    `{"columnas": ` + cols + `, "indicadoresRaiz": [{"idIndicador": 318}]}`,
		"bad value":         `{"columnas": ` + cols + `, "indicadoresRaiz": [{"idIndicador": 318, "series": {"serie1Ingles": "n/a"}}]}`,
		"bad date":          `{"columnas": [{"field": "nombre"}, {"field": "serie1", "tituloIngles": "soon"}], "indicadoresRaiz": [{"idIndicador": 318, "series": {"serie1Ingles": "1"}}]}`,
		"not json":          `<html>`,
	} {
		if _, err := parse([]byte(body)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
