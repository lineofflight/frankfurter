// Package bcp fetches rates from Banco Central del Paraguay, which publishes
// the "tipo de cambio referencial interbancario", a weighted average of
// interbank spot operations, on business days against the Paraguayan guaraní
// (PYG).
//
// The historical endpoint returns a 12-month x 31-day matrix per (year,
// currency); ND cells mark non-trading days. Rates are PYG per foreign unit, so
// PYG goes in quote and the foreign currency in base. XAU is published per troy
// ounce already.
package bcp

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.bcp.gov.py/webapps/web/cotizacion/monedas-historica"

// currencies are the quote currencies on the daily snapshot. Historical depth
// varies (USD/EUR back to 2001, most others from ~2012), so empty cells are
// expected on older years.
//
// SDR/XDR is omitted: BCP exposes ?moneda=SDR but the page returns 100% ND for
// every year, so BCP doesn't actually publish it.
var currencies = []string{
	"USD", "EUR", "GBP", "JPY", "CHF", "CAD", "AUD", "CNY", "BRL", "ARS", "CLP", "MXN", "UYU",
	"COP", "BOB", "NZD", "ZAR", "SEK", "DKK", "NOK", "AED", "PEN", "SGD", "TWD", "XAU",
}

var dayPattern = regexp.MustCompile(`^\d{1,2}$`)

func init() {
	adapter.Register("BCP", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BCP rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 365 }

// Fetch implements adapter.Adapter. An open upto means today.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	start := end.Year()
	if !after.IsZero() {
		start = after.Year()
	}

	var rates []adapter.Rate
	first := true
	for year := start; year <= end.Year(); year++ {
		for _, currency := range currencies {
			if !first {
				if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
					return nil, err
				}
			}
			first = false

			body, err := a.Get(ctx, baseURL, url.Values{"anho": {strconv.Itoa(year)}, "moneda": {currency}})
			if err != nil {
				return nil, err
			}
			parsed, err := parse(body, year, currency)
			if err != nil {
				return nil, err
			}
			rates = append(rates, parsed...)
		}
	}
	return adapter.Window(rates, after, end), nil
}

func parse(html []byte, year int, currency string) ([]adapter.Rate, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	doc.Find("tbody tr").Each(func(_ int, row *goquery.Selection) {
		dayText := strings.TrimSpace(row.Find("th").First().Text())
		if !dayPattern.MatchString(dayText) {
			return
		}
		day, _ := strconv.Atoi(dayText)
		cells := row.Find("td")
		if cells.Length() < 12 {
			return
		}
		cells.Slice(0, 12).Each(func(i int, cell *goquery.Selection) {
			value, ok := parseValue(cell.Text())
			if !ok {
				return
			}
			date, ok := safeDate(year, time.Month(i+1), day)
			if !ok {
				return
			}
			rates = append(rates, adapter.Rate{Date: date, Base: currency, Quote: "PYG", Rate: value})
		})
	})
	return rates, nil
}

// parseValue reads a pt-BR number such as 7.271,63; ND, empty and non-positive
// cells yield false.
func parseValue(text string) (float64, bool) {
	text = strings.TrimSpace(text)
	if text == "" || text == "ND" {
		return 0, false
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, ".", ""), ",", ".")
	value, ok := adapter.ParseFloat(text)
	if !ok || value <= 0 {
		return 0, false
	}
	return value, true
}

// safeDate rejects combinations like February 30 instead of normalizing them.
func safeDate(year int, month time.Month, day int) (time.Time, bool) {
	date := adapter.Date(year, month, day)
	if date.Day() != day || date.Month() != month {
		return time.Time{}, false
	}
	return date, true
}
