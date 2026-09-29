// Package hmrc fetches rates from HM Revenue & Customs (UK), which publishes monthly customs exchange rates for 150+
// currencies against the British pound. Rates are published on the penultimate Thursday of every month and take effect
// on the 1st of the following month. Frequency monthly, so these never blend (#172, #612, #646).
//
// Fetch treats after as inclusive, as the Ruby adapter does.
package hmrc

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.trade-tariff.service.gov.uk/uk/api/exchange_rates/files"

var coverageStart = adapter.Date(2021, 1, 1)

var columns = []string{"Currency Code", "Currency Units per £1", "Start date"}

// Labels HMRC uses that aren't ISO 4217 codes: VED for the bolívar (VES), ZIG for Zimbabwe Gold (ZWG). The retired
// sucre code ECS on Ecuador's row is left as published and excluded from blends by its terminal date.
var aliases = map[string]string{
	"VED": "VES",
	"ZIG": "ZWG",
}

// HMRC's November 2022 file labels an old-leone value (15631 per pound) as SLE. The new leone, ~24 per pound, only
// appears in its files from March 2023, so earlier SLE rows price the old unit.
var predecessors = map[string]adapter.Predecessor{
	"SLE": {Code: "SLL", Cutover: adapter.Date(2023, 3, 1)},
}

func init() {
	adapter.Register("HMRC", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches HMRC rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 365 }

// Revises implements adapter.Adapter. HMRC may issue a corrected rate mid-month when a currency moves more than 5%.
// Every file so far adds such a row with its own start date, which parse keeps as a second observation.
func (a *Adapter) Revises() bool { return true }

// LeadDays implements adapter.Adapter. Next month's file appears on the penultimate Thursday, so its rows sit up to two
// weeks ahead; a month covers any slack.
func (a *Adapter) LeadDays() int { return 31 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	start := after
	if start.IsZero() {
		start = coverageStart
	}
	// HMRC publishes the coming month's file on the penultimate Thursday of this one. Look one month past the window so
	// the rows dated the coming 1st are stored the day they appear rather than the first poll after they apply.
	end := upto
	if end.IsZero() {
		end = firstOfMonth(a.Today()).AddDate(0, 1, 0)
	}

	var rates []adapter.Rate
	for cursor := firstOfMonth(start); !cursor.After(firstOfMonth(end)); cursor = cursor.AddDate(0, 1, 0) {
		month, err := a.fetchMonth(ctx, cursor)
		if err != nil {
			return nil, err
		}
		rates = append(rates, month...)
	}

	if after.IsZero() {
		return rates, nil
	}
	return slices.DeleteFunc(rates, func(r adapter.Rate) bool { return r.Date.Before(after) }), nil
}

func (a *Adapter) fetchMonth(ctx context.Context, month time.Time) ([]adapter.Rate, error) {
	url := fmt.Sprintf("%s/monthly_csv_%d-%d.csv", baseURL, month.Year(), int(month.Month()))
	body, err := a.Get(ctx, url, nil)
	if err != nil {
		// Only the coming month's file may be missing, until HMRC publishes it. Any other 404 is a moved endpoint.
		var se *adapter.StatusError
		if errors.As(err, &se) && se.StatusCode == http.StatusNotFound && month.After(a.Today()) {
			return nil, nil
		}
		return nil, err
	}
	return parse(body)
}

func firstOfMonth(t time.Time) time.Time {
	return adapter.Date(t.Year(), t.Month(), 1)
}

func parse(data []byte) ([]adapter.Rate, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	index := map[string]int{}
	for i, h := range header {
		if _, ok := index[h]; !ok {
			index[h] = i
		}
	}
	var missing []string
	for _, c := range columns {
		if _, ok := index[c]; !ok {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("HMRC: CSV is missing columns %s", strings.Join(missing, ", "))
	}

	field := func(rec []string, name string) (string, bool) {
		i := index[name]
		if i >= len(rec) {
			return "", false
		}
		return rec[i], true
	}

	type key struct {
		date  time.Time
		quote string
	}
	seen := map[key]bool{}
	var rates []adapter.Rate
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read CSV: %w", err)
		}
		rawCode, ok := field(rec, "Currency Code")
		if !ok {
			continue
		}
		start, ok1 := field(rec, "Start date")
		value, ok2 := field(rec, "Currency Units per £1")
		if !ok1 || !ok2 {
			continue
		}
		rawCode = strings.TrimSpace(rawCode)

		date, err := time.Parse("2/1/2006", strings.TrimSpace(start))
		if err != nil {
			return nil, fmt.Errorf("parse start date: %w", err)
		}
		code := rawCode
		if alias, ok := aliases[code]; ok {
			code = alias
		}
		code = adapter.HistoricalCode(predecessors, code, date)
		rate, ok := adapter.ParseFloat(value)
		if !ok || rate <= 0 {
			continue
		}

		k := key{date, code}
		if seen[k] {
			continue
		}
		seen[k] = true
		rates = append(rates, adapter.Rate{Date: date, Base: "GBP", Quote: code, Rate: rate})
	}
	return rates, nil
}
