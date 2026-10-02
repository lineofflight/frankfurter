// Package cbbh fetches rates from the Central Bank of Bosnia and Herzegovina,
// which publishes middle rates as BAM per foreign Units.
//
// Lists are generally published Mon-Fri after 16:00 Sarajevo for the following
// day; their effective dates (usually Tue-Sat) are kept. Unlike most adapters,
// Fetch treats after as inclusive, as the Ruby adapter does.
package cbbh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	periodURL    = "https://www.cbbh.ba/CurrencyExchange/GetJsonForPeriod"
	dailyURL     = "https://www.cbbh.ba/CurrencyExchange/GetJson"
	exportFailed = `"Problem with export"`
)

var (
	coverageStart = adapter.Date(1998, 1, 6)

	// Before 16 July 1998 the source labels ordinary peseta and ECU quotes as
	// ESB/995 and XBA/955. Both codes change together to ESP/724 and XEU/954
	// while country, units, and magnitudes continue unchanged. The earlier
	// values agree with peseta/DEM and the official ECU basket, not distinct
	// funds-unit histories.
	earlyAliases  = map[string]string{"ESB": "ESP", "XBA": "XEU"}
	aliasesCutoff = adapter.Date(1998, 7, 16)

	codePattern = regexp.MustCompile(`\A[A-Z]{3}\z`)
)

func init() {
	adapter.Register("CBBH", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBBH rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 365 }

// Fetch implements adapter.Adapter. Rows dated from after (inclusive) through
// upto are returned.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	start := after
	if start.Before(coverageStart) {
		start = coverageStart
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	if start.After(end) {
		return nil, nil
	}

	body, err := a.fetchPeriod(ctx, start, end)
	if err != nil {
		return nil, err
	}
	if string(bytes.TrimSpace(body)) == exportFailed {
		return nil, a.confirmEmpty(ctx, start, end)
	}

	rates, err := parse(body)
	if err != nil {
		return nil, err
	}
	var out []adapter.Rate
	for _, r := range rates {
		if !r.Date.Before(start) && !r.Date.After(end) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (a *Adapter) fetchPeriod(ctx context.Context, start, end time.Time) ([]byte, error) {
	u := periodURL + "?" + url.Values{
		"dateFrom": {start.Format(time.DateOnly)},
		"dateTo":   {end.Format(time.DateOnly)},
	}.Encode()
	req, err := a.NewRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.Do(req, http.StatusNotFound)
	if err != nil {
		return nil, err
	}
	// The export error comes back as a 404; any other 404 is a real failure.
	if resp.StatusCode == http.StatusNotFound && string(bytes.TrimSpace(resp.Body)) != exportFailed {
		return nil, &adapter.StatusError{Method: req.Method, URL: u, StatusCode: resp.StatusCode, Body: resp.Body}
	}
	return resp.Body, nil
}

// confirmEmpty handles the period endpoint's generic export-error string, which
// it also returns for genuinely empty Sun/Mon and holiday ranges. The range
// counts as empty only when the daily endpoint confirms its most recent actual
// list precedes the window; otherwise it returns an error.
func (a *Adapter) confirmEmpty(ctx context.Context, start, end time.Time) error {
	body, err := a.Get(ctx, dailyURL, url.Values{"date": {end.Format("01/02/2006 00:00:00")}})
	if err != nil {
		return err
	}
	var list exchangeList
	if err := json.Unmarshal(body, &list); err != nil || len(list.Items) == 0 {
		return errors.New("could not confirm empty export")
	}
	date, err := parseDate(list.Date)
	if err != nil {
		return err
	}
	if date.Before(start) {
		return nil
	}
	return errors.New("period export failed despite an available list")
}

type exchangeList struct {
	Date  *string           `json:"Date"`
	Items []json.RawMessage `json:"CurrencyExchangeItems"`
}

type item struct {
	AlphaCode any `json:"AlphaCode"`
	Units     any `json:"Units"`
	Middle    any `json:"Middle"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var lists []json.RawMessage
	if err := json.Unmarshal(data, &lists); err != nil {
		return nil, errors.New("expected an array of exchange lists")
	}

	var rates []adapter.Rate
	for _, raw := range lists {
		var list exchangeList
		if err := json.Unmarshal(raw, &list); err != nil || list.Items == nil {
			return nil, errors.New("missing exchange-list items")
		}
		date, err := parseDate(list.Date)
		if err != nil {
			return nil, err
		}
		for _, rawItem := range list.Items {
			var it item
			if err := json.Unmarshal(rawItem, &it); err != nil {
				return nil, fmt.Errorf("malformed exchange item: %w", err)
			}
			code, ok := it.AlphaCode.(string)
			if !ok || !codePattern.MatchString(code) {
				continue
			}
			if date.Before(aliasesCutoff) {
				if alias, ok := earlyAliases[code]; ok {
					code = alias
				}
			}
			units, ok := decimal(it.Units)
			if !ok || units.Sign() <= 0 {
				continue
			}
			middle, ok := decimal(it.Middle)
			if !ok || middle.Sign() <= 0 {
				continue
			}
			// Source Middle is not recomputed from Buy/Sell. Divide in exact
			// decimal to preserve its digits.
			rate, _ := new(big.Rat).Quo(middle, units).Float64()
			rates = append(rates, adapter.Rate{Date: date, Base: code, Quote: "BAM", Rate: rate})
		}
	}
	return rates, nil
}

func parseDate(s *string) (time.Time, error) {
	if s == nil {
		return time.Time{}, errors.New("exchange list has no date")
	}
	return adapter.ParseDate(*s, "2006-01-02T15:04:05", time.DateOnly)
}

// decimal reads a value the way BigDecimal(value.to_s.tr(",", "."), exception:
// false) does, reporting false where Ruby gets nil, NaN or infinity.
func decimal(v any) (*big.Rat, bool) {
	var s string
	switch v := v.(type) {
	case string:
		s = v
	case float64:
		s = fmt.Sprint(v)
	default:
		return nil, false
	}
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	// Rat.SetString also takes fractions and 0x/0b/0o prefixes, which
	// BigDecimal rejects.
	if s == "" || strings.Trim(s, "0123456789+-._eE") != "" {
		return nil, false
	}
	return new(big.Rat).SetString(s)
}
