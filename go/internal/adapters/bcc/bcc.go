// Package bcc fetches rates from Banco Central de Cuba, which publishes daily exchange rates against CUP for 13
// currencies through a JSON REST API.
//
// The bank publishes three parallel series: tasaOficial (Segment I, USD pegged at 24 CUP), tasaPublica (Segment II,
// retail bank rate, USD = 120 CUP) and tasaEspecial (Segment III, the informal/MLC market rate, the only float). We
// relay tasaEspecial: it tracks the de facto parallel market and is the only one that moves day to day. The other two
// are administered pegs published for legal and accounting purposes.
//
// The /historico endpoint accepts arbitrarily wide date ranges but only one currency at a time, so each window makes
// one request per currency. Rates are 1 foreign = X CUP; JPY is per unit.
//
// Like the Ruby adapter, Fetch sends after as the inclusive start date and does not clip the response, so rows dated
// after itself are returned.
package bcc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const historicoURL = "https://api.bc.gob.cu/v1/tasas-de-cambio/historico"

var currencies = []string{"AUD", "CAD", "CHF", "CNY", "DKK", "EUR", "GBP", "JPY", "MXN", "NOK", "RUB", "SEK", "USD"}

func init() {
	adapter.Register("BCC", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BCC rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	start, end := after, upto
	if start.IsZero() {
		start = adapter.Date(2025, 12, 19)
	}
	if end.IsZero() {
		end = a.Today()
	}
	if start.After(end) {
		return nil, nil
	}

	var rates []adapter.Rate
	for i, code := range currencies {
		if i > 0 {
			if err := a.Sleep(ctx, time.Second); err != nil {
				return nil, err
			}
		}
		body, err := a.Get(ctx, historicoURL, url.Values{
			"fechaInicio":  {start.Format(time.DateOnly)},
			"fechaFin":     {end.Format(time.DateOnly)},
			"codigoMoneda": {code},
		})
		if err != nil {
			return nil, err
		}
		parsed, err := parse(body, code)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

type entry struct {
	Date string          `json:"fecha"`
	Rate json.RawMessage `json:"tasaEspecial"`
}

func parse(data []byte, code string) ([]adapter.Rate, error) {
	var entries []entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("expected JSON array from %s: %w", historicoURL, err)
	}

	var rates []adapter.Rate
	for _, e := range entries {
		if len(e.Rate) == 0 || string(e.Rate) == "null" {
			continue
		}
		value, err := number(e.Rate)
		if err != nil {
			return nil, fmt.Errorf("invalid tasaEspecial on %s: %w", e.Date, err)
		}
		if value == 0 {
			continue
		}
		date, err := time.Parse(time.DateOnly, e.Date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, adapter.Rate{Date: date, Base: code, Quote: "CUP", Rate: value})
	}
	return rates, nil
}

// number reads a JSON number or numeric string, as Ruby's Float() does, and fails on anything else.
func number(raw json.RawMessage) (float64, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if v, ok := adapter.ParseFloat(s); ok {
			return v, nil
		}
		return 0, fmt.Errorf("not a number: %q", s)
	}
	return strconv.ParseFloat(string(raw), 64)
}
