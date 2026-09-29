// Package cbllr fetches rates from the Central Bank of Liberia (CBL), which publishes a daily indicative US dollar
// rate against the Liberian dollar (LRD) on a Drupal-rendered HTML page.
//
// The bare acronym "CBL" already collides with the Central Bank of Libya (#394, declined), so the key adds the
// country code: CBLLR.
//
// The page lists ~14 entries newest-first, with a 0-indexed ?page=N pager reaching back to 2012-07-05 (~105 pages).
// Fetch walks pages newest-first and stops once it sees a row on or before after, so incremental fetches usually
// touch one page.
//
// CBL publishes buy and sell prices ("L$X/US$1.00"); we coerce to the mid (issue #314). Rates keep CBL's direction:
// USD base, LRD quote.
package cbllr

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.cbl.org.lr/research/buying-selling-rates"

// maxPages is a hard ceiling against runaway pagination if the markup changes; the archive is ~105 pages today.
const maxPages = 500

var ratePattern = regexp.MustCompile(`L\$\s*([\d.]+)\s*/\s*US\$`)

func init() {
	adapter.Register("CBLLR", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBLLR rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter. A zero upto means today. Rows come back oldest first.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if upto.IsZero() {
		upto = a.Today()
	}

	var records []adapter.Rate
	for page := range maxPages {
		html, err := a.fetchPage(ctx, page)
		if err != nil {
			return nil, err
		}
		pageRecords, err := parse(html)
		if err != nil {
			return nil, err
		}
		if len(pageRecords) == 0 {
			break
		}
		records = append(records, pageRecords...)

		oldest := pageRecords[0].Date
		for _, r := range pageRecords[1:] {
			if r.Date.Before(oldest) {
				oldest = r.Date
			}
		}
		if !after.IsZero() && !oldest.After(after) {
			break
		}

		if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
			return nil, err
		}
	}

	type key struct {
		date        time.Time
		base, quote string
	}
	seen := map[key]bool{}
	var out []adapter.Rate
	for _, r := range adapter.Window(records, after, upto) {
		k := key{r.Date, r.Base, r.Quote}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	slices.SortStableFunc(out, func(x, y adapter.Rate) int { return x.Date.Compare(y.Date) })
	return out, nil
}

func (a *Adapter) fetchPage(ctx context.Context, page int) ([]byte, error) {
	var query url.Values
	if page > 0 {
		query = url.Values{"page": {strconv.Itoa(page)}}
	}
	return a.Get(ctx, baseURL, query)
}

// parse returns the rows in page order, newest first.
func parse(html []byte) ([]adapter.Rate, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	var parseErr error
	doc.Find(".view-content table tr").EachWithBreak(func(_ int, row *goquery.Selection) bool {
		timeEl := row.Find("td.views-field-field-content-post-date time").First()
		buyCell := row.Find("td.views-field-field-buying-us").First()
		sellCell := row.Find("td.views-field-field-selling-us").First()
		if timeEl.Length() == 0 || buyCell.Length() == 0 || sellCell.Length() == 0 {
			return true
		}
		datetime, ok := timeEl.Attr("datetime")
		if !ok {
			return true
		}
		date, err := parseDate(datetime)
		if err != nil {
			parseErr = err
			return false
		}

		buy, okBuy := extractRate(buyCell.Text())
		sell, okSell := extractRate(sellCell.Text())
		if !okBuy || !okSell {
			return true
		}
		mid := adapter.Midpoint(buy, sell)
		if mid <= 0 {
			return true
		}
		rates = append(rates, adapter.Rate{
			Date: date, Base: "USD", Quote: "LRD", Rate: mid,
			Bid: adapter.Float(buy), Ask: adapter.Float(sell),
		})
		return true
	})
	if parseErr != nil {
		return nil, parseErr
	}
	return rates, nil
}

// parseDate takes the calendar date as written at the start of the timestamp (2026-05-21T12:00:00Z), as Ruby's
// Date.parse does. Ruby raises on an unparseable date, so this is an error rather than a skip.
func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if len(s) >= 10 {
		if t, err := time.Parse("2006-01-02", s[:10]); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date %q", s)
}

func extractRate(text string) (float64, bool) {
	m := ratePattern.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	return adapter.ParseFloat(m[1])
}
