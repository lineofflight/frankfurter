// Package cbs fetches rates from the Central Bank of Samoa, which publishes a daily fix as one XLSX archive
// workbook covering 2008-01-02 to the latest business day.
//
// The workbook has one sheet per year, each holding twelve month sections stacked vertically: a month-name banner in
// column B, a "DATE | TALA/USD | TALA/NZD | ..." header row, then one row per business day with an Excel serial date
// in column B. Rates are units of foreign currency per tala, so rows carry WST as base and the foreign currency as
// quote.
//
// The workbook's filename is date-stamped and changes daily, so each fetch scrapes the data page for its current
// ".xlsx" link. Unlike most adapters, Fetch keeps rows dated on after (inclusive), as the Ruby adapter does.
package cbs

import (
	"bytes"
	"context"
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
	"github.com/xuri/excelize/v2"
)

const dataURL = "https://cbs.gov.ws/daily-exchange-rates"

// currencies maps the header labels above each month section to ISO 4217 codes.
var currencies = map[string]string{
	"TALA/USD":  "USD",
	"TALA/NZD":  "NZD",
	"TALA/AUD":  "AUD",
	"TALA/EURO": "EUR",
	"TALA/FJD":  "FJD",
	"TALA/YEN":  "JPY",
	"TALA/GBP":  "GBP",
	"TALA/CNY":  "CNY",
	"TALA/CNH":  "CNH",
}

var monthNames = []string{
	"JANUARY", "FEBRUARY", "MARCH", "APRIL", "MAY", "JUNE",
	"JULY", "AUGUST", "SEPTEMBER", "OCTOBER", "NOVEMBER", "DECEMBER",
}

// Excel serial dates count days from this epoch, which absorbs the 1900 leap-year quirk.
var excelEpoch = adapter.Date(1899, 12, 30)

var linkPattern = regexp.MustCompile(`(?i)href=["']([^"']*\.xlsx)["']`)

func init() {
	adapter.Register("CBS", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches CBS rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	page, err := a.Get(ctx, dataURL, nil)
	if err != nil {
		return nil, err
	}
	link, err := archiveURL(string(page))
	if err != nil {
		return nil, err
	}
	workbook, err := a.Get(ctx, link, nil)
	if err != nil {
		return nil, err
	}
	rates, err := parse(workbook)
	if err != nil {
		return nil, err
	}
	var out []adapter.Rate
	for _, r := range rates {
		if !after.IsZero() && r.Date.Before(after) {
			continue
		}
		if !upto.IsZero() && r.Date.After(upto) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// archiveURL resolves the workbook link from the data page. The filename scheme has shifted between hyphen- and
// space-separated forms, so the path is percent-encoded before joining.
func archiveURL(html string) (string, error) {
	m := linkPattern.FindStringSubmatch(html)
	if m == nil {
		return "", fmt.Errorf("no workbook link on %s", dataURL)
	}
	ref, err := url.Parse(escapeRFC2396(m[1]))
	if err != nil {
		return "", err
	}
	base, _ := url.Parse(dataURL)
	return base.ResolveReference(ref).String(), nil
}

// escapeRFC2396 percent-encodes every byte outside RFC 2396's unreserved and reserved sets, like Ruby's
// URI::RFC2396_PARSER.escape.
func escapeRFC2396(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
			strings.IndexByte("-_.!~*'();/?:@&=+$,[]", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// parse reads every sheet in the workbook. Sheets are not in chronological order; the date filter sorts it out.
func parse(data []byte) ([]adapter.Rate, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var rates []adapter.Rate
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", sheet, err)
		}
		rates = parseSheet(rows, rates)
	}
	if len(rates) == 0 {
		return nil, errors.New("no rates in workbook")
	}
	return rates, nil
}

// parseSheet walks rows keyed on column B: a month banner, a DATE header, or an Excel serial date.
func parseSheet(rows [][]string, rates []adapter.Rate) []adapter.Rate {
	var columns map[int]string
	for _, r := range rows {
		var label string
		if len(r) > 1 {
			label = r[1]
		}
		if label == "DATE" {
			columns = columnMap(r)
			continue
		}
		// A month banner resets the map so a malformed section cannot leak headers across months.
		if slices.Contains(monthNames, strings.ToUpper(label)) {
			columns = nil
			continue
		}
		if len(columns) == 0 {
			continue
		}
		date, ok := serialDate(label)
		if !ok {
			continue
		}
		for i, v := range r {
			iso, ok := columns[i]
			if !ok {
				continue
			}
			if value, ok := adapter.ParseFloat(v); ok && value > 0 {
				rates = append(rates, adapter.Rate{Date: date, Base: "WST", Quote: iso, Rate: value})
			}
		}
	}
	return rates
}

func columnMap(r []string) map[int]string {
	m := map[int]string{}
	for i, label := range r {
		if iso, ok := currencies[strings.ToUpper(label)]; ok && i != 1 {
			m[i] = iso
		}
	}
	return m
}

func serialDate(s string) (time.Time, bool) {
	serial, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || serial <= 30_000 || serial >= 80_000 {
		return time.Time{}, false
	}
	return excelEpoch.AddDate(0, 0, int(serial)), true
}
