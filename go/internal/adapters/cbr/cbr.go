// Package cbr fetches rates from the Bank of Russia, which publishes daily rates for about 54 currencies against the
// Russian ruble (RUB), plus daily reference prices for gold, silver, platinum and palladium.
//
// FX uses XML_daily for the currency list and XML_dynamic for date ranges. Metals come from xml_metall in RUB per
// gram; values are normalized to per troy ounce here. Rows are not clipped to the window: the endpoints take the date
// range themselves.
package cbr

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html/charset"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	dailyURL   = "https://www.cbr.ru/scripts/XML_daily.asp"
	dynamicURL = "https://www.cbr.ru/scripts/XML_dynamic.asp"
	metalURL   = "https://www.cbr.ru/scripts/xml_metall.asp"
)

var metalCodes = map[string]string{
	"1": "XAU",
	"2": "XAG",
	"3": "XPT",
	"4": "XPD",
}

// XML_dynamic serves a currency's whole history under its current code. The TJS series is not restated across the
// 2000-10-30 introduction of the somoni: 1000 "TJS" = 13.54 RUB on 2000-10-01, 1 TJS = 12.65 on 2000-11-01. Earlier
// rows are Tajikistani ruble (TJR).
var predecessors = map[string]adapter.Predecessor{
	"TJS": {Code: "TJR", Cutover: adapter.Date(2000, 10, 30)},
}

func init() {
	adapter.Register("CBR", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBR rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	currencies, err := a.fetchCurrencyList(ctx)
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, c := range currencies {
		body, err := a.Get(ctx, dynamicURL, dateRange(after, end, url.Values{"VAL_NM_RQ": {c.id}}))
		if err != nil {
			return nil, err
		}
		parsed, err := parseDynamic(body, c.code)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}

	body, err := a.Get(ctx, metalURL, dateRange(after, end, url.Values{}))
	if err != nil {
		return nil, err
	}
	metals, err := parseMetals(body)
	if err != nil {
		return nil, err
	}
	return append(rates, metals...), nil
}

func dateRange(start, end time.Time, params url.Values) url.Values {
	params.Set("date_req1", start.Format("02/01/2006"))
	params.Set("date_req2", end.Format("02/01/2006"))
	return params
}

type currency struct{ id, code string }

func (a *Adapter) fetchCurrencyList(ctx context.Context) ([]currency, error) {
	body, err := a.Get(ctx, dailyURL, nil)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Valutes []struct {
			ID       string `xml:"ID,attr"`
			CharCode string `xml:"CharCode"`
		} `xml:"Valute"`
	}
	if err := decode(body, &doc); err != nil {
		return nil, err
	}
	var list []currency
	for _, v := range doc.Valutes {
		if v.CharCode != "" {
			list = append(list, currency{v.ID, v.CharCode})
		}
	}
	return list, nil
}

// decode reads the Windows-1251 documents CBR serves.
func decode(data []byte, v any) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.CharsetReader = charset.NewReaderLabel
	return d.Decode(v)
}

type record struct {
	Date      string  `xml:"Date,attr"`
	Nominal   string  `xml:"Nominal"`
	Value     *string `xml:"Value"`
	VunitRate *string `xml:"VunitRate"`
}

func parseDynamic(data []byte, code string) ([]adapter.Rate, error) {
	var doc struct {
		Records []record `xml:"Record"`
	}
	if err := decode(data, &doc); err != nil {
		return nil, err
	}
	var rates []adapter.Rate
	for _, r := range doc.Records {
		date, err := time.Parse(dateLayout, r.Date)
		if err != nil {
			return nil, err
		}
		if weekend(date) {
			continue
		}
		rate, ok, err := extractRate(r)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		rates = append(rates, adapter.Rate{
			Date: date, Base: adapter.HistoricalCode(predecessors, code, date), Quote: "RUB", Rate: rate,
		})
	}
	return rates, nil
}

// extractRate prefers the per-unit VunitRate and falls back to Value divided by Nominal. Whitespace-only text counts
// as absent, as Ox drops it. Malformed numbers are errors, as Ruby's Float() raises.
func extractRate(r record) (float64, bool, error) {
	if r.VunitRate != nil && strings.TrimSpace(*r.VunitRate) != "" {
		v, err := parseNumber(*r.VunitRate)
		return v, err == nil, err
	}
	if r.Value == nil || strings.TrimSpace(*r.Value) == "" {
		return 0, false, nil
	}
	v, err := parseNumber(*r.Value)
	if err != nil {
		return 0, false, err
	}
	return v / float64(toI(r.Nominal)), true, nil
}

func parseNumber(s string) (float64, error) {
	v, ok := adapter.ParseFloat(strings.ReplaceAll(s, ",", "."))
	if !ok {
		return 0, fmt.Errorf("cbr: invalid number %q", s)
	}
	return v, nil
}

// toI reads the leading integer the way Ruby's String#to_i does: 0 when absent, so a missing Nominal divides to Inf
// as in Ruby.
func toI(s string) int {
	s = strings.TrimLeft(s, " \t\n\r\f\v")
	end := 0
	if end < len(s) && (s[end] == '+' || s[end] == '-') {
		end++
	}
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}

func parseMetals(data []byte) ([]adapter.Rate, error) {
	var doc struct {
		Records []struct {
			Date string  `xml:"Date,attr"`
			Code string  `xml:"Code,attr"`
			Buy  *string `xml:"Buy"`
		} `xml:"Record"`
	}
	if err := decode(data, &doc); err != nil {
		return nil, err
	}
	var rates []adapter.Rate
	for _, r := range doc.Records {
		base, ok := metalCodes[r.Code]
		if !ok {
			continue
		}
		date, err := time.Parse(dateLayout, r.Date)
		if err != nil {
			return nil, err
		}
		if weekend(date) || r.Buy == nil {
			continue
		}
		rate, ok := adapter.ParseFloat(strings.ReplaceAll(*r.Buy, ",", "."))
		if !ok || rate <= 0 {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: base, Quote: "RUB", Rate: rate * adapter.GramsPerTroyOunce})
	}
	return rates, nil
}

// dateLayout matches Ruby's strptime("%d.%m.%Y"), which also takes one-digit days and months.
const dateLayout = "2.1.2006"

func weekend(d time.Time) bool {
	return d.Weekday() == time.Saturday || d.Weekday() == time.Sunday
}
