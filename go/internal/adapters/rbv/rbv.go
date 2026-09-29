// Package rbv fetches rates from the Reserve Bank of Vanuatu, which publishes daily reference rates against the
// Vanuatu vatu (VUV) on business days, 08:30-09:00 Pacific/Efate: six quote currencies (USD, JPY, NZD, GBP, AUD,
// EUR), the VUV trade-weighted basket.
//
// The exchange-rates page is a Joomla Fabrik list. A CSV export endpoint exists but is hard-capped at 100 rows per
// call, so we scrape the HTML list directly with a limit1 query parameter large enough to return every row in one
// response. Rows render the date as "DD Month YYYY" or "DD-Mon-YY" depending on age.
//
// Rates are VUV per unit of foreign currency. JPY is published per single unit, not per 100.
//
// www.rbv.gov.vu omits its Trustico intermediate; adapter.NewClient bundles it.
package rbv

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	pageURL  = "https://www.rbv.gov.vu/index.php/en/exchange-rates"
	pageSize = "100000"
)

// quoteColumns are the cell class suffixes, in page order; GBP's is upper case on the page.
var quoteColumns = []string{"usd", "jpy", "nzd", "GBP", "aud", "eur"}

var dateLayouts = []string{"2 January 2006", "2 Jan 2006", "2-Jan-06", "2-January-2006"}

func init() {
	adapter.Register("RBV", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches RBV rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	body, err := a.Get(ctx, pageURL, url.Values{"limit1": {pageSize}})
	if err != nil {
		return nil, err
	}
	rates, err := parse(body)
	if err != nil {
		return nil, err
	}
	return adapter.Window(rates, after, upto), nil
}

// parse returns the rows in page order, newest first.
func parse(html []byte) ([]adapter.Rate, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	doc.Find("tr.fabrik_row").Each(func(_ int, row *goquery.Selection) {
		text := strings.TrimSpace(row.Find("td.exchange_rates___date").First().Text())
		if text == "" {
			return
		}
		date, err := adapter.ParseDate(text, dateLayouts...)
		if err != nil {
			return // unparseable dates are skipped, not fatal
		}
		for _, code := range quoteColumns {
			cell := row.Find("td.exchange_rates___" + code).First()
			if cell.Length() == 0 {
				continue
			}
			rate, ok := adapter.ParseFloat(cell.Text())
			if !ok || rate <= 0 {
				continue
			}
			rates = append(rates, adapter.Rate{Date: date, Base: strings.ToUpper(code), Quote: "VUV", Rate: rate})
		}
	})
	return rates, nil
}
