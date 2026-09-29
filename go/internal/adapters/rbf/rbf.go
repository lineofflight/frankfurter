// Package rbf fetches rates from the Reserve Bank of Fiji, which publishes the daily mid-rate series "8.8 Exchange
// Rates Daily" as a single rolling XLSX covering 2001-01-02 to present. Eight quote currencies: SDR, STG (GBP), YEN
// (JPY), CHF, EURO (EUR), A$ (AUD), NZ$ (NZD), US$ (USD).
//
// The XLSX URL embeds the publication year/month under /wp-content/uploads/YYYY/MM/, so the adapter scrapes the
// statistics hub for the current link rather than hardcoding a path.
//
// The header reads "RBF Mid-Rate Per Fiji Dollar", so each row records "1 FJD = X foreign": rows have FJD as base and
// the foreign currency as quote. SDR is relabelled to XDR.
//
// As in Ruby, after is inclusive here: rows dated on after are kept.
package rbf

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/xuri/excelize/v2"
)

const hubURL = "https://www.rbf.gov.fj/statistics/economic-and-financial-statistics/"

var (
	archiveLink = regexp.MustCompile(`href="(https://www\.rbf\.gov\.fj/wp-content/uploads/\d{4}/\d{2}/8\.8-Exchange-Rates-Daily[^"]*\.xlsx)"`)
	excelEpoch  = adapter.Date(1899, 12, 30)
)

// currencies maps the workbook's non-ISO column labels to ISO 4217 codes.
var currencies = map[string]string{
	"SDR":  "XDR",
	"STG":  "GBP",
	"YEN":  "JPY",
	"CHF":  "CHF",
	"EURO": "EUR",
	"A$":   "AUD",
	"NZ$":  "NZD",
	"US$":  "USD",
}

func init() {
	adapter.Register("RBF", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches RBF rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter. The full series ships as a single workbook refreshed daily, so a large
// range keeps the fetch in one download.
func (a *Adapter) BackfillRange() int { return 36525 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	hub, err := a.Get(ctx, hubURL, nil)
	if err != nil {
		return nil, err
	}
	m := archiveLink.FindSubmatch(hub)
	if m == nil {
		return nil, fmt.Errorf("exchange-rates XLSX link not found on %s", hubURL)
	}
	body, err := a.Get(ctx, string(m[1]), nil)
	if err != nil {
		return nil, err
	}
	return parse(body, after, upto)
}

func parse(data []byte, after, upto time.Time) ([]adapter.Rate, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := f.GetRows(f.GetSheetName(0), excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	var columns map[int]string
	for _, r := range rows {
		if len(columns) == 0 {
			columns = columnMap(r)
			continue
		}

		var date time.Time
		var quotes []string
		values := map[string]float64{}
		for i, text := range r {
			if text == "" {
				continue
			}
			if i == 0 {
				if serial, ok := excelSerial(text); ok {
					date = excelEpoch.AddDate(0, 0, serial)
				}
				continue
			}
			iso, ok := columns[i]
			if !ok {
				continue
			}
			rate, ok := adapter.ParseFloat(text)
			if !ok || rate <= 0 {
				continue
			}
			if _, seen := values[iso]; !seen {
				quotes = append(quotes, iso)
			}
			values[iso] = rate
		}

		if date.IsZero() {
			continue
		}
		if !after.IsZero() && date.Before(after) {
			continue
		}
		if !upto.IsZero() && date.After(upto) {
			continue
		}
		for _, q := range quotes {
			rates = append(rates, adapter.Rate{Date: date, Base: "FJD", Quote: q, Rate: values[q]})
		}
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("currency header row not found in workbook")
	}
	return rates, nil
}

// excelSerial reads a day serial as Ruby's Integer(text) || Float(text)&.to_i does.
func excelSerial(text string) (int, bool) {
	if n, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
		return n, true
	}
	f, ok := adapter.ParseFloat(text)
	if !ok {
		return 0, false
	}
	return int(math.Trunc(f)), true
}

// columnMap resolves the header row's labels to ISO codes, keyed by column index.
func columnMap(r []string) map[int]string {
	m := map[int]string{}
	for i, label := range r {
		if iso, ok := currencies[strings.TrimSpace(label)]; ok {
			m[i] = iso
		}
	}
	return m
}
