// Package bcra fetches rates from the Central Bank of Argentina (Banco Central de la República Argentina), which
// publishes official exchange rates in ARS. The API accepts a single date per request, so Fetch walks day by day,
// skipping weekends.
package bcra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://api.bcra.gob.ar/estadisticascambiarias/v1.0/Cotizaciones"

// skipCodes holds the self-reference, internal reference, defunct and duplicate codes.
var skipCodes = []string{"ARS", "REF", "VEB", "MXP"}

// BCRA quotes some low-value currencies per N units, as in "DONG VIETNAM (C/1.000 UNIDADES)", with a period as the
// thousands separator.
var multiplierPattern = regexp.MustCompile(`(?i)C/([\d.]+)\s*UNIDADES`)

func init() {
	adapter.Register("BCRA", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BCRA rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 90 }

// Fetch implements adapter.Adapter. Unlike most adapters it treats after as inclusive and requires it, as the Ruby
// adapter does.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if after.IsZero() {
		return nil, errors.New("after is required")
	}
	if upto.IsZero() {
		upto = a.Today()
	}

	var rates []adapter.Rate
	first := true
	for date := after; !date.After(upto); date = date.AddDate(0, 0, 1) {
		if wd := date.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		if !first {
			if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
				return nil, err
			}
		}
		first = false

		body, err := a.Get(ctx, baseURL, url.Values{"fecha": {date.Format(time.DateOnly)}})
		if err != nil {
			return nil, err
		}
		day, err := parse(body)
		if err != nil {
			return nil, err
		}
		rates = append(rates, day...)
	}
	return rates, nil
}

type item struct {
	Code        *string `json:"codigoMoneda"`
	Description *string `json:"descripcion"`
	Rate        any     `json:"tipoCotizacion"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	var results map[string]json.RawMessage
	if raw, ok := envelope["results"]; !ok || json.Unmarshal(raw, &results) != nil || results == nil {
		return nil, errors.New("results envelope missing from response")
	}

	var detalle []item
	if raw, ok := results["detalle"]; !ok || json.Unmarshal(raw, &detalle) != nil || detalle == nil {
		return nil, errors.New("detalle missing from results envelope")
	}

	rawFecha, hasFecha := results["fecha"]
	var fecha any
	if hasFecha {
		if err := json.Unmarshal(rawFecha, &fecha); err != nil {
			return nil, err
		}
	}
	if fecha == nil || fecha == false {
		// Holidays return {"results":{"fecha":null,"detalle":[]}} with HTTP 200.
		if hasFecha && fecha == nil && len(detalle) == 0 {
			return nil, nil
		}
		return nil, errors.New("undated results that do not match the holiday shape")
	}

	// Skip the entire date if any currency code appears more than once.
	seen := map[string]bool{}
	for _, it := range detalle {
		if it.Code == nil {
			continue
		}
		code := strings.TrimSpace(*it.Code)
		if seen[code] {
			return nil, nil
		}
		seen[code] = true
	}

	s, ok := fecha.(string)
	if !ok {
		return nil, fmt.Errorf("invalid fecha %v", fecha)
	}
	date, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, it := range detalle {
		if it.Code == nil {
			continue
		}
		code := strings.TrimSpace(*it.Code)
		if slices.Contains(skipCodes, code) {
			continue
		}

		value, err := toFloat(it.Rate)
		if err != nil {
			return nil, fmt.Errorf("%s on %s: %w", code, s, err)
		}
		if value == 0 {
			continue
		}

		multiplier, err := extractMultiplier(it.Description)
		if err != nil {
			return nil, err
		}
		if multiplier > 1 {
			value /= float64(multiplier)
		}

		rates = append(rates, adapter.Rate{Date: date, Base: code, Quote: "ARS", Rate: value})
	}
	return rates, nil
}

// toFloat mirrors Ruby's strict Float(): numbers pass through, strings must parse, anything else fails.
func toFloat(v any) (float64, error) {
	switch v := v.(type) {
	case float64:
		return v, nil
	case string:
		if f, ok := adapter.ParseFloat(v); ok {
			return f, nil
		}
	}
	return 0, fmt.Errorf("invalid tipoCotizacion %v", v)
}

func extractMultiplier(description *string) (int64, error) {
	if description == nil {
		return 1, nil
	}
	m := multiplierPattern.FindStringSubmatch(*description)
	if m == nil {
		return 1, nil
	}
	// Base 0 reads a leading zero as octal, as Ruby's Integer() does.
	n, err := strconv.ParseInt(strings.ReplaceAll(m[1], ".", ""), 0, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid multiplier in %q: %w", *description, err)
	}
	return n, nil
}
