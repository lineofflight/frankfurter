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
	"bytes"
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

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

// sheet wraps the first worksheet's raw cell values. Cell types are looked up lazily, since only a few cells per row
// need them and Rows/GetRows do not expose them.
type sheet struct {
	f    *excelize.File
	name string
	rows [][]string
}

func (s sheet) cellType(r, c int) excelize.CellType {
	ref, _ := excelize.CoordinatesToCellName(c+1, r+1)
	t, _ := s.f.GetCellType(s.name, ref)
	return t
}

// isText reports whether a cell holds a string rather than a stored number. The old XML parser only saw <v>, which
// shared strings index into and inline strings lack.
func (s sheet) isText(r, c int) bool {
	t := s.cellType(r, c)
	return t == excelize.CellTypeSharedString || t == excelize.CellTypeInlineString
}

func parse(data []byte) ([]adapter.Rate, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, ok := f.Pkg.Load("xl/sharedStrings.xml"); !ok {
		return nil, errors.New("xl/sharedStrings.xml missing from workbook")
	}
	// Both workbooks hold a single worksheet (named Sheet1 in one, Sheet2 in the other), stored as sheet1.xml.
	names := f.GetSheetList()
	if len(names) == 0 {
		return nil, errors.New("worksheet missing from workbook")
	}
	rows, err := f.GetRows(names[0])
	if err != nil {
		return nil, err
	}
	s := sheet{f, names[0], rows}

	dates := s.dateMap()
	var rates []adapter.Rate
	for r, rw := range rows {
		iso, ok := currencies[normalizeLabel(s.currencyLabel(r))]
		if !ok {
			continue
		}
		for c, v := range rw {
			date, ok := dates[c]
			if !ok {
				continue
			}
			rate, ok := adapter.ParseFloat(v)
			if !ok || rate <= 0 || s.isText(r, c) {
				continue
			}
			rates = append(rates, adapter.Rate{Date: date, Base: iso, Quote: "PKR", Rate: rate})
		}
	}
	return rates, nil
}

func isSerial(v string) bool {
	n := toF(v)
	return n > 30000 && n < 80000
}

// dateMap finds the date header row structurally: the first row with at least three numeric cells holding plausible
// Excel serial dates (post-1980, pre-2100), keyed by column index. SBP moves metadata rows around between revisions,
// so row numbers are not fixed.
func (s sheet) dateMap() map[int]time.Time {
	for r, rw := range s.rows {
		serials := 0
		for c, v := range rw {
			if isSerial(v) && !s.isText(r, c) {
				serials++
			}
		}
		if serials < 3 {
			continue
		}

		m := map[int]time.Time{}
		for c, v := range rw {
			if isSerial(v) && !s.isText(r, c) {
				m[c] = excelEpoch.AddDate(0, 0, toI(v))
			}
		}
		return m
	}
	return map[int]time.Time{}
}

// currencyLabel returns the first shared-string cell whose reference starts with B, as the Ruby adapter's
// start_with?("B") does (so BA, BB, ... also qualify when B itself is not a shared string).
func (s sheet) currencyLabel(r int) string {
	for c := range s.rows[r] {
		name, _ := excelize.ColumnNumberToName(c + 1)
		if strings.HasPrefix(name, "B") && s.cellType(r, c) == excelize.CellTypeSharedString {
			return s.rows[r][c]
		}
	}
	return ""
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
