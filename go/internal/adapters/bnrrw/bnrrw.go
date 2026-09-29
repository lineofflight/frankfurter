// Package bnrrw fetches rates from Banque Nationale du Rwanda, which publishes daily reference rates for 16 currencies
// against the Rwandan franc through a public JSON API at fxrates.bnr.rw. Rates are RWF per unit of the foreign
// currency, so RWF is the quote.
//
// The endpoint serves one currency per request, so a fetch iterates the currency list. buying_rate, average_rate and
// selling_rate are published; average_rate is the mid (issue #314). Some historical values carry thousands commas
// ("1,253.60"). Key BNR is taken by Banca Națională a României.
//
// Fetch does not clip to the window: it returns whatever the API serves for start_date..end_date, and the API includes
// start_date, as the Ruby adapter does.
package bnrrw

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://fxrates.bnr.rw/currency_history/"

// currencies verified live 2026-05-24. SDR returns no data so it's omitted.
var currencies = []string{
	"USD", "EUR", "GBP", "JPY", "CHF", "CAD", "AUD", "CNY",
	"INR", "ZAR", "AED", "SAR", "KES", "UGX", "TZS", "BIF",
}

var codePattern = regexp.MustCompile(`\A[A-Z]{3}\z`)

func init() {
	adapter.Register("BNRRW", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BNRRW rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 365 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	start, end := after, upto
	if start.IsZero() {
		start = a.Today().AddDate(0, 0, -30)
	}
	if end.IsZero() {
		end = a.Today()
	}

	var rates []adapter.Rate
	for i, currency := range currencies {
		if i > 0 {
			if err := a.Sleep(ctx, 300*time.Millisecond); err != nil {
				return nil, err
			}
		}
		body, err := a.Get(ctx, baseURL, url.Values{
			"currency_name": {currency},
			"start_date":    {start.Format(time.DateOnly)},
			"end_date":      {end.Format(time.DateOnly)},
		})
		if err != nil {
			return nil, err
		}
		parsed, err := parse(body)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

func parse(data []byte) ([]adapter.Rate, error) {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("expected JSON array from currency_history, got %T", raw)
	}

	var rates []adapter.Rate
	for _, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected JSON object entry, got %T", e)
		}
		code, _ := entry["currency_name"].(string)
		if !codePattern.MatchString(code) {
			continue
		}
		rateVal, ok := entry["average_rate"]
		if !ok || rateVal == nil {
			continue
		}
		text := strings.ReplaceAll(fmt.Sprint(rateVal), ",", "")
		rate, ok := adapter.ParseFloat(text)
		if !ok {
			return nil, fmt.Errorf("invalid average_rate %q", text)
		}
		if rate <= 0 {
			continue
		}
		dateStr, ok := entry["post_date"].(string)
		if !ok {
			continue
		}
		date, err := time.Parse("2-Jan-06", dateStr)
		if err != nil {
			return nil, fmt.Errorf("invalid post_date %q: %w", dateStr, err)
		}
		rates = append(rates, adapter.Rate{Date: date, Base: code, Quote: "RWF", Rate: rate})
	}
	return rates, nil
}
