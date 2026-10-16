// Package bceao fetches rates from the Central Bank of West African States
// (Banque Centrale des Etats de l'Afrique de l'Ouest), which publishes daily
// reference rates for 27 currencies against the CFA Franc (XOF). The API only
// accepts a single date per request, so Fetch iterates day by day, skipping
// Saturdays.
//
// As in the Ruby adapter, after is inclusive: the first request is for after
// itself.
package bceao

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.bceao.int/fr/cours/get_all_reference_by_date"

var currencies = map[string]string{
	"Euro":                       "EUR",
	"Dollar us":                  "USD",
	"Yen japonais":               "JPY",
	"Couronne danoise":           "DKK",
	"Couronne suédoise":          "SEK",
	"Livre sterling":             "GBP",
	"Couronne norvégienne":       "NOK",
	"Couronne thèque":            "CZK",
	"Forint hongrois":            "HUF",
	"Zloty polonais":             "PLN",
	"Franc suisse":               "CHF",
	"Dollar canadien":            "CAD",
	"Dollar australien":          "AUD",
	"Dollar néo-zélandais":       "NZD",
	"Rand sud-africain":          "ZAR",
	"Yuan chinois":               "CNY",
	"Roupie Indienne":            "INR",
	"Baht thailandais":           "THB",
	"Real brésilien":             "BRL",
	"Dollar singapourien":        "SGD",
	"Nouvelle livre turque":      "TRY",
	"Dirham Emirats Arabes Unis": "AED",
	"Nouveau Shekel":             "ILS",
	"Won Coréen":                 "KRW",
	"Dollar Hong Kong":           "HKD",
	"Ryal Saudien":               "SAR",
	"Dinar Koweitien":            "KWD",
}

var rowPattern = regexp.MustCompile(`<tr>[^<]*<td>([^<]*)</td>[^<]*<td>([\d.,]+)</td>[^<]*</tr>`)

func init() {
	adapter.Register("BCEAO", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BCEAO rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 90 }

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
		// Publishes Sun-Fri; Sunday covers Gulf markets (AED, SAR, KWD)
		if date.Weekday() == time.Saturday {
			continue
		}
		if !first {
			if err := a.Sleep(ctx, time.Second); err != nil {
				return nil, err
			}
		}
		first = false

		body, err := a.Get(ctx, baseURL, url.Values{"dateJour": {date.Format("2006-01-02")}})
		if err != nil {
			return nil, err
		}
		day, err := parse(string(body), date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, day...)
	}
	return rates, nil
}

func parse(html string, date time.Time) ([]adapter.Rate, error) {
	if !strings.Contains(html, "<table") {
		// Holidays render just the day header with no rates table (observed
		// 2026-01-01)
		if strings.Contains(html, "Cours des devises") {
			return nil, nil
		}
		return nil, fmt.Errorf("neither rates table nor day header in response for %s", date.Format("2006-01-02"))
	}

	var rates []adapter.Rate
	for _, m := range rowPattern.FindAllStringSubmatch(html, -1) {
		// Trim what Ruby's String#strip trims: ASCII whitespace and NUL, not
		// U+00A0.
		iso, ok := currencies[strings.Trim(m[1], " \t\n\v\f\r\x00")]
		if !ok {
			continue
		}
		// Rates normally use French format (period=thousands, comma=decimal).
		// Fall back to English format if no comma is present.
		text := m[2]
		if strings.Contains(text, ",") {
			text = strings.ReplaceAll(strings.ReplaceAll(text, ".", ""), ",", ".")
		}
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, fmt.Errorf("parse rate %q: %w", m[2], err)
		}
		if value == 0 {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: iso, Quote: "XOF", Rate: value})
	}
	return rates, nil
}
