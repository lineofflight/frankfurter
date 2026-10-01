// Package bna fetches rates from Banco Nacional de Angola, which publishes
// daily reference rates for about 70 currencies against the Angolan kwanza
// (AOA), Mon-Fri from 2000-01-01 onwards.
//
// The time-series endpoint accepts a single currency per request, so we iterate
// the currency list and fetch each one over the requested window. Query params
// must be lowercase (datainicio, datafim, tipocambio, moeda): mixed case
// silently returns "datainicio 'null' inválida.". Each rate row carries
// tipoCambio in {B=venda/sell, G=compra/buy, M=medio/mid}; we filter to mid via
// tipocambio=M.
//
// XDRUSD is a non-ISO composite the API includes alongside real currencies. XAU
// is excluded too: BNA's series has documented unit inconsistencies (mid-2024
// rows alternate between AOA-per-ounce and USD-per-ounce in the same column).
//
// Rates are published as 1 foreign = X AOA, so the foreign currency is the base
// and AOA the quote. The window is sent to the API as is (after inclusive) and
// rows are not clipped afterwards, as in Ruby. Some series responses also carry
// rows for other currencies; those are kept, duplicates included, as Ruby keeps
// them.
package bna

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	baseURL    = "https://www.bna.ao/service/rest/taxas"
	listPath   = "/get/lista/moedas"
	seriesPath = "/get/evolucao/taxa/intervalo"
)

var (
	excludedCodes = []string{"XDRUSD", "XAU"}
	isoCode       = regexp.MustCompile(`\A[A-Z]{3}\z`)
	listCode      = regexp.MustCompile(`\A[A-Z]{3,6}\z`)
)

// Three of those codes switch to their successor's values without changing
// label. MZM's 2014 to 2016 rows track the new metical (3.1 AOA). STD holds a
// frozen old-dobra cross until 2023-02-17 (0.024 AOA) and the new dobra from
// 2023-02-22 (21.89 AOA). VEF holds the last pre-2018 official rate until
// October 2022 (0.002 AOA) and the current bolivar from 2023-10-18 (23.74 AOA),
// against BCV's VES.
var successors = map[string]adapter.Successor{
	"MZM": {Code: "MZN", Cutover: adapter.Date(2006, 7, 1)},
	"STD": {Code: "STN", Cutover: adapter.Date(2023, 2, 22)},
	"VEF": {Code: "VES", Cutover: adapter.Date(2023, 10, 18)},
}

func init() {
	adapter.Register("BNA", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BNA rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	start := after
	if start.IsZero() {
		start = adapter.Date(2000, 1, 1)
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	if start.After(end) {
		return nil, nil
	}

	codes, err := a.currencyCodes(ctx)
	if err != nil {
		return nil, err
	}
	// Fetch relabelled series last, so a day BNA also publishes under the
	// successor's own code keeps that row when the insert skips the duplicate.
	slices.SortStableFunc(codes, func(x, y string) int {
		_, relabelX := successors[x]
		_, relabelY := successors[y]
		switch {
		case relabelX == relabelY:
			return 0
		case relabelX:
			return 1
		}
		return -1
	})

	var rates []adapter.Rate
	for i, code := range codes {
		if i > 0 {
			if err := a.Sleep(ctx, 200*time.Millisecond); err != nil {
				return nil, err
			}
		}
		body, err := a.Get(ctx, baseURL+seriesPath, url.Values{
			"datainicio": {start.Format(time.DateOnly)},
			"datafim":    {end.Format(time.DateOnly)},
			"tipocambio": {"M"},
			"moeda":      {code},
		})
		if err != nil {
			return nil, err
		}
		parsed, err := parse(body)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

type row struct {
	Rate     any     `json:"taxa"`
	Type     *string `json:"tipoCambio"`
	Date     *string `json:"data"`
	Currency *string `json:"codigoMoeda"`
}

// decode reads the {genericResponse: [...], success: bool} envelope. ok is
// false when the body is not an object or genericResponse is not an array.
func decode(data []byte) (fields map[string]json.RawMessage, rows []row, ok bool, err error) {
	if err := json.Unmarshal(data, &fields); err != nil {
		var probe any
		if json.Unmarshal(data, &probe) != nil {
			return nil, nil, false, err
		}
		return nil, nil, false, nil
	}
	list, present := fields["genericResponse"]
	if !present {
		return fields, nil, false, nil
	}
	if err := json.Unmarshal(list, &rows); err != nil {
		return fields, nil, false, nil
	}
	if rows == nil {
		return fields, nil, false, nil // null
	}
	return fields, rows, true, nil
}

// message renders the error message field the way Ruby interpolates it.
func message(fields map[string]json.RawMessage) string {
	if fields == nil {
		return "not a JSON object"
	}
	var v any
	if raw, ok := fields["message"]; ok && json.Unmarshal(raw, &v) == nil && v != nil {
		if s, ok := v.(string); ok {
			return s
		}
		return string(raw)
	}
	return ""
}

func truthy(fields map[string]json.RawMessage, key string) bool {
	var v any
	if json.Unmarshal(fields[key], &v) != nil {
		return false
	}
	return v != nil && v != false
}

func parse(data []byte) ([]adapter.Rate, error) {
	fields, rows, ok, err := decode(data)
	if err != nil {
		return nil, err
	}
	if !ok || !truthy(fields, "success") {
		return nil, fmt.Errorf("series request failed: %s", message(fields))
	}

	var rates []adapter.Rate
	for _, r := range rows {
		if r.Type == nil || *r.Type != "M" {
			continue
		}
		if r.Currency == nil || !isoCode.MatchString(*r.Currency) {
			continue
		}
		rate, ok := toFloat(r.Rate)
		if !ok || rate == 0 {
			continue
		}
		if r.Date == nil {
			return nil, fmt.Errorf("missing data for %s", *r.Currency)
		}
		date, err := adapter.ParseDate(*r.Date, time.DateOnly)
		if err != nil {
			return nil, err
		}
		rates = append(rates, adapter.Rate{
			Date:  date,
			Base:  adapter.SuccessorCode(successors, *r.Currency, date),
			Quote: "AOA",
			Rate:  rate,
		})
	}
	return rates, nil
}

// toFloat is Float(v, exception: false) for a decoded JSON value.
func toFloat(v any) (float64, bool) {
	switch v := v.(type) {
	case float64:
		return v, true
	case string:
		return adapter.ParseFloat(v)
	}
	return 0, false
}

func (a *Adapter) currencyCodes(ctx context.Context) ([]string, error) {
	body, err := a.Get(ctx, baseURL+listPath, nil)
	if err != nil {
		return nil, err
	}
	fields, rows, ok, err := decode(body)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("currency list request failed: %s", message(fields))
	}

	var codes []string
	for _, r := range rows {
		if r.Currency == nil || !listCode.MatchString(*r.Currency) {
			continue
		}
		code := *r.Currency
		if code == "AOA" || slices.Contains(excludedCodes, code) {
			continue
		}
		codes = append(codes, code)
	}
	return codes, nil
}
