// Package nbu fetches rates from the National Bank of Ukraine, which publishes daily rates for about 45 currencies
// against the hryvnia.
package nbu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://bank.gov.ua/NBU_Exchange/exchange_site"

// The TJS series starts in 1999 under the Tajikistani ruble, which the somoni replaced at 1000:1 on 2000-10-30, and
// is not restated: 1000 "TJS" = 2.78 UAH on 2000-09-01 against 1 TJS = 1.81 in 2002. Earlier rows are TJR.
var predecessors = map[string]adapter.Predecessor{
	"TJS": {Code: "TJR", Cutover: adapter.Date(2000, 10, 30)},
}

var isoCode = regexp.MustCompile(`\A[A-Z]{3}\z`)

func init() {
	adapter.Register("NBU", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches NBU rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 365 }

// Fetch implements adapter.Adapter. Like the Ruby adapter, it returns what the server sends for the requested range
// without clipping it further.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("start date required")
	}
	if upto.IsZero() {
		upto = a.Today()
	}
	body, err := a.Get(ctx, baseURL, url.Values{
		"start": {after.Format("20060102")},
		"end":   {upto.Format("20060102")},
		"sort":  {"exchangedate"},
		"order": {"asc"},
		"json":  {""},
	})
	if err != nil {
		return nil, err
	}
	return parse(body)
}

type record struct {
	ExchangeDate *string         `json:"exchangedate"`
	CC           *string         `json:"cc"`
	Units        json.RawMessage `json:"units"`
	Rate         json.RawMessage `json:"rate"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var records []record
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("expected JSON array from %s: %w", baseURL, err)
	}

	var rates []adapter.Rate
	for _, r := range records {
		if r.ExchangeDate == nil || r.CC == nil || r.Rate == nil {
			return nil, errors.New("record missing exchangedate, cc or rate")
		}
		date, err := time.Parse("2.1.2006", *r.ExchangeDate)
		if err != nil {
			return nil, err
		}
		if wd := date.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		iso := *r.CC
		if !isoCode.MatchString(iso) {
			continue
		}
		units := 1.0
		if r.Units != nil {
			if units, err = toF(r.Units); err != nil {
				return nil, err
			}
		}
		rate, err := toF(r.Rate)
		if err != nil {
			return nil, err
		}
		if rate == 0 || units == 0 {
			continue
		}
		rates = append(rates, adapter.Rate{
			Date:  date,
			Base:  adapter.HistoricalCode(predecessors, iso, date),
			Quote: "UAH",
			Rate:  rate / units,
		})
	}
	return rates, nil
}

var numericPrefix = regexp.MustCompile(`\A\s*[+-]?(?:\d+(?:\.\d+)?|\.\d+)(?:[eE][+-]?\d+)?`)

// toF mirrors Ruby's to_f on a JSON value: numbers convert, strings parse their leading number (0 if none), null is 0,
// and anything else is an error, as to_f is undefined on it.
func toF(raw json.RawMessage) (float64, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, err
	}
	switch v := v.(type) {
	case nil:
		return 0, nil
	case float64:
		return v, nil
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(numericPrefix.FindString(v)), 64)
		return f, nil
	}
	return 0, fmt.Errorf("non-numeric value %s", raw)
}
