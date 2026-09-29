// Package rb fetches rates from Sveriges Riksbank, which publishes daily exchange rates for about 29 currencies
// against the Swedish krona through the SWEA API. The ByGroup endpoint (group 130) returns every currency series in
// one request, for at most a year at a time.
package rb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://api.riksbank.se/swea/v1/Observations/ByGroup/130"

// The Riksbank backfills a successor's series with its predecessor's values. The euro succeeded the ECU 1:1 on its
// first quoting day, 1999-01-04; earlier EUR observations are the ECU (XEU), matching how the BdP and AMCM adapters
// label the same data. The RUB series is not restated across the 1998 redenomination: 0.0013 SEK on 1997-12-30, 1.326
// on 1998-01-02, so earlier rows are old ruble (RUR).
var predecessors = map[string]adapter.Predecessor{
	"EUR": {Code: "XEU", Cutover: adapter.Date(1999, 1, 4)},
	"RUB": {Code: "RUR", Cutover: adapter.Date(1998, 1, 1)},
}

func init() {
	adapter.Register("RB", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches RB rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 365 }

// Fetch implements adapter.Adapter. The API filters by date itself; as in Ruby, an open after leaves its path segment
// empty.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if upto.IsZero() {
		upto = a.Today()
	}
	from := ""
	if !after.IsZero() {
		from = after.Format(time.DateOnly)
	}
	body, err := a.Get(ctx, fmt.Sprintf("%s/%s/%s", baseURL, from, upto.Format(time.DateOnly)), nil)
	if err != nil {
		return nil, err
	}
	return parse(body)
}

type observation struct {
	SeriesID *string `json:"seriesId"`
	Date     *string `json:"date"`
	Value    any     `json:"value"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if _, ok := raw.([]any); !ok {
		return nil, fmt.Errorf("expected JSON array from SWEA ByGroup, got %T", raw)
	}
	var observations []observation
	if err := json.Unmarshal(data, &observations); err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, o := range observations {
		// Ruby's `next unless value` skips false as well as nil.
		if o.SeriesID == nil || o.Date == nil || o.Value == nil || o.Value == false {
			continue
		}
		currency := strings.TrimSuffix(strings.TrimPrefix(*o.SeriesID, "SEK"), "PMI")
		if currency == "ETT" {
			continue
		}
		rate, err := toFloat(o.Value)
		if err != nil {
			return nil, err
		}
		if rate == 0 {
			continue
		}
		date, err := time.Parse(time.DateOnly, *o.Date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, adapter.Rate{
			Date:  date,
			Base:  adapter.HistoricalCode(predecessors, currency, date),
			Quote: "SEK",
			Rate:  rate,
		})
	}
	return rates, nil
}

// toFloat is Ruby's Float(value): numbers pass through, numeric strings parse, anything else is an error.
func toFloat(v any) (float64, error) {
	switch v := v.(type) {
	case float64:
		return v, nil
	case string:
		if f, ok := adapter.ParseFloat(v); ok {
			return f, nil
		}
	}
	return 0, fmt.Errorf("invalid value %v", v)
}
