// Package sbp fetches rates from the State Bank of Pakistan, which publishes the "Daily Average Banks' Floating
// Exchange Rates" series as two XLSX workbooks: a current-month file and a historical archive going back to
// 2013-07-02. Both share one wide layout: a row per foreign currency, a column per date, values in Pak Rupees per
// currency unit. Rows are emitted as foreign base, PKR quote.
//
// SBP publishes finalized monthly snapshots roughly three weeks after the month ends. Since a mid-2026 site
// restructure the current-month file has lagged the archive, so the archive wins on overlap.
//
// Unlike most adapters, Fetch treats after as inclusive, as the Ruby adapter does.
package sbp

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
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
	currentURL = "https://www.sbp.org.pk/assets/document/BFER_Daily.xlsx"
	archiveURL = "https://www.sbp.org.pk/assets/document/BFER_Daily_Arch.xlsx"
)

// excelEpoch sidesteps Excel's 1900 leap-year bug for dates after 1900-03-01.
var excelEpoch = adapter.Date(1899, 12, 30)

// currencies maps normalized currency-name labels (column B) to ISO codes. The current and archive workbooks spell
// several names differently, so both spellings are registered.
var currencies = map[string]string{
	"australian dollar":   "AUD",
	"bahraini dinar":      "BHD",
	"canadian dollar":     "CAD",
	"chinese yuan":        "CNY",
	"danish krone":        "DKK",
	"euro":                "EUR",
	"hong kong dollar":    "HKD",
	"hongkong dollar":     "HKD",
	"japanese yen":        "JPY",
	"japnese yen":         "JPY",
	"kuwaiti dinar":       "KWD",
	"malaysian ringgit":   "MYR",
	"new zealand dollar":  "NZD",
	"norwegian krone":     "NOK",
	"omani riyal":         "OMR",
	"qatari riyal":        "QAR",
	"saudi arabian riyal": "SAR",
	"singapore dollar":    "SGD",
	"singaporian dollar":  "SGD",
	"swedish krona":       "SEK",
	"swedish krone":       "SEK",
	"swiss franc":         "CHF",
	"thai baht":           "THB",
	"thai bhat":           "THB",
	"turkish lira":        "TRY",
	"uae dirham":          "AED",
	"u.a.e. dirham":       "AED",
	"uk pound sterling":   "GBP",
	"u.k. pound sterling": "GBP",
	"u.s. dollar":         "USD",
}

func init() {
	adapter.Register("SBP", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches SBP rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter. Rows dated on after are included.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	type key struct {
		date time.Time
		base string
	}
	index := map[key]int{}
	var out []adapter.Rate

	// Current first, archive second, so archive rows overwrite current ones on overlap.
	for _, u := range []string{currentURL, archiveURL} {
		body, err := a.Get(ctx, u, nil)
		if err != nil {
			return nil, err
		}
		rates, err := parse(body)
		if err != nil {
			return nil, err
		}
		for _, r := range rates {
			if !after.IsZero() && r.Date.Before(after) {
				continue
			}
			if !upto.IsZero() && r.Date.After(upto) {
				continue
			}
			k := key{r.Date, r.Base}
			if i, ok := index[k]; ok {
				out[i] = r
				continue
			}
			index[k] = len(out)
			out = append(out, r)
		}
	}
	return out, nil
}

type cell struct {
	Ref  string  `xml:"r,attr"`
	Type string  `xml:"t,attr"`
	V    *string `xml:"v"`
}

type row struct {
	Cells []cell `xml:"c"`
}

type worksheet struct {
	Rows []row `xml:"sheetData>row"`
}

var trailingDigits = regexp.MustCompile(`\d+$`)

func column(ref string) string { return trailingDigits.ReplaceAllString(ref, "") }

func parse(data []byte) ([]adapter.Rate, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	strs, err := sharedStrings(zr)
	if err != nil {
		return nil, err
	}
	sheetXML, err := readEntry(zr, "xl/worksheets/sheet1.xml")
	if err != nil {
		return nil, err
	}
	if sheetXML == nil {
		return nil, errors.New("xl/worksheets/sheet1.xml missing from workbook")
	}
	var ws worksheet
	if err := xml.Unmarshal(sheetXML, &ws); err != nil {
		return nil, err
	}

	dates := dateMap(ws.Rows)
	var rates []adapter.Rate
	for _, rw := range ws.Rows {
		label, ok := currencyLabel(rw, strs)
		if !ok {
			continue
		}
		iso, ok := currencies[normalizeLabel(label)]
		if !ok {
			continue
		}
		for _, c := range rw.Cells {
			if c.Ref == "" {
				continue
			}
			date, ok := dates[column(c.Ref)]
			if !ok {
				continue
			}
			value, ok := numericValue(c)
			if !ok {
				continue
			}
			rates = append(rates, adapter.Rate{Date: date, Base: iso, Quote: "PKR", Rate: value})
		}
	}
	return rates, nil
}

func readEntry(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, nil
}

func sharedStrings(zr *zip.Reader) ([]string, error) {
	data, err := readEntry(zr, "xl/sharedStrings.xml")
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, errors.New("xl/sharedStrings.xml missing from workbook")
	}
	var sst struct {
		XMLName xml.Name
		Items   []struct {
			Parts []struct {
				Text string `xml:",chardata"`
			} `xml:",any"`
		} `xml:"si"`
	}
	if err := xml.Unmarshal(data, &sst); err != nil {
		return nil, fmt.Errorf("sharedStrings: %w", err)
	}
	out := make([]string, len(sst.Items))
	for i, si := range sst.Items {
		var b strings.Builder
		for _, p := range si.Parts {
			b.WriteString(p.Text)
		}
		out[i] = b.String()
	}
	return out, nil
}

// dateMap finds the date header row structurally: the first row with at least three numeric cells holding plausible
// Excel serial dates (post-1980, pre-2100). SBP moves metadata rows around between revisions, so row numbers are not
// fixed.
func dateMap(rows []row) map[string]time.Time {
	for _, rw := range rows {
		serials := 0
		for _, c := range rw.Cells {
			if c.Type == "s" || c.V == nil {
				continue
			}
			if n := toF(*c.V); n > 30000 && n < 80000 {
				serials++
			}
		}
		if serials < 3 {
			continue
		}

		m := map[string]time.Time{}
		for _, c := range rw.Cells {
			if c.Type == "s" || c.Ref == "" || c.V == nil || *c.V == "" {
				continue
			}
			if n := toF(*c.V); n <= 30000 || n >= 80000 {
				continue
			}
			m[column(c.Ref)] = excelEpoch.AddDate(0, 0, toI(*c.V))
		}
		return m
	}
	return map[string]time.Time{}
}

func currencyLabel(rw row, strs []string) (string, bool) {
	for _, c := range rw.Cells {
		if !strings.HasPrefix(c.Ref, "B") || c.Type != "s" {
			continue
		}
		if c.V == nil {
			return "", false
		}
		i := toI(*c.V)
		if i < 0 || i >= len(strs) {
			return "", false
		}
		return strs[i], true
	}
	return "", false
}

func numericValue(c cell) (float64, bool) {
	if c.Type == "s" || c.V == nil {
		return 0, false
	}
	rate, ok := adapter.ParseFloat(*c.V)
	if !ok || rate <= 0 {
		return 0, false
	}
	return rate, true
}

var whitespace = regexp.MustCompile(`[[:space:]\x{00a0}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{0085}]+`)

func normalizeLabel(label string) string {
	return strings.TrimSpace(whitespace.ReplaceAllString(strings.ToLower(label), " "))
}

var leadingFloat = regexp.MustCompile(`^\s*[+-]?(\d+(\.\d+)?([eE][+-]?\d+)?|\.\d+)`)

// toF mirrors Ruby's String#to_f: the leading number, or 0.
func toF(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(leadingFloat.FindString(s)), 64)
	if err != nil {
		return 0
	}
	return f
}

var leadingInt = regexp.MustCompile(`^\s*[+-]?\d+`)

// toI mirrors Ruby's String#to_i: the leading integer, or 0.
func toI(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(leadingInt.FindString(s)))
	if err != nil {
		return 0
	}
	return n
}
