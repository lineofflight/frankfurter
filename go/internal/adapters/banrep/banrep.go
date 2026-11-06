// Package banrep fetches rates from Banco de la República Colombia, which
// publishes the daily TRM (Tasa Representativa del Mercado), the representative
// market rate of the US dollar in Colombian pesos, through the Socrata Open
// Data API on datos.gov.co.
package banrep

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.datos.gov.co/resource/32sa-8pi3.json"

func init() {
	adapter.Register("BANREP", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BANREP rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if upto.IsZero() {
		upto = a.Today()
	}
	body, err := a.Get(ctx, baseURL, url.Values{
		"$where": {fmt.Sprintf("vigenciadesde>='%sT00:00:00.000' AND vigenciadesde<='%sT00:00:00.000'",
			day(after), day(upto))},
		"$limit": {"50000"},
		"$order": {"vigenciadesde ASC"},
	})
	if err != nil {
		return nil, err
	}
	return parse(body)
}

type record struct {
	From  *string `json:"vigenciadesde"`
	Value *string `json:"valor"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var records []record
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("expected JSON array from %s: %w", baseURL, err)
	}

	var rates []adapter.Rate
	for _, r := range records {
		if r.From == nil || r.Value == nil {
			continue
		}
		date, err := time.Parse("2006-01-02T15:04:05.000", *r.From)
		if err != nil {
			return nil, err
		}
		value, ok := adapter.ParseFloat(*r.Value)
		if !ok {
			return nil, fmt.Errorf("invalid valor %q on %s", *r.Value, *r.From)
		}
		if value == 0 {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: "USD", Quote: "COP", Rate: value})
	}
	return rates, nil
}

// day renders a date for the query, leaving an open bound empty as the Ruby
// string interpolation of nil does.
func day(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.DateOnly)
}
