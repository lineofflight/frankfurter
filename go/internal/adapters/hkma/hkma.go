// Package hkma fetches rates from the Hong Kong Monetary Authority, which publishes daily HKD exchange rates for 17
// currencies through a public JSON API.
//
// Rates are HKD per 1 unit of foreign currency (base = foreign currency, quote = HKD). Data is published monthly with
// roughly a 1-month lag. History goes back to 1981-01-02. The API's from bound is inclusive and, as in Ruby, rows are
// not clipped further, so a row dated on after is returned.
package hkma

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	baseURL  = "https://api.hkma.gov.hk/public/market-data-and-statistics/monthly-statistical-bulletin/er-ir/er-eeri-daily"
	pageSize = 100
)

var currencyFields = []string{
	"usd", "eur", "gbp", "jpy", "cad", "aud", "sgd", "twd", "chf",
	"cny", "krw", "thb", "myr", "php", "inr", "idr", "zar",
}

func init() {
	adapter.Register("HKMA", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches HKMA rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 365 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if upto.IsZero() {
		upto = a.Today()
	}

	var records []map[string]any
	for offset := 0; ; offset += pageSize {
		page, err := a.fetchPage(ctx, after, upto, offset)
		if err != nil {
			return nil, err
		}
		records = append(records, page...)
		if len(page) < pageSize {
			break
		}
	}
	return parse(records)
}

func (a *Adapter) fetchPage(ctx context.Context, from, to time.Time, offset int) ([]map[string]any, error) {
	body, err := a.Get(ctx, baseURL, url.Values{
		"choose":    {"end_of_day"},
		"from":      {day(from)},
		"to":        {day(to)},
		"pagesize":  {strconv.Itoa(pageSize)},
		"offset":    {strconv.Itoa(offset)},
		"sortby":    {"end_of_day"},
		"sortorder": {"desc"},
	})
	if err != nil {
		return nil, err
	}
	var data struct {
		Result struct {
			Records []map[string]any `json:"records"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return data.Result.Records, nil
}

// day formats a date as Ruby's Date#to_s, with nil (zero) as the empty string.
func day(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

func parse(records []map[string]any) ([]adapter.Rate, error) {
	var rates []adapter.Rate
	for _, record := range records {
		s, ok := record["end_of_day"].(string)
		if !ok {
			continue
		}
		date, err := adapter.ParseDate(s, "2006-01-02")
		if err != nil {
			return nil, err
		}
		for _, field := range currencyFields {
			value, ok := record[field].(float64)
			if !ok || value <= 0 {
				continue
			}
			rates = append(rates, adapter.Rate{Date: date, Base: strings.ToUpper(field), Quote: "HKD", Rate: value})
		}
	}
	return rates, nil
}
