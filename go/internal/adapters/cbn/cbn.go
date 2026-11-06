// Package cbn fetches rates from the Central Bank of Nigeria, which publishes
// daily official exchange rates against the Nigerian naira. One JSON request
// returns the full historical dataset (2001-12-10 to present), which is fetched
// once and filtered by date in memory.
//
// Rates use the centralrate field (mid of buy/sell). NGN is the pivot currency,
// stored in the quote position; the foreign currency is the base.
//
// Currency names arrive with whitespace variants and dual spellings
// ("YEN"/"JAPANESE YEN", "POUND STERLING"/"POUNDS STERLING"), so they are
// normalized via nameToISO, which also maps SDR to XDR. WAUA (West African Unit
// of Account) is not ISO 4217 and is therefore not mapped.
//
// Terms: https://www.cbn.gov.ng/Legal.html (redistribution permitted with
// attribution, content may not be altered).
package cbn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.cbn.gov.ng/api/GetAllExchangeRates"

// nameToISO maps a normalized (trimmed, upper-cased) currency name to its ISO
// 4217 code.
var nameToISO = map[string]string{
	"CFA":                "XOF",
	"DANISH KRONA":       "DKK",
	"DANISH KRONER":      "DKK",
	"EURO":               "EUR",
	"JAPANESE YEN":       "JPY",
	"POUND STERLING":     "GBP",
	"POUNDS STERLING":    "GBP",
	"RIYAL":              "SAR",
	"SDR":                "XDR",
	"SOUTH AFRICAN RAND": "ZAR",
	"SWISS FRANC":        "CHF",
	"UAE DIRHAM":         "AED",
	"US DOLLAR":          "USD",
	"YEN":                "JPY",
	"YUAN/RENMINBI":      "CNY",
}

func init() {
	adapter.Register("CBN", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBN rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	body, err := a.Get(ctx, baseURL, nil)
	if err != nil {
		return nil, err
	}
	rates, err := parse(body)
	if err != nil {
		return nil, err
	}
	return adapter.Window(rates, after, upto), nil
}

type entry struct {
	Currency    any    `json:"currency"`
	RateDate    string `json:"ratedate"`
	CentralRate any    `json:"centralrate"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var entries []entry
	if err := json.Unmarshal(data, &entries); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) && typeErr.Field == "" {
			return nil, fmt.Errorf("expected JSON array from %s", baseURL)
		}
		return nil, err
	}

	var rates []adapter.Rate
	for _, e := range entries {
		iso, ok := nameToISO[strings.ToUpper(strip(text(e.Currency)))]
		if !ok {
			continue
		}

		value := text(e.CentralRate)
		if strip(value) == "" {
			continue
		}
		rate, ok := adapter.ParseFloat(value)
		if !ok {
			return nil, fmt.Errorf("invalid centralrate %q", value)
		}
		if rate == 0 {
			continue
		}

		date, err := adapter.ParseDate(strings.TrimSpace(e.RateDate), "2006-01-02", "2006-01-02T15:04:05")
		if err != nil {
			return nil, err
		}

		rates = append(rates, adapter.Rate{Date: date, Base: iso, Quote: "NGN", Rate: rate})
	}
	return rates, nil
}

// strip is Ruby's String#strip: it trims ASCII whitespace and NUL but not
// Unicode spaces such as NBSP, so an NBSP-padded name stays unmapped as it does
// in Ruby.
func strip(s string) string {
	return strings.Trim(s, " \t\n\v\f\r\x00")
}

// text is Ruby's to_s for a decoded JSON scalar: nil becomes "".
func text(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}
