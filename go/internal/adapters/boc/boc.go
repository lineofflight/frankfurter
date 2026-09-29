// Package boc fetches the Bank of Canada's daily indicative rates, quoted as CAD per unit of each foreign currency.
//
// The current series starts 2017-01-03. Legacy noon rates (2007-2017) are available under the LEGACY_NOON_RATES
// group with ~65 currencies.
//
// Like the Ruby adapter, Fetch passes after as the API's inclusive start_date and does not clip the response.
package boc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/db"
)

const baseURL = "https://www.bankofcanada.ca/valet/observations/group/FX_RATES_DAILY/json"

func init() {
	adapter.Register("BOC", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BOC rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	params := url.Values{}
	if !after.IsZero() {
		params.Set("start_date", db.FormatDate(after))
	}
	if !upto.IsZero() {
		params.Set("end_date", db.FormatDate(upto))
	}
	body, err := a.Get(ctx, baseURL, params)
	if err != nil {
		return nil, err
	}
	return parse(body)
}

func parse(data []byte) ([]adapter.Rate, error) {
	var doc struct {
		Observations []map[string]json.RawMessage `json:"observations"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, obs := range doc.Observations {
		var d string
		if err := json.Unmarshal(obs["d"], &d); err != nil {
			return nil, fmt.Errorf("observation date: %w", err)
		}
		date, err := adapter.ParseDate(d, time.DateOnly)
		if err != nil {
			return nil, err
		}

		series := make([]string, 0, len(obs))
		for key := range obs {
			if strings.HasPrefix(key, "FX") {
				series = append(series, key)
			}
		}
		slices.Sort(series)

		for _, key := range series {
			var v struct {
				V string `json:"v"`
			}
			if err := json.Unmarshal(obs[key], &v); err != nil {
				return nil, fmt.Errorf("%s on %s: %w", key, d, err)
			}
			rate, ok := adapter.ParseFloat(v.V)
			if !ok {
				return nil, fmt.Errorf("invalid %s value %q on %s", key, v.V, d)
			}
			iso := strings.TrimSuffix(strings.TrimPrefix(key, "FX"), "CAD")
			rates = append(rates, adapter.Rate{Date: date, Base: iso, Quote: "CAD", Rate: rate})
		}
	}
	return rates, nil
}
