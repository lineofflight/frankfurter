// Package bota fetches rates from the Bank of Tanzania, which publishes daily
// exchange rates for 35+ currencies against the Tanzanian shilling (TZS), 7
// days a week. It uses the Mean column (midpoint of buy and sell).
//
// The previous_rates endpoint is protected by ASP.NET MVC antiforgery
// validation: a POST must carry a __RequestVerificationToken both as a cookie
// and as a matching form field, or the server returns HTTP 500. Fetch first
// GETs the page to obtain the session cookie and scrape the hidden token field,
// then POSTs the date range with the cookie and token. Rows are returned as
// published, not clipped to the window.
package bota

import (
	"bytes"
	"context"
	"errors"
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

const (
	formURL    = "https://www.bot.go.tz/ExchangeRate/previous_rates"
	tokenField = "__RequestVerificationToken"
)

var (
	tokenPattern       = regexp.MustCompile(`name="` + tokenField + `"[^>]*value="([^"]+)"`)
	aliases            = map[string]string{"SDR": "XDR"}
	excludedCurrencies = []string{"GOLD", "ATS", "NLG", "MZM", "ZWD", "CUC"}
)

func init() {
	adapter.Register("BOTA", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BOTA rates.
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
	if upto.IsZero() {
		upto = a.Today()
	}

	req, err := a.NewRequest(ctx, http.MethodGet, formURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	cookie := adapter.CookieHeader(resp.Header)
	m := tokenPattern.FindSubmatch(resp.Body)
	if m == nil {
		return nil, errors.New("no antiforgery token")
	}

	form := url.Values{
		tokenField: {string(m[1])},
		"dateFrom": {after.Format("01/02/2006")},
		"dateTo":   {upto.Format("01/02/2006")},
	}
	req, err = a.NewRequest(ctx, http.MethodPost, formURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", cookie)
	resp, err = a.Do(req)
	if err != nil {
		return nil, err
	}

	if err := a.Sleep(ctx, 2*time.Second); err != nil {
		return nil, err
	}
	return parse(resp.Body)
}

func parse(html []byte) ([]adapter.Rate, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, err
	}
	tbody := doc.Find("tbody").First()
	if tbody.Length() == 0 {
		return nil, errors.New("rates table not found in previous_rates response")
	}

	var rates []adapter.Rate
	for _, tr := range tbody.ChildrenFiltered("tr").EachIter() {
		r, ok, err := parseRow(tr.ChildrenFiltered("td"))
		if err != nil {
			return nil, err
		}
		if ok {
			rates = append(rates, r)
		}
	}
	return rates, nil
}

func parseRow(cells *goquery.Selection) (adapter.Rate, bool, error) {
	if cells.Length() < 6 {
		return adapter.Rate{}, false, nil
	}

	currency := strings.ToUpper(strings.TrimSpace(cells.Eq(1).Text()))
	if currency == "" || slices.Contains(excludedCurrencies, currency) {
		return adapter.Rate{}, false, nil
	}
	if alias, ok := aliases[currency]; ok {
		currency = alias
	}

	mean, date := cells.Eq(4).Text(), cells.Eq(5).Text()
	if mean == "" || date == "" {
		return adapter.Rate{}, false, nil
	}

	rate, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(mean, ",", "")), 64)
	if err != nil {
		return adapter.Rate{}, false, fmt.Errorf("invalid mean %q: %w", mean, err)
	}
	if rate == 0 {
		return adapter.Rate{}, false, nil
	}

	d, err := adapter.ParseDate(strings.TrimSpace(date), "2-Jan-06")
	if err != nil {
		return adapter.Rate{}, false, err
	}
	return adapter.Rate{Date: d, Base: currency, Quote: "TZS", Rate: rate}, true, nil
}
