// Package fred fetches rates from Federal Reserve Economic Data (FRED), which publishes the daily H.10 exchange
// rates.
//
// Most series are quoted as foreign currency per USD. A few (AUD, EUR, GBP, NZD) are quoted as USD per foreign
// currency and stored with the foreign currency as base. The H.10 is a curated policy publication; these are all the
// series available.
package fred

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const apiURL = "https://api.stlouisfed.org/fred/series/observations"

type series struct {
	id, quote, base string
}

var allSeries = []series{
	{"DEXBZUS", "BRL", "USD"},
	{"DEXCAUS", "CAD", "USD"},
	{"DEXCHUS", "CNY", "USD"},
	{"DEXDNUS", "DKK", "USD"},
	{"DEXHKUS", "HKD", "USD"},
	{"DEXINUS", "INR", "USD"},
	{"DEXJPUS", "JPY", "USD"},
	{"DEXKOUS", "KRW", "USD"},
	{"DEXMAUS", "MYR", "USD"},
	{"DEXMXUS", "MXN", "USD"},
	{"DEXNOUS", "NOK", "USD"},
	{"DEXSDUS", "SEK", "USD"},
	{"DEXSFUS", "ZAR", "USD"},
	{"DEXSIUS", "SGD", "USD"},
	{"DEXSLUS", "LKR", "USD"},
	{"DEXSZUS", "CHF", "USD"},
	{"DEXTAUS", "TWD", "USD"},
	{"DEXTHUS", "THB", "USD"},
	{"DEXUSAL", "USD", "AUD"},
	{"DEXUSEU", "USD", "EUR"},
	{"DEXUSNZ", "USD", "NZD"},
	{"DEXUSUK", "USD", "GBP"},
}

func init() {
	adapter.Register("FRED", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches FRED rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter. Like the Ruby adapter, it asks FRED for observations from after onwards
// (observation_start is inclusive) and ignores upto.
func (a *Adapter) Fetch(ctx context.Context, after, _ time.Time) ([]adapter.Rate, error) {
	key := os.Getenv("FRED_API_KEY")
	if key == "" {
		return nil, errors.New("no API key")
	}

	var rates []adapter.Rate
	for _, s := range allSeries {
		if err := a.Sleep(ctx, 200*time.Millisecond); err != nil {
			return nil, err
		}
		params := url.Values{
			"series_id": {s.id},
			"api_key":   {key},
			"file_type": {"json"},
		}
		if !after.IsZero() {
			params.Set("observation_start", after.Format(time.DateOnly))
		}
		body, err := a.Get(ctx, apiURL, params)
		if err != nil {
			return nil, err
		}
		got, err := parse(body, s.quote, s.base)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.id, err)
		}
		rates = append(rates, got...)
	}
	return rates, nil
}

type response struct {
	Observations []struct {
		Date  string `json:"date"`
		Value string `json:"value"`
	} `json:"observations"`
}

func parse(data []byte, quote, base string) ([]adapter.Rate, error) {
	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, obs := range r.Observations {
		if obs.Value == "." {
			continue
		}
		value, ok := adapter.ParseFloat(obs.Value)
		if !ok {
			return nil, fmt.Errorf("invalid value %q on %s", obs.Value, obs.Date)
		}
		if value == 0 {
			continue
		}
		date, err := time.Parse(time.DateOnly, obs.Date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, adapter.Rate{Date: date, Base: base, Quote: quote, Rate: value})
	}
	return rates, nil
}
