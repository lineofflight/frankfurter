// Package bdi fetches rates from Banca d'Italia, which publishes daily rates
// for 150+ currencies against the euro via the "terze valute" (third
// currencies) portal. The dailyRates endpoint with currencyIsoCode=EUR returns
// every currency quoted against EUR for a single date, so Fetch makes one
// request per weekday.
//
// Unlike most adapters, Fetch includes the after date itself, as the Ruby
// adapter iterates after..upto inclusively.
package bdi

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://tassidicambio.bancaditalia.it/terzevalute-wf-web/rest/v1.0/dailyRates"

var isoCode = regexp.MustCompile(`^[A-Z]{3}$`)

// BDI labels its old-afghani quotes AFN. Until 2004-03-31 they hold the frozen
// official rate of 4750 AFA to the dollar (5806.4 per euro with the dollar at
// 1.2224), long past the October 2002 redenomination. On 2004-04-01 they
// switch to 47.5 new afghani to the dollar (58.52 per euro at 1.232).
var predecessors = map[string]adapter.Predecessor{
	"AFN": {Code: "AFA", Cutover: adapter.Date(2004, 4, 1)},
}

// BDI's ZWD series runs through the 2008 and 2009 Zimbabwe redenominations to
// 2013 without changing label. It jumps ten billionfold on 2008-08-01 into the
// third dollar (0.13 USD) and again on 2009-02-03 into the fourth, which
// settles at 361.9 to the dollar from 2010, near the 380 NBP and InforEuro
// publish as ZWL.
var successors = map[string]adapter.Successor{
	"ZWD": {Code: "ZWR", Cutover: adapter.Date(2008, 8, 1)},
	"ZWR": {Code: "ZWL", Cutover: adapter.Date(2009, 2, 3)},
}

func init() {
	adapter.Register("BDI", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BDI rates.
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
			if err := a.Sleep(ctx, 300*time.Millisecond); err != nil {
				return nil, err
			}
		}
		first = false

		body, err := a.Get(ctx, baseURL, url.Values{
			"referenceDate":   {date.Format("2006-01-02")},
			"currencyIsoCode": {"EUR"},
			"lang":            {"en"},
		})
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

func parse(data []byte) ([]adapter.Rate, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err == io.EOF {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	col := make(map[string]int, len(header))
	for i, h := range header {
		if _, ok := col[h]; !ok {
			col[h] = i
		}
	}
	field := func(rec []string, name string) (string, bool) {
		i, ok := col[name]
		if !ok || i >= len(rec) {
			return "", false
		}
		return rec[i], true
	}

	var rates []adapter.Rate
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		code, _ := field(rec, "ISO Code")
		if !isoCode.MatchString(code) {
			continue
		}
		// Ruby's CSV reads an unquoted empty field as nil, which the adapter
		// skips.
		rateText, ok := field(rec, "Rate")
		if !ok || rateText == "" || strings.TrimSpace(rateText) == "N.A." {
			continue
		}
		rate, err := strconv.ParseFloat(strings.TrimSpace(rateText), 64)
		if err != nil || math.IsNaN(rate) || math.IsInf(rate, 0) {
			return nil, fmt.Errorf("invalid rate %q for %s", rateText, code)
		}
		if rate == 0 {
			continue
		}
		dateText, ok := field(rec, "Reference date (CET)")
		if !ok || dateText == "" {
			continue
		}
		date, err := time.Parse("2006-01-02", strings.TrimSpace(dateText))
		if err != nil {
			return nil, fmt.Errorf("invalid date %q for %s", dateText, code)
		}
		quote := adapter.SuccessorCode(successors, adapter.HistoricalCode(predecessors, code, date), date)
		rates = append(rates, adapter.Rate{Date: date, Base: "EUR", Quote: quote, Rate: rate})
	}
	return rates, nil
}
