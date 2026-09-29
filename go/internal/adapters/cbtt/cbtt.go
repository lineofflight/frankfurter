// Package cbtt fetches rates from the Central Bank of Trinidad and Tobago, which publishes daily buying and selling
// rates for 8 currencies against the Trinidad and Tobago dollar (TTD), weighted averages of the day's
// authorised-dealer transactions (GYD and JMD are not weighted).
//
// The adapter takes the mid. A leg with no trades that day comes back null, and the mid is skipped when either is
// missing. History runs from 1991 via a public WordPress REST route that takes a year span; the latest-only route uses
// a different key shape (and carries JPY without a history) and is not used. Unlike most adapters, after is
// inclusive, as in Ruby.
package cbtt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.central-bank.org.tt/wp-json/rates/v1/forex-rate"

// currencies maps each column prefix in the payload to its ISO code.
var currencies = []struct{ prefix, code string }{
	{"USD", "USD"},
	{"CAD", "CAD"},
	{"GBP", "GBP"},
	{"Euro", "EUR"},
	{"CHF", "CHF"},
	{"BBD", "BBD"},
	{"GYD", "GYD"},
	{"JMD", "JMD"},
}

func init() {
	adapter.Register("CBTT", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBTT rates.
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
	span := "all"
	if !after.IsZero() {
		span = fmt.Sprintf("%d-%d", after.Year(), upto.Year())
	}
	body, err := a.Get(ctx, baseURL+"/"+span, nil)
	if err != nil {
		return nil, err
	}
	rates, err := parse(body)
	if err != nil {
		return nil, err
	}
	var out []adapter.Rate
	for _, r := range rates {
		if (after.IsZero() || !r.Date.Before(after)) && !r.Date.After(upto) {
			out = append(out, r)
		}
	}
	return out, nil
}

func parse(data []byte) ([]adapter.Rate, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	raw, ok := payload["cbttdailyforexrates"]
	if !ok {
		return nil, fmt.Errorf("no cbttdailyforexrates in payload from %s", baseURL)
	}
	// A span with no rows comes back as a single all-null object rather than an empty array.
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var rows []map[string]any
	if err := dec.Decode(&rows); err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, row := range rows {
		famedate, ok := row["famedate"].(string)
		if !ok {
			return nil, errors.New("row without famedate")
		}
		date, err := time.Parse(time.DateOnly, famedate)
		if err != nil {
			return nil, err
		}
		for _, c := range currencies {
			buy, err := price(row[c.prefix+"_Buying"])
			if err != nil {
				return nil, err
			}
			sell, err := price(row[c.prefix+"_Selling"])
			if err != nil {
				return nil, err
			}
			if buy == nil || sell == nil {
				continue
			}
			rate := adapter.Midpoint(*buy, *sell)
			if rate == 0 {
				continue
			}
			rates = append(rates, adapter.Rate{Date: date, Base: c.code, Quote: "TTD", Rate: rate, Bid: buy, Ask: sell})
		}
	}
	return rates, nil
}

// price reads a leg that may be null, a string or a number, failing on anything else as BigDecimal() raises.
func price(v any) (*float64, error) {
	var s string
	switch v := v.(type) {
	case nil:
		return nil, nil
	case string:
		s = v
	case json.Number:
		s = v.String()
	default:
		return nil, fmt.Errorf("invalid price %v", v)
	}
	f, ok := adapter.ParseFloat(s)
	if !ok {
		return nil, fmt.Errorf("invalid price %q", s)
	}
	return &f, nil
}
