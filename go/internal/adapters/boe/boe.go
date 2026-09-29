// Package boe fetches rates from the Bank of England, which publishes daily spot rates for 26 currencies against the
// British pound in its Statistical Interactive Database. Historical data is available from 2000-01-04.
//
// The CSV API returns pivoted data with series codes as column headers. Rates are foreign currency per 1 GBP.
// Fetch passes both bounds to the source and returns what it sends back unclipped, so rows dated on after itself are
// included, as in Ruby.
package boe

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.bankofengland.co.uk/boeapps/database/_iadb-fromshowcolumns.asp"

// series maps BOE series codes to ISO 4217 codes, in the order Ruby emits them.
var series = []struct{ code, currency string }{
	{"XUDLUSS", "USD"},
	{"XUDLERS", "EUR"},
	{"XUDLJYS", "JPY"},
	{"XUDLCDS", "CAD"},
	{"XUDLSFS", "CHF"},
	{"XUDLADS", "AUD"},
	{"XUDLNDS", "NZD"},
	{"XUDLNKS", "NOK"},
	{"XUDLSKS", "SEK"},
	{"XUDLDKS", "DKK"},
	{"XUDLHDS", "HKD"},
	{"XUDLSGS", "SGD"},
	{"XUDLSRS", "SAR"},
	{"XUDLZRS", "ZAR"},
	{"XUDLTWS", "TWD"},
	{"XUDLBK25", "CZK"},
	{"XUDLBK33", "HUF"},
	{"XUDLBK47", "PLN"},
	{"XUDLBK78", "ILS"},
	{"XUDLBK83", "MYR"},
	{"XUDLBK87", "THB"},
	{"XUDLBK89", "CNY"},
	{"XUDLBK93", "KRW"},
	{"XUDLBK95", "TRY"},
	{"XUDLBK97", "INR"},
	{"XUDLZOS4", "RON"},
}

func init() {
	adapter.Register("BOE", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BOE rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter. The source needs a start date, so after may not be open.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("after date required")
	}
	if upto.IsZero() {
		upto = a.Today()
	}
	codes := make([]string, len(series))
	for i, s := range series {
		codes[i] = s.code
	}
	body, err := a.Get(ctx, baseURL, url.Values{
		"csv.x":       {"yes"},
		"SeriesCodes": {strings.Join(codes, ",")},
		"UsingCodes":  {"Y"},
		"CSVF":        {"TN"},
		"Datefrom":    {after.Format("02/Jan/2006")},
		"Dateto":      {upto.Format("02/Jan/2006")},
	})
	if err != nil {
		return nil, err
	}
	return parse(body)
}

func parse(data []byte) ([]adapter.Rate, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	column := map[string]int{}
	for i, h := range records[0] {
		if _, ok := column[h]; !ok {
			column[h] = i
		}
	}
	field := func(row []string, name string) string {
		i, ok := column[name]
		if !ok || i >= len(row) {
			return ""
		}
		return row[i]
	}

	var rates []adapter.Rate
	for _, row := range records[1:] {
		dateStr := field(row, "DATE")
		if dateStr == "" {
			continue
		}
		date, err := adapter.ParseDate(dateStr, "2 Jan 2006")
		if err != nil {
			return nil, err
		}
		for _, s := range series {
			text := strings.TrimSpace(field(row, s.code))
			if text == "" {
				continue
			}
			value, err := strconv.ParseFloat(text, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid rate %q for %s: %w", text, s.code, err)
			}
			if value == 0 {
				continue
			}
			rates = append(rates, adapter.Rate{Date: date, Base: "GBP", Quote: s.currency, Rate: value})
		}
	}
	return rates, nil
}
