// Package boj fetches rates from the Bank of Japan, which publishes daily Tokyo-market spot rates for USD/JPY and
// EUR/USD through its Statistics API. No authentication is required, and these two pairs are all the API offers.
//
// Unlike most adapters, Fetch includes rows dated on after itself, as the Ruby adapter's between? does.
package boj

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const apiURL = "https://www.stat-search.boj.or.jp/api/v1/getDataCode"

type pair struct{ base, quote string }

var series = map[string]pair{
	"FXERD04": {"USD", "JPY"},
	"FXERD34": {"EUR", "USD"},
}

// seriesCodes keeps Ruby's SERIES key order for the code parameter.
var seriesCodes = []string{"FXERD04", "FXERD34"}

func init() {
	adapter.Register("BOJ", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BOJ rates.
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
		return nil, errors.New("after date is required")
	}
	if upto.IsZero() {
		upto = a.Today()
	}
	body, err := a.Get(ctx, apiURL, url.Values{
		"format":    {"json"},
		"lang":      {"en"},
		"db":        {"FM08"},
		"code":      {strings.Join(seriesCodes, ",")},
		"startDate": {after.Format("200601")},
		"endDate":   {upto.Format("200601")},
	})
	if err != nil {
		return nil, err
	}
	rates, err := parse(body)
	if err != nil {
		return nil, err
	}
	var out []adapter.Rate
	for _, r := range rates {
		if !r.Date.Before(after) && !r.Date.After(upto) {
			out = append(out, r)
		}
	}
	return out, nil
}

type response struct {
	ResultSet []struct {
		SeriesCode string `json:"SERIES_CODE"`
		Values     struct {
			Dates  []any `json:"SURVEY_DATES"`
			Values []any `json:"VALUES"`
		} `json:"VALUES"`
	} `json:"RESULTSET"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var resp response
	if err := dec.Decode(&resp); err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, s := range resp.ResultSet {
		meta, ok := series[s.SeriesCode]
		if !ok {
			continue
		}
		for i, rawDate := range s.Values.Dates {
			// Ruby zips dates with values, so a missing value is nil.
			if i >= len(s.Values.Values) || s.Values.Values[i] == nil {
				continue
			}
			value := fmt.Sprint(s.Values.Values[i])
			rate, ok := adapter.ParseFloat(value)
			if !ok {
				return nil, fmt.Errorf("invalid value %q for %s", value, s.SeriesCode)
			}
			if rate == 0 {
				continue
			}
			date, err := time.Parse("20060102", fmt.Sprint(rawDate))
			if err != nil {
				return nil, err
			}
			rates = append(rates, adapter.Rate{Date: date, Base: meta.base, Quote: meta.quote, Rate: rate})
		}
	}
	return rates, nil
}
