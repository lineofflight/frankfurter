// Package nbe fetches rates from the National Bank of Ethiopia, which publishes
// daily reference rates for 18 currencies against the Ethiopian birr (ETB) on
// weekdays. The API returns buying, selling and weighted_average per currency
// for a given date; weighted_average is the mid.
//
// XDR is published under its ISO 4217 code and passes through untouched.
// Coverage starts 2024-10-01 to skip the July to September 2024
// float-transition gap.
//
// As in Ruby, Fetch requests every weekday from after through upto, both
// inclusive (one request per day), and returns every row the API gives without
// further clipping.
package nbe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://api.nbe.gov.et/api/filter-exchange-rates"

func init() {
	adapter.Register("NBE", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches NBE rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 30 }

// Fetch implements adapter.Adapter. A zero after is an error, as Ruby's
// nil..date range raises.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("fetch needs a start date")
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}

	var rates []adapter.Rate
	first := true
	for d := after; !d.After(end); d = d.AddDate(0, 0, 1) {
		if wd := d.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		if !first {
			if err := a.Sleep(ctx, 200*time.Millisecond); err != nil {
				return nil, err
			}
		}
		first = false

		body, err := a.Get(ctx, baseURL, url.Values{"date": {d.Format("2006-01-02")}})
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
	Date            string `json:"date"`
	WeightedAverage any    `json:"weighted_average"`
	Currency        *struct {
		Code *string `json:"code"`
	} `json:"currency"`
}

var codePattern = regexp.MustCompile(`\A[A-Z]{3}\z`)

func parse(data []byte) ([]adapter.Rate, error) {
	var doc struct {
		Data *[]entry `json:"data"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Data == nil {
		return nil, errors.New("missing data array in response")
	}

	var rates []adapter.Rate
	for _, e := range *doc.Data {
		if e.Currency == nil || e.Currency.Code == nil || !codePattern.MatchString(*e.Currency.Code) {
			continue
		}
		rate := toF(e.WeightedAverage)
		if rate == 0 {
			continue
		}
		date, err := time.Parse("2006-01-02", e.Date)
		if err != nil {
			return nil, fmt.Errorf("parse date %q: %w", e.Date, err)
		}
		rates = append(rates, adapter.Rate{Date: date, Base: *e.Currency.Code, Quote: "ETB", Rate: rate})
	}
	return rates, nil
}

var leadingFloat = regexp.MustCompile(`^\s*[+-]?(\d+(\.\d+)?|\.\d+)([eE][+-]?\d+)?`)

// toF mirrors Ruby's to_f on a decoded JSON value: a string yields its leading
// number or 0, nil yields 0.
func toF(v any) float64 {
	switch v := v.(type) {
	case float64:
		return v
	case string:
		f, _ := strconv.ParseFloat(leadingFloat.FindString(v), 64)
		return f
	}
	return 0
}
