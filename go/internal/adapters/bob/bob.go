// Package bob fetches rates from the Bank of Botswana, which publishes daily rates for about 7 currencies against BWP.
// The CSV export only contains these columns.
//
// Fetch keeps rows dated on or after `after` (inclusive, as the Ruby adapter does) and ignores upto.
package bob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const csvURL = "https://www.bankofbotswana.bw/export/exchange-rates.csv?page&_format=csv"

// columns maps CSV headers to ISO currency codes, in the order rows are emitted.
var columns = []struct{ col, iso string }{
	{"CHN", "CNY"},
	{"EUR", "EUR"},
	{"GBP", "GBP"},
	{"USD", "USD"},
	{"YEN", "JPY"},
	{"ZAR", "ZAR"},
}

func init() {
	adapter.Register("BOB", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BOB rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, _ time.Time) ([]adapter.Rate, error) {
	body, err := a.Get(ctx, csvURL, nil)
	if err != nil {
		return nil, err
	}
	rates, err := parse(body)
	if err != nil {
		return nil, err
	}
	if after.IsZero() {
		return rates, nil
	}
	return slices.DeleteFunc(rates, func(r adapter.Rate) bool { return r.Date.Before(after) }), nil
}

func parse(data []byte) ([]adapter.Rate, error) {
	text := string(bytes.TrimPrefix(data, []byte("\uFEFF")))
	lines := strings.Split(text, "\n")
	if n := len(lines); lines[n-1] == "" {
		lines = lines[:n-1]
	}
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}

	dateIndex := -1
	var headers []string
	if len(lines) > 0 {
		headers = strings.Split(lines[0], ",")
		dateIndex = slices.Index(headers, "Date")
	}
	if dateIndex < 0 {
		return nil, errors.New("date column missing from exchange-rates.csv header")
	}

	type column struct {
		iso string
		idx int
	}
	var cols []column
	for _, c := range columns {
		if idx := slices.Index(headers, c.col); idx >= 0 {
			cols = append(cols, column{c.iso, idx})
		}
	}

	var rates []adapter.Rate
	for _, line := range lines[1:] {
		values := strings.Split(strings.ReplaceAll(line, `"`, ""), ",")
		if dateIndex >= len(values) {
			return nil, fmt.Errorf("date missing in row %q", line)
		}
		date, err := parseDate(strings.TrimSpace(values[dateIndex]))
		if err != nil {
			return nil, err
		}
		for _, c := range cols {
			if c.idx >= len(values) || values[c.idx] == "" {
				continue
			}
			rate, err := strconv.ParseFloat(strings.TrimSpace(values[c.idx]), 64)
			if err != nil || math.IsNaN(rate) || math.IsInf(rate, 0) {
				return nil, fmt.Errorf("invalid rate %q for %s", values[c.idx], c.iso)
			}
			if rate == 0 {
				continue
			}
			rates = append(rates, adapter.Rate{Date: date, Base: "BWP", Quote: c.iso, Rate: rate})
		}
	}
	sort.SliceStable(rates, func(i, j int) bool { return rates[i].Date.Before(rates[j].Date) })
	return rates, nil
}

// parseDate accepts abbreviated or full month names, as strptime's %b does.
func parseDate(s string) (time.Time, error) {
	if d, err := time.Parse("2 Jan 2006", s); err == nil {
		return d, nil
	}
	return time.Parse("2 January 2006", s)
}
