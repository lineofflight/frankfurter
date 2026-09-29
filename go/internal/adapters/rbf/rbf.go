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
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const hubURL = "https://www.rbf.gov.fj/statistics/economic-and-financial-statistics/"

var (
	archiveLink = regexp.MustCompile(`href="(https://www\.rbf\.gov\.fj/wp-content/uploads/\d{4}/\d{2}/8\.8-Exchange-Rates-Daily[^"]*\.xlsx)"`)
	columnRef   = regexp.MustCompile(`^[A-Z]+`)
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

type cell struct {
	Ref    string   `xml:"r,attr"`
	Type   string   `xml:"t,attr"`
	Values []string `xml:"v"`
}

type row struct {
	Cells []cell `xml:"c"`
}

type worksheet struct {
	Rows []row `xml:"sheetData>row"`
}

type sharedStrings struct {
	Items []struct {
		Texts []string `xml:"t"`
	} `xml:"si"`
}

func parse(data []byte, after, upto time.Time) ([]adapter.Rate, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	strs, err := readSharedStrings(zr)
	if err != nil {
		return nil, err
	}
	var sheet worksheet
	if err := unmarshalEntry(zr, "xl/worksheets/sheet1.xml", &sheet); err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	var columns map[string]string
	for _, r := range sheet.Rows {
		if len(columns) == 0 {
			columns = columnMap(r, strs)
			continue
		}

		var date time.Time
		var quotes []string
		values := map[string]float64{}
		for _, c := range r.Cells {
			if c.Ref == "" || c.Type == "s" || len(c.Values) == 0 || c.Values[0] == "" {
				continue
			}
			col := columnRef.FindString(c.Ref)
			text := c.Values[0]
			if col == "A" {
				if serial, ok := excelSerial(text); ok {
					date = excelEpoch.AddDate(0, 0, serial)
				}
				continue
			}
			iso, ok := columns[col]
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

// columnMap resolves the header row's shared-string labels to ISO codes, keyed by column letters.
func columnMap(r row, strs []string) map[string]string {
	m := map[string]string{}
	for _, c := range r.Cells {
		if c.Type != "s" || c.Ref == "" || len(c.Values) == 0 {
			continue
		}
		i, err := strconv.Atoi(strings.TrimSpace(c.Values[0]))
		if err != nil || i < 0 || i >= len(strs) {
			continue
		}
		if iso, ok := currencies[strings.TrimSpace(strs[i])]; ok {
			m[columnRef.FindString(c.Ref)] = iso
		}
	}
	return m
}

func readSharedStrings(zr *zip.Reader) ([]string, error) {
	var sst sharedStrings
	if err := unmarshalEntry(zr, "xl/sharedStrings.xml", &sst); err != nil {
		return nil, err
	}
	strs := make([]string, len(sst.Items))
	for i, si := range sst.Items {
		strs[i] = strings.Join(si.Texts, "")
	}
	return strs, nil
}

func unmarshalEntry(zr *zip.Reader, name string, v any) error {
	f, err := zr.Open(name)
	if err != nil {
		return fmt.Errorf("%s missing from workbook: %w", name, err)
	}
	defer f.Close()
	return xml.NewDecoder(f).Decode(v)
}
