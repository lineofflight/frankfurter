// Package nrbt fetches rates from the National Reserve Bank of Tonga, which
// publishes "Authorized Persons' Average Daily Exchange Rates" as a single
// rolling XLSX covering 2017-01 to present in biennial sheets.
//
// Each sheet has the same layout: an Excel serial date in column A, then three
// blocks of twelve quote currencies: BUY (B-M), MID (O-Z) and SELL (AB-AM),
// with N and AA as spacers. Rates read "1 TOP = X foreign", so TOP is the base.
// We emit the published MID rather than reconstructing it from buy and sell.
//
// Holidays and closures are flagged inline as shared strings in the rate cells
// (e.g. "Public Holiday: ANZAC Day"); those rows have no numeric values and are
// skipped.
//
// Unlike adapter.Window, the date filter treats after as inclusive, as the Ruby
// adapter does.
package nrbt

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const dataURL = "https://www.reservebank.to/data/docs/fmarkets/exrates/average_daily_exchange_rates.xlsx"

// quoteCurrencies are in column order; the sequence repeats across the BUY, MID
// and SELL blocks.
var quoteCurrencies = [12]string{"AUD", "EUR", "FJD", "GBP", "JPY", "NZD", "USD", "WST", "CHF", "CAD", "SEK", "SGD"}

// Zero-based column indexes of the MID block (O through Z).
const (
	midFirst = 14
	midLast  = 25
)

var excelEpoch = adapter.Date(1899, 12, 30)

func init() {
	adapter.Register("NRBT", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches NRBT rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter. A single XLSX holds the full
// history, so one window covers it.
func (a *Adapter) BackfillRange() int { return 36525 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	body, err := a.Get(ctx, dataURL, nil)
	if err != nil {
		return nil, err
	}
	return parse(body, after, upto)
}

// worksheet is the cell shape parseSheet reads: each cell's reference and raw
// value.
type worksheet struct {
	Rows []row
}

type row struct {
	Cells []cell
}

type cell struct {
	Ref string
	V   *string
}

func parse(data []byte, after, upto time.Time) ([]adapter.Rate, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var rates []adapter.Rate
	for _, name := range f.GetSheetList() {
		ws, err := readSheet(f, name)
		// A sheet whose relationship does not resolve to a worksheet part has
		// no data to read.
		if errors.As(err, new(excelize.ErrSheetNotExist)) {
			continue
		}
		if err != nil {
			return nil, err
		}
		rates = append(rates, parseSheet(ws, after, upto)...)
	}
	return rates, nil
}

// readSheet loads a sheet's raw cell values. String cells (shared or inline)
// never carry rates, so they are dropped.
func readSheet(f *excelize.File, name string) (*worksheet, error) {
	rows, err := f.GetRows(name)
	if err != nil {
		return nil, err
	}
	ws := &worksheet{Rows: make([]row, len(rows))}
	for r, values := range rows {
		for c, v := range values {
			if v == "" {
				continue
			}
			ref, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				return nil, err
			}
			// Only a numeric-looking value needs its type checked: anything
			// else fails to parse anyway.
			if _, ok := adapter.ParseFloat(v); ok {
				typ, err := f.GetCellType(name, ref)
				if err != nil {
					return nil, err
				}
				if typ == excelize.CellTypeSharedString || typ == excelize.CellTypeInlineString {
					continue
				}
			}
			ws.Rows[r].Cells = append(ws.Rows[r].Cells, cell{Ref: ref, V: &v})
		}
	}
	return ws, nil
}

func parseSheet(ws *worksheet, after, upto time.Time) []adapter.Rate {
	var rates []adapter.Rate
	for _, row := range ws.Rows {
		var mids [midLast - midFirst + 1]float64
		var have [len(mids)]bool
		serial, hasSerial := 0, false

		for _, c := range row.Cells {
			if c.Ref == "" || c.V == nil || *c.V == "" {
				continue
			}
			col, ok := columnIndex(c.Ref)
			if !ok {
				continue
			}
			switch {
			case col == 0:
				if n, err := strconv.Atoi(*c.V); err == nil {
					serial, hasSerial = n, true
				} else if f, ok := adapter.ParseFloat(*c.V); ok {
					serial, hasSerial = int(f), true
				}
			case col >= midFirst && col <= midLast:
				if f, ok := adapter.ParseFloat(*c.V); ok {
					mids[col-midFirst], have[col-midFirst] = f, true
				}
			}
		}

		if !hasSerial {
			continue
		}
		date := excelEpoch.AddDate(0, 0, serial)
		if !after.IsZero() && date.Before(after) {
			continue
		}
		if !upto.IsZero() && date.After(upto) {
			continue
		}
		for i, v := range mids {
			if !have[i] || v == 0 {
				continue
			}
			rates = append(rates, adapter.Rate{Date: date, Base: "TOP", Quote: quoteCurrencies[i], Rate: v})
		}
	}
	return rates
}

// columnIndex converts the letters leading a cell reference (A, B, ..., Z, AA,
// ...) to a zero-based index.
func columnIndex(ref string) (int, bool) {
	n, i := 0, 0
	for ; i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z'; i++ {
		n = n*26 + int(ref[i]-'A'+1)
	}
	return n - 1, i > 0
}
