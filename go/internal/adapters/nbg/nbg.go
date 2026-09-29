// Package nbg fetches rates from the National Bank of Georgia, which publishes daily rates for 40+ currencies
// against the Georgian lari (GEL).
//
// The API is queried one date at a time, Sundays skipped. As in Ruby, after is inclusive, rows are not clipped to
// the window, and each row carries the date the API reports, which can precede the requested one.
package nbg

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://nbg.gov.ge/gw/api/ct/monetarypolicy/currencies/"

var codePattern = regexp.MustCompile(`\A[A-Z]{3}\z`)

func init() {
	adapter.Register("NBG", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches NBG rates.
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
		return nil, errors.New("start date required")
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}

	var rates []adapter.Rate
	first := true
	for date := after; !date.After(end); date = date.AddDate(0, 0, 1) {
		if date.Weekday() == time.Sunday {
			continue
		}
		if !first {
			if err := a.Sleep(ctx, 200*time.Millisecond); err != nil {
				return nil, err
			}
		}
		first = false

		body, err := a.Get(ctx, baseURL, url.Values{"date": {date.Format("2006-01-02")}})
		if err != nil {
			return nil, err
		}
		day, err := parse(body)
		if err != nil {
			return nil, err
		}
		rates = append(rates, day...)
	}
	return rates, nil
}

type entry struct {
	Date       string `json:"date"`
	Currencies []struct {
		Code     any `json:"code"`
		Quantity any `json:"quantity"`
		Rate     any `json:"rate"`
	} `json:"currencies"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var entries []entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, errors.New("expected JSON array")
	}
	// The API 200s with [] for dates without data (e.g. pre-coverage); carry-forward covers holidays.
	if len(entries) == 0 {
		return nil, nil
	}

	e := entries[0]
	date, err := adapter.ParseDate(e.Date[:min(len(e.Date), 10)], "2006-01-02")
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, cur := range e.Currencies {
		code, ok := cur.Code.(string)
		if !ok || !codePattern.MatchString(code) {
			continue
		}
		quantity, rate := toF(cur.Quantity), toF(cur.Rate)
		if rate == 0 || quantity == 0 {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: code, Quote: "GEL", Rate: rate / quantity})
	}
	return rates, nil
}

// toF approximates Ruby's to_f on a decoded JSON value: numbers pass through, numeric strings parse, anything else
// is 0.
func toF(v any) float64 {
	switch v := v.(type) {
	case float64:
		return v
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0
		}
		return f
	}
	return 0
}
