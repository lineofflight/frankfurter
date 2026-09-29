package banguat

import (
	"context"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "banguat", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 20))
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

func TestFetchGTQRatesWithUSDBase(t *testing.T) {
	record := fetch(t)[0]
	if record.Base != "USD" || record.Quote != "GTQ" {
		t.Errorf("got %s/%s, want USD/GTQ", record.Base, record.Quote)
	}
}

func TestParseXMLWithCorrectFields(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    <TipoCambioRangoResponse xmlns="http://www.banguat.gob.gt/variables/ws/">
      <TipoCambioRangoResult>
        <Vars>
          <Var>
            <moneda>2</moneda>
            <fecha>10/03/2026</fecha>
            <venta>7.65857</venta>
            <compra>7.65857</compra>
          </Var>
        </Vars>
      </TipoCambioRangoResult>
    </TipoCambioRangoResponse>
  </soap:Body>
</soap:Envelope>
`
	records, err := parse([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	r := records[0]
	if r.Base != "USD" || r.Quote != "GTQ" {
		t.Errorf("got %s/%s, want USD/GTQ", r.Base, r.Quote)
	}
	if r.Rate != 7.65857 {
		t.Errorf("rate = %v, want 7.65857", r.Rate)
	}
	if !r.Date.Equal(adapter.Date(2026, 3, 10)) {
		t.Errorf("date = %v, want 2026-03-10", r.Date)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 20))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
