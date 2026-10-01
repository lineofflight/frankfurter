// Package bam fetches rates from Bank Al-Maghrib, which publishes daily
// mid-market rates for about 30 currencies against MAD, one request per
// business day.
//
// Older data (pre-2016) lacks a mid-rate field; the adapter averages buy/sell.
// As in Ruby, after is inclusive: every weekday from after through upto is
// requested.
package bam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://api.centralbankofmorocco.ma/cours/Version1/api/CoursVirement"

var currencyCode = regexp.MustCompile(`\A[A-Z]{3}\z`)

// The ouguiya keeps its MRO label after the 2018 redenomination: 100 MRO =
// 2.624 MAD on 2018-01-02 and 26.309 on 2018-01-03, with the new ouguiya at
// 0.263 MAD. It is quoted per 100 throughout, but on 53 days in 2018 the unit
// field reads 1 (25.857 MAD on 2018-04-17, against 25.864 per 100 the day
// before).
var successors = map[string]adapter.Successor{"MRO": {Code: "MRU", Cutover: adapter.Date(2018, 1, 3)}}

func init() {
	adapter.Register("BAM", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BAM rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 7 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("fetch needs a start date")
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	var rates []adapter.Rate
	for date := after; !date.After(end); date = date.AddDate(0, 0, 1) {
		if wd := date.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		day, err := a.fetchDate(ctx, date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, day...)
	}
	return rates, nil
}

func (a *Adapter) fetchDate(ctx context.Context, date time.Time) ([]adapter.Rate, error) {
	key := os.Getenv("BAM_API_KEY")
	if key == "" {
		return nil, errors.New("no API key")
	}
	query := url.Values{"date": {date.Format("2006-01-02") + "T12:30:00"}}
	req, err := a.NewRequest(ctx, http.MethodGet, baseURL+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", key)
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	if err := a.Sleep(ctx, time.Second); err != nil {
		return nil, err
	}
	return parse(resp.Body)
}

type record struct {
	Date        string   `json:"date"`
	LibDevise   *string  `json:"libDevise"`
	Moyen       *float64 `json:"moyen"`
	Achat       *float64 `json:"achat"`
	Vente       *float64 `json:"vente"`
	UniteDevise float64  `json:"uniteDevise"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if _, ok := raw.([]any); !ok {
		return nil, fmt.Errorf("expected JSON array from %s, got %T", baseURL, raw)
	}
	var records []record
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, r := range records {
		if r.LibDevise == nil || !currencyCode.MatchString(*r.LibDevise) {
			continue
		}
		date, err := parseDate(r.Date)
		if err != nil {
			return nil, err
		}
		var mid float64
		if r.Moyen != nil {
			mid = *r.Moyen
		} else {
			mid = adapter.Midpoint(value(r.Achat), value(r.Vente))
		}
		unit := r.UniteDevise
		if *r.LibDevise == "MRO" && unit == 1 {
			unit = 100
		}
		if mid == 0 || unit == 0 {
			continue
		}
		rates = append(rates, adapter.Rate{
			Date:  date,
			Base:  adapter.SuccessorCode(successors, *r.LibDevise, date),
			Quote: "MAD",
			Rate:  mid / unit,
			Bid:   perUnit(r.Achat, unit),
			Ask:   perUnit(r.Vente, unit),
			Mid:   perUnit(r.Moyen, unit),
		})
	}
	return rates, nil
}

// parseDate keeps the calendar date of a timestamp like 2026-03-25T12:30:00, as
// Ruby's Date.parse does.
func parseDate(s string) (time.Time, error) {
	t, err := adapter.ParseDate(s, "2006-01-02T15:04:05", time.RFC3339, "2006-01-02")
	if err != nil {
		return time.Time{}, err
	}
	return adapter.Date(t.Year(), t.Month(), t.Day()), nil
}

// value is Ruby's nil.to_f: a missing price counts as zero.
func value(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func perUnit(p *float64, unit float64) *float64 {
	if p == nil {
		return nil
	}
	return adapter.Float(adapter.PerUnit(*p, unit))
}
