// Package cbsl fetches rates from the Central Bank of Sri Lanka, which publishes daily indicative exchange rates for 55
// currencies (including XAU per troy ounce) against the Sri Lankan rupee (LKR). Indicative rates are derived at the
// start of business (09:30 Colombo time, UTC+5:30) from world currency rates against the US dollar and the USD/LKR
// spot rate.
//
// The endpoint is a PHP form handler that returns an HTML page with one table per selected currency. We POST all 55
// currencies in one request and parse each table by associating the header (e.g. "1 USD -> LKR") with the data rows.
//
// Rows keep CBSL's native direction: foreign currency as base, LKR as quote. The server applies the date range itself
// (its start date is inclusive), so Fetch does not clip the rows, as the Ruby adapter does not.
package cbsl

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	endpoint = "https://www.cbsl.gov.lk/cbsl_custom/exrates/exrates_results.php"
	formPage = "https://www.cbsl.gov.lk/cbsl_custom/exrates/exrates.php"
)

var (
	headerPattern = regexp.MustCompile(`\A\s*1\s+([A-Z]{3})\s+->\s+LKR\s*\z`)
	datePattern   = regexp.MustCompile(`\A\d{4}-\d{2}-\d{2}\z`)
)

func init() {
	adapter.Register("CBSL", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBSL rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter. The endpoint comfortably returns a year of all 55 currencies (~1 MB) in
// under two seconds, so chunk the backfill yearly.
func (a *Adapter) BackfillRange() int { return 365 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if upto.IsZero() {
		upto = a.Today()
	}
	currencies, err := a.fetchCurrencies(ctx)
	if err != nil {
		return nil, err
	}

	start := ""
	if !after.IsZero() {
		start = after.Format(time.DateOnly)
	}
	form := url.Values{
		"lookupPage":    {"lookup_daily_exchange_rates.php"},
		"rangeType":     {"dates"},
		"txtStart":      {start},
		"txtEnd":        {upto.Format(time.DateOnly)},
		"chk_cur[]":     currencies,
		"submit_button": {"Submit"},
	}
	body, err := a.PostForm(ctx, endpoint, form)
	if err != nil {
		return nil, err
	}
	return parse(body)
}

func (a *Adapter) fetchCurrencies(ctx context.Context) ([]string, error) {
	body, err := a.Get(ctx, formPage, nil)
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var currencies []string
	doc.Find(`input[name="chk_cur[]"]`).Each(func(_ int, s *goquery.Selection) {
		if v, ok := s.Attr("value"); ok {
			currencies = append(currencies, v)
		}
	})
	return currencies, nil
}

func parse(html []byte) ([]adapter.Rate, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	var parseErr error
	doc.Find("table").EachWithBreak(func(_ int, table *goquery.Selection) bool {
		code := currencyCode(table)
		if code == "" {
			return true
		}
		table.Find("tbody tr").EachWithBreak(func(_ int, row *goquery.Selection) bool {
			cells := row.Find("td")
			if cells.Length() < 2 {
				return true
			}
			dateText := strings.TrimSpace(cells.Eq(0).Text())
			if !datePattern.MatchString(dateText) {
				return true
			}
			rate, ok := adapter.ParseFloat(cells.Eq(1).Text())
			if !ok || rate <= 0 {
				return true
			}
			date, err := time.Parse(time.DateOnly, dateText)
			if err != nil {
				parseErr = err // Date.parse raises on an impossible date such as 2025-13-01
				return false
			}
			rates = append(rates, adapter.Rate{Date: date, Base: code, Quote: "LKR", Rate: rate})
			return true
		})
		return parseErr == nil
	})
	if parseErr != nil {
		return nil, parseErr
	}
	return rates, nil
}

func currencyCode(table *goquery.Selection) string {
	code := ""
	table.Find("thead th").EachWithBreak(func(_ int, th *goquery.Selection) bool {
		if m := headerPattern.FindStringSubmatch(th.Text()); m != nil {
			code = m[1]
			return false
		}
		return true
	})
	return code
}
