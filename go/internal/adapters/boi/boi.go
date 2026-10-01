// Package boi fetches rates from the Bank of Israel, which publishes daily
// representative exchange rates for 14 currencies against the Israeli new
// shekel through its SDMX API. It supports date range queries and full
// historical backfill.
//
// Like the Ruby adapter, Fetch passes the range to the API and returns every
// row it gets back without clipping.
package boi

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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://edge.boi.gov.il/FusionEdgeServer/sdmx/v2/data/dataflow/BOI.STATISTICS/EXR/1.0/"

// The pre-euro series to January 2002 quote some currencies per 10, 100 or
// 1000 units while UNIT_MULT says units: on 2000-01-03, with EUR at 4.1603
// ILS, ATS reads 3.0234 and ESP 2.5004, ten and a hundred times the euro
// conversion rates, and every day of the series holds the same multiple.
var nominalOverrides = map[string]float64{"ATS": 10, "BEL": 10, "ESP": 100, "ITL": 1000}

// BOI's codelist names BEL the financial franc, abolished in 1990, but its
// 1999-2002 series tracks 10 BEF at the euro conversion rate.
var aliases = map[string]string{"BEL": "BEF"}

var codeRE = regexp.MustCompile(`\A[A-Z]{3}\z`)

func init() {
	adapter.Register("BOI", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BOI rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if upto.IsZero() {
		upto = a.Today()
	}
	start := ""
	if !after.IsZero() {
		start = after.Format(time.DateOnly)
	}
	body, err := a.Get(ctx, baseURL, url.Values{
		"c[DATA_TYPE]": {"OF00"},
		"startperiod":  {start},
		"endperiod":    {upto.Format(time.DateOnly)},
		"format":       {"csv"},
	})
	if err != nil {
		return nil, err
	}
	return parse(body)
}

func parse(data []byte) ([]adapter.Rate, error) {
	r := csv.NewReader(bytes.NewReader(data))
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
	// field returns "" for a missing column or an empty cell, both nil in
	// Ruby's CSV.
	field := func(rec []string, name string) string {
		i, ok := col[name]
		if !ok || i >= len(rec) {
			return ""
		}
		return rec[i]
	}

	var rates []adapter.Rate
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		base := field(rec, "BASE_CURRENCY")
		dateStr := field(rec, "TIME_PERIOD")
		rateStr := field(rec, "OBS_VALUE")
		unitMult := field(rec, "UNIT_MULT")
		if base == "" || dateStr == "" || rateStr == "" {
			continue
		}
		// Aggregates such as CBK_L, the currency basket, share the flow with
		// currencies.
		if !codeRE.MatchString(base) {
			continue
		}

		rate, ok := adapter.ParseFloat(rateStr)
		if !ok {
			return nil, fmt.Errorf("invalid OBS_VALUE %q", rateStr)
		}
		// UNIT_MULT is power of 10: 2 means per 100 units, 1 means per 10
		if unitMult != "" && unitMult != "0" {
			n, err := strconv.Atoi(strings.TrimSpace(unitMult))
			if err != nil {
				return nil, fmt.Errorf("invalid UNIT_MULT %q", unitMult)
			}
			rate /= math.Pow10(n)
		}
		if nominal, ok := nominalOverrides[base]; ok {
			rate /= nominal
		}
		if rate == 0 {
			continue
		}

		date, err := time.Parse(time.DateOnly, strings.TrimSpace(dateStr))
		if err != nil {
			return nil, err
		}
		if alias, ok := aliases[base]; ok {
			base = alias
		}
		rates = append(rates, adapter.Rate{Date: date, Base: base, Quote: "ILS", Rate: rate})
	}
	return rates, nil
}
