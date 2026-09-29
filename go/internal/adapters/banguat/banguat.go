// Package banguat fetches rates from Banco de Guatemala, which publishes daily reference rates for GTQ per 1 USD via a
// SOAP web service, so base=USD, quote=GTQ.
//
// Currently disabled in production: requests from the production server are reset by Banguat's edge before the TLS
// handshake completes, which looks like an egress-network or source-IP policy. There is no provider seed, so the
// adapter does not register itself.
//
// As in Ruby, Fetch returns whatever the service sends for the requested range without clipping it: the service
// includes the start date, so the row dated after is kept.
package banguat

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const endpoint = "https://www.banguat.gob.gt/variables/ws/TipoCambio.asmx"

// Adapter fetches Banguat rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 365 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("start date required")
	}
	if upto.IsZero() {
		upto = a.Today()
	}

	req, err := a.NewRequest(ctx, http.MethodPost, endpoint, strings.NewReader(soapRequest(after, upto)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	return parse(resp.Body)
}

func soapRequest(start, end time.Time) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    <TipoCambioRango xmlns="http://www.banguat.gob.gt/variables/ws/">
      <fechainit>%s</fechainit>
      <fechafin>%s</fechafin>
    </TipoCambioRango>
  </soap:Body>
</soap:Envelope>
`, start.Format("02/01/2006"), end.Format("02/01/2006"))
}

type variable struct {
	Fecha []string `xml:"fecha"`
	Venta []string `xml:"venta"`
}

// parse reads every Var element, at any depth, as Ox's locate("*/Var") does.
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
		if !ok || start.Name.Local != "Var" {
			continue
		}
		var v variable
		if err := dec.DecodeElement(&v, &start); err != nil {
			return nil, err
		}
		// Ox gives nil text for a missing or empty element.
		if len(v.Fecha) == 0 || v.Fecha[0] == "" || len(v.Venta) == 0 || v.Venta[0] == "" {
			continue
		}
		date, err := time.Parse("2/1/2006", v.Fecha[0])
		if err != nil {
			return nil, err
		}
		rate, ok := adapter.ParseFloat(v.Venta[0])
		if !ok {
			return nil, fmt.Errorf("invalid rate %q", v.Venta[0])
		}
		if rate == 0 {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: "USD", Quote: "GTQ", Rate: rate})
	}
}
