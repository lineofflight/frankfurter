// Package cbar fetches rates from the Central Bank of the Republic of
// Azerbaijan, which publishes a daily bulletin of official rates for about 40
// currencies and four precious metals against AZN, one XML file per calendar
// date at https://www.cbar.az/currencies/DD.MM.YYYY.xml. The archive starts at
// the 26.11.1993 file, which carries the bulletin of 1993-11-25; earlier URLs
// redirect.
//
// The file for a date carries the latest bulletin effective on or before it,
// stamped with its own Date attribute, so weekend and holiday files repeat the
// previous bulletin. Rows are deduplicated on that attribute, not on the
// requested date.
//
// As in Ruby, Fetch walks every date from after through upto, both inclusive,
// and does not clip rows to the window.
package cbar

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.cbar.az/currencies/"

// CBAR labels every row in the archive with the currency's current ISO code,
// including bulletins from before a redenomination, without restating the
// values: the 2005-12-30 file quotes 1 USD = 4593 "AZN", old manat. Each entry
// maps the current code to its predecessor and the first date the successor
// applies, so a row keeps the code of the currency it actually prices.
var predecessors = map[string]adapter.Predecessor{
	"AZN": {Code: "AZM", Cutover: adapter.Date(2006, 1, 1)},
	"BYN": {Code: "BYR", Cutover: adapter.Date(2016, 7, 1)},
	"RUB": {Code: "RUR", Cutover: adapter.Date(1998, 1, 1)},
	"TMT": {Code: "TMM", Cutover: adapter.Date(2009, 1, 1)},
	"TRY": {Code: "TRL", Cutover: adapter.Date(2005, 1, 1)},
}

// Old Turkish lira rows carry Nominal 1 but price 1000 TRL: the last 2004
// bulletin quotes 3.61 and the first 2005 bulletin, after the 1,000,000:1
// redenomination, 3635.15. NBKR publishes the same series per 1000.
var nominalOverrides = map[string]int{"TRL": 1000}

// Labels CBAR uses that aren't ISO 4217 codes.
var aliases = map[string]string{"SDR": "XDR"}

var (
	isoCode = regexp.MustCompile(`^[A-Z]{3}$`)
	digits  = regexp.MustCompile(`\d+`)
)

func init() {
	adapter.Register("CBAR", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBAR rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter: one request per day.
func (a *Adapter) BackfillRange() int { return 30 }

// Fetch implements adapter.Adapter. Like Ruby, which cannot iterate a range
// from nil, it needs a start date.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("fetch needs a start date")
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}

	var rates []adapter.Rate
	seen := map[time.Time]bool{}
	for date := after; !date.After(end); date = date.AddDate(0, 0, 1) {
		if !date.Equal(after) {
			if err := a.Sleep(ctx, 200*time.Millisecond); err != nil {
				return nil, err
			}
		}
		body, err := a.Get(ctx, baseURL+date.Format("02.01.2006")+".xml", nil)
		if err != nil {
			return nil, err
		}
		records, err := parse(body)
		if err != nil {
			return nil, err
		}
		if len(records) == 0 || seen[records[0].Date] {
			continue
		}
		seen[records[0].Date] = true
		rates = append(rates, records...)
	}
	return rates, nil
}

type valCurs struct {
	XMLName xml.Name
	Date    string `xml:"Date,attr"`
	Valutes []struct {
		Code    *string `xml:"Code,attr"`
		Nominal string  `xml:"Nominal"`
		Value   string  `xml:"Value"`
	} `xml:"ValType>Valute"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var doc valCurs
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.XMLName.Local != "ValCurs" {
		return nil, fmt.Errorf("expected ValCurs root, got %q", doc.XMLName.Local)
	}
	date, err := time.Parse("02.01.2006", doc.Date)
	if err != nil {
		return nil, err
	}
	quote := adapter.HistoricalCode(predecessors, "AZN", date)

	var rates []adapter.Rate
	for _, v := range doc.Valutes {
		if v.Code == nil || !isoCode.MatchString(*v.Code) {
			continue
		}
		code := *v.Code
		if alias, ok := aliases[code]; ok {
			code = alias
		}
		base := adapter.HistoricalCode(predecessors, code, date)
		// Metals are quoted per troy ounce ("1 t.u."); currencies per 1, 100 or
		// 1000 units.
		nominal, _ := strconv.Atoi(digits.FindString(strings.TrimSpace(v.Nominal)))
		if override, ok := nominalOverrides[base]; ok {
			nominal *= override
		}
		rate, ok := adapter.ParseFloat(v.Value)
		if !ok || rate <= 0 || nominal <= 0 {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: base, Quote: quote, Rate: rate / float64(nominal)})
	}
	return rates, nil
}
