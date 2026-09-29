// Package bom fetches rates from the Bank of Mongolia, which publishes daily statutory reference rates in MNT for 38
// currencies plus XAU and XAG. They are the official reference for customs, tax and accounting in Mongolia (Law on
// Currency Regulation, Article 5(2)).
//
// The movement endpoint returns the entire archive (2001-01-02 onward) in one ~5 MB JSON response regardless of the
// requested range, so we fetch once and slice client-side. The archive is refreshed in arrears in periodic batches.
//
// The source publishes "1 foreign = X MNT", so the foreign currency is the base. All quotes are per unit, so
// high-denomination currencies show as small fractions (IDR=0.20 MNT) with no per-100 normalization. SDR is rewritten
// to XDR. XAU and XAG stay per troy ounce, as published.
//
// Unlike adapter.Window, after is inclusive, as in the Ruby adapter.
package bom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/db"
)

const endpoint = "https://www.mongolbank.mn/en/currency-rate-movement/data"

var (
	codeAliases = map[string]string{"SDR": "XDR"}
	codePattern = regexp.MustCompile(`\A[A-Z]{3}\z`)
)

func init() {
	adapter.Register("BOM", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BOM rates.
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
		start = adapter.Date(2001, 1, 1)
	}
	if end.IsZero() {
		end = a.Today()
	}
	body, err := json.Marshal(map[string]string{"startDate": db.FormatDate(start), "endDate": db.FormatDate(end)})
	if err != nil {
		return nil, err
	}
	req, err := a.NewRequest(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	rates, err := parse(resp.Body)
	if err != nil {
		return nil, err
	}
	kept := rates[:0]
	for _, r := range rates {
		if (!after.IsZero() && r.Date.Before(after)) || (!upto.IsZero() && r.Date.After(upto)) {
			continue
		}
		kept = append(kept, r)
	}
	return kept, nil
}

func parse(data []byte) ([]adapter.Rate, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid JSON from movement endpoint: %w", err)
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected JSON object from movement endpoint, got %T", doc)
	}
	rows, ok := obj["data"].([]any)
	if !ok {
		return nil, errors.New("data array missing from movement response")
	}

	var rates []adapter.Rate
	for _, r := range rows {
		row, ok := r.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected row object, got %T", r)
		}
		parsed, err := parseRow(row)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

func parseRow(row map[string]any) ([]adapter.Rate, error) {
	dateStr, ok := row["RATE_DATE"].(string)
	if !ok {
		return nil, errors.New("row missing RATE_DATE")
	}
	date, err := time.Parse(time.DateOnly, dateStr)
	if err != nil {
		return nil, err
	}

	// Go maps are unordered; sort the codes so output is deterministic.
	codes := make([]string, 0, len(row))
	for code := range row {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	var rates []adapter.Rate
	for _, code := range codes {
		if !codePattern.MatchString(code) {
			continue
		}
		rate, ok := parseRate(row[code])
		if !ok || rate == 0 {
			continue
		}
		base := code
		if alias, ok := codeAliases[code]; ok {
			base = alias
		}
		rates = append(rates, adapter.Rate{Date: date, Base: base, Quote: "MNT", Rate: rate})
	}
	return rates, nil
}

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
	return adapter.ParseFloat(strings.ReplaceAll(s, ",", ""))
}
