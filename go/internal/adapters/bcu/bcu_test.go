package bcu

import (
	"context"
	"math"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "bcu", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 27), adapter.Date(2026, 3, 31))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, xml string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(xml))
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

func TestFetchUYUQuote(t *testing.T) {
	if got := fetch(t)[0].Quote; got != "UYU" {
		t.Errorf("quote = %q, want UYU", got)
	}
}

func TestFetchNumericRatesGreaterThanZero(t *testing.T) {
	// Ruby also checks the rate is a Float; Go's type system guarantees it.
	for _, r := range fetch(t) {
		if !(r.Rate > 0) {
			t.Errorf("%s rate = %v, want > 0", r.Base, r.Rate)
		}
	}
}

// Ruby looks each base up with Money::Currency.find; no Go currency catalogue exists yet, so check the ISO shape.
func TestFetchISOCurrencyCodes(t *testing.T) {
	iso := regexp.MustCompile(`^[A-Z]{3}$`)
	for _, r := range fetch(t) {
		if !iso.MatchString(r.Base) {
			t.Errorf("base %q is not an ISO code", r.Base)
		}
	}
}

func TestFetchValidDates(t *testing.T) {
	for _, r := range fetch(t) {
		if r.Date.IsZero() || !r.Date.Equal(r.Date.Truncate(24*time.Hour)) || r.Date.Location() != time.UTC {
			t.Errorf("date %v is not a UTC calendar date", r.Date)
		}
	}
}

const envelope = `<?xml version="1.0" encoding="utf-8"?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <SOAP-ENV:Body>
    <wsbcucotizaciones.ExecuteResponse xmlns="Cotiza">
      <Salida xmlns="Cotiza">
        <respuestastatus>
          <status>1</status>
          <codigoerror>0</codigoerror>
          <mensaje/>
        </respuestastatus>
        <datoscotizaciones>
%s
        </datoscotizaciones>
      </Salida>
    </wsbcucotizaciones.ExecuteResponse>
  </SOAP-ENV:Body>
</SOAP-ENV:Envelope>`

func datoXML(moneda, name, tcc, tcv string) string {
	return `<datoscotizaciones.dato xmlns="Cotiza">
  <Fecha>2026-03-27</Fecha>
  <Moneda>` + moneda + `</Moneda>
  <Nombre>` + name + `</Nombre>
  <TCC>` + tcc + `</TCC>
  <TCV>` + tcv + `</TCV>
  <ArbAct>1.0</ArbAct>
  <FormaArbitrar>0</FormaArbitrar>
</datoscotizaciones.dato>`
}

func soap(datos ...string) string {
	body := ""
	for _, d := range datos {
		body += d + "\n"
	}
	return strings.Replace(envelope, "%s", body, 1)
}

func TestParseSOAPResponseFields(t *testing.T) {
	rates := mustParse(t, soap(
		datoXML("2225", "DLS. USA BILLETE", "38.5", "39.5"),
		datoXML("1111", "EURO", "42.2", "42.8"),
	))
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	if rates[0].Base != "USD" || rates[0].Quote != "UYU" {
		t.Errorf("rates[0] = %s/%s, want USD/UYU", rates[0].Base, rates[0].Quote)
	}
	if math.Abs(rates[0].Rate-39.0) > 0.01 {
		t.Errorf("rates[0].Rate = %v, want ~39.0", rates[0].Rate)
	}
	if !rates[0].Date.Equal(adapter.Date(2026, 3, 27)) {
		t.Errorf("rates[0].Date = %v, want 2026-03-27", rates[0].Date)
	}
	if rates[1].Base != "EUR" || rates[1].Quote != "UYU" {
		t.Errorf("rates[1] = %s/%s, want EUR/UYU", rates[1].Base, rates[1].Quote)
	}
	if math.Abs(rates[1].Rate-42.5) > 0.01 {
		t.Errorf("rates[1].Rate = %v, want ~42.5", rates[1].Rate)
	}
}

func TestParseSkipsUnknownCurrencyCodes(t *testing.T) {
	rates := mustParse(t, soap(
		datoXML("2225", "DLS. USA BILLETE", "38.5", "39.5"),
		datoXML("9999", "UNKNOWN", "1.0", "1.5"),
	))
	if len(rates) != 1 || rates[0].Base != "USD" {
		t.Fatalf("got %+v, want one USD rate", rates)
	}
}

func TestParseMidpointFromTCCAndTCV(t *testing.T) {
	rates := mustParse(t, soap(datoXML("2225", "DLS. USA BILLETE", "38.0", "40.0")))
	if rates[0].Rate != 39.0 {
		t.Errorf("rate = %v, want 39.0", rates[0].Rate)
	}
}

func TestParseSkipsUYUSelfReference(t *testing.T) {
	if rates := mustParse(t, soap(datoXML("0", "PESO URUGUAYO", "1.0", "1.0"))); len(rates) != 0 {
		t.Errorf("got %d rates, want 0", len(rates))
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 27), adapter.Date(2026, 3, 31))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
