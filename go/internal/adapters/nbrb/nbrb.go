// Package nbrb fetches rates from the National Bank of the Republic of Belarus,
// which publishes daily rates for about 30 currencies against BYN.
//
// Rates are keyed by an internal currency ID, and NBRB issues a new ID when a
// currency's terms change: it renumbered its currencies on 2021-07-09 (USD 145
// became 431), and BRL moved from a monthly to a daily ID on 2022-08-01. Each
// ID answers only for its own validity, so the currency reference, which lists
// every ID with its dates, scale and periodicity, is what reaches back past the
// latest renumbering.
//
// Fetch asks the dynamics endpoint for every daily ID within its own validity,
// in chunks of up to a year. Like the Ruby adapter, the range starts at after
// inclusive, and never before the BYN redenomination.
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
	baseURL = "https://api.nbrb.by/exrates"
	// The dynamics endpoint silently truncates longer ranges to 365 days.
	chunkDays = 365
)

// redenomination is when BYN replaced BYR at 10,000:1. IDs that predate it
// return BYR values before this date.
var redenomination = adapter.Date(2016, 7, 1)

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

// BackfillRange implements adapter.Adapter: one dynamics chunk per window.
func (a *Adapter) BackfillRange() int { return chunkDays }

// currency is one daily ID from the currency reference, valid from From
// through To.
type currency struct {
	ID       int
	ISO      string
	Scale    int
	From, To time.Time
}

type reference struct {
	CurID           *int    `json:"Cur_ID"`
	CurAbbreviation *string `json:"Cur_Abbreviation"`
	CurScale        *int    `json:"Cur_Scale"`
	CurPeriodicity  *int    `json:"Cur_Periodicity"`
	CurDateStart    *string `json:"Cur_DateStart"`
	CurDateEnd      *string `json:"Cur_DateEnd"`
}

type row struct {
	Date            *string  `json:"Date"`
	CurOfficialRate *float64 `json:"Cur_OfficialRate"`
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	start := redenomination
	if after.After(start) {
		start = after
	}
	stop := upto
	if stop.IsZero() {
		stop = a.Today()
	}
	currencies, err := a.dailyCurrencies(ctx)
	if err != nil {
		return nil, err
	}
	var rates []adapter.Rate
	for _, c := range currencies {
		from, to := start, stop
		if c.From.After(from) {
			from = c.From
		}
		if c.To.Before(to) {
			to = c.To
		}
		for chunkStart := from; !chunkStart.After(to); {
			chunkEnd := chunkStart.AddDate(0, 0, chunkDays-1)
			if chunkEnd.After(to) {
				chunkEnd = to
			}
			chunk, err := a.fetchDynamics(ctx, c, chunkStart, chunkEnd)
			if err != nil {
				return nil, err
			}
			rates = append(rates, chunk...)
			chunkStart = chunkEnd.AddDate(0, 0, 1)
		}
	}
	return rates, nil
}

func (a *Adapter) dailyCurrencies(ctx context.Context) ([]currency, error) {
	body, err := a.Get(ctx, baseURL+"/currencies", nil)
	if err != nil {
		return nil, err
	}
	return parseCurrencies(body)
}

// parseCurrencies keeps the daily IDs (periodicity 0) of the currency
// reference.
func parseCurrencies(data []byte) ([]currency, error) {
	var refs []reference
	if err := json.Unmarshal(data, &refs); err != nil {
		return nil, err
	}
	var currencies []currency
	for _, r := range refs {
		if r.CurPeriodicity == nil {
			return nil, errors.New("currency missing Cur_Periodicity")
		}
		if *r.CurPeriodicity != 0 {
			continue
		}
		if r.CurID == nil || r.CurAbbreviation == nil || r.CurScale == nil || r.CurDateStart == nil ||
			r.CurDateEnd == nil {
			return nil, errors.New("currency missing Cur_ID, Cur_Abbreviation, Cur_Scale, Cur_DateStart or Cur_DateEnd")
		}
		from, err := parseDate(*r.CurDateStart)
		if err != nil {
			return nil, err
		}
		to, err := parseDate(*r.CurDateEnd)
		if err != nil {
			return nil, err
		}
		currencies = append(currencies, currency{*r.CurID, *r.CurAbbreviation, *r.CurScale, from, to})
	}
	return currencies, nil
}

func parseDate(s string) (time.Time, error) {
	return adapter.ParseDate(s, "2006-01-02T15:04:05", time.DateOnly)
}

func (a *Adapter) fetchDynamics(ctx context.Context, c currency, start, end time.Time) ([]adapter.Rate, error) {
	body, err := a.Get(ctx, fmt.Sprintf("%s/rates/dynamics/%d", baseURL, c.ID), url.Values{
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
	return parseDate(*r.Date)
}

func weekend(d time.Time) bool {
	return d.Weekday() == time.Saturday || d.Weekday() == time.Sunday
}
