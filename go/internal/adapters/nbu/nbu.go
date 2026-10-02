// Package nbu fetches rates from the National Bank of Ukraine, which publishes
// daily rates for about 45 currencies against the hryvnia.
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

// The TJS series starts in 1999 under the Tajikistani ruble, which the somoni
// replaced at 1000:1 on 2000-10-30, and is not restated: 1000 "TJS" = 2.78 UAH
// on 2000-09-01 against 1 TJS = 1.81 in 2002. Earlier rows are TJR.
var predecessors = map[string]adapter.Predecessor{
	"TJS": {Code: "TJR", Cutover: adapter.Date(2000, 10, 30)},
}

// The archive keeps six retired codes after their redenominations and quotes
// the successor under them, until the current codes replace them in April 2014
// (RUB in 2004). Each entry is the first date in the new unit, which can trail
// the official one by a few days.
var successors = map[string]adapter.Successor{
	"RUR": {Code: "RUB", Cutover: adapter.Date(1998, 1, 1)},
	"BGL": {Code: "BGN", Cutover: adapter.Date(1999, 8, 1)},
	"TRL": {Code: "TRY", Cutover: adapter.Date(2005, 1, 6)},
	"ROL": {Code: "RON", Cutover: adapter.Date(2005, 7, 1)},
	"AZM": {Code: "AZN", Cutover: adapter.Date(2006, 1, 6)},
	"TMM": {Code: "TMT", Cutover: adapter.Date(2009, 1, 6)},
}

// From these dates the rate is per 100 units of the successor while the units
// field still reads 1000 or 10000: 10000 "TRL" = 372.9741 UAH on 2005-06-27,
// with the new lira at 3.73 UAH, and 100 TRY = 541.6837 when the label changes
// on 2014-04-04.
var perHundred = map[string]time.Time{
	"BGL": adapter.Date(2000, 1, 1),
	"TRL": adapter.Date(2005, 1, 6),
	"ROL": adapter.Date(2005, 7, 1),
	"AZM": adapter.Date(2006, 1, 6),
	"TMM": adapter.Date(2009, 1, 6),
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

// Fetch implements adapter.Adapter. Like the Ruby adapter, it returns what the
// server sends for the requested range without clipping it further.
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
		if from, ok := perHundred[iso]; ok && !date.Before(from) {
			units = 100
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
			Base:  adapter.SuccessorCode(successors, adapter.HistoricalCode(predecessors, iso, date), date),
			Quote: "UAH",
			Rate:  rate / units,
		})
	}
	return rates, nil
}

var numericPrefix = regexp.MustCompile(`\A\s*[+-]?(?:\d+(?:\.\d+)?|\.\d+)(?:[eE][+-]?\d+)?`)

// toF mirrors Ruby's to_f on a JSON value: numbers convert, strings parse their
// leading number (0 if none), null is 0, and anything else is an error, as to_f
// is undefined on it.
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
