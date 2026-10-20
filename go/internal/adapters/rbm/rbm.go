// Package rbm fetches rates from the Reserve Bank of Malawi, which publishes
// daily buy/middle/sell rates against MWK for about 38 currencies through an
// ASP.NET MVC site.
//
// The historical endpoint is a POST form that accepts US-formatted StartDate /
// EndDate (MM/DD/YYYY) and an optional RateTypes filter (omit to return all
// currencies). The response is an HTML page; each row carries the currency code
// in <strong>, then three numeric cells (Buying, Middle, Selling) and a date
// cell ("Jan 02 2024" with a pair of non-breaking spaces between the day and
// the year).
//
// RBM publishes "1 foreign = X MWK", so foreign is the base and MWK the quote.
// The source already publishes a middle column, so we use that directly instead
// of recomputing from buy/sell (issue #314).
//
// IEP (defunct Irish punt) and CMD (the non-ISO COMESA Dollar accounting unit)
// appear in the response. Both are registered through
// db/seeds/currency_patches.json. CMD remains separately published even where
// its value matches USD; IEP is subject to its terminal date when blended.
//
// Like Ruby, Fetch returns whatever the posted range yields without clipping,
// so rows dated on after are included.
package rbm

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const endpoint = "https://www.rbm.mw/Statistics/ExchangeRatesFilter/"

var (
	codeRE   = regexp.MustCompile(`\A[A-Z]{3}\z`)
	dateRE   = regexp.MustCompile(`\A([A-Z][a-z]{2}) (\d{1,2}) (\d{4})\z`)
	aliases  = map[string]string{"SDR": "XDR"}
	coverage = adapter.Date(2011, 6, 20)
)

func init() {
	adapter.Register("RBM", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches RBM rates.
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
	start := after
	if start.IsZero() {
		start = coverage
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	// Inverted window (caller already caught up); nothing to fetch.
	if start.After(end) {
		return nil, nil
	}

	body, err := a.PostForm(ctx, endpoint, url.Values{
		"RateTypes": {""},
		"StartDate": {start.Format("01/02/2006")},
		"EndDate":   {end.Format("01/02/2006")},
	})
	if err != nil {
		return nil, err
	}
	return parse(body)
}

func parse(data []byte) ([]adapter.Rate, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	table := doc.Find("table#exchange-rates").First()
	if table.Length() == 0 {
		table = doc.Find("table").First()
	}
	if table.Length() == 0 {
		return nil, errors.New("rates table not found in ExchangeRatesFilter response")
	}

	var rates []adapter.Rate
	table.Find("tr").Each(func(_ int, row *goquery.Selection) {
		cells := row.Find("td")
		if cells.Length() < 5 {
			return
		}
		code := strings.TrimSpace(cells.Eq(0).Text())
		if !codeRE.MatchString(code) {
			return
		}
		if alias, ok := aliases[code]; ok {
			code = alias
		}
		middle, ok := parseNumber(cells.Eq(2).Text())
		if !ok || middle == 0 {
			return
		}
		date, ok := parseDate(cells.Eq(4).Text())
		if !ok {
			return
		}
		rates = append(rates, adapter.Rate{Date: date, Base: code, Quote: "MWK", Rate: middle})
	})
	return rates, nil
}

func parseNumber(s string) (float64, bool) {
	cleaned := strings.ReplaceAll(strings.Join(strings.Fields(s), ""), ",", "")
	if cleaned == "" {
		return 0, false
	}
	return adapter.ParseFloat(cleaned)
}

func parseDate(s string) (time.Time, bool) {
	m := dateRE.FindStringSubmatch(strings.Join(strings.Fields(s), " "))
	if m == nil {
		return time.Time{}, false
	}
	month, err := time.Parse("Jan", m[1])
	if err != nil {
		return time.Time{}, false
	}
	day, _ := strconv.Atoi(m[2])
	year, _ := strconv.Atoi(m[3])
	date := adapter.Date(year, month.Month(), day)
	// Date.new raises on an impossible day; time.Date would normalize it.
	if date.Day() != day {
		return time.Time{}, false
	}
	return date, true
}
