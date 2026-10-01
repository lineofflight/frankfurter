// Package lb fetches rates from the Bank of Lithuania (Lietuvos Bankas), which
// publishes daily exchange rates for about 88 currencies. Pre-2015 rates are
// quoted against LTL (Lithuanian litas); post-2015 rates are EUR-based (ECB
// rates republished after euro adoption).
//
// Fetch requests one bulletin per weekday from after through upto, both
// inclusive, as the Ruby adapter does.
package lb

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.lb.lt/webservices/FxRates/FxRates.asmx/getFxRates"

var eurAdoption = adapter.Date(2015, 1, 1)

// LB labels a redenominated currency's whole history with its current code
// without restating the values: the 2005-12-30 bulletin quotes 1 "AZN" =
// 0.00063 LTL, old manat. Each entry maps the current code to its predecessor
// and the first date LB's values switch to the successor, which can trail the
// official date: the manat was redenominated on 2006-01-01, but LB kept quoting
// old manat through 2006-01-06 and jumped 5000x on 2006-01-09. The Turkmen
// manat switched on the official date: 10000 "TMT" = 1.7354 LTL on 2008-12-31,
// 10 TMT = 8.677 LTL on 2009-01-01. So did the Belarusian ruble, whose BYR
// series holds the 1994 ruble until 1999-12-31 (0.000004444 LTL) and the 2000
// ruble from 2000-01-03 (0.0044444).
var predecessors = map[string]adapter.Predecessor{
	"AZN": {Code: "AZM", Cutover: adapter.Date(2006, 1, 9)},
	"BYR": {Code: "BYB", Cutover: adapter.Date(2000, 1, 1)},
	"TMT": {Code: "TMM", Cutover: adapter.Date(2009, 1, 1)},
}

func init() {
	adapter.Register("LB", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches LB rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 30 }

// Fetch implements adapter.Adapter. An open after bound is an error, as
// iterating from nil raises in Ruby.
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

		tp := "EU"
		if date.Before(eurAdoption) {
			tp = "LT"
		}
		body, err := a.Get(ctx, baseURL, url.Values{"tp": {tp}, "dt": {date.Format(time.DateOnly)}})
		if err != nil {
			return nil, err
		}
		parsed, err := parse(body)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

type fxRates struct {
	Rates []struct {
		Tp      string `xml:"Tp"`
		Dt      string `xml:"Dt"`
		Amounts []struct {
			Ccy string `xml:"Ccy"`
			Amt string `xml:"Amt"`
		} `xml:"CcyAmt"`
	} `xml:"FxRate"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var doc fxRates
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, fx := range doc.Rates {
		if len(fx.Amounts) != 2 {
			continue
		}
		first, second := fx.Amounts[0], fx.Amounts[1]
		// Ox drops whitespace-only text, so Ruby sees such fields as missing.
		if blank(first.Ccy) || blank(first.Amt) || blank(second.Ccy) || blank(second.Amt) || blank(fx.Dt) {
			continue
		}
		date, err := time.Parse(time.DateOnly, strings.TrimSpace(fx.Dt))
		if err != nil {
			return nil, err
		}

		if fx.Tp == "LT" {
			quoteAmt, err := parseAmount(first.Amt)
			if err != nil {
				return nil, err
			}
			quantity, err := parseAmount(second.Amt)
			if err != nil {
				return nil, err
			}
			if quoteAmt == 0 || quantity == 0 {
				continue
			}
			rates = append(rates, adapter.Rate{
				Date:  date,
				Base:  adapter.HistoricalCode(predecessors, second.Ccy, date),
				Quote: "LTL",
				Rate:  quoteAmt / quantity,
			})
			continue
		}

		rate, err := parseAmount(second.Amt)
		if err != nil {
			return nil, err
		}
		if rate == 0 {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: "EUR", Quote: second.Ccy, Rate: rate})
	}
	return rates, nil
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }

// parseAmount is Ruby's raising Float(s).
func parseAmount(s string) (float64, error) {
	f, ok := adapter.ParseFloat(s)
	if !ok {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	return f, nil
}
