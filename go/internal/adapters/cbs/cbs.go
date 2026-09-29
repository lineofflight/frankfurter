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
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
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

var (
	linkPattern  = regexp.MustCompile(`(?i)href=["']([^"']*\.xlsx)["']`)
	sheetPattern = regexp.MustCompile(`\Axl/worksheets/sheet\d+\.xml\z`)
	rowDigits    = regexp.MustCompile(`\d+\z`)
)

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

type worksheet struct {
	Rows []row `xml:"sheetData>row"`
}

type row struct {
	Cells []cell `xml:"c"`
}

type cell struct {
	Ref   string  `xml:"r,attr"`
	Type  string  `xml:"t,attr"`
	Value *string `xml:"v"`
}

func (c cell) column() string { return rowDigits.ReplaceAllString(c.Ref, "") }

func parse(data []byte) ([]adapter.Rate, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	strs, err := sharedStrings(zr)
	if err != nil {
		return nil, err
	}

	// One sheet per year, but sheet numbering is not chronological; the date filter sorts it out.
	var sheets []*zip.File
	for _, f := range zr.File {
		if sheetPattern.MatchString(f.Name) {
			sheets = append(sheets, f)
		}
	}
	slices.SortFunc(sheets, func(a, b *zip.File) int { return strings.Compare(a.Name, b.Name) })

	var rates []adapter.Rate
	for _, f := range sheets {
		body, err := readFile(f)
		if err != nil {
			return nil, err
		}
		var ws worksheet
		if err := xml.Unmarshal(body, &ws); err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		rates = parseSheet(ws.Rows, strs, rates)
	}
	return rates, nil
}

func parseSheet(rows []row, strs []string, rates []adapter.Rate) []adapter.Rate {
	var columns map[string]string
	for _, r := range rows {
		label, ok := stringCell(r, "B", strs)
		if ok && label == "DATE" {
			columns = columnMap(r, strs)
			continue
		}
		// A month banner resets the map so a malformed section cannot leak headers across months.
		if ok && slices.Contains(monthNames, strings.ToUpper(label)) {
			columns = nil
			continue
		}
		if len(columns) == 0 {
			continue
		}
		date, ok := dateCell(r, "B")
		if !ok {
			continue
		}
		for _, c := range r.Cells {
			if c.Ref == "" {
				continue
			}
			iso, ok := columns[c.column()]
			if !ok {
				continue
			}
			value, ok := numericValue(c)
			if !ok {
				continue
			}
			rates = append(rates, adapter.Rate{Date: date, Base: "WST", Quote: iso, Rate: value})
		}
	}
	return rates
}

func columnMap(r row, strs []string) map[string]string {
	m := map[string]string{}
	for _, c := range r.Cells {
		if c.Ref == "" || c.column() == "B" {
			continue
		}
		label, ok := stringValue(c, strs)
		if !ok {
			continue
		}
		if iso, ok := currencies[strings.ToUpper(label)]; ok {
			m[c.column()] = iso
		}
	}
	return m
}

func findCell(r row, column string) (cell, bool) {
	for _, c := range r.Cells {
		if c.Ref != "" && c.column() == column {
			return c, true
		}
	}
	return cell{}, false
}

func stringCell(r row, column string, strs []string) (string, bool) {
	c, ok := findCell(r, column)
	if !ok {
		return "", false
	}
	return stringValue(c, strs)
}

func stringValue(c cell, strs []string) (string, bool) {
	if c.Type != "s" || c.Value == nil {
		return "", false
	}
	i, err := strconv.Atoi(strings.TrimSpace(*c.Value))
	if err != nil || i < 0 || i >= len(strs) {
		return "", false
	}
	return strs[i], true
}

func dateCell(r row, column string) (time.Time, bool) {
	c, ok := findCell(r, column)
	if !ok || c.Type == "s" || c.Value == nil {
		return time.Time{}, false
	}
	serial, err := strconv.ParseFloat(strings.TrimSpace(*c.Value), 64)
	if err != nil || serial <= 30_000 || serial >= 80_000 {
		return time.Time{}, false
	}
	return excelEpoch.AddDate(0, 0, int(serial)), true
}

func numericValue(c cell) (float64, bool) {
	if c.Type == "s" || c.Value == nil {
		return 0, false
	}
	v, ok := adapter.ParseFloat(*c.Value)
	if !ok || v <= 0 {
		return 0, false
	}
	return v, true
}

// sharedStringTable reads plain and rich-text entries alike, joining a rich entry's runs.
type sharedStringTable struct {
	Items []struct {
		Text string `xml:"t"`
		Runs []struct {
			Text string `xml:"t"`
		} `xml:"r"`
	} `xml:"si"`
}

func sharedStrings(zr *zip.Reader) ([]string, error) {
	f, err := zr.Open("xl/sharedStrings.xml")
	if err != nil {
		return nil, errors.New("xl/sharedStrings.xml missing from workbook")
	}
	defer f.Close()
	body, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var sst sharedStringTable
	if err := xml.Unmarshal(body, &sst); err != nil {
		return nil, fmt.Errorf("sharedStrings.xml: %w", err)
	}
	strs := make([]string, len(sst.Items))
	for i, si := range sst.Items {
		text := si.Text
		for _, r := range si.Runs {
			text += r.Text
		}
		strs[i] = text
	}
	return strs, nil
}

func readFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
