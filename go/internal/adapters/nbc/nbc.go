// Package nbc fetches rates from the National Bank of Cambodia, which publishes daily reference rates for ~29
// currencies against the Cambodian riel (KHR), Mon-Fri ~16:30 Asia/Phnom Penh.
//
// The page is form-based: a GET returns a hidden CSRF token (tk) and sets a session cookie; a POST with exdate, tk and
// view=View returns that date's HTML table. The token rotates per request, so every historical fetch is a
// GET-then-POST round trip.
//
// Rates are quoted as <CCY>/KHR with a unit multiplier (1, 100, 1000) and bid/ask/average columns. We use the
// published average as the rate and divide by the unit. The cross-rate table omits USD; we read the headline
// "KHR / USD" official exchange rate separately. Rows keep NBC's direction: foreign currency as base, KHR as quote.
// SDR is rewritten to XDR.
//
// Unlike most adapters, Fetch treats after as inclusive, as the Ruby adapter does (after.upto(end_date)).
package nbc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.nbc.gov.kh/english/economic_research/exchange_rate.php"

var (
	symbolPattern = regexp.MustCompile(`\A([A-Z]{3})/KHR\z`)
	oerPattern    = regexp.MustCompile(`\A(\d+)\z`)
	codeAliases   = map[string]string{"SDR": "XDR"}
)

func init() {
	adapter.Register("NBC", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches NBC rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange keeps chunks tiny: the endpoint serves one day per CSRF round trip.
func (a *Adapter) BackfillRange() int { return 1 }

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
	for date := after; !date.After(end); date = date.AddDate(0, 0, 1) {
		if date.Weekday() == time.Sunday {
			continue
		}
		day, err := a.fetchDate(ctx, date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, day...)
	}
	return rates, nil
}

// fetchDate returns an error on any non-2xx status: CloudFront's WAF intermittently 403s the POST, and that body
// would otherwise parse as an empty (holiday) day.
func (a *Adapter) fetchDate(ctx context.Context, date time.Time) ([]adapter.Rate, error) {
	if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
		return nil, err
	}

	req, err := a.NewRequest(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return nil, err
	}
	page, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	token, ok, err := extractToken(page.Body)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("CSRF token not found on landing page")
	}

	form := url.Values{"exdate": {date.Format("2006-01-02")}, "tk": {token}, "view": {"View"}}
	req, err = a.NewRequest(ctx, http.MethodPost, baseURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", baseURL)
	if cookies := adapter.CookieHeader(page.Header); cookies != "" {
		req.Header.Set("Cookie", cookies)
	}
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	return parse(resp.Body, date)
}

// extractToken reports whether the tk input carries a value attribute; an empty value is still posted, as in Ruby.
func extractToken(html []byte) (string, bool, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return "", false, err
	}
	token, ok := doc.Find("input[name='tk']").First().Attr("value")
	return token, ok, nil
}

func parse(html []byte, date time.Time) ([]adapter.Rate, error) {
	// Holidays and Saturdays render "There is no data available." within the normal page chrome.
	if bytes.Contains(html, []byte("There is no data available")) {
		return nil, nil
	}
	if !bytes.Contains(html, []byte("<table")) {
		return nil, fmt.Errorf("no rates table in response for %s", date.Format("2006-01-02"))
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	doc.Find("tr").Each(func(_ int, row *goquery.Selection) {
		cells := row.Find("td")
		if cells.Length() < 6 {
			return
		}
		cell := func(i int) string { return strip(cells.Eq(i).Text()) }

		m := symbolPattern.FindStringSubmatch(cell(1))
		if m == nil {
			return
		}
		unit, err := strconv.Atoi(cell(2))
		if err != nil || unit == 0 {
			return
		}
		// Not adapter.ParseFloat: its TrimSpace drops the &nbsp; that makes Ruby's Float reject a cell.
		average, err := strconv.ParseFloat(strings.ReplaceAll(cell(5), ",", ""), 64)
		if err != nil || math.IsNaN(average) || math.IsInf(average, 0) || average == 0 {
			return
		}
		base := m[1]
		if alias, ok := codeAliases[base]; ok {
			base = alias
		}
		rates = append(rates, adapter.Rate{Date: date, Base: base, Quote: "KHR", Rate: average / float64(unit)})
	})

	if oer, ok := extractOER(doc); ok {
		rates = append(rates, adapter.Rate{Date: date, Base: "USD", Quote: "KHR", Rate: oer})
	}
	return rates, nil
}

// extractOER reads the headline "Official Exchange Rate : <font>4022</font> KHR / USD".
func extractOER(doc *goquery.Document) (float64, bool) {
	var rate float64
	var found bool
	doc.Find("font").EachWithBreak(func(_ int, font *goquery.Selection) bool {
		parent := font.Parent().Text()
		if !strings.Contains(parent, "KHR") || !strings.Contains(parent, "USD") {
			return true
		}
		m := oerPattern.FindStringSubmatch(strip(font.Text()))
		if m == nil {
			return true
		}
		r, err := strconv.ParseFloat(m[1], 64)
		if err != nil || r == 0 {
			return true
		}
		rate, found = r, true
		return false
	})
	return rate, found
}

// strip is Ruby's String#strip, which unlike strings.TrimSpace keeps non-breaking spaces (the page is full of
// &nbsp;).
func strip(s string) string {
	return strings.Trim(s, " \t\n\v\f\r\x00")
}
