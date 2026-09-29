// Package mma fetches rates from the Maldives Monetary Authority, which publishes the daily reference rate of the
// rufiyaa against the US dollar.
//
// The rufiyaa is USD-pegged within a crawling band, so MMA only publishes the one pair (USD/MVR). The file always
// returns the full history, so we filter client-side. Unlike most adapters, after is inclusive, as in the Ruby
// adapter. The feed occasionally emits duplicate entries for the same date (e.g. "08/09 February 2021"); we keep the
// first occurrence per date so upserts stay deterministic.
package mma

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.mma.gov.mv/JSON/referencerates.json"

func init() {
	adapter.Register("MMA", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches MMA rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	v := upto
	if v.IsZero() {
		v = a.Today()
	}
	body, err := a.Get(ctx, baseURL, url.Values{"v": {v.Format("20060102")}})
	if err != nil {
		return nil, err
	}
	rates, err := parse(body)
	if err != nil {
		return nil, err
	}
	out := rates[:0]
	for _, r := range rates {
		if !after.IsZero() && r.Date.Before(after) {
			continue
		}
		if !upto.IsZero() && r.Date.After(upto) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

var errNotArray = errors.New("expected JSON array of reference rates from " + baseURL)

func parse(data []byte) ([]adapter.Rate, error) {
	var records []map[string]any
	if err := json.Unmarshal(data, &records); err != nil || records == nil {
		return nil, errNotArray
	}

	var rates []adapter.Rate
	seen := map[time.Time]bool{}
	for _, record := range records {
		dateRaw, rateRaw := record["Date"], record["Rate"]
		if dateRaw == nil || rateRaw == nil {
			continue
		}
		rate, err := toFloat(rateRaw)
		if err != nil {
			return nil, err
		}
		if rate == 0 {
			continue
		}
		s, ok := dateRaw.(string)
		if !ok {
			return nil, fmt.Errorf("invalid date %v", dateRaw)
		}
		date, err := adapter.ParseDate(s, "02 January 2006", "2 January 2006", "02 Jan 2006", "2 Jan 2006")
		if err != nil {
			return nil, err
		}
		if seen[date] {
			continue
		}
		seen[date] = true
		rates = append(rates, adapter.Rate{Date: date, Base: "USD", Quote: "MVR", Rate: rate})
	}
	return rates, nil
}

// toFloat mirrors Ruby's strict Float(): numbers pass through, numeric strings parse, anything else is an error.
func toFloat(v any) (float64, error) {
	switch v := v.(type) {
	case float64:
		return v, nil
	case string:
		if f, ok := adapter.ParseFloat(v); ok {
			return f, nil
		}
	}
	return 0, fmt.Errorf("invalid rate %v", v)
}
