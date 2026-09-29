// Package cbm fetches rates from the Central Bank of Myanmar, which publishes daily reference rates for about 37
// currencies as MMK per unit of foreign currency through a public JSON API.
//
// No historical API is available: only the latest rates are fetched, and Fetch ignores its bounds as the Ruby adapter
// does.
package cbm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://forex.cbm.gov.mm/api/latest"

func init() {
	adapter.Register("CBM", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBM rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, _, _ time.Time) ([]adapter.Rate, error) {
	body, err := a.Get(ctx, baseURL, nil)
	if err != nil {
		return nil, err
	}
	return parse(body)
}

func parse(data []byte) ([]adapter.Rate, error) {
	var doc struct {
		Timestamp json.RawMessage `json:"timestamp"`
		Rates     json.RawMessage `json:"rates"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if isNull(doc.Timestamp) || isNull(doc.Rates) {
		return nil, errors.New("timestamp or rates missing from latest response")
	}

	ts, err := strconv.ParseInt(unquote(doc.Timestamp), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp %s", doc.Timestamp)
	}
	t := time.Unix(ts, 0).UTC()
	date := adapter.Date(t.Year(), t.Month(), t.Day())

	pairs, err := orderedPairs(doc.Rates)
	if err != nil {
		return nil, err
	}
	var rates []adapter.Rate
	for _, p := range pairs {
		if p.key == "MMK" {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(unquote(p.value)), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid rate %s for %s", p.value, p.key)
		}
		if v == 0 {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: p.key, Quote: "MMK", Rate: v})
	}
	return rates, nil
}

type pair struct {
	key   string
	value json.RawMessage
}

// orderedPairs decodes a JSON object keeping key order, as Ruby's Hash does.
func orderedPairs(raw json.RawMessage) ([]pair, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("rates is not an object")
	}
	var pairs []pair
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		pairs = append(pairs, pair{tok.(string), v})
	}
	return pairs, nil
}

// isNull reports whether a field is absent or falsy, which Ruby's `unless timestamp && rates` rejects.
func isNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null" || string(raw) == "false"
}

// unquote returns a JSON string's contents, or the raw text of a number.
func unquote(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}
