// Package inforeuro fetches the European Commission's monthly accounting rates:
// foreign units per EUR, or per ECU (XEU) before January 1999. Each rate
// applies from the first of its month. These period observations never blend
// with daily reference rates.
//
// Unlike the common fetch contract, after is inclusive: a month whose first day
// equals after is fetched.
package inforeuro

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const apiURL = "https://ec.europa.eu/budg/inforeuro/api/public/monthly-rates"

var (
	coverageStart = adapter.Date(1994, 3, 1)
	euroStart     = adapter.Date(1999, 1, 1)
)

// FRC is the source's Congolese-franc label from July 1998 through February
// 1999, before it adopts CDF.
var aliases = map[string]string{"FRC": "CDF", "ZIG": "ZWG"}

func init() {
	adapter.Register("INFOREURO", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches INFOREURO rates.
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
	start := coverageStart
	if after.After(start) {
		start = after
	}
	end := a.Today()
	if !upto.IsZero() && upto.Before(end) {
		end = upto
	}
	cursor := adapter.Date(start.Year(), start.Month(), 1)
	if cursor.Before(start) {
		cursor = cursor.AddDate(0, 1, 0)
	}

	var rates []adapter.Rate
	for !cursor.After(end) {
		body, err := a.Get(ctx, apiURL, url.Values{
			"year":  {strconv.Itoa(cursor.Year())},
			"month": {strconv.Itoa(int(cursor.Month()))},
			"lang":  {"en"},
		})
		if err != nil {
			return nil, err
		}
		parsed, err := parse(body, cursor)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
		cursor = cursor.AddDate(0, 1, 0)
		if !cursor.After(end) {
			if err := a.Sleep(ctx, 100*time.Millisecond); err != nil {
				return nil, err
			}
		}
	}
	return rates, nil
}

func parse(data []byte, date time.Time) ([]adapter.Rate, error) {
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(data, &rows); err != nil || len(rows) == 0 {
		return nil, errors.New("expected monthly rates")
	}

	base := "EUR"
	if date.Before(euroStart) {
		base = "XEU"
	}
	var rates []adapter.Rate
	for _, row := range rows {
		rawCode, ok := row["isoA3Code"]
		if !ok {
			return nil, errors.New("missing isoA3Code")
		}
		var code string
		if err := json.Unmarshal(rawCode, &code); err != nil {
			return nil, fmt.Errorf("invalid isoA3Code %s: %w", rawCode, err)
		}
		quote := code
		if alias, ok := aliases[code]; ok {
			quote = alias
		}
		// Only January and February 2000 use AOK, at the new kwanza's magnitude
		// (~5.5 per EUR), after the million:1 reform. March calls the same unit
		// AOA; December 1999 still quotes AOR at 5,480,730 per EUR.
		if code == "AOK" && !date.Before(adapter.Date(2000, 1, 1)) && date.Before(adapter.Date(2000, 3, 1)) {
			quote = "AOA"
		}
		rawValue, ok := row["value"]
		if !ok {
			return nil, fmt.Errorf("missing value for %s", code)
		}
		var value *json.Number
		if err := json.Unmarshal(rawValue, &value); err != nil {
			return nil, fmt.Errorf("invalid value for %s: %w", code, err)
		}
		if value == nil || quote == base {
			continue
		}
		// Ruby keeps the published decimal to preserve hyperinflation-era
		// digits; parsing the JSON text directly gives the nearest float, the
		// same value that decimal becomes once stored.
		rate, err := strconv.ParseFloat(value.String(), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid value %s for %s: %w", value, code, err)
		}
		if rate <= 0 {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: base, Quote: quote, Rate: rate})
	}
	return rates, nil
}
