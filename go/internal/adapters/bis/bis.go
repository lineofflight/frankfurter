// Package bis fetches the Bank for International Settlements' WS_XRU monthly
// end-of-period USD exchange rates.
//
// These are distinct from its monthly averages and daily series. Historical
// observations can be restated in successor units; never reconstruct
// predecessor magnitudes. https://www.bis.org/statistics/xrusd/xrusd_doc.pdf
// documents the national sources and historical adjustments.
//
// Fetch treats after as inclusive, as the Ruby adapter does (it selects dates
// between after and upto).
package bis

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://stats.bis.org/api/v2/data/dataflow/BIS/WS_XRU/1.0/M...E"

var (
	coverageStart = adapter.Date(1900, 1, 1)
	columns       = []string{"FREQ", "REF_AREA", "CURRENCY", "COLLECTION", "TIME_PERIOD", "OBS_VALUE", "UNIT_MULT"}

	// National EUR series are synthetic legacy-currency histories and differ
	// before euro adoption. XM is the actual euro-area/ECU series. The currency
	// unions also have differing national histories (especially Guinea-Bissau's
	// XOF series). Select a fixed representative area, not whichever row
	// happens to arrive first.
	areas = map[string]string{"EUR": "XM", "AUD": "AU", "XOF": "WA", "XAF": "CM", "XCD": "AG"}
)

func init() {
	adapter.Register("BIS", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BIS rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 3650 }

// Revises implements adapter.Adapter.
func (a *Adapter) Revises() bool { return true }

// FetchEach replaces adapter.FetchEach for BIS, as the Ruby class overrides
// fetch_each.
//
// Major currencies arrive ahead of many IMF-sourced series. Provider resumes
// from the newest observation, so revisit a year to catch late monthly rows and
// flag recent revisions. Older revisions need a manual backfill.
func (a *Adapter) FetchEach(ctx context.Context, after time.Time, yield func([]adapter.Rate) error) error {
	return fetchEach(ctx, a, after, a.Today(), yield)
}

func fetchEach(ctx context.Context, a adapter.Adapter, after, today time.Time, yield func([]adapter.Rate) error) error {
	if !after.IsZero() && !after.Before(today) {
		return nil
	}
	if !after.IsZero() {
		after = yearBefore(after)
		if after.Before(coverageStart) {
			after = coverageStart
		}
	}
	return adapter.FetchEach(ctx, a, after, today, yield)
}

// yearBefore is Ruby's date << 12: the same day a year earlier, clamped to the
// month's end (2024-02-29 -> 2023-02-28).
func yearBefore(d time.Time) time.Time {
	prev := d.AddDate(-1, 0, 0)
	if prev.Day() != d.Day() {
		prev = prev.AddDate(0, 0, -prev.Day())
	}
	return prev
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	start := after
	if start.IsZero() {
		start = coverageStart
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	if start.After(end) {
		return nil, nil
	}

	req, err := a.NewRequest(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.sdmx.data+csv;version=1.0.0")
	// The query string http.rb sends: the plus is escaped, the colons are not.
	req.URL.RawQuery = "c%5BTIME_PERIOD%5D=ge:" + start.Format("2006-01") + "%2Ble:" + end.Format("2006-01")
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	rates, err := parse(resp.Body)
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

func parse(data []byte) ([]adapter.Rate, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
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
		return nil, fmt.Errorf("CSV is missing columns %s", strings.Join(missing, ", "))
	}

	var rates []adapter.Rate
	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		field := func(name string) string {
			if i := index[name]; i < len(record) {
				return record[i]
			}
			return ""
		}
		if field("FREQ") != "M" || field("COLLECTION") != "E" {
			continue
		}
		code := field("CURRENCY")
		if area, ok := areas[code]; code == "USD" || ok && field("REF_AREA") != area {
			continue
		}
		value, ok := decimal(field("OBS_VALUE"))
		if !ok || value.Sign() <= 0 {
			continue
		}
		month, err := time.Parse("2006-01", field("TIME_PERIOD"))
		if err != nil {
			return nil, err
		}
		date := month.AddDate(0, 1, -1)
		mult, err := strconv.Atoi(field("UNIT_MULT"))
		if err != nil {
			return nil, err
		}
		scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs(mult))), nil))
		if mult < 0 {
			value.Quo(value, scale)
		} else {
			value.Mul(value, scale)
		}
		rate, _ := value.Float64()
		rates = append(rates, adapter.Rate{Date: date, Base: "USD", Quote: currency(code, date), Rate: rate})
	}
	return rates, nil
}

// decimal reads s as BigDecimal(s, exception: false) does for plain decimal
// text. NaN and infinities, which BigDecimal parses but the adapter rejects as
// non-finite, fail here directly.
func decimal(s string) (*big.Rat, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, "/xXbBoO") {
		return nil, false
	}
	return new(big.Rat).SetString(s)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func currency(code string, date time.Time) string {
	switch {
	// SLL is restated throughout in new-leone units (2017-12: 7.53696; 2022-06:
	// 13.15315; 2022-07: 13.88).
	case code == "SLL":
		return "SLE"
	// VEF stops in 2018-07 at 172368, then resumes in 2019-06 at 6550.047641 in
	// soberano units. The 2021 redenomination retains VES, matching BCV's own
	// series. MRO and STD remain in genuine predecessor units even after
	// retirement (2024-08 MRO: 396; 2026-06 STD: 21479.9), so their labels and
	// values are preserved.
	case code == "VEF" && !date.Before(adapter.Date(2019, 6, 1)):
		return "VES"
	case code == "EUR" && date.Before(adapter.Date(1999, 1, 1)):
		return "XEU"
	}
	return code
}
