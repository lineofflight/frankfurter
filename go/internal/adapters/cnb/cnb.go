// Package cnb fetches rates from the Czech National Bank, which publishes daily
// exchange rates for about 30 currencies against the Czech koruna (CZK) through
// a REST JSON API.
//
// Unlike most adapters, Fetch treats after as inclusive, as the Ruby adapter
// does (Date#between?).
package cnb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://api.cnb.cz/cnbapi/exrates/daily-year"

var codePattern = regexp.MustCompile(`\A[A-Z]{3}\z`)

func init() {
	adapter.Register("CNB", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CNB rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter. It requests one year at a time and keeps
// rows dated after through upto, both inclusive.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("fetch needs a start date")
	}
	if upto.IsZero() {
		upto = a.Today()
	}

	var rates []adapter.Rate
	for year := after.Year(); year <= upto.Year(); year++ {
		body, err := a.Get(ctx, baseURL, url.Values{"year": {strconv.Itoa(year)}, "lang": {"EN"}})
		if err != nil {
			return nil, err
		}
		parsed, err := parse(body)
		if err != nil {
			return nil, err
		}
		for _, r := range parsed {
			if !r.Date.Before(after) && !r.Date.After(upto) {
				rates = append(rates, r)
			}
		}
	}
	return rates, nil
}

type response struct {
	Rates *[]struct {
		ValidFor     string  `json:"validFor"`
		CurrencyCode string  `json:"currencyCode"`
		Amount       float64 `json:"amount"`
		Rate         float64 `json:"rate"`
	} `json:"rates"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var resp response
	if err := json.Unmarshal(data, &resp); err != nil || resp.Rates == nil {
		return nil, errors.New("no rates array in daily-year response")
	}

	// The API 200s with {"rates":[]} for a year before its first fixing (e.g.
	// New Year's Day).
	var rates []adapter.Rate
	for _, r := range *resp.Rates {
		if !codePattern.MatchString(r.CurrencyCode) || r.Rate == 0 || r.Amount == 0 {
			continue
		}
		date, err := time.Parse(time.DateOnly, r.ValidFor)
		if err != nil {
			return nil, err
		}
		rates = append(rates, adapter.Rate{Date: date, Base: r.CurrencyCode, Quote: "CZK", Rate: r.Rate / r.Amount})
	}
	return rates, nil
}
