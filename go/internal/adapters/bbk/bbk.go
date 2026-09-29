// Package bbk fetches the Deutsche Bundesbank's pre-1999 historical Frankfurt fixings (SERIES_TYPE=AA) from the BBEX3
// dataflow: daily DEM-based rates, 1948-06-21 through 1998-12-30.
//
// Post-1999 BBK data mirrors ECB and is intentionally excluded by the hardcoded SERIES_TYPE=AA filter in sdmxURL.
//
// The SDMX-CSV response is semicolon-delimited. Rates are published per unit batch ("100 ATS = x DEM", "1 000 ITL = x
// DEM"); the multiplier is only embedded in the free-text BBK_TITLE column (BBK_UNIT_MULT is always 0 for this
// dataflow). The multipliers table is the source of truth, and the title is parsed as a guard that fails if the
// published title ever contradicts it.
//
// Records keep BBK's native direction: foreign currency as base, DEM as quote. Fetch returns whatever the API returns
// for the requested period, without clipping.
//
// Attribution required: "Quelle: Deutsche Bundesbank" / "Source: Deutsche Bundesbank".
package bbk

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
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const sdmxURL = "https://api.statistiken.bundesbank.de/rest/data/BBEX3/D..DEM.AA.AC.000"

var (
	titleMultiplier = regexp.MustCompile(`/\s*([\d\s]+?)\s+[A-Z]{3}\s*=`)
	currencyCode    = regexp.MustCompile(`^[A-Z]{3}$`)
	whitespace      = regexp.MustCompile(`\s+`)
)

// multipliers holds the per-currency batch sizes from each series' BBK_TITLE. They are invariant across the 1948-1998
// AA series.
var multipliers = map[string]float64{
	"ATS": 100, "BEF": 100, "CAD": 1, "CHF": 100, "DKK": 100, "ESP": 100,
	"FIM": 100, "FRF": 100, "GBP": 1, "IEP": 1, "ITL": 1000, "JPY": 100,
	"LUF": 100, "NLG": 100, "NOK": 100, "PTE": 100, "SEK": 100, "USD": 1,
}

func init() {
	adapter.Register("BBK", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BBK rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange is about 5 years per chunk, to avoid ~200 MB single-fetch responses.
func (a *Adapter) BackfillRange() int { return 1826 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	params := url.Values{"format": {"sdmx_csv"}}
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
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\uFEFF"))))
	r.Comma = ';'
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	header, err := r.Read()
	if errors.Is(err, io.EOF) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	index := make(map[string]int, len(header))
	for i, name := range header {
		if _, ok := index[name]; !ok {
			index[name] = i
		}
	}

	var rates []adapter.Rate
	for {
		fields, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		field := func(name string) (string, bool) {
			i, ok := index[name]
			if !ok || i >= len(fields) {
				return "", false
			}
			return fields[i], true
		}
		rate, ok, err := parseRow(field)
		if err != nil {
			return nil, err
		}
		if ok {
			rates = append(rates, rate)
		}
	}
	return rates, nil
}

func parseRow(field func(string) (string, bool)) (adapter.Rate, bool, error) {
	if freq, _ := field("BBK_STD_FREQ"); freq != "D" {
		return adapter.Rate{}, false, nil
	}
	if partner, _ := field("BBK_ERX_PARTNER_CURRENCY"); partner != "DEM" {
		return adapter.Rate{}, false, nil
	}
	code, _ := field("BBK_STD_CURRENCY")
	if !currencyCode.MatchString(code) {
		return adapter.Rate{}, false, nil
	}
	value, _ := field("OBS_VALUE")
	if v := strings.TrimSpace(value); v == "" || v == "." {
		return adapter.Rate{}, false, nil
	}

	period, _ := field("TIME_PERIOD")
	multiplier, ok := multipliers[code]
	if !ok {
		return adapter.Rate{}, false, fmt.Errorf("unknown multiplier for %s", code)
	}
	if title, ok := field("BBK_TITLE"); ok {
		fromTitle, found, err := parseTitleMultiplier(title)
		if err != nil {
			return adapter.Rate{}, false, err
		}
		if found && fromTitle != multiplier {
			return adapter.Rate{}, false, fmt.Errorf(
				"title multiplier mismatch for %s on %s: table=%v, title=%v (title: %q)",
				code, period, multiplier, fromTitle, title)
		}
	}

	f, ok := adapter.ParseFloat(value)
	if !ok {
		return adapter.Rate{}, false, fmt.Errorf("invalid OBS_VALUE %q for %s on %s", value, code, period)
	}
	date, err := time.Parse(time.DateOnly, period)
	if err != nil {
		return adapter.Rate{}, false, fmt.Errorf("invalid TIME_PERIOD %q for %s", period, code)
	}
	return adapter.Rate{Date: date, Base: code, Quote: "DEM", Rate: f / multiplier}, true, nil
}

func parseTitleMultiplier(title string) (float64, bool, error) {
	m := titleMultiplier.FindStringSubmatch(title)
	if m == nil {
		return 0, false, nil
	}
	n, err := strconv.Atoi(whitespace.ReplaceAllString(m[1], ""))
	if err != nil {
		return 0, false, fmt.Errorf("invalid title multiplier in %q", title)
	}
	return float64(n), true, nil
}
