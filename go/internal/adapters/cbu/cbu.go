// Package cbu fetches rates from the Central Bank of Uzbekistan, which
// publishes daily rates for 20+ currencies against UZS through a per-day JSON
// endpoint.
//
// Fetch treats after as inclusive, as the Ruby adapter's (after..end_date)
// range does.
package cbu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://cbu.uz/en/arkhiv-kursov-valyut/json/all/"

// The RUB series is not restated across the 1998 redenomination: 1000 "RUB" =
// 13.46 UZS in the 1997-12-30 bulletin, 1 RUB = 13.48 in the next one on
// 1998-01-06, CBU's first new-ruble bulletin. Earlier rows are old ruble (RUR).
var predecessors = map[string]adapter.Predecessor{
	"RUB": {Code: "RUR", Cutover: adapter.Date(1998, 1, 6)},
}

// Bulletins up to 2008-09-16 list the SDR under its own label (code 001).
// That day's bulletin also carries XDR (code 960) at the same rate, and later
// ones only XDR.
var aliases = map[string]string{"SDR": "XDR"}

var codeRE = regexp.MustCompile(`\A[A-Z]{3}\z`)

func init() {
	adapter.Register("CBU", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBU rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 30 }

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
	first := true
	for date := after; !date.After(end); date = date.AddDate(0, 0, 1) {
		if wd := date.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		if !first {
			if err := a.Sleep(ctx, 200*time.Millisecond); err != nil {
				return nil, err
			}
		}
		first = false

		body, err := a.Get(ctx, baseURL+date.Format("2006-01-02")+"/", nil)
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

// text accepts a JSON string or a bare number, as Ruby's Integer() and Float()
// do.
type text string

func (t *text) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*t = text(s)
		return nil
	}
	*t = text(bytes.TrimSpace(b))
	return nil
}

type row struct {
	Ccy     *string `json:"Ccy"`
	Nominal text    `json:"Nominal"`
	Rate    text    `json:"Rate"`
	Date    string  `json:"Date"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var rows []row
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("expected JSON array: %w", err)
	}
	var rates []adapter.Rate
	for _, r := range rows {
		if r.Ccy == nil || !codeRE.MatchString(*r.Ccy) {
			continue
		}
		// Base 0 reads prefixes and a leading 0 as octal, as Ruby's Integer()
		// does.
		nominal, err := strconv.ParseInt(strings.TrimSpace(string(r.Nominal)), 0, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid nominal %q", r.Nominal)
		}
		rate, ok := adapter.ParseFloat(string(r.Rate))
		if !ok {
			return nil, fmt.Errorf("invalid rate %q", r.Rate)
		}
		if rate == 0 || nominal == 0 {
			continue
		}
		date, err := time.Parse("2.1.2006", r.Date)
		if err != nil {
			return nil, err
		}
		code := *r.Ccy
		if alias, ok := aliases[code]; ok {
			code = alias
		}
		rates = append(rates, adapter.Rate{
			Date:  date,
			Base:  adapter.HistoricalCode(predecessors, code, date),
			Quote: "UZS",
			Rate:  rate / float64(nominal),
		})
	}
	return rates, nil
}
