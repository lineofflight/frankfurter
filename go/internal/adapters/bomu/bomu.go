// Package bomu fetches rates from the Bank of Mauritius, which publishes daily averages of banks' indicative retail
// transfer buy/sell rates, in MUR per foreign unit.
//
// Both date filters are required: omitting the end date asks Drupal for the entire remaining archive. Unlike most
// adapters, Fetch keeps rows dated on `after` itself: both bounds are inclusive, as in the Ruby adapter.
package bomu

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

	"github.com/PuerkitoBio/goquery"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	pageURL       = "https://www.bom.mu/markets/foreign-exchange/consolidated-indicative-exchange-rates"
	backfillRange = 31
)

var (
	coverageStart = adapter.Date(2001, 7, 3)
	labelPattern  = regexp.MustCompile(`\A([A-Z]{3})[ \t\n\v\f\r]+([0-9]+)\z`)
)

func init() {
	adapter.Register("BOMU", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BOMU rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return backfillRange }

// Fetch implements adapter.Adapter. It requests the range in chunks of backfillRange days and keeps rows dated from
// after through upto, both inclusive.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	start, end := after, upto
	if start.IsZero() {
		start = coverageStart
	}
	if end.IsZero() {
		end = a.Today()
	}

	var rates []adapter.Rate
	for cursor := start; !cursor.After(end); {
		last := cursor.AddDate(0, 0, backfillRange-1)
		if last.After(end) {
			last = end
		}
		if !cursor.Equal(start) {
			if err := a.Sleep(ctx, 200*time.Millisecond); err != nil {
				return nil, err
			}
		}
		body, err := a.Get(ctx, pageURL, url.Values{
			"field_transaction_date_value[value][date]":   {cursor.Format("02-01-2006")},
			"field_transaction_date_value_1[value][date]": {last.Format("02-01-2006")},
		})
		if err != nil {
			return nil, err
		}
		chunk, err := parse(body)
		if err != nil {
			return nil, err
		}
		rates = append(rates, chunk...)
		cursor = last.AddDate(0, 0, 1)
	}

	kept := rates[:0]
	for _, r := range rates {
		if !r.Date.Before(start) && !r.Date.After(end) {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

func parse(html []byte) ([]adapter.Rate, error) {
	// The live page contains a NUL in its navigation, which otherwise truncates Nokogiri's HTML parser.
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(bytes.ReplaceAll(html, []byte{0}, nil)))
	if err != nil {
		return nil, err
	}
	view := doc.Find(".view-display-id-page").First()
	if view.Length() == 0 {
		return nil, errors.New("missing exchange-rate view")
	}

	// Genuine empty date ranges omit the primary content block. A present but unrecognized table is a parse failure.
	content := view.ChildrenFiltered(".view-content").First()
	if content.Length() == 0 {
		return nil, nil
	}

	// An attachment repeats the filtered rates and a sidebar shows today's quotes. Use only the primary table.
	rows := content.ChildrenFiltered(".table-responsive").ChildrenFiltered("table").
		ChildrenFiltered("tbody").ChildrenFiltered("tr.tblConso")
	if rows.Length() == 0 {
		return nil, errors.New("unrecognized exchange-rate table")
	}

	var rates []adapter.Rate
	for i := range rows.Length() {
		rate, ok, err := parseRow(rows.Eq(i))
		if err != nil {
			return nil, err
		}
		if ok {
			rates = append(rates, rate)
		}
	}
	return rates, nil
}

func parseRow(row *goquery.Selection) (adapter.Rate, bool, error) {
	// Ruby's strip trims only ASCII whitespace, so a cell padded with a non-breaking space stays unparseable.
	cell := func(class string) string { return strings.Trim(row.Find(class).First().Text(), " \t\n\v\f\r\x00") }
	number := func(class string) (float64, bool) {
		s := cell(class)
		if strings.TrimSpace(s) != s {
			return 0, false
		}
		return adapter.ParseFloat(s)
	}

	match := labelPattern.FindStringSubmatch(cell(".views-field-field-currency"))
	if match == nil {
		return adapter.Rate{}, false, nil
	}
	unit, err := strconv.Atoi(match[2])
	if err != nil || unit <= 0 {
		return adapter.Rate{}, false, nil
	}

	buy, okBuy := number(".views-field-php")
	sell, okSell := number(".views-field-php-3")
	if !okBuy || !okSell || buy <= 0 || sell <= 0 {
		return adapter.Rate{}, false, nil
	}

	text := cell(".views-field-field-transaction-date")
	// strptime's %d and %m also take a single digit.
	date, err := time.Parse("2-1-2006", text)
	if err != nil {
		return adapter.Rate{}, false, fmt.Errorf("unrecognised date %q", text)
	}

	bid := adapter.PerUnit(buy, float64(unit))
	ask := adapter.PerUnit(sell, float64(unit))
	return adapter.Rate{
		Date:  date,
		Base:  match[1],
		Quote: "MUR",
		Rate:  adapter.Midpoint(bid, ask),
		Bid:   adapter.Float(bid),
		Ask:   adapter.Float(ask),
	}, true, nil
}
