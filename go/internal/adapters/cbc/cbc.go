// Package cbc fetches rates from the Central Bank of the Republic of China (Taiwan), which publishes daily interbank
// spot rates for 15+ currencies against the US dollar, captured at 16:00 Taipei time. The API returns the full
// historical dataset (from 1993-01-05) with no server-side date filtering, so rows are filtered here.
//
// The open-data file (BP01D01) regenerates nightly but its content is refreshed only in monthly batches in arrears: a
// month's daily rows all land early in the following month. The provider cadence is therefore monthly, and this
// multi-currency data inherently lags by about a month.
//
// Unlike most adapters, the lower bound is inclusive: rows dated on `after` are kept, as the Ruby adapter does.
package cbc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const apiURL = "https://cpx.cbc.gov.tw/API/DataAPI/Get?FileName=BP01D01"

// columns maps a row index to [quote, base]. Most rates are foreign currency per 1 USD; GBP, AUD and EUR are USD per
// 1 unit. Columns 15-17 (DEM, FRF, NLG) hold pre-2002 data.
var columns = []struct {
	index       int
	quote, base string
}{
	{1, "TWD", "USD"},
	{2, "JPY", "USD"},
	{3, "USD", "GBP"},
	{4, "HKD", "USD"},
	{5, "KRW", "USD"},
	{6, "CAD", "USD"},
	{7, "SGD", "USD"},
	{8, "CNY", "USD"},
	{9, "USD", "AUD"},
	{10, "IDR", "USD"},
	{11, "THB", "USD"},
	{12, "MYR", "USD"},
	{13, "PHP", "USD"},
	{14, "USD", "EUR"},
	{15, "DEM", "USD"},
	{16, "FRF", "USD"},
	{17, "NLG", "USD"},
	{18, "VND", "USD"},
}

func init() {
	adapter.Register("CBC", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBC rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if upto.IsZero() {
		upto = a.Today()
	}
	body, err := a.Get(ctx, apiURL, nil)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Data struct {
			DataSets []json.RawMessage `json:"dataSets"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	var rows [][]any
	for _, raw := range doc.Data.DataSets {
		var row []any
		if json.Unmarshal(raw, &row) != nil {
			continue // not an array
		}
		rows = append(rows, row)
	}
	return parse(rows, after, upto)
}

// parse turns data rows into rates dated from start through end, both inclusive; a zero bound is open.
func parse(rows [][]any, start, end time.Time) ([]adapter.Rate, error) {
	var rates []adapter.Rate
	for _, row := range rows {
		if len(row) <= 1 {
			continue
		}
		s, _ := row[0].(string)
		date, err := time.Parse("20060102", s)
		if err != nil {
			return nil, fmt.Errorf("invalid date %v: %w", row[0], err)
		}
		if (!start.IsZero() && date.Before(start)) || (!end.IsZero() && date.After(end)) {
			continue
		}
		for _, c := range columns {
			if c.index >= len(row) {
				continue
			}
			var rate float64
			switch v := row[c.index].(type) {
			case nil:
				continue
			case float64:
				rate = v
			case string:
				if v == "-" {
					continue
				}
				f, ok := adapter.ParseFloat(v)
				if !ok {
					return nil, fmt.Errorf("invalid %s value %q on %s", c.quote, v, s)
				}
				rate = f
			default:
				return nil, fmt.Errorf("invalid %s value %v on %s", c.quote, v, s)
			}
			if rate == 0 {
				continue
			}
			rates = append(rates, adapter.Rate{Date: date, Base: c.base, Quote: c.quote, Rate: rate})
		}
	}
	return rates, nil
}
