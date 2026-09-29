// Package ecb fetches the European Central Bank's daily euro reference rates for about 30 currencies from its SDMX
// data API.
//
// The API filters by date itself: startPeriod is inclusive, so a row dated after is kept, as in Ruby.
package ecb

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const sdmxURL = "https://data-api.ecb.europa.eu/service/data/EXR/D..EUR.SP00.A"

var isoCode = regexp.MustCompile(`^[A-Z]{3}$`)

func init() {
	adapter.Register("ECB", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches ECB rates.
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
	params := url.Values{"format": {"csvdata"}}
	if !after.IsZero() {
		params.Set("startPeriod", after.Format(time.DateOnly))
	}
	if !upto.IsZero() {
		params.Set("endPeriod", upto.Format(time.DateOnly))
	}
	body, err := a.Get(ctx, sdmxURL, params)
	if err != nil {
		return nil, err
	}
	return parse(body)
}

func parse(data []byte) ([]adapter.Rate, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.LazyQuotes = true
	r.FieldsPerRecord = -1

	header, err := r.Read()
	if errors.Is(err, io.EOF) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	col := map[string]int{}
	for i, name := range header {
		if _, ok := col[name]; !ok {
			col[name] = i
		}
	}
	field := func(record []string, name string) string {
		if i, ok := col[name]; ok && i < len(record) {
			return record[i]
		}
		return ""
	}

	var rates []adapter.Rate
	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			return rates, nil
		}
		if err != nil {
			return nil, err
		}
		if field(record, "FREQ") != "D" {
			continue
		}
		quote := field(record, "CURRENCY")
		if !isoCode.MatchString(quote) {
			continue
		}
		value := field(record, "OBS_VALUE")
		if value == "" {
			continue
		}
		rate, ok := adapter.ParseFloat(value)
		if !ok {
			return nil, fmt.Errorf("invalid rate %q", value)
		}
		date, err := time.Parse(time.DateOnly, field(record, "TIME_PERIOD"))
		if err != nil {
			return nil, err
		}
		rates = append(rates, adapter.Rate{Date: date, Base: "EUR", Quote: quote, Rate: rate})
	}
}
