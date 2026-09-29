// Package cbssc fetches rates from the Central Bank of Seychelles, which publishes the Consolidated Average Rates of
// Authorised Dealers: a daily buy, sell and mid rate for USD, EUR and GBP against the Seychellois rupee (SCR), expressed
// as rupees per unit of foreign currency (1 USD = 14.6 SCR). The pivot sits in quote, as with NBG.
//
// Two sources, both carrying the mid rate:
//
//   - A statistics workbook ("Exchange Rates-Daily.xlsx") with one sheet per currency, a "Date | SCR/USD" header row
//     and one row per business day from 2000-01-04, regenerated daily around 12:00 UTC. The FXArchivedRates.jsp feed
//     the site itself reads only reaches back to 2012, carries buy/sell alone and lags the live day by about two
//     months, so the workbook is the archive.
//   - The live CAR endpoint, which has only the current day. It runs ahead of the workbook by a day, so it fills the
//     head of the series.
//
// The workbook stores the raw dealer average to full float precision (14.616577838181803) while every published view
// of the same figure shows four decimals. Round to four so a day caught live and the same day read from the workbook
// agree.
//
// Unlike most adapters, Fetch treats after as inclusive, as the Ruby adapter does.
package cbssc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	archiveURL = "https://www.cbs.sc/Downloads/StaExcel/Exchange%20Rates-Daily.xlsx"
	liveURL    = "https://www.cbs.sc/Controller/MarketinfoController.jsp"
)

var (
	excelEpoch = adapter.Date(1899, 12, 30)
	header     = regexp.MustCompile(`\ASCR/([A-Z]{3})\z`)
)

func init() {
	adapter.Register("CBSSC", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBSSC rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	archive, err := a.Get(ctx, archiveURL, nil)
	if err != nil {
		return nil, err
	}
	rates, err := parse(archive)
	if err != nil {
		return nil, err
	}
	live, err := a.Get(ctx, liveURL, url.Values{"type": {"car"}})
	if err != nil {
		return nil, err
	}
	liveRates, err := parseLive(live)
	if err != nil {
		return nil, err
	}
	rates = append(rates, liveRates...)

	type key struct {
		date time.Time
		base string
	}
	seen := map[key]bool{}
	var out []adapter.Rate
	for _, r := range rates {
		if !after.IsZero() && r.Date.Before(after) {
			continue
		}
		if !upto.IsZero() && r.Date.After(upto) {
			continue
		}
		k := key{r.Date, r.Base}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	return out, nil
}

func parse(data []byte) ([]adapter.Rate, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var rates []adapter.Rate
	for _, sheet := range f.GetSheetList() {
		if rates, err = parseSheet(f, sheet, rates); err != nil {
			return nil, err
		}
	}
	if len(rates) == 0 {
		return nil, fmt.Errorf("no rates in workbook at %s", archiveURL)
	}
	return rates, nil
}

// parseSheet reads a currency sheet, which opens with a "Date | SCR/USD" header; rows beneath carry an Excel serial in
// A and the mid in B. Sheets without such a header (the notes, the hidden yearly averages) emit nothing. A day with no
// fixing holds a text placeholder in B instead of a number (GBP on 2020-04-09), which numericCell skips.
func parseSheet(f *excelize.File, sheet string, rates []adapter.Rate) ([]adapter.Rate, error) {
	rows, err := f.GetRows(sheet, excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, err
	}
	iso := ""
	for i, row := range rows {
		n := strconv.Itoa(i + 1)
		if iso == "" && len(row) > 1 {
			if m := header.FindStringSubmatch(row[1]); m != nil && cellType(f, sheet, "B"+n) == excelize.CellTypeSharedString {
				iso = m[1]
			}
		}
		if iso == "" || len(row) < 2 {
			continue
		}
		serial, ok := numericCell(f, sheet, "A"+n, row[0])
		if !ok {
			continue
		}
		rate, ok := numericCell(f, sheet, "B"+n, row[1])
		if !ok || rate <= 0 {
			continue
		}
		if serial <= 30_000 || serial >= 80_000 {
			continue
		}
		rates = append(rates, adapter.Rate{
			Date:  excelEpoch.AddDate(0, 0, int(serial)),
			Base:  iso,
			Quote: "SCR",
			Rate:  round4(rate),
		})
	}
	return rates, nil
}

func cellType(f *excelize.File, sheet, ref string) excelize.CellType {
	t, _ := f.GetCellType(sheet, ref)
	return t
}

// numericCell parses a stored number, rejecting strings that merely look numeric, as the Ruby adapter reads only the
// <v> of non-shared-string cells.
func numericCell(f *excelize.File, sheet, ref, value string) (float64, bool) {
	x, ok := adapter.ParseFloat(strings.TrimSpace(value))
	if !ok {
		return 0, false
	}
	switch cellType(f, sheet, ref) {
	case excelize.CellTypeSharedString, excelize.CellTypeInlineString:
		return 0, false
	}
	return x, true
}

// parseLive reads the CAR endpoint's JSON, which holds only the current day.
func parseLive(data []byte) ([]adapter.Rate, error) {
	var doc struct {
		Car []map[string]any `json:"car"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Car) == 0 {
		return nil, errors.New("no CAR rates in live response")
	}
	row := doc.Car[0]

	ds, ok := row["currentDate"].(string)
	if !ok {
		return nil, errors.New("live response has no currentDate")
	}
	// "2" rather than "02": strptime's %d also takes a single-digit day.
	date, err := time.Parse("2-Jan-2006", ds)
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, iso := range []string{"USD", "EUR", "GBP"} {
		field := strings.ToLower(iso) + "mid"
		var rate float64
		switch v := row[field].(type) {
		case float64:
			rate = v
		case string:
			// adapter.ParseFloat rejects NaN and Inf, as Ruby's Float() does.
			f, ok := adapter.ParseFloat(v)
			if !ok {
				return nil, fmt.Errorf("live %s: invalid number %q", field, v)
			}
			rate = f
		default:
			return nil, fmt.Errorf("live response has no %s", field)
		}
		if rate == 0 {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: iso, Quote: "SCR", Rate: rate})
	}
	return rates, nil
}

// round4 is Ruby's Float#round(4): round half up on x*10^4, corrected where the product lost precision.
func round4(x float64) float64 {
	const s = 1e4
	f := math.Round(x * s)
	if x > 0 {
		if (f+0.5)/s <= x {
			f++
		}
	} else if (f-0.5)/s >= x {
		f--
	}
	return f / s
}
