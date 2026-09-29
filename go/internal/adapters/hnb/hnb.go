// Package hnb fetches rates from the Croatian National Bank, which publishes daily mid rates against the euro via its
// v3 API.
//
// Historical HRK rates (pre-2023) are no longer available, as the v2 API was frozen after Croatia adopted the euro.
// The API filters by date itself and its lower bound is inclusive, so rows dated on after come back too, as in Ruby.
package hnb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://api.hnb.hr/tecajn-eur/v3"

func init() {
	adapter.Register("HNB", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches HNB rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 90 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	params := url.Values{}
	if !after.IsZero() {
		params.Set("datum-primjene-od", after.Format(time.DateOnly))
	}
	if !after.IsZero() || !upto.IsZero() {
		end := upto
		if end.IsZero() {
			end = a.Today()
		}
		params.Set("datum-primjene-do", end.Format(time.DateOnly))
	}
	body, err := a.Get(ctx, baseURL, params)
	if err != nil {
		return nil, err
	}
	return parse(body)
}

type row struct {
	Date     *string `json:"datum_primjene"`
	Rate     *string `json:"srednji_tecaj"`
	Currency *string `json:"valuta"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var rows []row
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, r := range rows {
		if r.Date == nil || r.Rate == nil || r.Currency == nil {
			continue
		}
		value, ok := adapter.ParseFloat(strings.ReplaceAll(*r.Rate, ",", "."))
		if !ok {
			return nil, fmt.Errorf("hnb: bad rate %q", *r.Rate)
		}
		if value == 0 {
			continue
		}
		date, err := time.Parse(time.DateOnly, *r.Date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, adapter.Rate{Date: date, Base: "EUR", Quote: *r.Currency, Rate: value})
	}
	return rates, nil
}
