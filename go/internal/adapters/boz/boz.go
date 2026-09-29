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
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

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

// Raw values keep the stored serial dates and unformatted decimals rather than their display strings.
var raw = excelize.Options{RawCellValue: true}

func parse(data []byte) ([]adapter.Rate, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s := &sheet{f: f, name: f.GetSheetName(0)}
	rows, err := f.Rows(s.name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.parse(rows)
}

type sheet struct {
	f    *excelize.File
	name string
}

// parse reads the two header rows that precede the data: the currency banner, whose labels sit over each currency's
// Buy column, and the "Date | Buy | Sale" row that confirms the pairing. Columns are resolved from both, so a
// reshuffled workbook fails loudly instead of pairing the wrong cells.
func (s *sheet) parse(rows *excelize.Rows) ([]adapter.Rate, error) {
	banner := map[int]string{}
	var columns []pair
	var rates []adapter.Rate

	// The iterator yields every row number in turn, empty ones included, so n tracks the 1-based row.
	for n := 1; rows.Next(); n++ {
		cells, err := rows.Columns(raw)
		if err != nil {
			return nil, err
		}
		if columns == nil {
			labels := labelCells(cells)
			if labels[1] == "DATE" {
				if columns, err = pairColumns(banner, labels); err != nil {
					return nil, err
				}
			} else {
				for col, label := range labels {
					if iso, ok := currencies[label]; ok {
						banner[col] = iso
					}
				}
			}
			continue
		}

		if rows.GetRowOpts().Hidden {
			continue
		}
		date, ok := s.dateCell(cells, n, 1)
		if !ok {
			continue
		}
		quote := "ZMW"
		if date.Before(redenomination) {
			quote = "ZMK"
		}

		for _, p := range columns {
			buy, ok := s.numericCell(cells, n, p.buy)
			if !ok {
				continue
			}
			sell, ok := s.numericCell(cells, n, p.buy+1)
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
	if err := rows.Error(); err != nil {
		return nil, err
	}

	if columns == nil {
		return nil, errors.New("no header row in workbook")
	}
	return rates, nil
}

// pair holds a currency and the zero-based index of its Buy column; Sale sits immediately right of it.
type pair struct {
	iso string
	buy int
}

func pairColumns(banner, labels map[int]string) ([]pair, error) {
	if len(banner) == 0 {
		return nil, errors.New("no currency banner above the header row")
	}
	pairs := make([]pair, 0, len(banner))
	for buy, iso := range banner {
		if labels[buy] != "BUY" || labels[buy+1] != "SALE" {
			b, _ := excelize.ColumnNumberToName(buy + 1)
			s, _ := excelize.ColumnNumberToName(buy + 2)
			return nil, fmt.Errorf("expected Buy/Sale under %s at %s/%s", iso, b, s)
		}
		pairs = append(pairs, pair{iso, buy})
	}
	// Keep the sheet's left-to-right order, as Ruby's insertion-ordered hash does.
	slices.SortFunc(pairs, func(a, b pair) int { return cmp.Compare(a.buy, b.buy) })
	return pairs, nil
}

// labelCells maps each non-empty cell's column index to its trimmed, upcased text.
func labelCells(cells []string) map[int]string {
	labels := map[int]string{}
	for i, v := range cells {
		if v != "" {
			labels[i] = strings.ToUpper(strings.TrimSpace(v))
		}
	}
	return labels
}

// value returns the raw text at column index col, rejecting string cells so a label never reads as a number.
func (s *sheet) value(cells []string, n, col int) (string, bool) {
	if col >= len(cells) || cells[col] == "" {
		return "", false
	}
	ref, err := excelize.CoordinatesToCellName(col+1, n)
	if err != nil {
		return "", false
	}
	typ, err := s.f.GetCellType(s.name, ref)
	if err != nil || typ == excelize.CellTypeSharedString || typ == excelize.CellTypeInlineString {
		return "", false
	}
	return strings.TrimSpace(cells[col]), true
}

func (s *sheet) dateCell(cells []string, n, col int) (time.Time, bool) {
	v, ok := s.value(cells, n, col)
	if !ok {
		return time.Time{}, false
	}
	serial, err := strconv.ParseFloat(v, 64)
	if err != nil || serial <= 30_000 || serial >= 80_000 {
		return time.Time{}, false
	}
	return excelEpoch.AddDate(0, 0, int(serial)), true
}

// numericCell returns the stored text as an exact decimal, so the midpoint sees the published digits rather than a
// float round-trip.
func (s *sheet) numericCell(cells []string, n, col int) (*big.Rat, bool) {
	text, ok := s.value(cells, n, col)
	if !ok {
		return nil, false
	}
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
