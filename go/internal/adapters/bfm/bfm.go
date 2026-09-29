// Package bfm fetches rates from Banky Foiben'i Madagasikara, which publishes daily reference rates in ariary per
// foreign unit.
//
// MID means the interbank FX market; coursMid is the reference, while coursMidMin/Max are daily extremes, not bid/ask
// prices. Unlike most adapters, Fetch treats after as inclusive, as the Ruby adapter does.
package bfm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.banky-foibe.mg/admin/wp-json/bfm/cours_mid_en_ar_filter"

var coverageStart = adapter.Date(2018, 1, 2)

var currencies = []string{"EUR", "USD", "GBP", "CHF", "JPY", "CAD", "DKK", "NOK", "SEK", "DJF", "XDR", "MUR", "ZAR",
	"AUD", "HKD", "SGD", "NZD", "INR", "CNY"}

func init() {
	adapter.Register("BFM", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BFM rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 365 }

// Fetch implements adapter.Adapter. The source is queried per currency in windows of at most a year.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	start, end := after, upto
	if start.IsZero() {
		start = coverageStart
	}
	if end.IsZero() {
		end = a.Today()
	}
	var out []adapter.Rate
	first := true
	for cursor := start; !cursor.After(end); {
		last := cursor.AddDate(0, 0, a.BackfillRange()-1)
		if last.After(end) {
			last = end
		}
		for _, code := range currencies {
			if !first {
				if err := a.Sleep(ctx, 200*time.Millisecond); err != nil {
					return nil, err
				}
			}
			first = false
			body, err := a.post(ctx, url.Values{
				"dateFilterDebut": {cursor.Format("2006/01/02")},
				"dateFilterFin":   {last.Format("2006/01/02")},
				"filterData":      {code},
			})
			if err != nil {
				return nil, err
			}
			rates, err := parse(body, code)
			if err != nil {
				return nil, err
			}
			for _, r := range rates {
				if !r.Date.Before(start) && !r.Date.After(end) {
					out = append(out, r)
				}
			}
		}
		cursor = last.AddDate(0, 0, 1)
	}
	return out, nil
}

func (a *Adapter) post(ctx context.Context, form url.Values) ([]byte, error) {
	req, err := a.NewRequest(ctx, http.MethodPost, baseURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://www.banky-foibe.mg")
	req.Header.Set("Referer", "https://www.banky-foibe.mg/taux-reference")
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

type payload struct {
	Data struct {
		Status json.Number `json:"status"`
		Data   struct {
			CoursMid json.RawMessage `json:"coursMid"`
		} `json:"data"`
	} `json:"data"`
}

func parse(data []byte, code string) ([]adapter.Rate, error) {
	invalid := fmt.Errorf("missing or invalid reference-rate data for %s", code)
	var p payload
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%w: %v", invalid, err)
	}
	if status, err := p.Data.Status.Float64(); err != nil || status != 200 {
		return nil, invalid
	}
	rows := bytes.TrimSpace(p.Data.Data.CoursMid)
	if len(rows) == 0 || rows[0] != '{' {
		var empty []json.RawMessage
		if json.Unmarshal(rows, &empty) == nil && empty != nil && len(empty) == 0 {
			return nil, nil
		}
		return nil, invalid
	}

	// Walk the object with a token decoder to keep the published order.
	dec := json.NewDecoder(bytes.NewReader(rows))
	dec.UseNumber()
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	var rates []adapter.Rate
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, invalid
		}
		var value any
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		rate, ok := parseRate(value)
		if !ok {
			continue
		}
		date, err := time.Parse(time.DateOnly, key)
		if err != nil {
			return nil, err
		}
		rates = append(rates, adapter.Rate{Date: date, Base: code, Quote: "MGA", Rate: rate})
	}
	return rates, nil
}

var cleaner = strings.NewReplacer(" ", "", " ", "", " ", "", ",", ".")

// parseRate mirrors BigDecimal(value.to_s.delete("   ").tr(",", "."), exception: false) followed by the
// finite and positive checks. A null, boolean or nested value never parses.
func parseRate(value any) (float64, bool) {
	var s string
	switch v := value.(type) {
	case string:
		s = v
	case json.Number:
		s = v.String()
	default:
		return 0, false
	}
	f, ok := adapter.ParseFloat(cleaner.Replace(s))
	if !ok || f <= 0 {
		return 0, false
	}
	return f, true
}
