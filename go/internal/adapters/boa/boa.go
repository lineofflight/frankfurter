// Package boa fetches rates from the Bank of Algeria, which publishes a daily reference rate (cours moyen) against
// DZD for 17 foreign currencies.
//
// The full series is a single consolidated XLSX with one sheet per currency, refreshed roughly once a month, so rates
// typically lag by up to about 3 weeks. The XLSX URL embeds the publication year and month under /stoodroa/YYYY/MM/,
// so the adapter scrapes the donnees-historiques hub for the current link rather than hardcoding a path. The site
// omits its DigiCert intermediate; see config/ca_bundles.
//
// Each sheet is named "<CCY> - DZD" ("EURO" in place of "EUR") with Excel serial dates in column A and "1 CCY = X
// DZD" rates in column B. Rows keep BoA's direction: foreign currency as base, DZD as quote. JPY is published per 100
// and normalised here. MRO is the legacy Mauritanian ouguiya, kept as published.
//
// Unlike most adapters, Fetch treats after as inclusive, as the Ruby adapter does.
package boa

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

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
	cellColumn    = regexp.MustCompile(`^([A-Z]+)`)
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

// BackfillRange implements adapter.Adapter: each refresh of the consolidated XLSX rebuilds the whole series, so one
// fetch covers it.
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

type workbook struct {
	Sheets []struct {
		Name string `xml:"name,attr"`
		// r:id; encoding/xml matches the local name whatever the namespace.
		ID string `xml:"id,attr"`
	} `xml:"sheets>sheet"`
}

type relationships struct {
	Rels []struct {
		ID     string `xml:"Id,attr"`
		Target string `xml:"Target,attr"`
	} `xml:"Relationship"`
}

type worksheet struct {
	Rows []struct {
		Cells []struct {
			Ref  string  `xml:"r,attr"`
			Type string  `xml:"t,attr"`
			V    *string `xml:"v"`
		} `xml:"c"`
	} `xml:"sheetData>row"`
}

func parse(data []byte, after, upto time.Time) ([]adapter.Rate, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var wb workbook
	if err := readXML(zr, "xl/workbook.xml", &wb); err != nil {
		return nil, err
	}
	var rels relationships
	if err := readXML(zr, "xl/_rels/workbook.xml.rels", &rels); err != nil {
		return nil, err
	}
	targets := make(map[string]string, len(rels.Rels))
	for _, r := range rels.Rels {
		targets[r.ID] = r.Target
	}

	var rates []adapter.Rate
	for _, sheet := range wb.Sheets {
		base, ok := sheetCurrency(sheet.Name)
		if !ok {
			continue
		}
		target, ok := targets[sheet.ID]
		if !ok {
			continue
		}
		var ws worksheet
		if err := readXML(zr, "xl/"+target, &ws); err != nil {
			return nil, err
		}
		rates = append(rates, parseSheet(ws, base, after, upto)...)
	}
	return rates, nil
}

func readXML(zr *zip.Reader, name string, v any) error {
	f, err := zr.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if err := xml.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
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

func parseSheet(ws worksheet, base string, after, upto time.Time) []adapter.Rate {
	var rates []adapter.Rate
	for _, row := range ws.Rows {
		var (
			serial         int
			value          float64
			hasSer, hasVal bool
		)
		for _, c := range row.Cells {
			if c.Ref == "" || c.Type == "s" || c.V == nil || *c.V == "" {
				continue
			}
			m := cellColumn.FindStringSubmatch(c.Ref)
			if m == nil {
				continue
			}
			switch m[1] {
			case "A":
				serial, hasSer = parseSerial(*c.V)
			case "B":
				value, hasVal = adapter.ParseFloat(*c.V)
			}
		}
		if !hasSer || !hasVal {
			continue
		}
		date := excelEpoch.AddDate(0, 0, serial)
		if !after.IsZero() && date.Before(after) {
			continue
		}
		if !upto.IsZero() && date.After(upto) {
			continue
		}
		rate := value
		if base == "JPY" {
			rate = value / jpyUnits
		}
		if rate == 0 {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: base, Quote: "DZD", Rate: rate})
	}
	return rates
}

// parseSerial mirrors Integer(text, exception: false) || Float(text, exception: false)&.to_i.
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
