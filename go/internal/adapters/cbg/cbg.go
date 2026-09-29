// Package cbg fetches rates from the Central Bank of The Gambia, which
// publishes "Daily Valuation Rates": indicative rates for 32 foreign currencies
// against the Gambian dalasi (GMD).
//
// Each per-currency endpoint returns the entire archive as a [[epoch_ms, rate],
// ...] array, so Fetch requests every currency and filters by date in memory.
// The archive reaches back to 2000-01-07 for USD/EUR/GBP; XOF starts 2005 and
// the rest 2019-11-18. Cadence is weekly until 2023, business-daily from 2024;
// weekly gaps are preserved as published.
//
// CBG quotes "1 FOREIGN = X GMD", so the foreign currency is the base and GMD
// the quote. WAUA (West African Unit of Account) is published but is not ISO
// 4217, so it is not requested. Rates come at two decimals, which floors
// high-denomination currencies such as GNF to 0.01; that is what CBG publishes.
package cbg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.cbg.gm/ajax/indicative-exchange-rates/"

// currencies lists the CBG Daily Valuation Rates table, excluding the GMD pivot
// and the non-ISO WAUA composite.
var currencies = []string{
	"USD", "EUR", "GBP", "CHF", "SEK", "CAD", "XOF", "NOK", "DKK", "SAR", "JPY", "AUD", "TWD", "LKR", "THB", "PHP",
	"NZD", "AED", "KWD", "NGN", "HKD", "ZAR", "EGP", "CNY", "BRL", "INR", "GHS", "SLL", "TRY", "GNF", "XDR", "SGD",
}

func init() {
	adapter.Register("CBG", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBG rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	var out []adapter.Rate
	for _, code := range currencies {
		body, err := a.Get(ctx, baseURL+code, nil)
		if err != nil {
			return nil, err
		}
		rates, err := parse(body, code)
		if err != nil {
			return nil, err
		}
		out = append(out, adapter.Window(rates, after, upto)...)
	}
	return out, nil
}

func parse(data []byte, code string) ([]adapter.Rate, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	// Like Ruby's JSON.parse, reject trailing content after the document.
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after JSON document for %s", code)
	}
	entries, ok := doc.([]any)
	if !ok {
		return nil, fmt.Errorf("expected JSON array for %s, got %T", code, doc)
	}

	var rates []adapter.Rate
	for _, e := range entries {
		entry, ok := e.([]any)
		if !ok || len(entry) != 2 || entry[0] == nil || entry[1] == nil {
			continue
		}
		rate, err := toFloat(entry[1])
		if err != nil {
			return nil, err
		}
		if rate == 0 {
			continue
		}
		ms, err := toInt(entry[0])
		if err != nil {
			return nil, err
		}
		// Ruby's Integer#/ floors.
		secs := ms / 1000
		if ms%1000 < 0 {
			secs--
		}
		t := time.Unix(secs, 0).UTC()
		rates = append(rates, adapter.Rate{Date: adapter.Date(t.Year(), t.Month(), t.Day()), Base: code, Quote: "GMD", Rate: rate})
	}
	return rates, nil
}

// toFloat mirrors Ruby's strict Float(value).
func toFloat(v any) (float64, error) {
	var s string
	switch v := v.(type) {
	case json.Number:
		s = v.String()
	case string:
		s = v
	default:
		return 0, fmt.Errorf("invalid rate %v", v)
	}
	f, ok := adapter.ParseFloat(s)
	if !ok {
		return 0, fmt.Errorf("invalid rate %q", s)
	}
	return f, nil
}

// toInt mirrors Ruby's strict Integer(value), which truncates floats.
func toInt(v any) (int64, error) {
	switch v := v.(type) {
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n, nil
		}
		f, err := v.Float64()
		if err != nil {
			return 0, fmt.Errorf("invalid timestamp %v", v)
		}
		return int64(math.Trunc(f)), nil
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid timestamp %q", v)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("invalid timestamp %v", v)
	}
}
