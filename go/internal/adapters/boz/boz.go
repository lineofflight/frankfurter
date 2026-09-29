// Package boz fetches rates from the Bank of Zambia, which publishes the daily average buy and sell rates of four
// currencies in kwacha as one XLSX workbook carrying the whole series from 2006.
//
// The workbook has one sheet: a currency banner in row 3 ("Dollar", "Pound", "Euro", "Rand"), a header row beneath it
// ("Date | Buy | Sale | Buy | Sale ..."), then one data row per business day with an Excel serial date in column B and
// a buy/sell pair per currency. Rates are kwacha per unit of foreign currency, so rows carry the foreign currency in
// Base and the kwacha in Quote. Each rate is the midpoint of the published buy and sell.
//
// The kwacha was rebased on 2013-01-01 at 1000 ZMK = 1 ZMW. The workbook does not restate earlier rows, so rows before
// the changeover are labelled ZMK and everything after ZMW. The first seven rows (2006-01-03 to 2006-01-11) are hidden
// and mostly hold placeholder text and stray formulas; hidden rows are skipped, so coverage starts on 2006-01-12.
//
// A new node is created on the site's Drupal JSON:API each business day, attached to a freshly uploaded copy of the
// workbook under a changing filename, so we ask for the most recently created node with its file included and follow
// the file URI.
//
// Unlike most adapters, Fetch treats after as inclusive, as the Ruby adapter does.
package boz

import (
	"archive/zip"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	siteURL = "https://www.boz.zm"
	apiURL  = siteURL + "/jsonapi/node/historical_average_exchange_rate"
)

var apiParams = url.Values{
	"include":     {"field_average_historical_file"},
	"sort":        {"-created"},
	"page[limit]": {"1"},
}

var (
	// Excel stores dates as days since this epoch (with the 1900 leap-year quirk baked into the offset).
	excelEpoch = adapter.Date(1899, 12, 30)

	// First date the rebased kwacha applies. Earlier rows price the old kwacha.
	redenomination = adapter.Date(2013, 1, 1)
)

// Banner labels in row 3 (whitespace-padded in the source) to ISO 4217 codes.
var currencies = map[string]string{
	"DOLLAR": "USD",
	"POUND":  "GBP",
	"EURO":   "EUR",
	"RAND":   "ZAR",
}

func init() {
	adapter.Register("BOZ", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BOZ rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	listing, err := a.Get(ctx, apiURL, apiParams)
	if err != nil {
		return nil, err
	}
	wbURL, err := workbookURL(listing)
	if err != nil {
		return nil, err
	}
	body, err := a.Get(ctx, wbURL, nil)
	if err != nil {
		return nil, err
	}
	rates, err := parse(body)
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

// workbookURL resolves the workbook URL from the JSON:API listing: the first node's file relationship points into the
// included array, whose entry carries the root-relative file URI.
func workbookURL(data []byte) (string, error) {
	var doc struct {
		Data []struct {
			ID            string `json:"id"`
			Relationships struct {
				File struct {
					Data struct {
						ID string `json:"id"`
					} `json:"data"`
				} `json:"field_average_historical_file"`
			} `json:"relationships"`
		} `json:"data"`
		Included []struct {
			ID         string `json:"id"`
			Attributes struct {
				URI struct {
					URL string `json:"url"`
				} `json:"uri"`
			} `json:"attributes"`
		} `json:"included"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", err
	}
	if len(doc.Data) == 0 {
		return "", fmt.Errorf("no historical_average_exchange_rate node at %s", apiURL)
	}
	node := doc.Data[0]
	fileID := node.Relationships.File.Data.ID
	var path string
	if fileID != "" {
		for _, entry := range doc.Included {
			if entry.ID == fileID {
				path = entry.Attributes.URI.URL
				break
			}
		}
	}
	if path == "" {
		return "", fmt.Errorf("node %s has no workbook attached", node.ID)
	}

	base, _ := url.Parse(siteURL)
	ref, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}

type cell struct {
	Ref  string  `xml:"r,attr"`
	Type string  `xml:"t,attr"`
	V    *string `xml:"v"`
}

// value is the text of the cell's <v>, empty when it has none.
func (c cell) value() string {
	if c.V == nil {
		return ""
	}
	return *c.V
}

// column is the cell reference without its row number ("C12" -> "C").
func (c cell) column() string {
	return strings.TrimRight(c.Ref, "0123456789")
}

type row struct {
	Hidden string `xml:"hidden,attr"`
	Cells  []cell `xml:"c"`
}

func (r row) find(column string) (cell, bool) {
	for _, c := range r.Cells {
		if c.Ref != "" && c.column() == column {
			return c, true
		}
	}
	return cell{}, false
}

func parse(data []byte) ([]adapter.Rate, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	strs, err := sharedStrings(zr)
	if err != nil {
		return nil, err
	}
	sheet, err := readEntry(zr, "xl/worksheets/sheet1.xml")
	if err != nil {
		return nil, err
	}
	var ws struct {
		SheetData *struct {
			Rows []row `xml:"row"`
		} `xml:"sheetData"`
	}
	if err := xml.Unmarshal(sheet, &ws); err != nil {
		return nil, err
	}
	if ws.SheetData == nil {
		return nil, errors.New("no sheetData in workbook")
	}
	return parseSheet(ws.SheetData.Rows, strs)
}

// parseSheet reads the two header rows that precede the data: the currency banner, whose labels sit over each
// currency's Buy column, and the "Date | Buy | Sale" row that confirms the pairing. Columns are resolved from both, so
// a reshuffled workbook fails loudly instead of pairing the wrong cells.
func parseSheet(rows []row, strs []string) ([]adapter.Rate, error) {
	banner := map[string]string{}
	var columns []pair
	var rates []adapter.Rate

	for _, r := range rows {
		if columns == nil {
			labels, err := stringCells(r, strs)
			if err != nil {
				return nil, err
			}
			if labels["B"] == "DATE" {
				if columns, err = pairColumns(banner, labels); err != nil {
					return nil, err
				}
			} else {
				for column, label := range labels {
					if iso, ok := currencies[label]; ok {
						banner[column] = iso
					}
				}
			}
			continue
		}

		if r.Hidden == "1" {
			continue
		}
		date, ok := dateCell(r, "B")
		if !ok {
			continue
		}
		quote := "ZMW"
		if date.Before(redenomination) {
			quote = "ZMK"
		}

		for _, p := range columns {
			buy, ok := numericCell(r, p.buy)
			if !ok {
				continue
			}
			sell, ok := numericCell(r, p.sell)
			if !ok {
				continue
			}
			rates = append(rates, adapter.Rate{
				Date:  date,
				Base:  p.iso,
				Quote: quote,
				Rate:  midpoint(buy, sell),
				Bid:   adapter.Float(ratFloat(buy)),
				Ask:   adapter.Float(ratFloat(sell)),
			})
		}
	}

	if columns == nil {
		return nil, errors.New("no header row in workbook")
	}
	return rates, nil
}

type pair struct {
	iso, buy, sell string
}

func pairColumns(banner, labels map[string]string) ([]pair, error) {
	if len(banner) == 0 {
		return nil, errors.New("no currency banner above the header row")
	}
	pairs := make([]pair, 0, len(banner))
	for buy, iso := range banner {
		sell := succ(buy)
		if labels[buy] != "BUY" || labels[sell] != "SALE" {
			return nil, fmt.Errorf("expected Buy/Sale under %s at %s/%s", iso, buy, sell)
		}
		pairs = append(pairs, pair{iso, buy, sell})
	}
	// Keep the sheet's left-to-right order, as Ruby's insertion-ordered hash does.
	slices.SortFunc(pairs, func(a, b pair) int {
		return cmp.Or(cmp.Compare(len(a.buy), len(b.buy)), strings.Compare(a.buy, b.buy))
	})
	return pairs, nil
}

// succ is Ruby's String#succ for column letters: "C" -> "D", "Z" -> "AA", "AZ" -> "BA".
func succ(column string) string {
	b := []byte(column)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] != 'Z' {
			b[i]++
			return string(b)
		}
		b[i] = 'A'
	}
	return "A" + string(b)
}

func stringCells(r row, strs []string) (map[string]string, error) {
	labels := map[string]string{}
	for _, c := range r.Cells {
		if c.Ref == "" || c.Type != "s" || c.value() == "" {
			continue
		}
		i, _ := strconv.Atoi(c.value())
		if i < 0 || i >= len(strs) {
			return nil, fmt.Errorf("shared string %d out of range at %s", i, c.Ref)
		}
		labels[c.column()] = strings.ToUpper(strings.TrimSpace(strs[i]))
	}
	return labels, nil
}

func dateCell(r row, column string) (time.Time, bool) {
	c, ok := r.find(column)
	if !ok || c.Type == "s" || c.value() == "" {
		return time.Time{}, false
	}
	serial, err := strconv.ParseFloat(strings.TrimSpace(c.value()), 64)
	if err != nil || serial <= 30_000 || serial >= 80_000 {
		return time.Time{}, false
	}
	return excelEpoch.AddDate(0, 0, int(serial)), true
}

// numericCell returns the stored text as an exact decimal, so the midpoint sees the published digits rather than a
// float round-trip.
func numericCell(r row, column string) (*big.Rat, bool) {
	c, ok := r.find(column)
	if !ok || c.Type == "s" {
		return nil, false
	}
	text := strings.TrimSpace(c.value())
	f, ok := adapter.ParseFloat(text)
	if !ok || f <= 0 {
		return nil, false
	}
	v, ok := new(big.Rat).SetString(text)
	return v, ok
}

func midpoint(buy, sell *big.Rat) float64 {
	sum := new(big.Rat).Add(buy, sell)
	return ratFloat(sum.Quo(sum, big.NewRat(2, 1)))
}

func ratFloat(r *big.Rat) float64 {
	f, _ := r.Float64()
	return f
}

func sharedStrings(zr *zip.Reader) ([]string, error) {
	data, err := readEntry(zr, "xl/sharedStrings.xml")
	if err != nil {
		return nil, err
	}
	var sst struct {
		Items []struct {
			T    []string `xml:"t"`
			Runs []struct {
				T string `xml:"t"`
			} `xml:"r"`
		} `xml:"si"`
	}
	if err := xml.Unmarshal(data, &sst); err != nil {
		return nil, err
	}
	strs := make([]string, len(sst.Items))
	for i, si := range sst.Items {
		s := strings.Join(si.T, "")
		for _, run := range si.Runs {
			s += run.T
		}
		strs[i] = s
	}
	return strs, nil
}

func readEntry(zr *zip.Reader, name string) ([]byte, error) {
	f, err := zr.Open(name)
	if err != nil {
		return nil, fmt.Errorf("%s missing from workbook", name)
	}
	defer f.Close()
	return io.ReadAll(f)
}
