// Package rba fetches rates from the Reserve Bank of Australia's F11.1 table, a CSV of daily AUD exchange rates and
// the trade-weighted index.
//
// As in Ruby, after is inclusive and upto is ignored: the CSV holds the whole current series.
package rba

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	csvURL       = "https://www.rba.gov.au/statistics/tables/csv/f11.1-data.csv"
	metadataRows = 11
)

var (
	aliases     = map[string]string{"SDR": "XDR"}
	datePattern = regexp.MustCompile(`\A\d{2}-[A-Za-z]{3}-\d{4}\z`)
)

func init() {
	adapter.Register("RBA", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches RBA rates.
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
	kept := rates[:0]
	for _, r := range rates {
		if !r.Date.Before(after) {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

func parse(data []byte) ([]adapter.Rate, error) {
	lines := strings.SplitAfter(strings.ToValidUTF8(string(data), "�"), "\n")

	units := findLine(lines, "Units,")
	if units == nil {
		return nil, errors.New("units header row not found in F11.1 CSV")
	}
	series := findLine(lines, "Series ID,")
	if series == nil {
		return nil, errors.New("series ID header row not found in F11.1 CSV")
	}

	// The trade-weighted index has "Index" as its unit, so its series ID stands in for a code.
	codes := make([]string, len(units)-1)
	for i, unit := range units[1:] {
		code := unit
		if unit == "Index" {
			code = ""
			if i+1 < len(series) {
				code = series[i+1]
			}
		}
		if alias, ok := aliases[code]; ok {
			code = alias
		}
		codes[i] = code
	}

	var rates []adapter.Rate
	for _, line := range lines[min(metadataRows, len(lines)):] {
		row := parseLine(line)
		if len(row) == 0 || !datePattern.MatchString(row[0]) {
			continue
		}
		date, err := adapter.ParseDate(row[0], "02-Jan-2006")
		if err != nil {
			return nil, err
		}
		for i, value := range row[1:] {
			if i >= len(codes) {
				break
			}
			if codes[i] == "" || strings.TrimSpace(value) == "" {
				continue
			}
			rate, ok := adapter.ParseFloat(value)
			if !ok {
				return nil, fmt.Errorf("invalid rate %q on %s", value, row[0])
			}
			rates = append(rates, adapter.Rate{Date: date, Base: "AUD", Quote: codes[i], Rate: rate})
		}
	}
	return rates, nil
}

func findLine(lines []string, prefix string) []string {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return parseLine(l)
		}
	}
	return nil
}

// parseLine is CSV.parse_line: one record, nil for a blank or malformed line.
func parseLine(line string) []string {
	r := csv.NewReader(strings.NewReader(line))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	record, err := r.Read()
	if err != nil {
		return nil
	}
	return record
}
