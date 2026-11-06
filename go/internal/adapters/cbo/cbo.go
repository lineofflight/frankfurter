// Package cbo fetches rates from the Central Bank of Oman, which publishes
// daily buying and selling rates against the Omani rial (OMR) for 44
// currencies, plus gold, silver and platinum per troy ounce and the SDR, via a
// SharePoint WebForms search page. History reaches back to 2017-10-15.
//
// The page's export button posts the form back with the ASP.NET VIEWSTATE and
// returns an HTML table dressed up as ExchangeRates.xls. The "All" currency
// option ignores the date fields and returns only the latest snapshot, so a
// date range has to be requested one currency at a time: a fetch is one GET for
// the tokens and currency list followed by one POST per currency. Any range
// works in a single request, so there is no backfill range.
//
// Rates are OMR per unit of foreign currency (1 USD = 0.3845 OMR), so foreign
// goes in base and OMR in quote. A date can carry several intraday rows when
// the bank revised its fixing; the latest timestamp wins.
//
// Unlike most adapters, Fetch treats after as inclusive, as the Ruby adapter
// does.
package cbo

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

const pageURL = "https://cbo.gov.om/Pages/DFESearch.aspx"

var (
	tokens = []string{"__VIEWSTATE", "__VIEWSTATEGENERATOR", "__EVENTVALIDATION"}

	// Cell values arrive as SharePoint-typed strings, e.g. "string;#0.3845".
	valuePrefix = regexp.MustCompile(`\A[a-z]+;#`)

	// The web part's control prefix carries a SharePoint-assigned GUID, so read
	// it off the page rather than pin it.
	prefixPattern = regexp.MustCompile(`name="(ctl00\$[^"]*\$)ddCurrencyCodes"`)

	firstDate = adapter.Date(2017, 10, 15)
)

func init() {
	adapter.Register("CBO", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBO rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	start, end := after, upto
	if start.IsZero() {
		start = firstDate
	}
	if end.IsZero() {
		end = a.Today()
	}
	if start.After(end) {
		return nil, nil
	}

	req, err := a.NewRequest(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	page, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	cookies := adapter.CookieHeader(page.Header)
	html := string(page.Body)
	form, err := extractTokens(html)
	if err != nil {
		return nil, err
	}
	m := prefixPattern.FindStringSubmatch(html)
	if m == nil {
		return nil, fmt.Errorf("currency dropdown not found on %s", pageURL)
	}
	prefix := m[1]
	codes, err := extractCurrencies(page.Body)
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for i, code := range codes {
		if i > 0 {
			if err := a.Sleep(ctx, time.Second); err != nil {
				return nil, err
			}
		}
		form.Set(prefix+"ddCurrencyCodes", code)
		form.Set(prefix+"dateFrom", start.Format("02/01/2006"))
		form.Set(prefix+"dateTo", end.Format("02/01/2006"))
		form.Set(prefix+"btnExport", "")
		req, err := a.NewRequest(ctx, http.MethodPost, pageURL, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Cookie", cookies)
		resp, err := a.Do(req)
		if err != nil {
			return nil, err
		}
		parsed, err := parse(resp.Body)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}

	var out []adapter.Rate
	for _, r := range rates {
		if !r.Date.Before(start) && !r.Date.After(end) {
			out = append(out, r)
		}
	}
	return out, nil
}

func parse(html []byte) ([]adapter.Rate, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, err
	}
	header := doc.Find("tr").FilterFunction(func(_ int, s *goquery.Selection) bool {
		return strings.Contains(s.Text(), "Currency Code")
	}).First()
	if header.Length() == 0 {
		return nil, errors.New("no rates table in export")
	}

	type key struct {
		date time.Time
		code string
	}
	type entry struct {
		published time.Time
		rate      adapter.Rate
	}
	latest := map[key]entry{}
	var order []key

	var parseErr error
	header.NextAll().Filter("tr").EachWithBreak(func(_ int, tr *goquery.Selection) bool {
		var cells []string
		tr.Find("td").Each(func(_ int, td *goquery.Selection) {
			cells = append(cells, strings.TrimSpace(td.Text()))
		})
		if len(cells) < 6 {
			return true
		}
		code := cells[0]
		published, err := time.Parse("2/1/2006 3:04:05 PM", cells[1])
		if err != nil {
			parseErr = err
			return false
		}
		buy, okBuy := number(cells[4])
		sell, okSell := number(cells[5])
		if !okBuy || !okSell || buy <= 0 || sell <= 0 {
			return true
		}
		date := time.Date(published.Year(), published.Month(), published.Day(), 0, 0, 0, 0, time.UTC)
		k := key{date, code}
		prev, seen := latest[k]
		if seen && prev.published.After(published) {
			return true
		}
		if !seen {
			order = append(order, k)
		}
		latest[k] = entry{published, adapter.Rate{
			Date:  date,
			Base:  code,
			Quote: "OMR",
			Rate:  adapter.Midpoint(buy, sell),
			Bid:   adapter.Float(buy),
			Ask:   adapter.Float(sell),
		}}
		return true
	})
	if parseErr != nil {
		return nil, parseErr
	}

	rates := make([]adapter.Rate, 0, len(order))
	for _, k := range order {
		rates = append(rates, latest[k].rate)
	}
	return rates, nil
}

func number(text string) (float64, bool) {
	return adapter.ParseFloat(valuePrefix.ReplaceAllString(text, ""))
}

func extractTokens(html string) (url.Values, error) {
	form := url.Values{}
	for _, name := range tokens {
		re := regexp.MustCompile(`name="` + regexp.QuoteMeta(name) + `"[^>]*value="([^"]*)"`)
		m := re.FindStringSubmatch(html)
		if m == nil {
			return nil, fmt.Errorf("%s not found on %s", name, pageURL)
		}
		form.Set(name, m[1])
	}
	return form, nil
}

func extractCurrencies(html []byte) ([]string, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, err
	}
	var codes []string
	doc.Find("select.ddCurrencyCodes option").Each(func(_ int, o *goquery.Selection) {
		if v, _ := o.Attr("value"); v != "All" {
			codes = append(codes, v)
		}
	})
	if len(codes) == 0 {
		return nil, fmt.Errorf("no currencies listed on %s", pageURL)
	}
	return codes, nil
}
