// Package nb fetches rates from Norges Bank, which publishes business-day NOK
// rates for about 40 currencies and a few trade-weighted indices through its
// SDMX API.
package nb

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const sdmxURL = "https://data.norges-bank.no/api/data/EXR/B..NOK.SP"

func init() {
	adapter.Register("NB", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches NB rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 365 }

// Fetch implements adapter.Adapter. As in Ruby, the API does the date clipping
// (startPeriod and endPeriod are both inclusive) and rows come back unfiltered.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	q := url.Values{"format": {"csvdata"}}
	if !after.IsZero() {
		q.Set("startPeriod", after.Format(time.DateOnly))
	}
	if !upto.IsZero() {
		q.Set("endPeriod", upto.Format(time.DateOnly))
	}
	body, err := a.Get(ctx, sdmxURL, q)
	if err != nil {
		return nil, err
	}
	return parse(body)
}

func parse(data []byte) ([]adapter.Rate, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	header, err := r.Read()
	if errors.Is(err, io.EOF) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	col := make(map[string]int, len(header))
	for i, h := range header {
		if _, ok := col[h]; !ok {
			col[h] = i
		}
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
		// As with a Ruby CSV::Row, a missing or empty field reads as nil ("").
		field := func(name string) string {
			if i, ok := col[name]; ok && i < len(record) {
				return record[i]
			}
			return ""
		}
		rate, ok, err := parseRow(field)
		if err != nil {
			return nil, err
		}
		if ok {
			rates = append(rates, rate)
		}
	}
}

func parseRow(field func(string) string) (adapter.Rate, bool, error) {
	if field("FREQ") != "B" {
		return adapter.Rate{}, false, nil
	}
	base := field("BASE_CUR")
	if base == "" {
		return adapter.Rate{}, false, nil
	}
	rate, ok := adapter.ParseFloat(field("OBS_VALUE"))
	if !ok {
		return adapter.Rate{}, false, fmt.Errorf("invalid OBS_VALUE %q", field("OBS_VALUE"))
	}
	if s := field("UNIT_MULT"); s != "" {
		mult, err := strconv.Atoi(s)
		if err != nil {
			return adapter.Rate{}, false, fmt.Errorf("invalid UNIT_MULT %q", s)
		}
		if mult > 0 {
			rate /= math.Pow10(mult)
		}
	}
	date, err := time.Parse(time.DateOnly, field("TIME_PERIOD"))
	if err != nil {
		return adapter.Rate{}, false, err
	}
	return adapter.Rate{Date: date, Base: base, Quote: "NOK", Rate: rate}, true, nil
}
