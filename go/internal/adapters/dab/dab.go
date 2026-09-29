// Package dab fetches rates from Da Afghanistan Bank, which publishes daily reference rates for the Afghan afghani
// (AFN) against about 10 currencies on its exchange-rates page. Coverage starts 2019-03-31, and the page takes a
// field_date_value=YYYY-MM-DD query parameter for historical days.
//
// The page has two tables: a daily snapshot (which we consume) and a monthly average (which we ignore). Each has four
// columns: Cash Sell/Buy and Transfer Sell/Buy. The rate is the transfer mid.
//
// Row labels are descriptive ("USD$", "EURO€", "INDIAN Rs.", "IRAN Toman"); labelMap turns them into ISO codes.
// "IRAN Toman" is a non-ISO unit equal to 10 IRR, so its values are divided by 10.
//
// Rates are in DAB's native direction: foreign currency as base, AFN as quote.
//
// Fetch treats after as inclusive: it requests every day from after through upto, as the Ruby adapter does.
package dab

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	baseURL     = "https://www.dab.gov.af/exchange-rates"
	irrPerToman = 10.0
)

// labelMap maps row labels, after stripping currency symbols and normalizing whitespace and case, to ISO codes.
var labelMap = map[string]string{
	"USD":          "USD",
	"EURO":         "EUR",
	"POUND":        "GBP",
	"SWISS":        "CHF",
	"INDIAN RS.":   "INR",
	"PAKISTAN RS.": "PKR",
	"IRAN TOMAN":   "IRR",
	"CNY":          "CNY",
	"UAE DIRHAM":   "AED",
	"SAUDI RIYAL":  "SAR",
}

var (
	symbols    = regexp.MustCompile(`[$€£₣¥]`)
	whitespace = regexp.MustCompile(`\s+`)
)

func init() {
	adapter.Register("DAB", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches DAB rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter. The endpoint serves one day per request.
func (a *Adapter) BackfillRange() int { return 1 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("fetch needs a start date")
	}
	if upto.IsZero() {
		upto = a.Today()
	}
	var rates []adapter.Rate
	for date := after; !date.After(upto); date = date.AddDate(0, 0, 1) {
		if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
			return nil, err
		}
		body, err := a.Get(ctx, baseURL, url.Values{"field_date_value": {date.Format(time.DateOnly)}})
		if err != nil {
			return nil, err
		}
		day, err := parse(body, date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, day...)
	}
	return rates, nil
}

func parse(html []byte, date time.Time) ([]adapter.Rate, error) {
	// Holidays render "There were no results." instead of the rates table.
	if bytes.Contains(html, []byte("There were no results")) {
		return nil, nil
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, err
	}
	table := doc.Find("div.table-responsive table.table-striped").First()
	if table.Length() == 0 {
		return nil, fmt.Errorf("no rates table for %s at %s", date.Format(time.DateOnly), baseURL)
	}

	var rates []adapter.Rate
	table.Find("tbody tr").Each(func(_ int, row *goquery.Selection) {
		cells := row.Find("td")
		if cells.Length() < 5 {
			return
		}
		code, ok := labelMap[normalizeLabel(cells.Eq(0).Text())]
		if !ok {
			return
		}
		sell, ok1 := parseDecimal(cells.Eq(3).Text())
		buy, ok2 := parseDecimal(cells.Eq(4).Text())
		if !ok1 || !ok2 {
			return
		}
		mid := adapter.Midpoint(sell, buy)
		if mid <= 0 {
			return
		}
		rate, unit := mid, 1.0
		if code == "IRR" {
			rate, unit = mid/irrPerToman, irrPerToman
		}
		rates = append(rates, adapter.Rate{
			Date:  date,
			Base:  code,
			Quote: "AFN",
			Rate:  rate,
			Bid:   adapter.Float(adapter.PerUnit(buy, unit)),
			Ask:   adapter.Float(adapter.PerUnit(sell, unit)),
		})
	})
	return rates, nil
}

func normalizeLabel(text string) string {
	s := symbols.ReplaceAllString(strings.TrimSpace(text), "")
	s = whitespace.ReplaceAllString(s, " ")
	return strings.ToUpper(strings.TrimSpace(s))
}

// parseDecimal returns a positive number, or false for blank, malformed or non-positive text.
func parseDecimal(text string) (float64, bool) {
	s := strings.ReplaceAll(strings.TrimSpace(text), ",", "")
	if s == "" {
		return 0, false
	}
	v, ok := adapter.ParseFloat(s)
	if !ok || v <= 0 {
		return 0, false
	}
	return v, true
}
