// Package bcu fetches rates from the Central Bank of Uruguay (Banco Central del Uruguay), which publishes official
// buying and selling rates in Uruguayan pesos (UYU) through a SOAP web service, one currency per request.
//
// Rows are stored with base = foreign currency, quote = UYU. As in the Ruby adapter, the requested range starts at
// after itself (inclusive) and the response is not clipped.
package bcu

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const endpoint = "https://cotizaciones.bcu.gub.uy/wscotizaciones/servlet/awsbcucotizaciones"

// currencies maps BCU numeric currency codes to ISO 4217, in request order.
var currencies = []struct{ code, iso string }{
	{"2225", "USD"}, // DLS. USA BILLETE
	{"1111", "EUR"}, // EURO
	{"1000", "BRL"}, // REAL
	{"0500", "ARS"}, // PESO ARGENTINO
	{"2309", "CAD"}, // DOLAR CANADIENSE
	{"1300", "CLP"}, // PESO CHILENO
	{"4150", "CNY"}, // YUAN RENMIMBI
	{"5500", "COP"}, // PESO COLOMBIANO
	{"1800", "DKK"}, // CORONA DANESA
	{"5100", "HKD"}, // DOLAR HONG KONG
	{"4300", "HUF"}, // FORINT HUNGARO
	{"5700", "INR"}, // RUPIA INDIA
	{"2700", "GBP"}, // LIBRA ESTERLINA
	{"4900", "ISK"}, // CORONA ISLANDESA
	{"3600", "JPY"}, // YEN
	{"5300", "KRW"}, // WON
	{"5600", "MYR"}, // RINGGIT
	{"4200", "MXN"}, // PESO MEXICANO
	{"4600", "NOK"}, // CORONA NORUEGA
	{"1490", "NZD"}, // DOL. NEOZELANDES
	{"4800", "PYG"}, // GUARANI
	{"4000", "PEN"}, // NVO.SOL PERUANO
	{"5400", "RUB"}, // RUBLO
	{"1620", "ZAR"}, // RAND SUDAFRICANO
	{"5800", "SEK"}, // CORONA SUECA
	{"5900", "CHF"}, // FRANCO SUIZO
	{"4400", "TRY"}, // LIRA TURCA
}

func isoFor(code string) (string, bool) {
	for _, c := range currencies {
		if c.code == code {
			return c.iso, true
		}
	}
	return "", false
}

func init() {
	adapter.Register("BCU", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BCU rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 365 }

// Fetch implements adapter.Adapter. The service needs a start date; Ruby fails on a nil after too.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("start date required")
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}

	var rates []adapter.Rate
	for _, c := range currencies {
		req, err := a.NewRequest(ctx, http.MethodPost, endpoint, strings.NewReader(soapRequest(c.code, after, end)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "text/xml; charset=utf-8")
		resp, err := a.Do(req)
		if err != nil {
			return nil, err
		}
		parsed, err := parse(resp.Body)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
		if err := a.Sleep(ctx, time.Second); err != nil {
			return nil, err
		}
	}
	return rates, nil
}

type dato struct {
	Fecha  *string `xml:"Fecha"`
	Moneda *string `xml:"Moneda"`
	TCC    *string `xml:"TCC"`
	TCV    *string `xml:"TCV"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var rates []adapter.Rate
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return rates, nil
		}
		if err != nil {
			return nil, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "datoscotizaciones.dato" {
			continue
		}
		var d dato
		if err := dec.DecodeElement(&d, &start); err != nil {
			return nil, err
		}
		if d.Fecha == nil || d.Moneda == nil || d.TCC == nil || d.TCV == nil {
			continue
		}
		base, ok := isoFor(*d.Moneda)
		if !ok || base == "UYU" {
			continue
		}
		date, err := time.Parse(time.DateOnly, strings.TrimSpace(*d.Fecha))
		if err != nil {
			return nil, err
		}
		tcc, err := strconv.ParseFloat(strings.TrimSpace(*d.TCC), 64)
		if err != nil {
			return nil, fmt.Errorf("TCC: %w", err)
		}
		tcv, err := strconv.ParseFloat(strings.TrimSpace(*d.TCV), 64)
		if err != nil {
			return nil, fmt.Errorf("TCV: %w", err)
		}
		if tcc == 0 || tcv == 0 {
			continue
		}
		rates = append(rates, adapter.Rate{
			Date:  date,
			Base:  base,
			Quote: "UYU",
			Rate:  adapter.Midpoint(tcc, tcv),
			Bid:   adapter.Float(tcc),
			Ask:   adapter.Float(tcv),
		})
	}
}

func soapRequest(code string, start, end time.Time) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"
                   xmlns:cot="Cotiza">
   <soapenv:Header />
   <soapenv:Body>
      <cot:wsbcucotizaciones.Execute>
         <cot:Entrada>
            <cot:Moneda>
               <cot:item>` + code + `</cot:item>
            </cot:Moneda>
            <cot:FechaDesde>` + start.Format(time.DateOnly) + `</cot:FechaDesde>
            <cot:FechaHasta>` + end.Format(time.DateOnly) + `</cot:FechaHasta>
            <cot:Grupo>0</cot:Grupo>
         </cot:Entrada>
      </cot:wsbcucotizaciones.Execute>
   </soapenv:Body>
</soapenv:Envelope>
`
}
