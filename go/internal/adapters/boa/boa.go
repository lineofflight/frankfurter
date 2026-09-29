// Package boa fetches rates from the Bank of Algeria, which publishes a daily
// reference rate (cours moyen) against DZD for 17 foreign currencies.
//
// The full series is a single consolidated XLSX with one sheet per currency,
// refreshed roughly once a month, so rates typically lag by up to about 3
// weeks. The XLSX URL embeds the publication year and month under
// /stoodroa/YYYY/MM/, so the adapter scrapes the donnees-historiques hub for
// the current link rather than hardcoding a path. The site omits its DigiCert
// intermediate; see config/ca_bundles.
//
// Each sheet is named "<CCY> - DZD" ("EURO" in place of "EUR") with Excel
// serial dates in column A and "1 CCY = X DZD" rates in column B. Rows keep
// BoA's direction: foreign currency as base, DZD as quote. JPY is published per
// 100 and normalised here. MRO is the legacy Mauritanian ouguiya, kept as
// published.
//
// Unlike most adapters, Fetch treats after as inclusive, as the Ruby adapter
// does.
package boa

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	hubURL   = "https://www.bank-of-algeria.dz/donnees-historiques/"
	jpyUnits = 100
)

var (
	archiveLink   = regexp.MustCompile(`href="(https://www\.bank-of-algeria\.dz/stoodroa/\d{4}/\d{2}/Cotation-DZD-[^"]+\.xlsx)"`)
	sheetNameSep  = regexp.MustCompile(`\s*[-/]\s*`)
	isoCode       = regexp.MustCompile(`^[A-Z]{3}$`)
	excelEpoch    = adapter.Date(1899, 12, 30)
	nameOverrides = map[string]string{"EURO": "EUR"}
)

func init() {
	adapter.Register("BOA", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BOA rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter: each refresh of the consolidated
// XLSX rebuilds the whole series, so one fetch covers it.
func (a *Adapter) BackfillRange() int { return 36_525 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	hub, err := a.Get(ctx, hubURL, nil)
	if err != nil {
		return nil, err
	}
	xlsxURL, err := archiveURL(hub)
	if err != nil {
		return nil, err
	}
	data, err := a.Get(ctx, xlsxURL, nil)
	if err != nil {
		return nil, err
	}
	return parse(data, after, upto)
}

func archiveURL(hub []byte) (string, error) {
	m := archiveLink.FindSubmatch(hub)
	if m == nil {
		return "", fmt.Errorf("archive XLSX link not found on %s", hubURL)
	}
	return string(m[1]), nil
}

func parse(data []byte, after, upto time.Time) ([]adapter.Rate, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var rates []adapter.Rate
	for _, sheet := range f.GetSheetList() {
		base, ok := sheetCurrency(sheet)
		if !ok {
			continue
		}
		rows, err := f.GetRows(sheet, excelize.Options{RawCellValue: true})
		if err != nil {
			return nil, err
		}
		for i, row := range rows {
			rate, ok := parseRow(row, base, after, upto)
			if !ok {
				continue
			}
			// Ruby ignores string cells, so a numeric-looking header does not
			// count.
			if isString(f, sheet, "A", i+1) || isString(f, sheet, "B", i+1) {
				continue
			}
			rates = append(rates, rate)
		}
	}
	return rates, nil
}

func isString(f *excelize.File, sheet, col string, row int) bool {
	t, _ := f.GetCellType(sheet, col+strconv.Itoa(row))
	return t == excelize.CellTypeSharedString || t == excelize.CellTypeInlineString
}

func sheetCurrency(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	code := strings.ToUpper(strings.TrimSpace(sheetNameSep.Split(name, 2)[0]))
	if o, ok := nameOverrides[code]; ok {
		code = o
	}
	return code, isoCode.MatchString(code)
}

// parseRow reads a raw-valued row: Excel serial date in column A, rate in
// column B.
func parseRow(cols []string, base string, after, upto time.Time) (adapter.Rate, bool) {
	if len(cols) < 2 || cols[0] == "" || cols[1] == "" {
		return adapter.Rate{}, false
	}
	serial, ok := parseSerial(cols[0])
	if !ok {
		return adapter.Rate{}, false
	}
	value, ok := adapter.ParseFloat(cols[1])
	if !ok {
		return adapter.Rate{}, false
	}
	date := excelEpoch.AddDate(0, 0, serial)
	if !after.IsZero() && date.Before(after) {
		return adapter.Rate{}, false
	}
	if !upto.IsZero() && date.After(upto) {
		return adapter.Rate{}, false
	}
	rate := value
	if base == "JPY" {
		rate = value / jpyUnits
	}
	if rate == 0 {
		return adapter.Rate{}, false
	}
	return adapter.Rate{Date: date, Base: base, Quote: "DZD", Rate: rate}, true
}

// parseSerial mirrors Integer(text, exception: false) || Float(text, exception:
// false)&.to_i.
func parseSerial(s string) (int, bool) {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n, true
	}
	f, ok := adapter.ParseFloat(s)
	if !ok {
		return 0, false
	}
	return int(f), true
}
