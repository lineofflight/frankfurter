// Package nbp fetches rates from the National Bank of Poland, which publishes
// daily mid-market rates (Table A) for about 32 currencies and weekly
// mid-market rates (Table B) for about 150 more against PLN, plus a daily gold
// reference price. Gold comes in PLN per gram and is normalized to per troy
// ounce.
//
// Unlike most adapters, Fetch treats after as inclusive: it is the first day of
// the first query, as in Ruby.
package nbp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	tableAURL = "https://api.nbp.pl/api/exchangerates/tables/A"
	tableBURL = "https://api.nbp.pl/api/exchangerates/tables/B"
	goldURL   = "https://api.nbp.pl/api/cenyzlota"
)

// The NBP API limits queries to 93 days.
const chunkDays = 93

var isoCode = regexp.MustCompile(`\A[A-Z]{3}\z`)

func init() {
	adapter.Register("NBP", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches NBP rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

type source struct {
	url   string
	parse func([]byte) ([]adapter.Rate, error)
}

var sources = []source{{tableAURL, parse}, {tableBURL, parse}, {goldURL, parseGold}}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("fetch needs a start date")
	}
	if upto.IsZero() {
		upto = a.Today()
	}

	var rates []adapter.Rate
	for start := after; !start.After(upto); {
		if !start.Equal(after) {
			if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
				return nil, err
			}
		}
		end := start.AddDate(0, 0, chunkDays-1)
		if end.After(upto) {
			end = upto
		}
		for _, src := range sources {
			chunk, err := a.fetchRange(ctx, src, start, end)
			if err != nil {
				return nil, err
			}
			rates = append(rates, chunk...)
		}
		start = end.AddDate(0, 0, 1)
	}
	return rates, nil
}

func (a *Adapter) fetchRange(ctx context.Context, src source, start, end time.Time) ([]adapter.Rate, error) {
	u := fmt.Sprintf("%s/%s/%s/?format=json", src.url, start.Format(time.DateOnly), end.Format(time.DateOnly))
	req, err := a.NewRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.Do(req, http.StatusNotFound)
	if err != nil {
		return nil, err
	}
	// A 404 means the range includes no working days.
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	return src.parse(resp.Body)
}

// Table B carried retired codes until table 23/B/NBP/2003 (2003-11-12): AON
// for the kwanza, at AOA values (0.0503 PLN on 2003-10-28, 0.0511 as AOA
// next), and BYB for the Belarusian ruble in tables 5 to 18 of 2002, with BYR
// before and after.
var aliases = map[string]string{"AON": "AOA", "BYB": "BYR"}

// AFA rows switch to the new afghani on 2003-01-07 (0.000816 PLN on
// 2002-12-24, 0.089056 next) and keep the old label until AFN replaces it in
// the same 2003-11-12 table. ZWR rows switch to the 2009 Zimbabwe dollar on
// 2009-02-25 (0.00000001 PLN on 2009-02-04, 0.043749 next) and keep the old
// label until ZWL on 2010-06-02.
var successors = map[string]adapter.Successor{
	"AFA": {Code: "AFN", Cutover: adapter.Date(2003, 1, 7)},
	"ZWR": {Code: "ZWL", Cutover: adapter.Date(2009, 2, 25)},
}

type table struct {
	EffectiveDate string `json:"effectiveDate"`
	Rates         []struct {
		Code string   `json:"code"`
		Mid  *float64 `json:"mid"`
	} `json:"rates"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var tables []table
	if err := json.Unmarshal(data, &tables); err != nil {
		return nil, err
	}
	var rates []adapter.Rate
	for _, t := range tables {
		date, err := time.Parse(time.DateOnly, t.EffectiveDate)
		if err != nil {
			return nil, err
		}
		for _, r := range t.Rates {
			if !isoCode.MatchString(r.Code) || r.Mid == nil || *r.Mid == 0 {
				continue
			}
			code := r.Code
			if alias, ok := aliases[code]; ok {
				code = alias
			}
			rates = append(rates, adapter.Rate{
				Date: date, Base: adapter.SuccessorCode(successors, code, date), Quote: "PLN", Rate: *r.Mid,
			})
		}
	}
	return rates, nil
}

type goldRow struct {
	Date  string   `json:"data"`
	Price *float64 `json:"cena"`
}

func parseGold(data []byte) ([]adapter.Rate, error) {
	var rows []goldRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	var rates []adapter.Rate
	for _, r := range rows {
		if r.Price == nil || *r.Price == 0 {
			continue
		}
		date, err := time.Parse(time.DateOnly, r.Date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, adapter.Rate{
			Date: date, Base: "XAU", Quote: "PLN", Rate: *r.Price * adapter.GramsPerTroyOunce,
		})
	}
	return rates, nil
}
