// Package bcb fetches rates from Banco Central do Brasil, which publishes daily PTAX closing rates for 10 currencies
// against the Brazilian real through the PTAX OData API. PTAX only publishes these 10 currencies.
//
// Fetch passes after and upto straight to the API as the first and last quotation dates, so after is inclusive, as
// in the Ruby adapter, and it must be set (Ruby raises on a nil after).
package bcb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const apiURL = "https://olinda.bcb.gov.br/olinda/servico/PTAX/versao/v1/odata/" +
	"CotacaoMoedaPeriodo(moeda=@moeda,dataInicial=@dataInicial,dataFinalCotacao=@dataFinalCotacao)"

var currencies = []string{"AUD", "CAD", "CHF", "DKK", "EUR", "GBP", "JPY", "NOK", "SEK", "USD"}

func init() {
	adapter.Register("BCB", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BCB rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("after date is required")
	}
	if upto.IsZero() {
		upto = a.Today()
	}

	var rates []adapter.Rate
	for _, currency := range currencies {
		if err := a.Sleep(ctx, 200*time.Millisecond); err != nil {
			return nil, err
		}
		body, err := a.Get(ctx, apiURL+"?"+query(currency, after, upto), nil)
		if err != nil {
			return nil, err
		}
		parsed, err := parse(body, currency)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

// query builds the OData query string by hand, with the literal quotes and escapes the API expects.
func query(currency string, from, upto time.Time) string {
	return strings.Join([]string{
		fmt.Sprintf("@moeda='%s'", currency),
		fmt.Sprintf("@dataInicial='%s'", from.Format("01-02-2006")),
		fmt.Sprintf("@dataFinalCotacao='%s'", upto.Format("01-02-2006")),
		"$filter=tipoBoletim%20eq%20'Fechamento'",
		"$format=json",
	}, "&")
}

type record struct {
	Rate *float64 `json:"cotacaoVenda"`
	Time *string  `json:"dataHoraCotacao"`
}

func parse(data []byte, currency string) ([]adapter.Rate, error) {
	var doc struct {
		Value []record `json:"value"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, r := range doc.Value {
		if r.Rate == nil || *r.Rate == 0 || r.Time == nil {
			continue
		}
		raw := strings.TrimSpace(*r.Time)
		if len(raw) < 10 {
			continue
		}
		date, err := time.Parse(time.DateOnly, raw[:10])
		if err != nil {
			return nil, err
		}
		rates = append(rates, adapter.Rate{Date: date, Base: currency, Quote: "BRL", Rate: *r.Rate})
	}
	return rates, nil
}
