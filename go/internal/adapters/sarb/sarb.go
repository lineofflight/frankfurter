// Package sarb fetches rates from the South African Reserve Bank, which publishes daily weighted-average exchange rates
// for 23 currencies. USD, GBP and EUR are quoted as ZAR per foreign unit (foreign base); all others as foreign per ZAR
// (ZAR base).
//
// The server clips to the requested range itself, so rows are returned as it sends them.
package sarb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://custom.resbank.co.za/SarbWebApi/WebIndicators/Shared/GetTimeseriesObservations"

type series struct {
	code, base, quote string
}

// Order matters: the cassette is matched on host alone, so requests replay in this order.
var allSeries = []series{
	{"EXCX135D", "USD", "ZAR"},
	{"EXCZ001D", "GBP", "ZAR"},
	{"EXCZ002D", "EUR", "ZAR"},
	{"EXCB080D", "ZAR", "AUD"},
	{"EXCB051D", "ZAR", "BWP"},
	{"EXCB036D", "ZAR", "BRL"},
	{"EXCB031D", "ZAR", "CAD"},
	{"EXCB121D", "ZAR", "CNY"},
	{"EXCB003D", "ZAR", "DKK"},
	{"EXCB122D", "ZAR", "HKD"},
	{"EXCB123D", "ZAR", "INR"},
	{"EXCB094D", "ZAR", "ILS"},
	{"EXCB120D", "ZAR", "JPY"},
	{"EXCB059D", "ZAR", "KES"},
	{"EXCB063D", "ZAR", "MWK"},
	{"EXCB081D", "ZAR", "NZD"},
	{"EXCB013D", "ZAR", "NOK"},
	{"EXCB015D", "ZAR", "SEK"},
	{"EXCB016D", "ZAR", "CHF"},
	{"EXCB126D", "ZAR", "TWD"},
	{"EXCB115D", "ZAR", "THB"},
	{"EXCB071D", "ZAR", "ZMW"},
	{"EXCB118D", "ZAR", "KRW"},
}

// The kwacha series runs under ZMW from 2000 without restating the 2013 rebasing: 612.34 per rand on 2012-12-31,
// 0.6188 on 2013-01-02. Rows before the rebasing are old kwacha.
var predecessors = map[string]adapter.Predecessor{
	"ZMW": {Code: "ZMK", Cutover: adapter.Date(2013, 1, 1)},
}

func init() {
	adapter.Register("SARB", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches SARB rates.
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
	var start string
	if !after.IsZero() {
		start = after.Format(time.DateOnly)
	}
	if upto.IsZero() {
		upto = a.Today()
	}
	end := upto.Format(time.DateOnly)

	var rates []adapter.Rate
	for _, s := range allSeries {
		if err := a.Sleep(ctx, 200*time.Millisecond); err != nil {
			return nil, err
		}
		body, err := a.Get(ctx, baseURL+"/"+s.code+"/"+start+"/"+end, nil)
		if err != nil {
			return nil, err
		}
		parsed, err := parse(body, s.base, s.quote)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

type observation struct {
	Period string          `json:"Period"`
	Value  json.RawMessage `json:"Value"`
}

func parse(data []byte, base, quote string) ([]adapter.Rate, error) {
	var observations []observation
	if err := json.Unmarshal(data, &observations); err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, obs := range observations {
		rate, ok, err := value(obs.Value)
		if err != nil {
			return nil, err
		}
		if !ok || rate == 0 {
			continue
		}
		date, err := adapter.ParseDate(obs.Period, "2006-01-02T15:04:05", "2006-01-02")
		if err != nil {
			return nil, err
		}
		rates = append(rates, adapter.Rate{
			Date:  date,
			Base:  adapter.HistoricalCode(predecessors, base, date),
			Quote: adapter.HistoricalCode(predecessors, quote, date),
			Rate:  rate,
		})
	}
	return rates, nil
}

// value reads Value, a number or a numeric string. ok is false when it is missing, null or blank.
func value(raw json.RawMessage) (float64, bool, error) {
	var v any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &v); err != nil {
			return 0, false, err
		}
	}
	switch v := v.(type) {
	case nil:
		return 0, false, nil
	case float64:
		return v, true, nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, false, nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false, fmt.Errorf("invalid value %q", v)
		}
		return f, true, nil
	default:
		return 0, false, fmt.Errorf("invalid value %s", raw)
	}
}
