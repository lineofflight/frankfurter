// Package bct fetches rates from the Banque Centrale de Tunisie, which
// publishes daily interbank reference rates for 20 currencies against the
// Tunisian dinar (TND).
//
// The endpoint accepts a single date per POST and returns an HTML fragment with
// two tables: the interbank reference rates and a manual-exchange table below.
// We parse only the first.
//
// Caveats:
//   - The meta tag declares ISO-8859-1 but the bytes are UTF-8; we trust whichever decoding yields valid characters.
//   - The decimal separator is the French comma, and rates may be quoted per 1, 10, 100 or 1000 units (JPY per 1000).
//   - When the requested date has no data the page either echoes an "Exhausted Resultset" notice or silently falls
//     back to a nearby trading day, so we check the echoed "Journée du DD/MM/YYYY" and otherwise drop the records.
//   - The POST requires a Referer header.
//
// Fetch walks every weekday from after through upto, both inclusive, as the
// Ruby adapter does.
package bct

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/text/encoding/charmap"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	pageURL = "https://www.bct.gov.tn/bct/siteprod/cours_archiv.jsp"
	referer = "https://www.bct.gov.tn/bct/siteprod/cours_archive.jsp"
)

var (
	dateRE   = regexp.MustCompile(`Journée du\s*(\d{2})/(\d{2})/(\d{4})`)
	sigleRE  = regexp.MustCompile(`\A[A-Z]{3}\z`)
	numberRE = regexp.MustCompile(`[0-9.,]+`)
)

func init() {
	adapter.Register("BCT", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BCT rates.
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
			if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
				return nil, err
			}
		}
		first = false

		day, err := a.fetchDate(ctx, date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, day...)
	}
	return rates, nil
}

func (a *Adapter) fetchDate(ctx context.Context, date time.Time) ([]adapter.Rate, error) {
	form := url.Values{"input": {date.Format("2006-01-02")}, "langue": {"_AN"}}
	req, err := a.NewRequest(ctx, http.MethodPost, pageURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", referer)
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	return parse(decode(resp.Body), date)
}

// decode returns the body as UTF-8, transcoding from ISO-8859-1 only when it
// isn't valid UTF-8.
func decode(body []byte) string {
	if utf8.Valid(body) {
		return string(body)
	}
	s, _ := charmap.ISO8859_1.NewDecoder().Bytes(body)
	return string(s)
}

func parse(html string, date time.Time) ([]adapter.Rate, error) {
	echoed, ok := echoedDate(html)
	if !ok {
		// No-data days can serve an undated "Exhausted Resultset" error page.
		if strings.Contains(html, "Exhausted Resultset") {
			return nil, nil
		}
		return nil, fmt.Errorf("no dated rates page in response for %s", date.Format("2006-01-02"))
	}
	if !echoed.Equal(date) {
		return nil, nil
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader([]byte(html)))
	if err != nil {
		return nil, err
	}
	table := doc.Find("table").First()
	if table.Length() == 0 {
		return nil, fmt.Errorf("no rates table on page for %s", date.Format("2006-01-02"))
	}

	var rates []adapter.Rate
	table.Find("tr").Each(func(_ int, row *goquery.Selection) {
		var cells []string
		row.Find("td").Each(func(_ int, c *goquery.Selection) {
			cells = append(cells, strip(c.Text()))
		})
		if len(cells) < 4 || !sigleRE.MatchString(cells[1]) {
			return
		}
		unit, ok1 := parseNumber(cells[2])
		value, ok2 := parseNumber(cells[3])
		if !ok1 || !ok2 || unit == 0 || value == 0 {
			return
		}
		rates = append(rates, adapter.Rate{Date: date, Base: cells[1], Quote: "TND", Rate: value / unit})
	})
	return rates, nil
}

func echoedDate(html string) (time.Time, bool) {
	m := dateRE.FindStringSubmatch(html)
	if m == nil {
		return time.Time{}, false
	}
	d, err := time.Parse("02/01/2006", m[1]+"/"+m[2]+"/"+m[3])
	if err != nil {
		return time.Time{}, false
	}
	return d, true
}

// parseNumber reads a French-format number: dot as thousands separator, comma
// as decimal.
func parseNumber(s string) (float64, bool) {
	m := numberRE.FindString(strip(s))
	if m == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(strings.ReplaceAll(m, ".", ""), ",", "."), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// strip trims the whitespace Ruby's String#strip removes, leaving non-breaking
// spaces alone.
func strip(s string) string {
	return strings.Trim(s, " \t\n\v\f\r\x00")
}
