// Package nbrb fetches rates from the National Bank of the Republic of Belarus,
// which publishes daily rates for about 30 currencies against BYN.
//
// BYN was redenominated on 2016-07-01; earlier data uses different currency
// IDs. Fetch lists today's currencies, then asks the dynamics endpoint for each
// one in chunks of up to a year. Like the Ruby adapter, the range starts at
// after inclusive.
package nbrb

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

const (
	ratesURL  = "https://api.nbrb.by/exrates/rates"
	chunkDays = 365
)

func init() {
	adapter.Register("NBRB", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches NBRB rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

type currency struct {
	ID    int
	ISO   string
	Scale int
}

type row struct {
	CurID           *int     `json:"Cur_ID"`
	Date            *string  `json:"Date"`
	CurAbbreviation *string  `json:"Cur_Abbreviation"`
	CurScale        *int     `json:"Cur_Scale"`
	CurOfficialRate *float64 `json:"Cur_OfficialRate"`
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("a start date is required")
	}
	if upto.IsZero() {
		upto = a.Today()
	}
	currencies, err := a.currentCurrencies(ctx)
	if err != nil {
		return nil, err
	}
	var rates []adapter.Rate
	for _, c := range currencies {
		for start := after; !start.After(upto); {
			end := start.AddDate(0, 0, chunkDays-1)
			if end.After(upto) {
				end = upto
			}
			chunk, err := a.fetchDynamics(ctx, c, start, end)
			if err != nil {
				return nil, err
			}
			rates = append(rates, chunk...)
			start = end.AddDate(0, 0, 1)
		}
	}
	return rates, nil
}

func (a *Adapter) currentCurrencies(ctx context.Context) ([]currency, error) {
	body, err := a.Get(ctx, ratesURL, url.Values{"periodicity": {"0"}})
	if err != nil {
		return nil, err
	}
	var rows []row
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, err
	}
	currencies := make([]currency, 0, len(rows))
	for _, r := range rows {
		if r.CurID == nil || r.CurAbbreviation == nil || r.CurScale == nil {
			return nil, errors.New("currency row missing Cur_ID, Cur_Abbreviation or Cur_Scale")
		}
		currencies = append(currencies, currency{*r.CurID, *r.CurAbbreviation, *r.CurScale})
	}
	return currencies, nil
}

func (a *Adapter) fetchDynamics(ctx context.Context, c currency, start, end time.Time) ([]adapter.Rate, error) {
	body, err := a.Get(ctx, fmt.Sprintf("%s/dynamics/%d", ratesURL, c.ID), url.Values{
		"startDate": {start.Format(time.DateOnly)},
		"endDate":   {end.Format(time.DateOnly)},
	})
	if err != nil {
		return nil, err
	}
	return parseDynamics(body, c)
}

func parseDynamics(data []byte, c currency) ([]adapter.Rate, error) {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if _, ok := raw.([]any); !ok {
		return nil, fmt.Errorf("expected JSON array from dynamics endpoint, got %T", raw)
	}
	var rows []row
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	var rates []adapter.Rate
	for _, r := range rows {
		date, err := rowDate(r)
		if err != nil {
			return nil, err
		}
		if weekend(date) {
			continue
		}
		if r.CurOfficialRate == nil {
			return nil, errors.New("row missing Cur_OfficialRate")
		}
		rates = append(rates, adapter.Rate{Date: date, Base: c.ISO, Quote: "BYN", Rate: *r.CurOfficialRate / float64(c.Scale)})
	}
	return rates, nil
}

func rowDate(r row) (time.Time, error) {
	if r.Date == nil {
		return time.Time{}, errors.New("row missing Date")
	}
	return adapter.ParseDate(*r.Date, "2006-01-02T15:04:05", time.DateOnly)
}

func weekend(d time.Time) bool {
	return d.Weekday() == time.Saturday || d.Weekday() == time.Sunday
}
