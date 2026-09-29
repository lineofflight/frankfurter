// Package nrb fetches rates from Nepal Rastra Bank, which publishes daily buy and sell rates for 22 currencies against
// NPR through a paginated JSON API.
//
// The rate is the midpoint of buy and sell. Currencies quoted per several units (JPY per 10, say) are divided down to
// one unit.
package nrb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.nrb.org.np/api/forex/v1/rates"

func init() {
	adapter.Register("NRB", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches NRB rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 90 }

type response struct {
	Data struct {
		Payload json.RawMessage `json:"payload"`
	} `json:"data"`
	Pagination struct {
		Pages *int `json:"pages"`
	} `json:"pagination"`
}

// Fetch implements adapter.Adapter. The API filters by date itself, so rows are not clipped again.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if upto.IsZero() {
		upto = a.Today()
	}
	from := ""
	if !after.IsZero() {
		from = after.Format(time.DateOnly)
	}

	var rates []adapter.Rate
	for page := 1; ; page++ {
		if page > 1 {
			if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
				return nil, err
			}
		}
		body, err := a.Get(ctx, baseURL, url.Values{
			"from":     {from},
			"to":       {upto.Format(time.DateOnly)},
			"page":     {strconv.Itoa(page)},
			"per_page": {"100"},
		})
		if err != nil {
			return nil, err
		}
		var resp response
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, err
		}
		parsed, err := parse(resp.Data.Payload)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)

		pages := 1
		if resp.Pagination.Pages != nil {
			pages = *resp.Pagination.Pages
		}
		if page >= pages {
			return rates, nil
		}
	}
}

type day struct {
	Date  *string `json:"date"`
	Rates []entry `json:"rates"`
}

type entry struct {
	Currency struct {
		ISO3 string `json:"iso3"`
		Unit any    `json:"unit"`
	} `json:"currency"`
	Buy  any `json:"buy"`
	Sell any `json:"sell"`
}

// parse reads the API's payload: an array of days, or a JSON string holding one.
func parse(payload []byte) ([]adapter.Rate, error) {
	payload = bytes.TrimSpace(payload)
	if len(payload) > 0 && payload[0] == '"' {
		var s string
		if err := json.Unmarshal(payload, &s); err != nil {
			return nil, err
		}
		payload = []byte(s)
	}
	if string(payload) == "null" {
		return nil, errors.New("expected payload array from forex API, got null")
	}
	var days []day
	if err := json.Unmarshal(payload, &days); err != nil {
		return nil, fmt.Errorf("expected payload array from forex API: %w", err)
	}

	var rates []adapter.Rate
	for _, d := range days {
		if d.Date == nil {
			return nil, errors.New("missing date in payload")
		}
		date, err := time.Parse(time.DateOnly, *d.Date)
		if err != nil {
			continue
		}
		for _, e := range d.Rates {
			unit, err := integer(e.Currency.Unit)
			if err != nil {
				return nil, err
			}
			// Ruby divides by a zero unit into Infinity; skip the row instead.
			if e.Buy == nil || e.Sell == nil || unit == 0 {
				continue
			}
			buy, err := float(e.Buy)
			if err != nil {
				return nil, err
			}
			sell, err := float(e.Sell)
			if err != nil {
				return nil, err
			}
			rate := adapter.Midpoint(buy, sell) / float64(unit)
			if rate == 0 {
				continue
			}
			rates = append(rates, adapter.Rate{
				Date:  date,
				Base:  e.Currency.ISO3,
				Quote: "NPR",
				Rate:  rate,
				Bid:   adapter.Float(adapter.PerUnit(buy, float64(unit))),
				Ask:   adapter.Float(adapter.PerUnit(sell, float64(unit))),
			})
		}
	}
	return rates, nil
}

// integer is Ruby's Integer(v || 1) on a decoded JSON value.
func integer(v any) (int, error) {
	switch v := v.(type) {
	case nil:
		return 1, nil
	case float64:
		return int(v), nil
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, fmt.Errorf("invalid unit %q", v)
		}
		return n, nil
	}
	return 0, fmt.Errorf("invalid unit %v", v)
}

// float is Ruby's Float(v) on a decoded JSON value.
func float(v any) (float64, error) {
	switch v := v.(type) {
	case float64:
		return v, nil
	case string:
		if f, ok := adapter.ParseFloat(v); ok {
			return f, nil
		}
	}
	return 0, fmt.Errorf("invalid price %v", v)
}
